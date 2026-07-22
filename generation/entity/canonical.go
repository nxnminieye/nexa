package entity

import (
	"encoding/json"
	"sort"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

type canonicalSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type canonicalSourceSet struct {
	APIVersion string            `json:"apiVersion"`
	Sources    []canonicalSource `json:"sources"`
}

type canonicalIdentity struct {
	Kind      IdentityKind `json:"kind"`
	Name      string       `json:"name"`
	Type      ScalarType   `json:"type"`
	SourceRef string       `json:"sourceRef"`
}

type canonicalEnumValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type canonicalField struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	SourceRef     string               `json:"sourceRef"`
	Type          ScalarType           `json:"type"`
	EnumValues    []canonicalEnumValue `json:"enumValues"`
	Optional      bool                 `json:"optional"`
	Nillable      bool                 `json:"nillable"`
	Immutable     bool                 `json:"immutable"`
	HasDefault    bool                 `json:"hasDefault"`
	Sensitive     bool                 `json:"sensitive"`
	IsIdentity    bool                 `json:"isIdentity"`
	IsTenantField bool                 `json:"isTenantField"`
	FieldMeta     json.RawMessage      `json:"fieldMeta"`
}
type canonicalEdge struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	SourceRef      string        `json:"sourceRef"`
	TargetEntityID string        `json:"targetEntityId"`
	Direction      EdgeDirection `json:"direction"`
	InverseName    string        `json:"inverseName,omitempty"`
	BoundFieldID   string        `json:"boundFieldId,omitempty"`
	Optional       bool          `json:"optional"`
	Unique         bool          `json:"unique"`
}

type canonicalEntity struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	SourceRef  string            `json:"sourceRef"`
	SchemaMeta json.RawMessage   `json:"schemaMeta"`
	CRUD       *json.RawMessage  `json:"crud,omitempty"`
	Identity   canonicalIdentity `json:"identity"`
	Fields     []canonicalField  `json:"fields"`
	Edges      []canonicalEdge   `json:"edges"`
}

type canonicalDocument struct {
	APIVersion   string            `json:"apiVersion"`
	Kind         string            `json:"kind"`
	SourceDigest string            `json:"sourceDigest"`
	Sources      []canonicalSource `json:"sources"`
	Entities     []canonicalEntity `json:"entities"`
}

func CanonicalJSON(document Document) ([]byte, error) {
	if !document.state.Valid() || document.state.APIVersion() != APIVersion {
		return nil, irError("canonical_invalid", "/document", "")
	}
	return document.state.CanonicalJSON(), nil
}

func (s Snapshot) CanonicalJSON() ([]byte, error) {
	if s.state == nil || s.state.apiVersion != APIVersion {
		return nil, snapshotError("canonical_invalid", "", "")
	}
	return append([]byte(nil), s.state.canonical...), nil
}

func computeSourceDigest(sources []provenance.Source) (provenance.Digest, error) {
	document := canonicalSourceSet{APIVersion: SourceSetAPIVersion, Sources: canonicalSources(sources)}
	encoded, err := canonicalize(document)
	if err != nil {
		return provenance.Digest{}, err
	}
	return provenance.SHA256(encoded), nil
}

func canonicalSources(sources []provenance.Source) []canonicalSource {
	result := make([]canonicalSource, len(sources))
	for index, source := range sources {
		result[index] = canonicalSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	return result
}

func canonicalize(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

func canonicalSchemaMeta(meta nexaent.SchemaMeta) ([]byte, error) {
	return nexaent.Schema(meta).CanonicalJSON()
}

func canonicalFieldMeta(meta nexaent.FieldMeta) ([]byte, error) {
	return nexaent.Field(meta).CanonicalJSON()
}

func canonicalCRUD(spec nexaent.CRUDSpec) ([]byte, error) {
	return nexaent.CRUD(spec.Operations()...).CanonicalJSON()
}

func canonicalDocumentForSnapshot(state *snapshotState) ([]byte, error) {
	document := canonicalDocument{
		APIVersion:   APIVersion,
		Kind:         Kind,
		SourceDigest: state.sourceDigest.String(),
		Sources:      canonicalSources(state.sources),
		Entities:     make([]canonicalEntity, len(state.entities)),
	}
	entities := append([]*snapshotEntityState(nil), state.entities...)
	sort.Slice(entities, func(i, j int) bool { return entities[i].id < entities[j].id })
	for index, entity := range entities {
		schemaMeta, err := canonicalSchemaMeta(entity.meta)
		if err != nil {
			return nil, err
		}
		item := canonicalEntity{
			ID: entity.id, Name: entity.name, SourceRef: entity.sourceRef.String(),
			SchemaMeta: schemaMeta,
			Identity:   canonicalIdentity{Kind: entity.identity.kind, Name: entity.identity.name, Type: entity.identity.typeID, SourceRef: entity.identity.sourceRef.String()},
			Fields:     make([]canonicalField, len(entity.fields)),
			Edges:      make([]canonicalEdge, len(entity.edges)),
		}
		if entity.hasCRUD {
			crud, err := canonicalCRUD(entity.crud)
			if err != nil {
				return nil, err
			}
			value := json.RawMessage(crud)
			item.CRUD = &value
		}
		fields := append([]*snapshotFieldState(nil), entity.fields...)
		sort.Slice(fields, func(i, j int) bool { return fields[i].id < fields[j].id })
		for fieldIndex, field := range fields {
			meta, err := canonicalFieldMeta(field.meta)
			if err != nil {
				return nil, err
			}
			enums := append([]EnumValue(nil), field.enumValues...)
			sort.Slice(enums, func(i, j int) bool {
				if enums[i].Name == enums[j].Name {
					return enums[i].Value < enums[j].Value
				}
				return enums[i].Name < enums[j].Name
			})
			item.Fields[fieldIndex] = canonicalField{
				ID: field.id, Name: field.name, SourceRef: field.sourceRef.String(), Type: field.typeID,
				EnumValues: canonicalEnumValues(enums), Optional: field.optional, Nillable: field.nillable,
				Immutable: field.immutable, HasDefault: field.hasDefault, Sensitive: field.sensitive,
				IsIdentity: field.isIdentity, IsTenantField: field.isTenantField, FieldMeta: meta,
			}
		}
		edges := append([]*snapshotEdgeState(nil), entity.edges...)
		sort.Slice(edges, func(i, j int) bool { return edges[i].id < edges[j].id })
		for edgeIndex, edge := range edges {
			item.Edges[edgeIndex] = canonicalEdge{ID: edge.id, Name: edge.name, SourceRef: edge.sourceRef.String(), TargetEntityID: edge.targetEntityID, Direction: edge.direction, InverseName: edge.inverseName, BoundFieldID: edge.boundFieldID, Optional: edge.optional, Unique: edge.unique}
		}
		document.Entities[index] = item
	}
	return canonicalize(document)
}

func canonicalEnumValues(values []EnumValue) []canonicalEnumValue {
	result := make([]canonicalEnumValue, len(values))
	for index, value := range values {
		result[index] = canonicalEnumValue{Name: value.Name, Value: value.Value}
	}
	return result
}
