// Package entityvalue owns the immutable value state behind EntityIR.
// Its exported names remain internal to the generation tree by Go's internal boundary.
package entityvalue

import (
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	apiVersion          = "nexa.dev/entity-ir/v2"
	kind                = "EntityIR"
	sourceSetAPIVersion = "nexa.dev/entity-source-set/v1"
)

// Projection is the sole mutable input accepted by NewDocument.
type Projection struct {
	Entities               []EntityProjection
	ExecutionModuleSources []provenance.Source
}

type EntityProjection struct {
	Name      string
	SourceRef provenance.SourceRef
	Meta      nexaent.SchemaMeta
	CRUD      *nexaent.CRUDSpec
	Identity  IdentityProjection
	Fields    []FieldProjection
	Edges     []EdgeProjection
}

type EdgeProjection struct {
	Name           string
	SourceRef      provenance.SourceRef
	TargetEntityID string
	Direction      string
	InverseName    string
	BoundFieldID   string
	Optional       bool
	Unique         bool
}

type IdentityProjection struct {
	Kind string
	Name string
	Type string
}

type FieldProjection struct {
	Name          string
	SourceRef     provenance.SourceRef
	Type          string
	EnumValues    []EnumValue
	Optional      bool
	Nillable      bool
	Immutable     bool
	HasDefault    bool
	Sensitive     bool
	IsIdentity    bool
	IsTenantField bool
	Meta          nexaent.FieldMeta
}

type EnumValue struct {
	Name  string
	Value string
}

type Document struct{ state *documentState }
type Entity struct{ state *entityState }
type Field struct{ state *fieldState }
type Edge struct{ state *edgeState }
type Identity struct{ state *identityState }

type documentState struct {
	sourceDigest           provenance.Digest
	sources                []provenance.Source
	executionModuleSources []provenance.Source
	entities               []*entityState
	canonical              []byte
}

type entityState struct {
	id              string
	name            string
	source          provenance.Source
	canonicalSource []byte
	meta            nexaent.SchemaMeta
	crud            nexaent.CRUDSpec
	hasCRUD         bool
	identity        *identityState
	fields          []*fieldState
	edges           []*edgeState
}

type edgeState struct {
	id, name, targetEntityID, direction, inverseName, boundFieldID string
	source                                                         provenance.Source
	canonicalSource                                                []byte
	optional, unique                                               bool
}

type fieldState struct {
	id              string
	name            string
	source          provenance.Source
	canonicalSource []byte
	typeID          string
	enumValues      []EnumValue
	optional        bool
	nillable        bool
	immutable       bool
	hasDefault      bool
	sensitive       bool
	isIdentity      bool
	isTenantField   bool
	meta            nexaent.FieldMeta
}

type identityState struct {
	kind   string
	name   string
	typeID string
	source provenance.Source
}

type Error struct {
	reason  string
	pointer string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return "invalid EntityIR projection"
}

func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}

func invalid(reason, pointer string) *Error { return &Error{reason: reason, pointer: pointer} }

// UnsupportedFieldType reports an exact Ent model that standard CRUD cannot use.
func UnsupportedFieldType(pointer string) *Error {
	return invalid("field_type_unsupported", pointer)
}

func (d Document) Valid() bool { return d.state != nil }
func (d Document) APIVersion() string {
	if d.state == nil {
		return ""
	}
	return apiVersion
}
func (d Document) SourceDigest() provenance.Digest {
	if d.state == nil {
		return provenance.Digest{}
	}
	return d.state.sourceDigest
}
func (d Document) Sources() []provenance.Source {
	if d.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), d.state.sources...)
}
func (d Document) ExecutionModuleSources() []provenance.Source {
	if d.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), d.state.executionModuleSources...)
}
func (d Document) Source(ref provenance.SourceRef) (provenance.Source, bool) {
	if d.state == nil {
		return provenance.Source{}, false
	}
	for _, source := range d.state.sources {
		if source.Ref == ref {
			return source, true
		}
	}
	return provenance.Source{}, false
}
func (d Document) CanonicalJSON() []byte {
	if d.state == nil {
		return nil
	}
	return append([]byte(nil), d.state.canonical...)
}
func (d Document) Entities() []Entity {
	if d.state == nil {
		return nil
	}
	result := make([]Entity, len(d.state.entities))
	for i, state := range d.state.entities {
		result[i] = Entity{state: state}
	}
	return result
}
func (d Document) Entity(id string) (Entity, bool) {
	if d.state == nil {
		return Entity{}, false
	}
	for _, state := range d.state.entities {
		if state.id == id {
			return Entity{state: state}, true
		}
	}
	return Entity{}, false
}

