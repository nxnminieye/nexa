package entity

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

func ParseSnapshot(source provenance.DomainSource, data []byte) (Snapshot, error) {
	sourceName := source.String()
	if sourceName == "" {
		return Snapshot{}, snapshotError("document_invalid", "", sourceName)
	}
	if pointer, invalid := entityUnicodeFailure(data); invalid {
		return Snapshot{}, snapshotError("unicode_invalid", pointer, sourceName)
	}
	document, err := strictdoc.ParseJSON(sourceName, data)
	if err != nil {
		return Snapshot{}, projectStrictDocumentError(sourceName, err)
	}
	var root map[string]any
	if err := json.Unmarshal(document.JSON(), &root); err != nil || root == nil {
		return Snapshot{}, snapshotError("document_invalid", "", sourceName)
	}
	if pointer := firstUnknown(document, root, "", "apiVersion", "kind", "sourceDigest", "sources", "entities"); pointer != "" {
		return Snapshot{}, snapshotError("document_unknown_field", pointer, sourceName)
	}
	for _, member := range []string{"apiVersion", "kind", "sourceDigest", "sources", "entities"} {
		if _, ok := root[member]; !ok {
			return Snapshot{}, snapshotError("document_required_missing", "/"+member, sourceName)
		}
	}
	apiVersion, ok := root["apiVersion"].(string)
	if !ok {
		return Snapshot{}, snapshotError("document_type_invalid", "/apiVersion", sourceName)
	}
	kind, ok := root["kind"].(string)
	if !ok {
		return Snapshot{}, snapshotError("document_type_invalid", "/kind", sourceName)
	}
	digestString, ok := root["sourceDigest"].(string)
	if !ok {
		return Snapshot{}, snapshotError("document_type_invalid", "/sourceDigest", sourceName)
	}
	sourceValues, ok := root["sources"].([]any)
	if !ok {
		return Snapshot{}, snapshotError("document_type_invalid", "/sources", sourceName)
	}
	entityValues, ok := root["entities"].([]any)
	if !ok {
		return Snapshot{}, snapshotError("document_type_invalid", "/entities", sourceName)
	}
	if apiVersion != APIVersion {
		return Snapshot{}, snapshotError("version_unsupported", "/apiVersion", sourceName)
	}
	if kind != Kind {
		return Snapshot{}, snapshotError("kind_invalid", "/kind", sourceName)
	}

	storedDigest, err := provenance.ParseDigest(digestString)
	if err != nil {
		return Snapshot{}, snapshotError("source_digest_invalid", "/sourceDigest", sourceName)
	}
	sources, err := parseSnapshotSources(document, sourceName, sourceValues)
	if err != nil {
		return Snapshot{}, err
	}
	entities, err := parseSnapshotEntities(document, sourceName, entityValues)
	if err != nil {
		return Snapshot{}, err
	}
	state := &snapshotState{apiVersion: apiVersion, sourceDigest: storedDigest, sources: sources, entities: entities}
	if err := validateSnapshotClosure(state, sourceName); err != nil {
		return Snapshot{}, err
	}
	if err := validateEntitySchema(root); err != nil {
		return Snapshot{}, snapshotError("document_invalid", "", sourceName)
	}
	computedDigest, err := computeSourceDigest(sources)
	if err != nil {
		return Snapshot{}, snapshotError("canonical_invalid", "/sources", sourceName)
	}
	if computedDigest != storedDigest {
		return Snapshot{}, snapshotError("source_digest_mismatch", "/sourceDigest", sourceName)
	}
	canonical, err := canonicalDocumentForSnapshot(state)
	if err != nil {
		return Snapshot{}, snapshotError("canonical_invalid", "", sourceName)
	}
	if !bytes.Equal(data, canonical) {
		return Snapshot{}, snapshotError("canonical_order_invalid", "", sourceName)
	}
	state.canonical = append([]byte(nil), canonical...)
	return Snapshot{state: state}, nil
}

