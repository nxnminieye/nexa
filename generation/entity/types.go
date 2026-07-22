// Package entity exposes immutable EntityIR documents and read-only snapshots.
package entity

import (
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	APIVersion          = "nexa.dev/entity-ir/v2"
	Kind                = "EntityIR"
	SourceSetAPIVersion = "nexa.dev/entity-source-set/v1"
)

type ScalarType string

const (
	ScalarBool      ScalarType = "bool"
	ScalarInt64     ScalarType = "int64"
	ScalarUint64    ScalarType = "uint64"
	ScalarFloat     ScalarType = "float"
	ScalarDouble    ScalarType = "double"
	ScalarString    ScalarType = "string"
	ScalarBytes     ScalarType = "bytes"
	ScalarTimestamp ScalarType = "timestamp"
	ScalarUUID      ScalarType = "uuid"
	ScalarJSON      ScalarType = "json"
	ScalarEnum      ScalarType = "enum"
)

type IdentityKind string

const (
	IdentityImplicit IdentityKind = "implicit"
	IdentityField    IdentityKind = "field"
)

type EdgeDirection string

const (
	EdgeDirectionTo   EdgeDirection = "to"
	EdgeDirectionFrom EdgeDirection = "from"
)

type EnumValue struct{ Name, Value string }

type Document struct{ state entityvalue.Document }
type Entity struct{ state entityvalue.Entity }
type Field struct{ state entityvalue.Field }
type Edge struct{ state entityvalue.Edge }
type Identity struct{ state entityvalue.Identity }

type Snapshot struct {
	state  *snapshotState
	marker snapshotMarker
}
type snapshotMarker struct{ _ [0]func() }
type SnapshotEntity struct{ state *snapshotEntityState }
type SnapshotField struct{ state *snapshotFieldState }
type SnapshotEdge struct{ state *snapshotEdgeState }
type SnapshotIdentity struct{ state *snapshotIdentityState }

type snapshotState struct {
	apiVersion   string
	sourceDigest provenance.Digest
	sources      []provenance.Source
	entities     []*snapshotEntityState
	canonical    []byte
}
type snapshotEntityState struct {
	id, name  string
	sourceRef provenance.SourceRef
	meta      nexaent.SchemaMeta
	crud      nexaent.CRUDSpec
	hasCRUD   bool
	identity  *snapshotIdentityState
	fields    []*snapshotFieldState
	edges     []*snapshotEdgeState
}
type snapshotEdgeState struct {
	id, name, targetEntityID, inverseName, boundFieldID string
	sourceRef                                           provenance.SourceRef
	source                                              provenance.Source
	canonicalSource                                     []byte
	direction                                           EdgeDirection
	optional, unique                                    bool
}
type snapshotFieldState struct {
	id, name                                                                        string
	sourceRef                                                                       provenance.SourceRef
	typeID                                                                          ScalarType
	enumValues                                                                      []EnumValue
	optional, nillable, immutable, hasDefault, sensitive, isIdentity, isTenantField bool
	meta                                                                            nexaent.FieldMeta
}
type snapshotIdentityState struct {
	kind      IdentityKind
	name      string
	typeID    ScalarType
	sourceRef provenance.SourceRef
}

// These private node forms are used only to independently revalidate Snapshot node digests.
type identityState struct {
	kind   IdentityKind
	name   string
	typeID ScalarType
	source provenance.Source
}
type fieldState struct {
	id, name                                                                        string
	source                                                                          provenance.Source
	canonicalSource                                                                 []byte
	typeID                                                                          ScalarType
	enumValues                                                                      []EnumValue
	optional, nillable, immutable, hasDefault, sensitive, isIdentity, isTenantField bool
	meta                                                                            nexaent.FieldMeta
}

func (d Document) APIVersion() string              { return d.state.APIVersion() }
func (d Document) SourceDigest() provenance.Digest { return d.state.SourceDigest() }
func (d Document) Sources() []provenance.Source    { return d.state.Sources() }
func (d Document) ExecutionModuleSources() []provenance.Source {
	return d.state.ExecutionModuleSources()
}
func (d Document) Source(ref provenance.SourceRef) (provenance.Source, bool) {
	return d.state.Source(ref)
}
func (d Document) Entities() []Entity {
	values := d.state.Entities()
	result := make([]Entity, len(values))
	for i, value := range values {
		result[i] = Entity{state: value}
	}
	return result
}
func (d Document) Entity(id string) (Entity, bool) {
	value, ok := d.state.Entity(id)
	return Entity{state: value}, ok
}