func (e Entity) Valid() bool { return e.state != nil }
func (e Entity) ID() string {
	if e.state == nil {
		return ""
	}
	return e.state.id
}
func (e Entity) Name() string {
	if e.state == nil {
		return ""
	}
	return e.state.name
}
func (e Entity) Source() provenance.Source {
	if e.state == nil {
		return provenance.Source{}
	}
	return e.state.source
}
func (e Entity) CanonicalSourceJSON() []byte {
	if e.state == nil {
		return nil
	}
	return append([]byte(nil), e.state.canonicalSource...)
}
func (e Entity) Meta() nexaent.SchemaMeta {
	if e.state == nil {
		return nexaent.SchemaMeta{}
	}
	return cloneSchemaMeta(e.state.meta)
}
func (e Entity) CRUD() (nexaent.CRUDSpec, bool) {
	if e.state == nil || !e.state.hasCRUD {
		return nexaent.CRUDSpec{}, false
	}
	return e.state.crud, true
}
func (e Entity) Identity() Identity {
	if e.state == nil {
		return Identity{}
	}
	return Identity{state: e.state.identity}
}
func (e Entity) Fields() []Field {
	if e.state == nil {
		return nil
	}
	result := make([]Field, len(e.state.fields))
	for i, state := range e.state.fields {
		result[i] = Field{state: state}
	}
	return result
}
func (e Entity) Field(id string) (Field, bool) {
	if e.state == nil {
		return Field{}, false
	}
	for _, state := range e.state.fields {
		if state.id == id {
			return Field{state: state}, true
		}
	}
	return Field{}, false
}
func (e Entity) Edges() []Edge {
	if e.state == nil {
		return nil
	}
	result := make([]Edge, len(e.state.edges))
	for i, state := range e.state.edges {
		result[i] = Edge{state: state}
	}
	return result
}
func (e Entity) Edge(id string) (Edge, bool) {
	if e.state == nil {
		return Edge{}, false
	}
	for _, state := range e.state.edges {
		if state.id == id {
			return Edge{state: state}, true
		}
	}
	return Edge{}, false
}

func (e Edge) ID() string {
	if e.state == nil {
		return ""
	}
	return e.state.id
}
func (e Edge) Name() string {
	if e.state == nil {
		return ""
	}
	return e.state.name
}
func (e Edge) Source() provenance.Source {
	if e.state == nil {
		return provenance.Source{}
	}
	return e.state.source
}
func (e Edge) SourceRef() provenance.SourceRef {
	if e.state == nil {
		return provenance.SourceRef{}
	}
	return e.state.source.Ref
}
func (e Edge) CanonicalSourceJSON() []byte {
	if e.state == nil {
		return nil
	}
	return append([]byte(nil), e.state.canonicalSource...)
}
func (e Edge) TargetEntityID() string {
	if e.state == nil {
		return ""
	}
	return e.state.targetEntityID
}
func (e Edge) Direction() string {
	if e.state == nil {
		return ""
	}
	return e.state.direction
}
func (e Edge) InverseName() (string, bool) {
	if e.state == nil || e.state.inverseName == "" {
		return "", false
	}
	return e.state.inverseName, true
}
func (e Edge) BoundFieldID() (string, bool) {
	if e.state == nil || e.state.boundFieldID == "" {
		return "", false
	}
	return e.state.boundFieldID, true
}
func (e Edge) Optional() bool { return e.state != nil && e.state.optional }
func (e Edge) Unique() bool   { return e.state != nil && e.state.unique }

func (i Identity) Valid() bool { return i.state != nil }
func (i Identity) Kind() string {
	if i.state == nil {
		return ""
	}
	return i.state.kind
}
func (i Identity) Name() string {
	if i.state == nil {
		return ""
	}
	return i.state.name
}
func (i Identity) Type() string {
	if i.state == nil {
		return ""
	}
	return i.state.typeID
}
func (i Identity) Source() provenance.Source {
	if i.state == nil {
		return provenance.Source{}
	}
	return i.state.source
}

func (f Field) Valid() bool { return f.state != nil }
func (f Field) ID() string {
	if f.state == nil {
		return ""
	}
	return f.state.id
}
func (f Field) Name() string {
	if f.state == nil {
		return ""
	}
	return f.state.name
}
func (f Field) Source() provenance.Source {
	if f.state == nil {
		return provenance.Source{}
	}
	return f.state.source
}
func (f Field) CanonicalSourceJSON() []byte {
	if f.state == nil {
		return nil
	}
	return append([]byte(nil), f.state.canonicalSource...)
}
func (f Field) Type() string {
	if f.state == nil {
		return ""
	}
	return f.state.typeID
}
func (f Field) EnumValues() []EnumValue {
	if f.state == nil {
		return nil
	}
	return append([]EnumValue(nil), f.state.enumValues...)
}
func (f Field) Optional() bool      { return f.state != nil && f.state.optional }
func (f Field) Nillable() bool      { return f.state != nil && f.state.nillable }
func (f Field) Immutable() bool     { return f.state != nil && f.state.immutable }
func (f Field) HasDefault() bool    { return f.state != nil && f.state.hasDefault }
func (f Field) Sensitive() bool     { return f.state != nil && f.state.sensitive }
func (f Field) IsIdentity() bool    { return f.state != nil && f.state.isIdentity }
func (f Field) IsTenantField() bool { return f.state != nil && f.state.isTenantField }
func (f Field) Meta() nexaent.FieldMeta {
	if f.state == nil {
		return nexaent.FieldMeta{}
	}
	return cloneFieldMeta(f.state.meta)
}

func cloneSchemaMeta(meta nexaent.SchemaMeta) nexaent.SchemaMeta { return meta }
func cloneFieldMeta(meta nexaent.FieldMeta) nexaent.FieldMeta {
	result := meta
	if meta.PhysicalDisplay != nil {
		value := *meta.PhysicalDisplay
		result.PhysicalDisplay = &value
	}
	if meta.LogicalReference != nil {
		value := *meta.LogicalReference
		result.LogicalReference = &value
	}
	if meta.CRUD != nil {
		value := *meta.CRUD
		result.CRUD = &value
	}
	return result
}