func parseSnapshotSources(document strictdoc.Document, sourceName string, values []any) ([]provenance.Source, error) {
	result := make([]provenance.Source, len(values))
	seen := make(map[string]provenance.Digest, len(values))
	previous := ""
	for index, value := range values {
		base := "/sources/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", base, sourceName)
		}
		if pointer := firstUnknown(document, object, base, "ref", "digest"); pointer != "" {
			return nil, snapshotError("document_unknown_field", pointer, sourceName)
		}
		for _, member := range []string{"ref", "digest"} {
			if _, exists := object[member]; !exists {
				return nil, snapshotError("document_required_missing", base+"/"+member, sourceName)
			}
		}
		refString, ok := object["ref"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/ref", sourceName)
		}
		digestString, ok := object["digest"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/digest", sourceName)
		}
		ref, err := provenance.ParseSourceRef(refString)
		if err != nil {
			return nil, snapshotError("source_ref_invalid", base+"/ref", sourceName)
		}
		digest, err := provenance.ParseDigest(digestString)
		if err != nil {
			return nil, snapshotError("source_digest_invalid", base+"/digest", sourceName)
		}
		if existing, duplicate := seen[refString]; duplicate {
			pointer := base + "/ref"
			if existing != digest {
				pointer = base + "/digest"
			}
			return nil, snapshotError("source_conflict", pointer, sourceName)
		}
		if previous != "" && refString <= previous {
			return nil, snapshotError("canonical_order_invalid", base+"/ref", sourceName)
		}
		previous = refString
		seen[refString] = digest
		result[index] = provenance.Source{Ref: ref, Digest: digest}
	}
	return result, nil
}

func parseSnapshotEntities(document strictdoc.Document, sourceName string, values []any) ([]*snapshotEntityState, error) {
	result := make([]*snapshotEntityState, len(values))
	seen := make(map[string]struct{}, len(values))
	previous := ""
	for index, value := range values {
		base := "/entities/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", base, sourceName)
		}
		if pointer := firstUnknown(document, object, base, "id", "name", "sourceRef", "schemaMeta", "crud", "identity", "fields", "edges"); pointer != "" {
			return nil, snapshotError("document_unknown_field", pointer, sourceName)
		}
		for _, member := range []string{"id", "name", "sourceRef", "schemaMeta", "identity", "fields", "edges"} {
			if _, exists := object[member]; !exists {
				return nil, snapshotError("document_required_missing", base+"/"+member, sourceName)
			}
		}
		id, ok := object["id"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/id", sourceName)
		}
		name, ok := object["name"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/name", sourceName)
		}
		refString, ok := object["sourceRef"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/sourceRef", sourceName)
		}
		if id == "" || name == "" || id != "schema:"+name {
			return nil, snapshotError("source_closure_invalid", base+"/id", sourceName)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, snapshotError("source_closure_invalid", base+"/id", sourceName)
		}
		if previous != "" && id <= previous {
			return nil, snapshotError("canonical_order_invalid", base+"/id", sourceName)
		}
		previous = id
		seen[id] = struct{}{}
		ref, err := provenance.ParseSourceRef(refString)
		if err != nil {
			return nil, snapshotError("source_ref_invalid", base+"/sourceRef", sourceName)
		}
		meta, err := decodeSnapshotSchemaMeta(object["schemaMeta"])
		if err != nil {
			return nil, projectSnapshotAnnotationError(sourceName, base+"/schemaMeta", err)
		}
		item := &snapshotEntityState{id: id, name: name, sourceRef: ref, meta: meta}
		if crudValue, exists := object["crud"]; exists {
			if crudValue == nil {
				return nil, snapshotError("document_type_invalid", base+"/crud", sourceName)
			}
			item.crud, err = decodeSnapshotCRUD(crudValue)
			if err != nil {
				return nil, projectSnapshotAnnotationError(sourceName, base+"/crud", err)
			}
			item.hasCRUD = true
		}
		item.identity, err = parseSnapshotIdentity(document, sourceName, base+"/identity", object["identity"])
		if err != nil {
			return nil, err
		}
		fieldValues, ok := object["fields"].([]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/fields", sourceName)
		}
		item.fields, err = parseSnapshotFields(document, sourceName, base, id, fieldValues)
		if err != nil {
			return nil, err
		}
		edgeValues, ok := object["edges"].([]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/edges", sourceName)
		}
		item.edges, err = parseSnapshotEdges(document, sourceName, base, id, edgeValues)
		if err != nil {
			return nil, err
		}
		result[index] = item
	}
	return result, nil
}

