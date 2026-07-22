package httpapi

import (
	"context"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

const APIVersion = "nexa.dev/http-api-ir/v1"

type LoadOptions struct {
	RepositoryRoot string
	EntryFile      string
	SourceResolver SourceResolver
}

type SourceResolver interface {
	Resolve(context.Context, provenance.SourceRef, provenance.Digest) error
}

type NodeFactKind string

const (
	NodeFactNative    NodeFactKind = "native"
	NodeFactGenerated NodeFactKind = "generated"
)

type ValueKind string

const (
	ValueObject   ValueKind = "object"
	ValueScalar   ValueKind = "scalar"
	ValueRef      ValueKind = "ref"
	ValueArray    ValueKind = "array"
	ValueOptional ValueKind = "optional"
	ValueMap      ValueKind = "map"
)

type NodeProvenance struct {
	kind      NodeFactKind
	sources   []provenance.Source
	canonical []byte
}

func (p NodeProvenance) Kind() NodeFactKind { return p.kind }
func (p NodeProvenance) Sources() []provenance.Source {
	return append([]provenance.Source(nil), p.sources...)
}
func (p NodeProvenance) NativeSource() (provenance.Source, bool) {
	if p.kind != NodeFactNative || len(p.sources) != 1 || len(p.canonical) == 0 {
		return provenance.Source{}, false
	}
	return p.sources[0], true
}
func (p NodeProvenance) CanonicalSourceJSON() ([]byte, bool) {
	if p.kind != NodeFactNative || len(p.canonical) == 0 {
		return nil, false
	}
	return append([]byte(nil), p.canonical...), true
}

type ValueType struct {
	kind         ValueKind
	name         string
	element, key *ValueType
	value        *ValueType
}

func (v ValueType) Kind() ValueKind { return v.kind }
func (v ValueType) Name() string    { return v.name }
func (v ValueType) Element() (ValueType, bool) {
	if v.element == nil {
		return ValueType{}, false
	}
	return cloneValue(*v.element), true
}
func (v ValueType) Key() (ValueType, bool) {
	if v.key == nil {
		return ValueType{}, false
	}
	return cloneValue(*v.key), true
}
func (v ValueType) Value() (ValueType, bool) {
	if v.value == nil {
		return ValueType{}, false
	}
	return cloneValue(*v.value), true
}

type Binding struct {
	location api.RequestBindingLocation
	name     string
}

func (b Binding) Location() api.RequestBindingLocation { return b.location }
func (b Binding) Name() string                         { return b.name }

type Type struct{ state *typeState }
type Field struct{ state *fieldState }
type Operation struct{ state *operationState }
type Document struct{ state *documentState }

type typeState struct {
	name       string
	shape      ValueType
	provenance NodeProvenance
	fields     []*fieldState
	fieldIndex map[string]int
}

type fieldState struct {
	ownerType  string
	path       []string
	required   bool
	valueType  ValueType
	binding    Binding
	hasBinding bool
	origin     provenance.Source
	hasOrigin  bool
	provenance NodeProvenance
}

type operationState struct {
	id, path, requestType, responseType, permission string
	method                                          api.HTTPMethod
	responseBody                                    api.ResponseBodyMode
	auth                                            Auth
	capability                                      Capability
	hasCapability                                   bool
	provenance                                      NodeProvenance
	errorProjections                                []api.ErrorProjectionSpec
}

type documentState struct {
	types          []*typeState
	operations     []*operationState
	typeIndex      map[string]int
	operationIndex map[string]int
	sources        []provenance.Source
	sourceIndex    map[string]int
}

func (d Document) APIVersion() string { return APIVersion }
func (d Document) Types() []Type {
	if d.state == nil {
		return nil
	}
	result := make([]Type, len(d.state.types))
	for i, item := range d.state.types {
		result[i] = Type{state: cloneTypeState(item)}
	}
	return result
}
func (d Document) Type(name string) (Type, bool) {
	if d.state == nil {
		return Type{}, false
	}
	index, ok := d.state.typeIndex[name]
	if !ok {
		return Type{}, false
	}
	return Type{state: cloneTypeState(d.state.types[index])}, true
}
func (d Document) Operations() []Operation {
	if d.state == nil {
		return nil
	}
	result := make([]Operation, len(d.state.operations))
	for i, item := range d.state.operations {
		result[i] = Operation{state: cloneOperationState(item)}
	}
	return result
}
func (d Document) Operation(id string) (Operation, bool) {
	if d.state == nil {
		return Operation{}, false
	}
	index, ok := d.state.operationIndex[id]
	if !ok {
		return Operation{}, false
	}
	return Operation{state: cloneOperationState(d.state.operations[index])}, true
}
func (d Document) Sources() []provenance.Source {
	if d.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), d.state.sources...)
}
func (d Document) Source(ref provenance.SourceRef) (provenance.Source, bool) {
	if d.state == nil {
		return provenance.Source{}, false
	}
	index, ok := d.state.sourceIndex[ref.String()]
	if !ok {
		return provenance.Source{}, false
	}
	return d.state.sources[index], true
}

