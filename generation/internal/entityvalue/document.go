package entityvalue

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

var (
	entityNamePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)
	fieldNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	edgeNamePattern   = fieldNamePattern
)

type wireSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type wireSourceSet struct {
	APIVersion string       `json:"apiVersion"`
	Sources    []wireSource `json:"sources"`
}
type wireEnumValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type wireIdentity struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	SourceRef string `json:"sourceRef"`
}
type wireField struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	SourceRef     string          `json:"sourceRef"`
	Type          string          `json:"type"`
	EnumValues    []wireEnumValue `json:"enumValues"`
	Optional      bool            `json:"optional"`
	Nillable      bool            `json:"nillable"`
	Immutable     bool            `json:"immutable"`
	HasDefault    bool            `json:"hasDefault"`
	Sensitive     bool            `json:"sensitive"`
	IsIdentity    bool            `json:"isIdentity"`
	IsTenantField bool            `json:"isTenantField"`
	FieldFacts    json.RawMessage `json:"fieldFacts"`
}
type wireEdge struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SourceRef      string `json:"sourceRef"`
	TargetEntityID string `json:"targetEntityId"`
	Direction      string `json:"direction"`
	InverseName    string `json:"inverseName,omitempty"`
	BoundFieldID   string `json:"boundFieldId,omitempty"`
	Optional       bool   `json:"optional"`
	Unique         bool   `json:"unique"`
}
type wireEntity struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	SourceRef   string           `json:"sourceRef"`
	SchemaFacts json.RawMessage  `json:"schemaFacts"`
	CRUD        *json.RawMessage `json:"crud,omitempty"`
	Identity    wireIdentity     `json:"identity"`
	Fields      []wireField      `json:"fields"`
	Edges       []wireEdge       `json:"edges"`
}
type wireDocument struct {
	APIVersion   string       `json:"apiVersion"`
	Kind         string       `json:"kind"`
	SourceDigest string       `json:"sourceDigest"`
	Sources      []wireSource `json:"sources"`
	Entities     []wireEntity `json:"entities"`
}
type wireNodeIdentity struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type wireEntityNode struct {
	APIVersion  string           `json:"apiVersion"`
	CRUD        *json.RawMessage `json:"crud,omitempty"`
	ID          string           `json:"id"`
	Identity    wireNodeIdentity `json:"identity"`
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	SchemaFacts json.RawMessage  `json:"schemaFacts"`
}
type wireFieldNode struct {
	APIVersion    string          `json:"apiVersion"`
	EntityID      string          `json:"entityId"`
	EnumValues    []wireEnumValue `json:"enumValues"`
	FieldFacts    json.RawMessage `json:"fieldFacts"`
	HasDefault    bool            `json:"hasDefault"`
	ID            string          `json:"id"`
	Immutable     bool            `json:"immutable"`
	IsIdentity    bool            `json:"isIdentity"`
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	Nillable      bool            `json:"nillable"`
	Optional      bool            `json:"optional"`
	Sensitive     bool            `json:"sensitive"`
	Type          string          `json:"type"`
	IsTenantField bool            `json:"isTenantField"`
}
type wireEdgeNode struct {
	APIVersion     string `json:"apiVersion"`
	BoundFieldID   string `json:"boundFieldId,omitempty"`
	Direction      string `json:"direction"`
	EntityID       string `json:"entityId"`
	ID             string `json:"id"`
	InverseName    string `json:"inverseName,omitempty"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	TargetEntityID string `json:"targetEntityId"`
	Optional       bool   `json:"optional"`
	Unique         bool   `json:"unique"`
}

func NewDocument(projection Projection) (Document, error) {
	entities := append([]EntityProjection(nil), projection.Entities...)
	sort.SliceStable(entities, func(i, j int) bool { return entities[i].Name < entities[j].Name })
	state := &documentState{entities: make([]*entityState, len(entities))}
	seenEntities := make(map[string]struct{}, len(entities))
	for index, input := range entities {
		pointer := "/entities/" + indexString(index)
		entity, err := buildEntity(input, pointer)
		if err != nil {
			return Document{}, err
		}
		if _, duplicate := seenEntities[entity.id]; duplicate {
			return Document{}, invalid("entity_id_duplicate", pointer+"/id")
		}
		seenEntities[entity.id] = struct{}{}
		state.entities[index] = entity
	}
	if err := validateDocumentSemantics(state.entities); err != nil {
		return Document{}, err
	}
	sources, err := buildSourceClosure(state.entities)
	if err != nil {
		return Document{}, err
	}
	state.sources = sources
	state.executionModuleSources, err = normalizeSources(projection.ExecutionModuleSources, "/executionModuleSources")
	if err != nil {
		return Document{}, err
	}
	state.sourceDigest, err = computeSourceDigest(state.sources)
	if err != nil {
		return Document{}, invalid("canonical_invalid", "/sources")
	}
	state.canonical, err = canonicalDocument(state)
	if err != nil {
		return Document{}, invalid("canonical_invalid", "/document")
	}
	return Document{state: state}, nil
}

func buildEntity(input EntityProjection, pointer string) (*entityState, error) {
	if !entityNamePattern.MatchString(input.Name) {
		return nil, invalid("entity_name_invalid", pointer+"/name")
	}
	id := "schema:" + input.Name
	if err := validateRef(input.SourceRef, "schema:"+input.Name); err != nil {
		return nil, invalid("source_ref_invalid", pointer+"/sourceRef")
	}
	if err := sourcecomment.ValidateSchemaFacts(input.Meta); err != nil {
		return nil, invalid("schema_facts_invalid", pointer+"/schemaFacts")
	}
	identity, err := buildIdentity(input.Identity, pointer+"/identity")
	if err != nil {
		return nil, err
	}
	fields := append([]FieldProjection(nil), input.Fields...)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	entity := &entityState{id: id, name: input.Name, meta: input.Meta, identity: identity, fields: make([]*fieldState, len(fields))}
	if input.CRUD != nil {
		if crudErr := sourcecomment.ValidateCRUDOperations(*input.CRUD); crudErr != nil {
			return nil, invalid("crud_facts_invalid", pointer+"/crud")
		}
		entity.crud = *input.CRUD
		entity.hasCRUD = true
	}
	seenFields := make(map[string]struct{}, len(fields))
	for fieldIndex, fieldInput := range fields {
		fieldPointer := pointer + "/fields/" + indexString(fieldIndex)
		field, fieldErr := buildField(id, input.Name, input.SourceRef, fieldInput, fieldPointer)
		if fieldErr != nil {
			return nil, fieldErr
		}
		if _, duplicate := seenFields[field.id]; duplicate {
			return nil, invalid("field_id_duplicate", fieldPointer+"/id")
		}
		seenFields[field.id] = struct{}{}
		entity.fields[fieldIndex] = field
	}
	if err := validateCRUDPolicies(entity, pointer); err != nil {
		return nil, err
	}
	edges := append([]EdgeProjection(nil), input.Edges...)
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].Name < edges[j].Name })
	entity.edges = make([]*edgeState, len(edges))
	seenEdges := make(map[string]struct{}, len(edges))
	for edgeIndex, edgeInput := range edges {
		edgePointer := pointer + "/edges/" + indexString(edgeIndex)
		edge, edgeErr := buildEdge(id, input.Name, input.SourceRef, edgeInput, edgePointer)
		if edgeErr != nil {
			return nil, edgeErr
		}
		if _, duplicate := seenEdges[edge.id]; duplicate {
			return nil, invalid("edge_id_duplicate", edgePointer+"/id")
		}
		seenEdges[edge.id] = struct{}{}
		entity.edges[edgeIndex] = edge
	}
	if identity.kind == "field" {
		matches := 0
		for _, field := range entity.fields {
			if field.isIdentity && field.name == identity.name && field.typeID == identity.typeID {
				identity.source = field.source
				matches++
			}
		}
		if matches == 0 {
			return nil, invalid("identity_missing", pointer+"/identity")
		}
		if matches > 1 {
			return nil, invalid("identity_composite_unsupported", pointer+"/identity")
		}
	}
	schemaFacts, err := sourcecomment.CanonicalSchemaFacts(entity.meta)
	if err != nil {
		return nil, err
	}
	entityBytes, err := canonicalEntityNode(entity, schemaFacts)
	if err != nil {
		return nil, invalid("canonical_invalid", pointer)
	}
	entity.canonicalSource = entityBytes
	entity.source = provenance.Source{Ref: input.SourceRef, Digest: provenance.SHA256(entityBytes)}
	if identity.kind == "implicit" {
		identity.source = entity.source
	}
	return entity, nil
}

func buildEdge(entityID, entityName string, entitySourceRef provenance.SourceRef, input EdgeProjection, pointer string) (*edgeState, error) {
	if !edgeNamePattern.MatchString(input.Name) {
		return nil, invalid("edge_name_invalid", pointer+"/name")
	}
	if err := validateRef(input.SourceRef, "schema:"+entityName+"/edge:"+input.Name); err != nil || input.SourceRef.Path() != entitySourceRef.Path() {
		return nil, invalid("source_ref_invalid", pointer+"/sourceRef")
	}
	if !strings.HasPrefix(input.TargetEntityID, "schema:") {
		return nil, invalid("edge_target_invalid", pointer+"/targetEntityId")
	}
	if input.Direction != "to" && input.Direction != "from" {
		return nil, invalid("edge_direction_invalid", pointer+"/direction")
	}
	if input.InverseName != "" && !edgeNamePattern.MatchString(input.InverseName) {
		return nil, invalid("edge_inverse_invalid", pointer+"/inverseName")
	}
	if input.BoundFieldID != "" && !strings.HasPrefix(input.BoundFieldID, "schema:") {
		return nil, invalid("edge_bound_field_invalid", pointer+"/boundFieldId")
	}
	edge := &edgeState{id: entityID + "/edge:" + input.Name, name: input.Name, targetEntityID: input.TargetEntityID, direction: input.Direction, inverseName: input.InverseName, boundFieldID: input.BoundFieldID, optional: input.Optional, unique: input.Unique}
	edgeBytes, err := canonicalEdgeNode(entityID, edge)
	if err != nil {
		return nil, invalid("canonical_invalid", pointer)
	}
	edge.canonicalSource = edgeBytes
	edge.source = provenance.Source{Ref: input.SourceRef, Digest: provenance.SHA256(edgeBytes)}
	return edge, nil
}

func buildIdentity(input IdentityProjection, pointer string) (*identityState, error) {
	if input.Kind != "implicit" && input.Kind != "field" {
		return nil, invalid("identity_missing", pointer)
	}
	if input.Name == "" {
		return nil, invalid("identity_missing", pointer)
	}
	if !validScalar(input.Type) {
		return nil, invalid("field_type_unsupported", pointer)
	}
	if input.Kind == "implicit" && (input.Name != "id" || input.Type != "int64") {
		return nil, invalid("identity_strategy_invalid", pointer)
	}
	return &identityState{kind: input.Kind, name: input.Name, typeID: input.Type}, nil
}

func buildField(entityID, entityName string, entitySourceRef provenance.SourceRef, input FieldProjection, pointer string) (*fieldState, error) {
	if !fieldNamePattern.MatchString(input.Name) {
		return nil, invalid("field_name_invalid", pointer+"/name")
	}
	if !validScalar(input.Type) {
		return nil, invalid("field_type_unsupported", pointer+"/type")
	}
	if err := validateRef(input.SourceRef, "schema:"+entityName+"/field:"+input.Name); err != nil {
		return nil, invalid("source_ref_invalid", pointer+"/sourceRef")
	}
	if input.SourceRef.Path() != entitySourceRef.Path() {
		return nil, invalid("source_ref_invalid", pointer+"/sourceRef")
	}
	meta := cloneFieldFacts(input.Meta)
	if _, err := sourcecomment.CanonicalFieldFacts(meta); err != nil {
		return nil, invalid("field_facts_invalid", pointer+"/fieldFacts")
	}
	if input.Immutable && meta.CRUD != nil && (meta.CRUD.Mutation == sourcecomment.MutationUpdate || meta.CRUD.Mutation == sourcecomment.MutationCreateUpdate) {
		return nil, invalid("policy_conflict", pointer+"/fieldFacts/crud")
	}
	enums := append([]EnumValue(nil), input.EnumValues...)
	sort.SliceStable(enums, func(i, j int) bool {
		if enums[i].Name == enums[j].Name {
			return enums[i].Value < enums[j].Value
		}
		return enums[i].Name < enums[j].Name
	})
	if input.Type == "enum" && len(enums) == 0 || input.Type != "enum" && len(enums) != 0 {
		return nil, invalid("enum_invalid", pointer+"/enumValues")
	}
	seenNames, seenValues := map[string]struct{}{}, map[string]struct{}{}
	for enumIndex, value := range enums {
		if value.Name == "" || value.Value == "" {
			return nil, invalid("enum_invalid", pointer+"/enumValues/"+indexString(enumIndex))
		}
		if _, ok := seenNames[value.Name]; ok {
			return nil, invalid("enum_duplicate", pointer+"/enumValues/"+indexString(enumIndex)+"/name")
		}
		if _, ok := seenValues[value.Value]; ok {
			return nil, invalid("enum_duplicate", pointer+"/enumValues/"+indexString(enumIndex)+"/value")
		}
		seenNames[value.Name], seenValues[value.Value] = struct{}{}, struct{}{}
	}
	field := &fieldState{
		id: entityID + "/field:" + input.Name, name: input.Name, typeID: input.Type, enumValues: enums,
		optional: input.Optional, nillable: input.Nillable, immutable: input.Immutable, hasDefault: input.HasDefault,
		sensitive: input.Sensitive, isIdentity: input.IsIdentity, isTenantField: input.IsTenantField, meta: meta,
	}
	fieldBytes, err := canonicalFieldNode(entityID, field)
	if err != nil {
		return nil, invalid("canonical_invalid", pointer)
	}
	field.canonicalSource = fieldBytes
	field.source = provenance.Source{Ref: input.SourceRef, Digest: provenance.SHA256(fieldBytes)}
	return field, nil
}

func buildSourceClosure(entities []*entityState) ([]provenance.Source, error) {
	byRef := make(map[string]provenance.Source)
	add := func(source provenance.Source, pointer string) error {
		key := source.Ref.String()
		if key == "" || source.Digest.String() == "" {
			return invalid("source_digest_invalid", pointer)
		}
		if existing, ok := byRef[key]; ok && existing.Digest != source.Digest {
			return invalid("source_conflict", pointer)
		}
		byRef[key] = source
		return nil
	}
	for entityIndex, entity := range entities {
		base := "/entities/" + indexString(entityIndex)
		if err := add(entity.source, base+"/sourceRef"); err != nil {
			return nil, err
		}
		for fieldIndex, field := range entity.fields {
			fieldBase := base + "/fields/" + indexString(fieldIndex)
			if err := add(field.source, fieldBase+"/sourceRef"); err != nil {
				return nil, err
			}
		}
		for edgeIndex, edge := range entity.edges {
			if err := add(edge.source, base+"/edges/"+indexString(edgeIndex)+"/sourceRef"); err != nil {
				return nil, err
			}
		}
	}
	result := make([]provenance.Source, 0, len(byRef))
	for _, source := range byRef {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref.String() < result[j].Ref.String() })
	return result, nil
}

func normalizeSources(input []provenance.Source, pointer string) ([]provenance.Source, error) {
	result := append([]provenance.Source(nil), input...)
	sort.Slice(result, func(i, j int) bool { return result[i].Ref.String() < result[j].Ref.String() })
	for i, source := range result {
		if _, err := provenance.ParseSourceRef(source.Ref.String()); err != nil {
			return nil, invalid("source_ref_invalid", pointer+"/"+indexString(i)+"/ref")
		}
		if _, err := provenance.ParseDigest(source.Digest.String()); err != nil {
			return nil, invalid("source_digest_invalid", pointer+"/"+indexString(i)+"/digest")
		}
		if i > 0 && result[i-1].Ref == source.Ref {
			return nil, invalid("source_conflict", pointer+"/"+indexString(i)+"/ref")
		}
	}
	return result, nil
}

func validateDocumentSemantics(entities []*entityState) error {
	byID := make(map[string]*entityState, len(entities))
	for _, entity := range entities {
		byID[entity.id] = entity
	}
	for entityIndex, entity := range entities {
		for edgeIndex, edge := range entity.edges {
			base := "/entities/" + indexString(entityIndex) + "/edges/" + indexString(edgeIndex)
			target := byID[edge.targetEntityID]
			if target == nil {
				return invalid("edge_target_missing", base+"/targetEntityId")
			}
			boundOwner := entity
			if edge.direction == "from" {
				boundOwner = target
			}
			if edge.boundFieldID != "" && findEntityFieldByID(boundOwner, edge.boundFieldID) == nil {
				return invalid("edge_bound_field_invalid", base+"/boundFieldId")
			}
			if edge.inverseName != "" {
				inverse := findEdgeByName(target, edge.inverseName)
				if inverse == nil || inverse.targetEntityID != entity.id || inverse.inverseName != edge.name || inverse.direction == edge.direction || (edge.boundFieldID == "") != (inverse.boundFieldID == "") || edge.boundFieldID != "" && inverse.boundFieldID != edge.boundFieldID {
					return invalid("edge_inverse_not_closed", base+"/inverseName")
				}
			}
		}
		for fieldIndex, field := range entity.fields {
			base := "/entities/" + indexString(entityIndex) + "/fields/" + indexString(fieldIndex) + "/fieldFacts/"
			bound := localEdgesForField(entity, field.id)
			if field.meta.Reference != nil {
				target := byID["schema:"+field.meta.Reference.Target]
				if target == nil || !hasFieldName(target, field.meta.Reference.Display) {
					return invalid("reference_target_missing", base+"reference")
				}
				if len(bound) > 1 || len(bound) == 1 && bound[0].targetEntityID != target.id {
					return invalid("reference_edge_conflict", base+"reference")
				}
			}
		}
	}
	return validateLocalizedKeys(entities)
}

func findEntityFieldByID(entity *entityState, id string) *fieldState {
	for _, field := range entity.fields {
		if field.id == id {
			return field
		}
	}
	return nil
}
func findEdgeByName(entity *entityState, name string) *edgeState {
	for _, edge := range entity.edges {
		if edge.name == name {
			return edge
		}
	}
	return nil
}
func localEdgesForField(entity *entityState, id string) []*edgeState {
	var result []*edgeState
	for _, edge := range entity.edges {
		if edge.boundFieldID == id {
			result = append(result, edge)
		}
	}
	return result
}

func validateCRUDPolicies(entity *entityState, pointer string) error {
	for index, field := range entity.fields {
		base := pointer + "/fields/" + indexString(index) + "/fieldFacts/crud"
		if sensitiveTupleConflict(field.sensitive, field.meta.Visibility, field.meta.Control) {
			return invalid("policy_conflict", base)
		}
		policy := field.meta.CRUD
		if field.isTenantField {
			if policy != nil {
				return invalid("policy_conflict", base)
			}
			continue
		}
		if entity.hasCRUD != (policy != nil) {
			return invalid("policy_presence_conflict", base)
		}
		if policy == nil {
			continue
		}
		if field.meta.Visibility == sourcecomment.VisibilityInternal && (policy.Read != sourcecomment.ReadExclude || policy.Mutation != sourcecomment.MutationNone) {
			return invalid("policy_conflict", base)
		}
		if field.meta.Visibility == sourcecomment.VisibilitySensitive && policy.Read != sourcecomment.ReadExclude {
			return invalid("policy_conflict", base)
		}
		if field.meta.Control == sourcecomment.UIControlReadonly && policy.Mutation != sourcecomment.MutationNone {
			return invalid("policy_conflict", base)
		}
		if field.isIdentity && (policy.Read != sourcecomment.ReadInclude || policy.Mutation != sourcecomment.MutationNone) {
			return invalid("policy_conflict", base)
		}
		if field.immutable && (policy.Mutation == sourcecomment.MutationUpdate || policy.Mutation == sourcecomment.MutationCreateUpdate) {
			return invalid("policy_conflict", base)
		}
	}
	return nil
}

func sensitiveTupleConflict(sensitive bool, visibility sourcecomment.FieldVisibility, hint sourcecomment.UIControl) bool {
	return sensitive != (visibility == sourcecomment.VisibilitySensitive) || sensitive != (hint == sourcecomment.UIControlSensitive)
}

func validateLocalizedKeys(entities []*entityState) error {
	type text struct{ zh, en string }
	seen := map[string]text{}
	add := func(value sourcecomment.LocalizedText, pointer string) error {
		current := text{value.ZhCN, value.EnUS}
		if old, ok := seen[value.Key]; ok && old != current {
			return invalid("localized_text_conflict", pointer)
		}
		seen[value.Key] = current
		return nil
	}
	for ei, entity := range entities {
		base := "/entities/" + indexString(ei)
		if err := add(entity.meta.Label, base+"/schemaFacts/label"); err != nil {
			return err
		}
		if err := add(entity.meta.Description, base+"/schemaFacts/description"); err != nil {
			return err
		}
		for fi, field := range entity.fields {
			fieldBase := base + "/fields/" + indexString(fi) + "/fieldFacts"
			if err := add(field.meta.Label, fieldBase+"/label"); err != nil {
				return err
			}
			if err := add(field.meta.Description, fieldBase+"/description"); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasFieldName(entity *entityState, name string) bool {
	for _, field := range entity.fields {
		if field.name == name {
			return true
		}
	}
	return false
}

func validateRef(ref provenance.SourceRef, fragment string) error {
	parsed, err := provenance.ParseSourceRef(ref.String())
	if err != nil || parsed.Fragment() != fragment {
		return invalid("source_ref_invalid", "")
	}
	return nil
}

func validScalar(value string) bool {
	switch value {
	case "bool", "int64", "uint64", "float", "double", "string", "bytes", "timestamp", "uuid", "json", "enum":
		return true
	}
	return false
}

func canonicalEntityNode(entity *entityState, schemaFacts []byte) ([]byte, error) {
	doc := wireEntityNode{APIVersion: "nexa.dev/entity-node/v2", ID: entity.id, Identity: wireNodeIdentity{Kind: entity.identity.kind, Name: entity.identity.name, Type: entity.identity.typeID}, Kind: "Entity", Name: entity.name, SchemaFacts: schemaFacts}
	if entity.hasCRUD {
		raw, err := sourcecomment.CanonicalCRUDOperations(entity.crud)
		if err != nil {
			return nil, err
		}
		value := json.RawMessage(raw)
		doc.CRUD = &value
	}
	return canonicalize(doc)
}

func canonicalFieldNode(entityID string, field *fieldState) ([]byte, error) {
	meta, err := sourcecomment.CanonicalFieldFacts(field.meta)
	if err != nil {
		return nil, err
	}
	return canonicalize(wireFieldNode{APIVersion: "nexa.dev/entity-field-node/v3", EntityID: entityID, EnumValues: wireEnums(field.enumValues), FieldFacts: meta, HasDefault: field.hasDefault, ID: field.id, Immutable: field.immutable, IsIdentity: field.isIdentity, IsTenantField: field.isTenantField, Kind: "Field", Name: field.name, Nillable: field.nillable, Optional: field.optional, Sensitive: field.sensitive, Type: field.typeID})
}
func canonicalEdgeNode(entityID string, edge *edgeState) ([]byte, error) {
	return canonicalize(wireEdgeNode{APIVersion: "nexa.dev/entity-edge-node/v1", BoundFieldID: edge.boundFieldID, Direction: edge.direction, EntityID: entityID, ID: edge.id, InverseName: edge.inverseName, Kind: "Edge", Name: edge.name, TargetEntityID: edge.targetEntityID, Optional: edge.optional, Unique: edge.unique})
}

func computeSourceDigest(sources []provenance.Source) (provenance.Digest, error) {
	encoded, err := canonicalize(wireSourceSet{APIVersion: sourceSetAPIVersion, Sources: wireSources(sources)})
	if err != nil {
		return provenance.Digest{}, err
	}
	return provenance.SHA256(encoded), nil
}

func canonicalDocument(state *documentState) ([]byte, error) {
	doc := wireDocument{APIVersion: apiVersion, Kind: kind, SourceDigest: state.sourceDigest.String(), Sources: wireSources(state.sources), Entities: make([]wireEntity, len(state.entities))}
	for i, entity := range state.entities {
		schemaFacts, err := sourcecomment.CanonicalSchemaFacts(entity.meta)
		if err != nil {
			return nil, err
		}
		item := wireEntity{ID: entity.id, Name: entity.name, SourceRef: entity.source.Ref.String(), SchemaFacts: schemaFacts, Identity: wireIdentity{Kind: entity.identity.kind, Name: entity.identity.name, Type: entity.identity.typeID, SourceRef: entity.identity.source.Ref.String()}, Fields: make([]wireField, len(entity.fields)), Edges: make([]wireEdge, len(entity.edges))}
		if entity.hasCRUD {
			raw, err := sourcecomment.CanonicalCRUDOperations(entity.crud)
			if err != nil {
				return nil, err
			}
			value := json.RawMessage(raw)
			item.CRUD = &value
		}
		for j, field := range entity.fields {
			meta, err := sourcecomment.CanonicalFieldFacts(field.meta)
			if err != nil {
				return nil, err
			}
			item.Fields[j] = wireField{ID: field.id, Name: field.name, SourceRef: field.source.Ref.String(), Type: field.typeID, EnumValues: wireEnums(field.enumValues), Optional: field.optional, Nillable: field.nillable, Immutable: field.immutable, HasDefault: field.hasDefault, Sensitive: field.sensitive, IsIdentity: field.isIdentity, IsTenantField: field.isTenantField, FieldFacts: meta}
		}
		for j, edge := range entity.edges {
			item.Edges[j] = wireEdge{ID: edge.id, Name: edge.name, SourceRef: edge.source.Ref.String(), TargetEntityID: edge.targetEntityID, Direction: edge.direction, InverseName: edge.inverseName, BoundFieldID: edge.boundFieldID, Optional: edge.optional, Unique: edge.unique}
		}
		doc.Entities[i] = item
	}
	return canonicalize(doc)
}

func wireSources(sources []provenance.Source) []wireSource {
	result := make([]wireSource, len(sources))
	for i, s := range sources {
		result[i] = wireSource{Ref: s.Ref.String(), Digest: s.Digest.String()}
	}
	return result
}
func wireEnums(values []EnumValue) []wireEnumValue {
	result := make([]wireEnumValue, len(values))
	for i, v := range values {
		result[i] = wireEnumValue{Name: v.Name, Value: v.Value}
	}
	return result
}
func canonicalize(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}
func indexString(index int) string {
	if index == 0 {
		return "0"
	}
	var buffer [24]byte
	position := len(buffer)
	for index > 0 {
		position--
		buffer[position] = byte('0' + index%10)
		index /= 10
	}
	return string(buffer[position:])
}