func parseSnapshotEdges(document strictdoc.Document, sourceName, entityBase, entityID string, values []any) ([]*snapshotEdgeState, error) {
	result := make([]*snapshotEdgeState, len(values))
	seen := map[string]struct{}{}
	previous := ""
	for index, value := range values {
		base := entityBase + "/edges/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", base, sourceName)
		}
		allowed := []string{"id", "name", "sourceRef", "targetEntityId", "direction", "inverseName", "boundFieldId", "optional", "unique"}
		if pointer := firstUnknown(document, object, base, allowed...); pointer != "" {
			return nil, snapshotError("document_unknown_field", pointer, sourceName)
		}
		for _, member := range []string{"id", "name", "sourceRef", "targetEntityId", "direction", "optional", "unique"} {
			if _, exists := object[member]; !exists {
				return nil, snapshotError("document_required_missing", base+"/"+member, sourceName)
			}
		}
		id, idOK := object["id"].(string)
		name, nameOK := object["name"].(string)
		refString, refOK := object["sourceRef"].(string)
		target, targetOK := object["targetEntityId"].(string)
		directionString, directionOK := object["direction"].(string)
		optional, optionalOK := object["optional"].(bool)
		unique, uniqueOK := object["unique"].(bool)
		if !idOK || !nameOK || !refOK || !targetOK || !directionOK || !optionalOK || !uniqueOK {
			return nil, snapshotError("document_type_invalid", base, sourceName)
		}
		if id != entityID+"/edge:"+name {
			return nil, snapshotError("source_closure_invalid", base+"/id", sourceName)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, snapshotError("source_closure_invalid", base+"/id", sourceName)
		}
		if previous != "" && id <= previous {
			return nil, snapshotError("canonical_order_invalid", base+"/id", sourceName)
		}
		seen[id] = struct{}{}
		previous = id
		ref, err := provenance.ParseSourceRef(refString)
		if err != nil {
			return nil, snapshotError("source_ref_invalid", base+"/sourceRef", sourceName)
		}
		direction := EdgeDirection(directionString)
		if direction != EdgeDirectionTo && direction != EdgeDirectionFrom {
			return nil, snapshotError("source_closure_invalid", base+"/direction", sourceName)
		}
		inverse, bound := "", ""
		if value, exists := object["inverseName"]; exists {
			inverse, ok = value.(string)
			if !ok || inverse == "" {
				return nil, snapshotError("document_type_invalid", base+"/inverseName", sourceName)
			}
		}
		if value, exists := object["boundFieldId"]; exists {
			bound, ok = value.(string)
			if !ok || bound == "" {
				return nil, snapshotError("document_type_invalid", base+"/boundFieldId", sourceName)
			}
		}
		result[index] = &snapshotEdgeState{id: id, name: name, sourceRef: ref, targetEntityID: target, direction: direction, inverseName: inverse, boundFieldID: bound, optional: optional, unique: unique}
	}
	return result, nil
}

