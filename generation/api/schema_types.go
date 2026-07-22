package api

import "github.com/nxnminieye/nexa/provenance"

const APIVersion = "nexa.dev/api-manifest/v1"
const Kind = "APIManifest"
const SourceSetAPIVersion = "nexa.dev/api-source-set/v1"

const (
	// RequestContentTypeHeader is owned by the framework whenever an operation has body bindings.
	RequestContentTypeHeader = "content-type"
	// RequestJSONMediaType is the canonical media type for the generated request body object.
	RequestJSONMediaType = "application/json"
)

type HTTPMethod string

const (
	MethodGET     HTTPMethod = "GET"
	MethodPOST    HTTPMethod = "POST"
	MethodPUT     HTTPMethod = "PUT"
	MethodPATCH   HTTPMethod = "PATCH"
	MethodDELETE  HTTPMethod = "DELETE"
	MethodHEAD    HTTPMethod = "HEAD"
	MethodOPTIONS HTTPMethod = "OPTIONS"
)

type SchemaKind string

const (
	SchemaObject  SchemaKind = "object"
	SchemaArray   SchemaKind = "array"
	SchemaString  SchemaKind = "string"
	SchemaInteger SchemaKind = "integer"
	SchemaNumber  SchemaKind = "number"
	SchemaBoolean SchemaKind = "boolean"
)

type RequestBindingLocation string

const (
	RequestBindingPath   RequestBindingLocation = "path"
	RequestBindingQuery  RequestBindingLocation = "query"
	RequestBindingHeader RequestBindingLocation = "header"
	RequestBindingBody   RequestBindingLocation = "body"
)

type ResponseBodyMode string

const (
	ResponseBodyJSON ResponseBodyMode = "json"
	ResponseBodyNone ResponseBodyMode = "none"
)

type AuthMode string

const (
	AuthNone     AuthMode = "none"
	AuthOptional AuthMode = "optional"
	AuthRequired AuthMode = "required"
)

type CredentialType string

const (
	CredentialBearer        CredentialType = "bearer"
	CredentialAPIKey        CredentialType = "api-key"
	CredentialSessionCookie CredentialType = "session-cookie"
)

type CredentialLocation string

const (
	CredentialLocationHeader CredentialLocation = "header"
	CredentialLocationQuery  CredentialLocation = "query"
	CredentialLocationCookie CredentialLocation = "cookie"
)

type ManifestSpec struct {
	Sources    []provenance.Source
	Schemas    []SchemaSpec
	Operations []OperationSpec
}

// NodeProvenanceKind identifies how an API node relates to its owner sources.
type NodeProvenanceKind string

const (
	// NodeCanonical identifies a node authored by exactly one owner source.
	NodeCanonical NodeProvenanceKind = "canonical"
	// NodeDerived identifies a node projected from one or more owner sources.
	NodeDerived NodeProvenanceKind = "derived"
)

// NodeProvenanceSpec declares the owner sources used to construct an API node.
type NodeProvenanceSpec struct {
	Kind NodeProvenanceKind
	Refs []provenance.SourceRef
}

type SchemaSpec struct {
	ID            string
	Kind          SchemaKind
	Provenance    *NodeProvenanceSpec
	Fields        []FieldSpec
	ItemSchemaRef string
}

type FieldSpec struct {
	Name       string
	SchemaRef  string
	Required   bool
	Provenance NodeProvenanceSpec
	Origin     *OriginBindingSpec
}

// OriginBindingSpec declares an optional typed cross-source field relation.
type OriginBindingSpec struct {
	Ref provenance.SourceRef
}

type OperationSpec struct {
	ID                string
	Method            HTTPMethod
	Path              string
	Provenance        NodeProvenanceSpec
	RequestSchemaRef  string
	ResponseBody      ResponseBodyMode
	ResponseSchemaRef string
	RequestBindings   []RequestBindingSpec
	Auth              AuthSpec
	Permission        string
	Capability        *CapabilitySpec
	ErrorProjections  []ErrorProjectionSpec
}

type RequestBindingSpec struct {
	Field    string
	Location RequestBindingLocation
	Name     string
}

type AuthSpec struct {
	Mode        AuthMode
	Credentials []CredentialSpec
}

type CredentialSpec struct {
	ID       string
	Type     CredentialType
	Location CredentialLocation
	Name     string
}