func (t Type) Name() string {
	if t.state == nil {
		return ""
	}
	return t.state.name
}
func (t Type) Shape() ValueType {
	if t.state == nil {
		return ValueType{}
	}
	return cloneValue(t.state.shape)
}
func (t Type) Provenance() NodeProvenance {
	if t.state == nil {
		return NodeProvenance{}
	}
	return cloneProvenance(t.state.provenance)
}
func (t Type) Fields() []Field {
	if t.state == nil {
		return nil
	}
	result := make([]Field, len(t.state.fields))
	for i, item := range t.state.fields {
		result[i] = Field{state: cloneFieldState(item)}
	}
	return result
}
func (t Type) Field(path string) (Field, bool) {
	if t.state == nil {
		return Field{}, false
	}
	index, ok := t.state.fieldIndex[path]
	if !ok {
		return Field{}, false
	}
	return Field{state: cloneFieldState(t.state.fields[index])}, true
}

func (f Field) OwnerType() string {
	if f.state == nil {
		return ""
	}
	return f.state.ownerType
}
func (f Field) Path() []string {
	if f.state == nil {
		return nil
	}
	return append([]string(nil), f.state.path...)
}
func (f Field) Required() bool { return f.state != nil && f.state.required }
func (f Field) ValueType() ValueType {
	if f.state == nil {
		return ValueType{}
	}
	return cloneValue(f.state.valueType)
}
func (f Field) Binding() (Binding, bool) {
	if f.state == nil {
		return Binding{}, false
	}
	return f.state.binding, f.state.hasBinding
}
func (f Field) Origin() (provenance.Source, bool) {
	if f.state == nil {
		return provenance.Source{}, false
	}
	return f.state.origin, f.state.hasOrigin
}
func (f Field) Provenance() NodeProvenance {
	if f.state == nil {
		return NodeProvenance{}
	}
	return cloneProvenance(f.state.provenance)
}

func (o Operation) ID() string {
	if o.state == nil {
		return ""
	}
	return o.state.id
}
func (o Operation) Method() api.HTTPMethod {
	if o.state == nil {
		return ""
	}
	return o.state.method
}
func (o Operation) Path() string {
	if o.state == nil {
		return ""
	}
	return o.state.path
}
func (o Operation) RequestType() string {
	if o.state == nil {
		return ""
	}
	return o.state.requestType
}
func (o Operation) ResponseType() string {
	if o.state == nil {
		return ""
	}
	return o.state.responseType
}
func (o Operation) ResponseBody() api.ResponseBodyMode {
	if o.state == nil {
		return ""
	}
	return o.state.responseBody
}
func (o Operation) Auth() Auth {
	if o.state == nil {
		return Auth{}
	}
	return cloneAuth(o.state.auth)
}
func (o Operation) Permission() string {
	if o.state == nil {
		return ""
	}
	return o.state.permission
}
func (o Operation) Capability() (Capability, bool) {
	if o.state == nil {
		return Capability{}, false
	}
	return o.state.capability, o.state.hasCapability
}
func (o Operation) Provenance() NodeProvenance {
	if o.state == nil {
		return NodeProvenance{}
	}
	return cloneProvenance(o.state.provenance)
}
func (o Operation) ErrorProjections() []api.ErrorProjectionSpec {
	if o.state == nil {
		return nil
	}
	return append([]api.ErrorProjectionSpec(nil), o.state.errorProjections...)
}

type Auth struct {
	mode        api.AuthMode
	credentials []Credential
}
type Credential struct {
	id       string
	typeID   api.CredentialType
	location api.CredentialLocation
	name     string
}
type Capability struct{ id, apiVersion string }