func parseSnapshotIdentity(document strictdoc.Document, sourceName, base string, value any) (*snapshotIdentityState, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, snapshotError("document_type_invalid", base, sourceName)
	}
	if pointer := firstUnknown(document, object, base, "kind", "name", "type", "sourceRef"); pointer != "" {
		return nil, snapshotError("document_unknown_field", pointer, sourceName)
	}
	for _, member := range []string{"kind", "name", "type", "sourceRef"} {
		if _, exists := object[member]; !exists {
			return nil, snapshotError("document_required_missing", base+"/"+member, sourceName)
		}
	}
	kindString, ok := object["kind"].(string)
	if !ok {
		return nil, snapshotError("document_type_invalid", base+"/kind", sourceName)
	}
	name, ok := object["name"].(string)
	if !ok {
		return nil, snapshotError("document_type_invalid", base+"/name", sourceName)
	}
	typeString, ok := object["type"].(string)
	if !ok {
		return nil, snapshotError("document_type_invalid", base+"/type", sourceName)
	}
	refString, ok := object["sourceRef"].(string)
	if !ok {
		return nil, snapshotError("document_type_invalid", base+"/sourceRef", sourceName)
	}
	kind := IdentityKind(kindString)
	if kind != IdentityImplicit && kind != IdentityField {
		return nil, snapshotError("source_closure_invalid", base+"/kind", sourceName)
	}
	typeID := ScalarType(typeString)
	if !validScalar(typeID) {
		return nil, snapshotError("source_closure_invalid", base+"/type", sourceName)
	}
	ref, err := provenance.ParseSourceRef(refString)
	if err != nil {
		return nil, snapshotError("source_ref_invalid", base+"/sourceRef", sourceName)
	}
	if name == "" {
		return nil, snapshotError("source_closure_invalid", base+"/name", sourceName)
	}
	return &snapshotIdentityState{kind: kind, name: name, typeID: typeID, sourceRef: ref}, nil
}

func parseSnapshotFields(document strictdoc.Document, sourceName, entityBase, entityID string, values []any) ([]*snapshotFieldState, error) {
	result := make([]*snapshotFieldState, len(values))
	seen := make(map[string]struct{}, len(values))
	previous := ""
	for index, value := range values {
		base := entityBase + "/fields/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", base, sourceName)
		}
		allowed := []string{"id", "name", "sourceRef", "type", "enumValues", "optional", "nillable", "immutable", "hasDefault", "sensitive", "isIdentity", "isTenantField", "fieldMeta"}
		if pointer := firstUnknown(document, object, base, allowed...); pointer != "" {
			return nil, snapshotError("document_unknown_field", pointer, sourceName)
		}
		for _, member := range allowed {
			if _, exists := object[member]; !exists {
				return nil, snapshotError("document_required_missing", base+"/"+member, sourceName)
			}
		}
		id, ok := object["id"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/id", sourceName)
		}
		name, ok := object["name"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/name", sourceName)
		}
		refString, ok := object["sourceRef"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/sourceRef", sourceName)
		}
		typeString, ok := object["type"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/type", sourceName)
		}
		enumValues, ok := object["enumValues"].([]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", base+"/enumValues", sourceName)
		}
		if id == "" || name == "" || id != entityID+"/field:"+name {
			return nil, snapshotError("source_closure_invalid", base+"/id", sourceName)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, snapshotError("source_closure_invalid", base+"/id", sourceName)
		}
		if previous != "" && id <= previous {
			return nil, snapshotError("canonical_order_invalid", base+"/id", sourceName)
		}
		previous = id
		seen[id] = struct{}{}
		ref, err := provenance.ParseSourceRef(refString)
		if err != nil {
			return nil, snapshotError("source_ref_invalid", base+"/sourceRef", sourceName)
		}
		typeID := ScalarType(typeString)
		if !validScalar(typeID) {
			return nil, snapshotError("source_closure_invalid", base+"/type", sourceName)
		}
		enums, err := parseSnapshotEnums(document, sourceName, base+"/enumValues", enumValues)
		if err != nil {
			return nil, err
		}
		if (typeID == ScalarEnum) != (len(enums) > 0) {
			return nil, snapshotError("source_closure_invalid", base+"/enumValues", sourceName)
		}
		meta, err := decodeSnapshotFieldMeta(object["fieldMeta"])
		if err != nil {
			return nil, projectSnapshotAnnotationError(sourceName, base+"/fieldMeta", err)
		}
		flags := make([]bool, 7)
		for flagIndex, member := range []string{"optional", "nillable", "immutable", "hasDefault", "sensitive", "isIdentity", "isTenantField"} {
			flags[flagIndex], ok = object[member].(bool)
			if !ok {
				return nil, snapshotError("document_type_invalid", base+"/"+member, sourceName)
			}
		}
		result[index] = &snapshotFieldState{id: id, name: name, sourceRef: ref, typeID: typeID, enumValues: enums, optional: flags[0], nillable: flags[1], immutable: flags[2], hasDefault: flags[3], sensitive: flags[4], isIdentity: flags[5], isTenantField: flags[6], meta: meta}
	}
	return result, nil
}