type CapabilitySpec struct{ ID, APIVersion string }
type ErrorProjectionSpec struct {
	Match   ErrorMatchSpec
	Project ErrorTargetSpec
}
type ErrorMatchSpec struct{ Domain, Code string }
type ErrorTargetSpec struct {
	Domain, Code string
	HTTPStatus   int
}

type Manifest struct {
	apiVersion     string
	sourceDigest   provenance.Digest
	sources        []provenance.Source
	schemas        []Schema
	operations     []Operation
	schemaIndex    map[string]int
	operationIndex map[string]int
	routeIndex     map[string]int
	sourceIndex    map[string]int
}

type Schema struct {
	id            string
	kind          SchemaKind
	provenance    NodeProvenance
	hasProvenance bool
	fields        []Field
	fieldIndex    map[string]int
	itemSchemaRef string
}

type Field struct {
	name       string
	schemaRef  string
	required   bool
	provenance NodeProvenance
	origin     OriginBinding
	hasOrigin  bool
}

type Operation struct {
	id, path, requestSchemaRef, responseSchemaRef, permission string
	method                                                    HTTPMethod
	provenance                                                NodeProvenance
	responseBody                                              ResponseBodyMode
	requestBindings                                           []RequestBinding
	auth                                                      Auth
	capability                                                Capability
	hasCapability                                             bool
	errorProjections                                          []ErrorProjection
}

type RequestBinding struct {
	field    string
	location RequestBindingLocation
	name     string
}

type Auth struct {
	mode        AuthMode
	credentials []Credential
}

type Credential struct {
	id       string
	typeID   CredentialType
	location CredentialLocation
	name     string
}

type Capability struct{ id, apiVersion string }

// NodeProvenance is the immutable owner-source projection of an API node.
type NodeProvenance struct {
	kind NodeProvenanceKind
	refs []provenance.SourceRef
}

// OriginBinding is the immutable typed cross-source relation of a field.
type OriginBinding struct {
	ref provenance.SourceRef
}
type ErrorProjection struct {
	match   ErrorMatch
	project ErrorTarget
}
type ErrorMatch struct{ domain, code string }
type ErrorTarget struct {
	domain, code string
	httpStatus   int
}

func (m Manifest) APIVersion() string              { return m.apiVersion }
func (m Manifest) SourceDigest() provenance.Digest { return m.sourceDigest }
func (m Manifest) Sources() []provenance.Source {
	return append([]provenance.Source(nil), m.sources...)
}

// Source returns the source whose canonical reference exactly matches ref.
func (m Manifest) Source(ref provenance.SourceRef) (provenance.Source, bool) {
	index, ok := m.sourceIndex[ref.String()]
	if !ok {
		return provenance.Source{}, false
	}
	return m.sources[index], true
}
func (m Manifest) Schemas() []Schema       { return cloneSchemas(m.schemas) }
func (m Manifest) Operations() []Operation { return cloneOperations(m.operations) }
func (m Manifest) Schema(id string) (Schema, bool) {
	index, ok := m.schemaIndex[id]
	if !ok {
		return Schema{}, false
	}
	return cloneSchema(m.schemas[index]), true
}
func (m Manifest) Operation(id string) (Operation, bool) {
	index, ok := m.operationIndex[id]
	if !ok {
		return Operation{}, false
	}
	return cloneOperation(m.operations[index]), true
}
func (m Manifest) OperationByRoute(method HTTPMethod, path string) (Operation, bool) {
	index, ok := m.routeIndex[string(method)+"\x00"+path]
	if !ok {
		return Operation{}, false
	}
	return cloneOperation(m.operations[index]), true
}

func (s Schema) ID() string       { return s.id }
func (s Schema) Kind() SchemaKind { return s.kind }

// Provenance returns the schema owner sources; built-in scalar schemas have none.
func (s Schema) Provenance() (NodeProvenance, bool) {
	return cloneNodeProvenance(s.provenance), s.hasProvenance
}
func (s Schema) Fields() []Field { return cloneFields(s.fields) }
func (s Schema) Field(name string) (Field, bool) {
	index, ok := s.fieldIndex[name]
	if !ok {
		return Field{}, false
	}
	return cloneField(s.fields[index]), true
}
func (s Schema) ItemSchemaRef() string { return s.itemSchemaRef }
func (f Field) Name() string           { return f.name }
func (f Field) SchemaRef() string      { return f.schemaRef }
func (f Field) Required() bool         { return f.required }

// Provenance returns the field owner sources.
func (f Field) Provenance() NodeProvenance {
	return cloneNodeProvenance(f.provenance)
}

