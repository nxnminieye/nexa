package entity

import (
	"encoding/json"

	"github.com/nxnminieye/nexa/nexaent"
)

const (
	entityNodeAPIVersion = "nexa.dev/entity-node/v1"
	fieldNodeAPIVersion  = "nexa.dev/entity-field-node/v2"
	edgeNodeAPIVersion   = "nexa.dev/entity-edge-node/v1"
)

type canonicalNodeIdentity struct {
	Kind IdentityKind `json:"kind"`
	Name string       `json:"name"`
	Type ScalarType   `json:"type"`
}

type canonicalEntityNode struct {
	APIVersion string                `json:"apiVersion"`
	CRUD       *json.RawMessage      `json:"crud,omitempty"`
	ID         string                `json:"id"`
	Identity   canonicalNodeIdentity `json:"identity"`
	Kind       string                `json:"kind"`
	Name       string                `json:"name"`
	SchemaMeta json.RawMessage       `json:"schemaMeta"`
}

type canonicalFieldNode struct {
	APIVersion    string               `json:"apiVersion"`
	EntityID      string               `json:"entityId"`
	EnumValues    []canonicalEnumValue `json:"enumValues"`
	FieldMeta     json.RawMessage      `json:"fieldMeta"`
	HasDefault    bool                 `json:"hasDefault"`
	ID            string               `json:"id"`
	Immutable     bool                 `json:"immutable"`
	IsIdentity    bool                 `json:"isIdentity"`
	IsTenantField bool                 `json:"isTenantField"`
	Kind          string               `json:"kind"`
	Name          string               `json:"name"`
	Nillable      bool                 `json:"nillable"`
	Optional      bool                 `json:"optional"`
	Sensitive     bool                 `json:"sensitive"`
	Type          ScalarType           `json:"type"`
}
type canonicalEdgeNode struct {
	APIVersion     string        `json:"apiVersion"`
	BoundFieldID   string        `json:"boundFieldId,omitempty"`
	Direction      EdgeDirection `json:"direction"`
	EntityID       string        `json:"entityId"`
	ID             string        `json:"id"`
	InverseName    string        `json:"inverseName,omitempty"`
	Kind           string        `json:"kind"`
	Name           string        `json:"name"`
	TargetEntityID string        `json:"targetEntityId"`
	Optional       bool          `json:"optional"`
	Unique         bool          `json:"unique"`
}

func canonicalEntitySource(id, name string, meta nexaent.SchemaMeta, crud nexaent.CRUDSpec, hasCRUD bool, identity *identityState) ([]byte, error) {
	schemaMeta, err := canonicalSchemaMeta(meta)
	if err != nil {
		return nil, err
	}
	document := canonicalEntityNode{
		APIVersion: entityNodeAPIVersion,
		ID:         id,
		Identity:   canonicalNodeIdentity{Kind: identity.kind, Name: identity.name, Type: identity.typeID},
		Kind:       "Entity",
		Name:       name,
		SchemaMeta: schemaMeta,
	}
	if hasCRUD {
		encoded, err := canonicalCRUD(crud)
		if err != nil {
			return nil, err
		}
		value := json.RawMessage(encoded)
		document.CRUD = &value
	}
	return canonicalize(document)
}

func canonicalFieldSource(entityID string, field *fieldState) ([]byte, error) {
	meta, err := canonicalFieldMeta(field.meta)
	if err != nil {
		return nil, err
	}
	return canonicalize(canonicalFieldNode{
		APIVersion:    fieldNodeAPIVersion,
		EntityID:      entityID,
		EnumValues:    canonicalEnumValues(field.enumValues),
		FieldMeta:     meta,
		HasDefault:    field.hasDefault,
		ID:            field.id,
		Immutable:     field.immutable,
		IsIdentity:    field.isIdentity,
		IsTenantField: field.isTenantField,
		Kind:          "Field",
		Name:          field.name,
		Nillable:      field.nillable,
		Optional:      field.optional,
		Sensitive:     field.sensitive,
		Type:          field.typeID,
	})
}

func canonicalEdgeSource(entityID string, edge *snapshotEdgeState) ([]byte, error) {
	return canonicalize(canonicalEdgeNode{APIVersion: edgeNodeAPIVersion, BoundFieldID: edge.boundFieldID, Direction: edge.direction, EntityID: entityID, ID: edge.id, InverseName: edge.inverseName, Kind: "Edge", Name: edge.name, TargetEntityID: edge.targetEntityID, Optional: edge.optional, Unique: edge.unique})
}