func parseSnapshotEnums(document strictdoc.Document, sourceName, base string, values []any) ([]EnumValue, error) {
	result := make([]EnumValue, len(values))
	names := make(map[string]struct{}, len(values))
	authored := make(map[string]struct{}, len(values))
	previousName, previousValue := "", ""
	for index, value := range values {
		itemBase := base + "/" + strconv.Itoa(index)
		object, ok := value.(map[string]any)
		if !ok {
			return nil, snapshotError("document_type_invalid", itemBase, sourceName)
		}
		if pointer := firstUnknown(document, object, itemBase, "name", "value"); pointer != "" {
			return nil, snapshotError("document_unknown_field", pointer, sourceName)
		}
		for _, member := range []string{"name", "value"} {
			if _, exists := object[member]; !exists {
				return nil, snapshotError("document_required_missing", itemBase+"/"+member, sourceName)
			}
		}
		name, ok := object["name"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", itemBase+"/name", sourceName)
		}
		authoredValue, ok := object["value"].(string)
		if !ok {
			return nil, snapshotError("document_type_invalid", itemBase+"/value", sourceName)
		}
		if name == "" || authoredValue == "" {
			return nil, snapshotError("source_closure_invalid", itemBase, sourceName)
		}
		if _, duplicate := names[name]; duplicate {
			return nil, snapshotError("source_closure_invalid", itemBase+"/name", sourceName)
		}
		names[name] = struct{}{}
		if _, duplicate := authored[authoredValue]; duplicate {
			return nil, snapshotError("source_closure_invalid", itemBase+"/value", sourceName)
		}
		authored[authoredValue] = struct{}{}
		if index > 0 && (name < previousName || (name == previousName && authoredValue <= previousValue)) {
			return nil, snapshotError("canonical_order_invalid", itemBase, sourceName)
		}
		previousName, previousValue = name, authoredValue
		result[index] = EnumValue{Name: name, Value: authoredValue}
	}
	return result, nil
}