func (a Auth) Mode() api.AuthMode                     { return a.mode }
func (a Auth) Credentials() []Credential              { return append([]Credential(nil), a.credentials...) }
func (c Credential) ID() string                       { return c.id }
func (c Credential) Type() api.CredentialType         { return c.typeID }
func (c Credential) Location() api.CredentialLocation { return c.location }
func (c Credential) Name() string                     { return c.name }
func (c Capability) ID() string                       { return c.id }
func (c Capability) APIVersion() string               { return c.apiVersion }

func cloneValue(input ValueType) ValueType {
	result := input
	if input.element != nil {
		value := cloneValue(*input.element)
		result.element = &value
	}
	if input.key != nil {
		value := cloneValue(*input.key)
		result.key = &value
	}
	if input.value != nil {
		value := cloneValue(*input.value)
		result.value = &value
	}
	return result
}
func cloneProvenance(input NodeProvenance) NodeProvenance {
	input.sources = append([]provenance.Source(nil), input.sources...)
	input.canonical = append([]byte(nil), input.canonical...)
	return input
}
func cloneAuth(input Auth) Auth {
	input.credentials = append([]Credential(nil), input.credentials...)
	return input
}
func cloneFieldState(input *fieldState) *fieldState {
	if input == nil {
		return nil
	}
	out := *input
	out.path = append([]string(nil), input.path...)
	out.valueType = cloneValue(input.valueType)
	out.provenance = cloneProvenance(input.provenance)
	return &out
}
func cloneTypeState(input *typeState) *typeState {
	if input == nil {
		return nil
	}
	out := *input
	out.shape = cloneValue(input.shape)
	out.provenance = cloneProvenance(input.provenance)
	out.fields = make([]*fieldState, len(input.fields))
	for i, field := range input.fields {
		out.fields[i] = cloneFieldState(field)
	}
	out.fieldIndex = make(map[string]int, len(input.fieldIndex))
	for key, value := range input.fieldIndex {
		out.fieldIndex[key] = value
	}
	return &out
}
func cloneOperationState(input *operationState) *operationState {
	if input == nil {
		return nil
	}
	out := *input
	out.auth = cloneAuth(input.auth)
	out.provenance = cloneProvenance(input.provenance)
	out.errorProjections = append([]api.ErrorProjectionSpec(nil), input.errorProjections...)
	return &out
}

func newDocument(types []*typeState, operations []*operationState, extra []provenance.Source) (Document, error) {
	typeValues := make([]*typeState, len(types))
	for index, value := range types {
		typeValues[index] = cloneTypeState(value)
	}
	operationValues := make([]*operationState, len(operations))
	for index, value := range operations {
		operationValues[index] = cloneOperationState(value)
	}
	sort.Slice(typeValues, func(i, j int) bool { return typeValues[i].name < typeValues[j].name })
	sort.Slice(operationValues, func(i, j int) bool { return operationValues[i].id < operationValues[j].id })
	state := &documentState{types: typeValues, operations: operationValues, typeIndex: map[string]int{}, operationIndex: map[string]int{}, sourceIndex: map[string]int{}}
	for i, item := range typeValues {
		state.typeIndex[item.name] = i
		for _, source := range item.provenance.sources {
			extra = append(extra, source)
		}
		for _, field := range item.fields {
			extra = append(extra, field.provenance.sources...)
			if field.hasOrigin {
				extra = append(extra, field.origin)
			}
		}
	}
	for i, item := range operationValues {
		state.operationIndex[item.id] = i
		extra = append(extra, item.provenance.sources...)
	}
	byRef := map[string]provenance.Source{}
	for _, source := range extra {
		key := source.Ref.String()
		if existing, duplicate := byRef[key]; duplicate && existing.Digest != source.Digest {
			return Document{}, invalid("source_digest_conflict", source.Ref.Path(), "", "one source reference has conflicting digests")
		}
		byRef[key] = source
	}
	for _, source := range byRef {
		state.sources = append(state.sources, source)
	}
	sort.Slice(state.sources, func(i, j int) bool { return state.sources[i].Ref.String() < state.sources[j].Ref.String() })
	for i, source := range state.sources {
		state.sourceIndex[source.Ref.String()] = i
	}
	return Document{state: state}, nil
}

func pathKey(path []string) string { return strings.Join(path, ".") }