func (e Entity) ID() string                     { return e.state.ID() }
func (e Entity) Name() string                   { return e.state.Name() }
func (e Entity) Source() provenance.Source      { return e.state.Source() }
func (e Entity) CanonicalSourceJSON() []byte    { return e.state.CanonicalSourceJSON() }
func (e Entity) Meta() nexaent.SchemaMeta       { return e.state.Meta() }
func (e Entity) CRUD() (nexaent.CRUDSpec, bool) { return e.state.CRUD() }
func (e Entity) Identity() Identity             { return Identity{state: e.state.Identity()} }
func (e Entity) Fields() []Field {
	values := e.state.Fields()
	result := make([]Field, len(values))
	for i, value := range values {
		result[i] = Field{state: value}
	}
	return result
}
func (e Entity) Field(id string) (Field, bool) {
	value, ok := e.state.Field(id)
	return Field{state: value}, ok
}
func (e Entity) Edges() []Edge {
	values := e.state.Edges()
	result := make([]Edge, len(values))
	for i, value := range values {
		result[i] = Edge{state: value}
	}
	return result
}
func (e Entity) Edge(id string) (Edge, bool) {
	value, ok := e.state.Edge(id)
	return Edge{state: value}, ok
}

func (e Edge) ID() string                      { return e.state.ID() }
func (e Edge) Name() string                    { return e.state.Name() }
func (e Edge) Source() provenance.Source       { return e.state.Source() }
func (e Edge) SourceRef() provenance.SourceRef { return e.state.SourceRef() }
func (e Edge) CanonicalSourceJSON() []byte     { return e.state.CanonicalSourceJSON() }
func (e Edge) TargetEntityID() string          { return e.state.TargetEntityID() }
func (e Edge) Direction() EdgeDirection        { return EdgeDirection(e.state.Direction()) }
func (e Edge) InverseName() (string, bool)     { return e.state.InverseName() }
func (e Edge) BoundFieldID() (string, bool)    { return e.state.BoundFieldID() }
func (e Edge) Optional() bool                  { return e.state.Optional() }
func (e Edge) Unique() bool                    { return e.state.Unique() }

func (i Identity) Kind() IdentityKind        { return IdentityKind(i.state.Kind()) }
func (i Identity) Name() string              { return i.state.Name() }
func (i Identity) Type() ScalarType          { return ScalarType(i.state.Type()) }
func (i Identity) Source() provenance.Source { return i.state.Source() }

func (f Field) ID() string                  { return f.state.ID() }
func (f Field) Name() string                { return f.state.Name() }
func (f Field) Source() provenance.Source   { return f.state.Source() }
func (f Field) CanonicalSourceJSON() []byte { return f.state.CanonicalSourceJSON() }
func (f Field) Type() ScalarType            { return ScalarType(f.state.Type()) }
func (f Field) EnumValues() []EnumValue {
	values := f.state.EnumValues()
	result := make([]EnumValue, len(values))
	for i, value := range values {
		result[i] = EnumValue{Name: value.Name, Value: value.Value}
	}
	return result
}
func (f Field) Optional() bool          { return f.state.Optional() }
func (f Field) Nillable() bool          { return f.state.Nillable() }
func (f Field) Immutable() bool         { return f.state.Immutable() }
func (f Field) HasDefault() bool        { return f.state.HasDefault() }
func (f Field) Sensitive() bool         { return f.state.Sensitive() }
func (f Field) IsIdentity() bool        { return f.state.IsIdentity() }
func (f Field) IsTenantField() bool     { return f.state.IsTenantField() }
func (f Field) Meta() nexaent.FieldMeta { return f.state.Meta() }

func (s Snapshot) APIVersion() string {
	if s.state == nil {
		return ""
	}
	return s.state.apiVersion
}
func (s Snapshot) SourceDigest() provenance.Digest {
	if s.state == nil {
		return provenance.Digest{}
	}
	return s.state.sourceDigest
}
func (s Snapshot) ProjectedSources() []provenance.Source {
	if s.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), s.state.sources...)
}
func (s Snapshot) Entities() []SnapshotEntity {
	if s.state == nil {
		return nil
	}
	result := make([]SnapshotEntity, len(s.state.entities))
	for i, state := range s.state.entities {
		result[i] = SnapshotEntity{state: state}
	}
	return result
}
func (s Snapshot) Entity(id string) (SnapshotEntity, bool) {
	if s.state == nil {
		return SnapshotEntity{}, false
	}
	for _, state := range s.state.entities {
		if state.id == id {
			return SnapshotEntity{state: state}, true
		}
	}
	return SnapshotEntity{}, false
}