func validateSnapshotClosure(state *snapshotState, sourceName string) error {
	available := make(map[string]provenance.Digest, len(state.sources))
	used := make(map[string]struct{})
	for _, source := range state.sources {
		available[source.Ref.String()] = source.Digest
	}
	entities := make(map[string]*snapshotEntityState, len(state.entities))
	for _, entity := range state.entities {
		entities[entity.id] = entity
	}
	for entityIndex, entity := range state.entities {
		base := "/entities/" + strconv.Itoa(entityIndex)
		entityDigest, ok := available[entity.sourceRef.String()]
		if !ok {
			return snapshotError("source_closure_invalid", base+"/sourceRef", sourceName)
		}
		if entity.sourceRef.Fragment() != "schema:"+entity.name {
			return snapshotError("source_closure_invalid", base+"/sourceRef", sourceName)
		}
		used[entity.sourceRef.String()] = struct{}{}
		if entity.meta.Identity != nexaent.IdentityEntID {
			return snapshotError("source_closure_invalid", base+"/schemaMeta", sourceName)
		}
		identity := &identityState{kind: entity.identity.kind, name: entity.identity.name, typeID: entity.identity.typeID}
		canonicalEntity, err := canonicalEntitySource(entity.id, entity.name, entity.meta, entity.crud, entity.hasCRUD, identity)
		if err != nil {
			return snapshotError("canonical_invalid", base, sourceName)
		}
		if provenance.SHA256(canonicalEntity) != entityDigest {
			return snapshotError("source_closure_invalid", base+"/sourceRef", sourceName)
		}
		identityFields := 0
		for fieldIndex, field := range entity.fields {
			fieldBase := base + "/fields/" + strconv.Itoa(fieldIndex)
			fieldDigest, ok := available[field.sourceRef.String()]
			if !ok {
				return snapshotError("source_closure_invalid", fieldBase+"/sourceRef", sourceName)
			}
			if field.sourceRef.Path() != entity.sourceRef.Path() || field.sourceRef.Fragment() != "schema:"+entity.name+"/field:"+field.name {
				return snapshotError("source_closure_invalid", fieldBase+"/sourceRef", sourceName)
			}
			used[field.sourceRef.String()] = struct{}{}
			if field.isIdentity {
				identityFields++
			}
			ownerField := &fieldState{
				id: field.id, name: field.name, typeID: field.typeID,
				enumValues: append([]EnumValue(nil), field.enumValues...), optional: field.optional,
				nillable: field.nillable, immutable: field.immutable, hasDefault: field.hasDefault,
				sensitive: field.sensitive, isIdentity: field.isIdentity, isTenantField: field.isTenantField, meta: cloneFieldMeta(field.meta),
			}
			canonicalField, err := canonicalFieldSource(entity.id, ownerField)
			if err != nil {
				return snapshotError("canonical_invalid", fieldBase, sourceName)
			}
			if provenance.SHA256(canonicalField) != fieldDigest {
				return snapshotError("source_closure_invalid", fieldBase+"/sourceRef", sourceName)
			}
			if snapshotFieldPolicyConflict(entity, field) {
				return snapshotError("source_closure_invalid", fieldBase+"/fieldMeta", sourceName)
			}
		}
		for edgeIndex, edge := range entity.edges {
			edgeBase := base + "/edges/" + strconv.Itoa(edgeIndex)
			digest, ok := available[edge.sourceRef.String()]
			if !ok || edge.sourceRef.Path() != entity.sourceRef.Path() || edge.sourceRef.Fragment() != "schema:"+entity.name+"/edge:"+edge.name {
				return snapshotError("source_closure_invalid", edgeBase+"/sourceRef", sourceName)
			}
			used[edge.sourceRef.String()] = struct{}{}
			canonicalEdge, err := canonicalEdgeSource(entity.id, edge)
			if err != nil || provenance.SHA256(canonicalEdge) != digest {
				return snapshotError("source_closure_invalid", edgeBase+"/sourceRef", sourceName)
			}
			edge.source = provenance.Source{Ref: edge.sourceRef, Digest: digest}
			edge.canonicalSource = append([]byte(nil), canonicalEdge...)
			target := entities[edge.targetEntityID]
			if target == nil {
				return snapshotError("source_closure_invalid", edgeBase+"/targetEntityId", sourceName)
			}
			boundOwner := entity
			if edge.direction == EdgeDirectionFrom {
				boundOwner = target
			}
			if edge.boundFieldID != "" && snapshotEntityFieldByID(boundOwner, edge.boundFieldID) == nil {
				return snapshotError("source_closure_invalid", edgeBase+"/boundFieldId", sourceName)
			}
			if edge.inverseName != "" {
				inverse := snapshotEdgeByName(target, edge.inverseName)
				if inverse == nil || inverse.targetEntityID != entity.id || inverse.inverseName != edge.name || inverse.direction == edge.direction || (edge.boundFieldID == "") != (inverse.boundFieldID == "") || edge.boundFieldID != "" && inverse.boundFieldID != edge.boundFieldID {
					return snapshotError("source_closure_invalid", edgeBase+"/inverseName", sourceName)
				}
			}
		}
		for _, field := range entity.fields {
			bound := snapshotEdgesForField(entity, field.id)
			if field.meta.LogicalReference != nil && len(bound) != 0 {
				return snapshotError("source_closure_invalid", base+"/fields", sourceName)
			}
			if field.meta.PhysicalDisplay != nil {
				if len(bound) != 1 {
					return snapshotError("source_closure_invalid", base+"/fields", sourceName)
				}
				target := entities[bound[0].targetEntityID]
				if target == nil || !snapshotHasField(target, field.meta.PhysicalDisplay.Field) {
					return snapshotError("source_closure_invalid", base+"/fields", sourceName)
				}
			}
		}
		switch entity.identity.kind {
		case IdentityImplicit:
			if entity.identity.name != "id" || entity.identity.typeID != ScalarInt64 || entity.identity.sourceRef != entity.sourceRef || identityFields != 0 {
				return snapshotError("source_closure_invalid", base+"/identity", sourceName)
			}
		case IdentityField:
			field := snapshotFieldByName(entity, entity.identity.name)
			if field == nil || !field.isIdentity || identityFields != 1 || field.typeID != entity.identity.typeID || field.sourceRef != entity.identity.sourceRef {
				return snapshotError("source_closure_invalid", base+"/identity", sourceName)
			}
		}
	}
	if snapshotLocalizedConflict(state.entities) {
		return snapshotError("source_closure_invalid", "/entities", sourceName)
	}
	if len(used) != len(available) {
		return snapshotError("source_closure_invalid", "/sources", sourceName)
	}
	return nil
}