// Origin returns the field's optional typed cross-source relation.
func (f Field) Origin() (OriginBinding, bool) { return f.origin, f.hasOrigin }
func (o Operation) ID() string                { return o.id }
func (o Operation) Method() HTTPMethod        { return o.method }
func (o Operation) Path() string              { return o.path }

// Provenance returns the operation owner sources.
func (o Operation) Provenance() NodeProvenance {
	return cloneNodeProvenance(o.provenance)
}
func (o Operation) RequestSchemaRef() string       { return o.requestSchemaRef }
func (o Operation) ResponseBody() ResponseBodyMode { return o.responseBody }
func (o Operation) ResponseSchemaRef() string      { return o.responseSchemaRef }
func (o Operation) RequestBindings() []RequestBinding {
	return append([]RequestBinding(nil), o.requestBindings...)
}
func (o Operation) Auth() Auth                     { return cloneAuth(o.auth) }
func (o Operation) Permission() string             { return o.permission }
func (o Operation) Capability() (Capability, bool) { return o.capability, o.hasCapability }
func (o Operation) ErrorProjections() []ErrorProjection {
	return append([]ErrorProjection(nil), o.errorProjections...)
}
func (b RequestBinding) Field() string                    { return b.field }
func (b RequestBinding) Location() RequestBindingLocation { return b.location }
func (b RequestBinding) Name() string                     { return b.name }
func (a Auth) Mode() AuthMode                             { return a.mode }
func (a Auth) Credentials() []Credential                  { return append([]Credential(nil), a.credentials...) }
func (c Credential) ID() string                           { return c.id }
func (c Credential) Type() CredentialType                 { return c.typeID }
func (c Credential) Location() CredentialLocation         { return c.location }
func (c Credential) Name() string                         { return c.name }
func (c Capability) ID() string                           { return c.id }
func (c Capability) APIVersion() string                   { return c.apiVersion }

// Kind returns whether this node provenance is canonical or derived.
func (p NodeProvenance) Kind() NodeProvenanceKind { return p.kind }

// Refs returns a defensive copy of the exact owner source references.
func (p NodeProvenance) Refs() []provenance.SourceRef {
	return append([]provenance.SourceRef(nil), p.refs...)
}

// Ref returns the exact owner relation source reference.
func (o OriginBinding) Ref() provenance.SourceRef { return o.ref }
func (p ErrorProjection) Match() ErrorMatch       { return p.match }
func (p ErrorProjection) Project() ErrorTarget    { return p.project }
func (m ErrorMatch) Domain() string               { return m.domain }
func (m ErrorMatch) Code() string                 { return m.code }
func (t ErrorTarget) Domain() string              { return t.domain }
func (t ErrorTarget) Code() string                { return t.code }
func (t ErrorTarget) HTTPStatus() int             { return t.httpStatus }

func cloneSchemas(input []Schema) []Schema {
	result := make([]Schema, len(input))
	for i := range input {
		result[i] = cloneSchema(input[i])
	}
	return result
}
func cloneSchema(input Schema) Schema {
	input.provenance = cloneNodeProvenance(input.provenance)
	input.fields = cloneFields(input.fields)
	input.fieldIndex = cloneIndex(input.fieldIndex)
	return input
}
func cloneOperations(input []Operation) []Operation {
	result := make([]Operation, len(input))
	for i := range input {
		result[i] = cloneOperation(input[i])
	}
	return result
}
func cloneOperation(input Operation) Operation {
	input.provenance = cloneNodeProvenance(input.provenance)
	input.requestBindings = append([]RequestBinding(nil), input.requestBindings...)
	input.auth = cloneAuth(input.auth)
	input.errorProjections = append([]ErrorProjection(nil), input.errorProjections...)
	return input
}
func cloneFields(input []Field) []Field {
	result := make([]Field, len(input))
	for index := range input {
		result[index] = cloneField(input[index])
	}
	return result
}
func cloneField(input Field) Field {
	input.provenance = cloneNodeProvenance(input.provenance)
	return input
}
func cloneNodeProvenance(input NodeProvenance) NodeProvenance {
	input.refs = append([]provenance.SourceRef(nil), input.refs...)
	return input
}
func cloneAuth(input Auth) Auth {
	input.credentials = append([]Credential(nil), input.credentials...)
	return input
}
func cloneIndex(input map[string]int) map[string]int {
	result := make(map[string]int, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