func (e SnapshotEntity) ID() string {
	if e.state == nil {
		return ""
	}
	return e.state.id
}
func (e SnapshotEntity) Name() string {
	if e.state == nil {
		return ""
	}
	return e.state.name
}
func (e SnapshotEntity) SourceRef() provenance.SourceRef {
	if e.state == nil {
		return provenance.SourceRef{}
	}
	return e.state.sourceRef
}
func (e SnapshotEntity) Meta() nexaent.SchemaMeta {
	if e.state == nil {
		return nexaent.SchemaMeta{}
	}
	return cloneSchemaMeta(e.state.meta)
}
func (e SnapshotEntity) CRUD() (nexaent.CRUDSpec, bool) {
	if e.state == nil || !e.state.hasCRUD {
		return nexaent.CRUDSpec{}, false
	}
	return e.state.crud, true
}
func (e SnapshotEntity) Identity() SnapshotIdentity {
	if e.state == nil {
		return SnapshotIdentity{}
	}
	return SnapshotIdentity{state: e.state.identity}
}
func (e SnapshotEntity) Fields() []SnapshotField {
	if e.state == nil {
		return nil
	}
	result := make([]SnapshotField, len(e.state.fields))
	for i, state := range e.state.fields {
		result[i] = SnapshotField{state: state}
	}
	return result
}
func (e SnapshotEntity) Field(id string) (SnapshotField, bool) {
	if e.state == nil {
		return SnapshotField{}, false
	}
	for _, state := range e.state.fields {
		if state.id == id {
			return SnapshotField{state: state}, true
		}
	}
	return SnapshotField{}, false
}
func (e SnapshotEntity) Edges() []SnapshotEdge {
	if e.state == nil {
		return nil
	}
	result := make([]SnapshotEdge, len(e.state.edges))
	for i, state := range e.state.edges {
		result[i] = SnapshotEdge{state: state}
	}
	return result
}
func (e SnapshotEntity) Edge(id string) (SnapshotEdge, bool) {
	if e.state == nil {
		return SnapshotEdge{}, false
	}
	for _, state := range e.state.edges {
		if state.id == id {
			return SnapshotEdge{state: state}, true
		}
	}
	return SnapshotEdge{}, false
}

func (e SnapshotEdge) ID() string {
	if e.state == nil {
		return ""
	}
	return e.state.id
}
func (e SnapshotEdge) Name() string {
	if e.state == nil {
		return ""
	}
	return e.state.name
}
func (e SnapshotEdge) SourceRef() provenance.SourceRef {
	if e.state == nil {
		return provenance.SourceRef{}
	}
	return e.state.sourceRef
}
func (e SnapshotEdge) Source() provenance.Source {
	if e.state == nil {
		return provenance.Source{}
	}
	return e.state.source
}
func (e SnapshotEdge) CanonicalSourceJSON() []byte {
	if e.state == nil {
		return nil
	}
	return append([]byte(nil), e.state.canonicalSource...)
}
func (e SnapshotEdge) TargetEntityID() string {
	if e.state == nil {
		return ""
	}
	return e.state.targetEntityID
}
func (e SnapshotEdge) Direction() EdgeDirection {
	if e.state == nil {
		return ""
	}
	return e.state.direction
}
func (e SnapshotEdge) InverseName() (string, bool) {
	if e.state == nil || e.state.inverseName == "" {
		return "", false
	}
	return e.state.inverseName, true
}
func (e SnapshotEdge) BoundFieldID() (string, bool) {
	if e.state == nil || e.state.boundFieldID == "" {
		return "", false
	}
	return e.state.boundFieldID, true
}
func (e SnapshotEdge) Optional() bool { return e.state != nil && e.state.optional }
func (e SnapshotEdge) Unique() bool   { return e.state != nil && e.state.unique }

func (f SnapshotField) ID() string {
	if f.state == nil {
		return ""
	}
	return f.state.id
}
func (f SnapshotField) Name() string {
	if f.state == nil {
		return ""
	}
	return f.state.name
}
func (f SnapshotField) SourceRef() provenance.SourceRef {
	if f.state == nil {
		return provenance.SourceRef{}
	}
	return f.state.sourceRef
}
func (f SnapshotField) Type() ScalarType {
	if f.state == nil {
		return ""
	}
	return f.state.typeID
}
func (f SnapshotField) EnumValues() []EnumValue {
	if f.state == nil {
		return nil
	}
	return append([]EnumValue(nil), f.state.enumValues...)
}
func (f SnapshotField) Optional() bool      { return f.state != nil && f.state.optional }
func (f SnapshotField) Nillable() bool      { return f.state != nil && f.state.nillable }
func (f SnapshotField) Immutable() bool     { return f.state != nil && f.state.immutable }
func (f SnapshotField) HasDefault() bool    { return f.state != nil && f.state.hasDefault }
func (f SnapshotField) Sensitive() bool     { return f.state != nil && f.state.sensitive }
func (f SnapshotField) IsIdentity() bool    { return f.state != nil && f.state.isIdentity }
func (f SnapshotField) IsTenantField() bool { return f.state != nil && f.state.isTenantField }
func (f SnapshotField) Meta() nexaent.FieldMeta {
	if f.state == nil {
		return nexaent.FieldMeta{}
	}
	return cloneFieldMeta(f.state.meta)
}

func (i SnapshotIdentity) Kind() IdentityKind {
	if i.state == nil {
		return ""
	}
	return i.state.kind
}
func (i SnapshotIdentity) Name() string {
	if i.state == nil {
		return ""
	}
	return i.state.name
}
func (i SnapshotIdentity) Type() ScalarType {
	if i.state == nil {
		return ""
	}
	return i.state.typeID
}
func (i SnapshotIdentity) SourceRef() provenance.SourceRef {
	if i.state == nil {
		return provenance.SourceRef{}
	}
	return i.state.sourceRef
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