func snapshotFieldPolicyConflict(entity *snapshotEntityState, field *snapshotFieldState) bool {
	if snapshotSensitiveTupleConflict(field.sensitive, field.meta.Visibility, field.meta.UIHint) {
		return true
	}
	policy := field.meta.CRUD
	if field.isTenantField {
		return policy != nil
	}
	if entity.hasCRUD != (policy != nil) {
		return true
	}
	if policy == nil {
		return false
	}
	if field.meta.Visibility == nexaent.VisibilityInternal && (policy.Read != nexaent.ReadExclude || policy.Mutation != nexaent.MutationNone) {
		return true
	}
	if field.meta.Visibility == nexaent.VisibilitySensitive && policy.Read != nexaent.ReadExclude {
		return true
	}
	if field.meta.UIHint == nexaent.UIHintReadonly && policy.Mutation != nexaent.MutationNone {
		return true
	}
	if field.isIdentity && (policy.Read != nexaent.ReadInclude || policy.Mutation != nexaent.MutationNone) {
		return true
	}
	if field.immutable && (policy.Mutation == nexaent.MutationUpdate || policy.Mutation == nexaent.MutationCreateUpdate) {
		return true
	}
	return false
}

func snapshotSensitiveTupleConflict(sensitive bool, visibility nexaent.FieldVisibility, hint nexaent.UIHint) bool {
	return sensitive != (visibility == nexaent.VisibilitySensitive) || sensitive != (hint == nexaent.UIHintSensitive)
}

func snapshotLocalizedConflict(entities []*snapshotEntityState) bool {
	type text struct{ zh, en string }
	seen := map[string]text{}
	add := func(value nexaent.LocalizedText) bool {
		current := text{value.ZhCN, value.EnUS}
		old, exists := seen[value.Key]
		if exists && old != current {
			return true
		}
		seen[value.Key] = current
		return false
	}
	for _, entity := range entities {
		if add(entity.meta.Label) || add(entity.meta.Description) {
			return true
		}
		for _, field := range entity.fields {
			if add(field.meta.Label) || add(field.meta.Description) {
				return true
			}
		}
	}
	return false
}

func snapshotFieldByID(entities []*snapshotEntityState, id string) *snapshotFieldState {
	for _, entity := range entities {
		for _, field := range entity.fields {
			if field.id == id {
				return field
			}
		}
	}
	return nil
}

func snapshotEntityFieldByID(entity *snapshotEntityState, id string) *snapshotFieldState {
	for _, field := range entity.fields {
		if field.id == id {
			return field
		}
	}
	return nil
}
func snapshotEdgeByName(entity *snapshotEntityState, name string) *snapshotEdgeState {
	for _, edge := range entity.edges {
		if edge.name == name {
			return edge
		}
	}
	return nil
}
func snapshotEdgesForField(entity *snapshotEntityState, id string) []*snapshotEdgeState {
	var result []*snapshotEdgeState
	for _, edge := range entity.edges {
		if edge.boundFieldID == id {
			result = append(result, edge)
		}
	}
	return result
}

func snapshotHasField(entity *snapshotEntityState, name string) bool {
	return snapshotFieldByName(entity, name) != nil
}
func snapshotFieldByName(entity *snapshotEntityState, name string) *snapshotFieldState {
	for _, field := range entity.fields {
		if field.name == name {
			return field
		}
	}
	return nil
}

func decodeSnapshotSchemaMeta(value any) (nexaent.SchemaMeta, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nexaent.SchemaMeta{}, err
	}
	return nexaent.DecodeSchema(encoded)
}
func decodeSnapshotFieldMeta(value any) (nexaent.FieldMeta, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nexaent.FieldMeta{}, err
	}
	return nexaent.DecodeField(encoded)
}
func decodeSnapshotCRUD(value any) (nexaent.CRUDSpec, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nexaent.CRUDSpec{}, err
	}
	return nexaent.DecodeCRUD(encoded)
}

func projectSnapshotAnnotationError(sourceName, base string, err error) error {
	owner, ok := err.(*nexaent.Error)
	if !ok || owner == nil {
		return snapshotError("document_type_invalid", base, sourceName)
	}
	pointer := base + owner.Pointer()
	switch owner.Reason() {
	case "document_unknown_field", "document_required_missing", "document_type_invalid", "document_duplicate_key", "unicode_invalid":
		return snapshotError(owner.Reason(), pointer, sourceName)
	case "source_ref_invalid", "source_digest_invalid":
		return snapshotError(owner.Reason(), pointer, sourceName)
	default:
		return snapshotError("document_type_invalid", pointer, sourceName)
	}
}

func validScalar(value ScalarType) bool {
	switch value {
	case ScalarBool, ScalarInt64, ScalarUint64, ScalarFloat, ScalarDouble, ScalarString, ScalarBytes, ScalarTimestamp, ScalarUUID, ScalarJSON, ScalarEnum:
		return true
	default:
		return false
	}
}

func projectStrictDocumentError(source string, err error) error {
	var documentError *strictdoc.Error
	if errors.As(err, &documentError) {
		return snapshotError(documentError.Code, documentError.Pointer, source)
	}
	return snapshotError("document_invalid", "", source)
}

func firstUnknown(document strictdoc.Document, object map[string]any, base string, allowed ...string) string {
	valid := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		valid[name] = struct{}{}
	}
	type candidate struct {
		pointer      string
		line, column int
	}
	var candidates []candidate
	for name := range object {
		if _, ok := valid[name]; ok {
			continue
		}
		pointer := base + "/" + escapePointer(name)
		line, column, _ := document.Location(pointer)
		candidates = append(candidates, candidate{pointer: pointer, line: line, column: column})
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].line == candidates[j].line {
			return candidates[i].column < candidates[j].column
		}
		return candidates[i].line < candidates[j].line
	})
	return candidates[0].pointer
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
