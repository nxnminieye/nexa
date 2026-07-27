package composition

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

type wireSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type wireProvenance struct {
	Sources []string `json:"sources"`
}
type wireValue struct {
	Kind    httpapi.ValueKind `json:"kind"`
	Name    string            `json:"name,omitempty"`
	Element *wireValue        `json:"element,omitempty"`
}
type wireProtoType struct {
	Kind protocol.TypeKind `json:"kind"`
	Name string            `json:"name"`
}
type wirePathSegment struct {
	ID          string               `json:"id"`
	FullName    string               `json:"fullName"`
	SourceRef   string               `json:"sourceRef"`
	Number      int                  `json:"number"`
	JSONName    string               `json:"jsonName,omitempty"`
	Cardinality protocol.Cardinality `json:"cardinality,omitempty"`
	Presence    protocol.Presence    `json:"presence,omitempty"`
	TypeKind    protocol.TypeKind    `json:"typeKind"`
	TypeName    string               `json:"typeName,omitempty"`
}
type wireFieldBinding struct {
	HTTPField  string            `json:"httpField"`
	RPCPath    []wirePathSegment `json:"rpcPath"`
	ValueType  wireValue         `json:"valueType"`
	Required   bool              `json:"required"`
	Provenance wireProvenance    `json:"provenance"`
}
type wireContextBinding struct {
	Source     protocol.ContextValue `json:"source"`
	RPCPath    []wirePathSegment     `json:"rpcPath"`
	ValueType  wireValue             `json:"valueType"`
	Required   bool                  `json:"required"`
	Provenance wireProvenance        `json:"provenance"`
}
type wireCredential struct {
	ID       string                      `json:"id"`
	Type     protocol.CredentialType     `json:"type"`
	Location protocol.CredentialLocation `json:"in"`
	Name     string                      `json:"name"`
}
type wireAuth struct {
	Mode        protocol.AuthMode `json:"mode"`
	Credentials []wireCredential  `json:"credentials"`
}
type wireError struct {
	Match   wireErrorMatch  `json:"match"`
	Project wireErrorTarget `json:"project"`
}
type wireErrorMatch struct {
	Domain string `json:"domain"`
	Code   string `json:"code"`
}
type wireErrorTarget struct {
	Domain     string `json:"domain"`
	Code       string `json:"code"`
	HTTPStatus int    `json:"httpStatus"`
}
type wireOperation struct {
	ID                  string               `json:"id"`
	ServiceID           string               `json:"serviceId"`
	MethodFullName      string               `json:"methodFullName"`
	InputName           string               `json:"inputName"`
	OutputName          string               `json:"outputName"`
	RequestType         string               `json:"requestType"`
	ResponseType        string               `json:"responseType"`
	Method              protocol.HTTPMethod  `json:"method"`
	Path                string               `json:"path"`
	Permission          string               `json:"permission"`
	Auth                wireAuth             `json:"auth"`
	RequestFields       []wireFieldBinding   `json:"requestFields"`
	ContextFields       []wireContextBinding `json:"contextFields"`
	ResponseFields      []wireFieldBinding   `json:"responseFields"`
	Errors              []wireError          `json:"errors"`
	OperationProvenance wireProvenance       `json:"operationProvenance"`
	RequestProvenance   wireProvenance       `json:"requestProvenance"`
	ResponseProvenance  wireProvenance       `json:"responseProvenance"`
}
type wireProjectedField struct {
	ID             string         `json:"id"`
	ProtoName      string         `json:"protoName"`
	JSONName       string         `json:"jsonName"`
	Number         int            `json:"number"`
	ProtoType      wireProtoType  `json:"protoType"`
	ValueType      wireValue      `json:"valueType"`
	Required       bool           `json:"required"`
	FieldSourceRef string         `json:"fieldSourceRef"`
	Provenance     wireProvenance `json:"provenance"`
}
type wireProjectedType struct {
	Name             string               `json:"name"`
	ServiceID        string               `json:"serviceId"`
	MessageFullName  string               `json:"messageFullName"`
	MessageSourceRef string               `json:"messageSourceRef"`
	Fields           []wireProjectedField `json:"fields"`
	Provenance       wireProvenance       `json:"provenance"`
}
type wireDocument struct {
	APIVersion         string               `json:"apiVersion"`
	Kind               string               `json:"kind"`
	CoreServiceID      string               `json:"coreServiceId"`
	ConsumerModulePath string               `json:"consumerModulePath"`
	SourceDigest       string               `json:"sourceDigest"`
	Sources            []wireSource         `json:"sources"`
	Operations         []wireOperation      `json:"operations"`
	Types              *[]wireProjectedType `json:"types,omitempty"`
}

type wireDocumentV1 struct {
	APIVersion         string          `json:"apiVersion"`
	Kind               string          `json:"kind"`
	CoreServiceID      string          `json:"coreServiceId"`
	ConsumerModulePath string          `json:"consumerModulePath"`
	SourceDigest       string          `json:"sourceDigest"`
	Sources            []wireSource    `json:"sources"`
	Operations         []wireOperation `json:"operations"`
}

type wireDocumentV2 struct {
	APIVersion         string              `json:"apiVersion"`
	Kind               string              `json:"kind"`
	CoreServiceID      string              `json:"coreServiceId"`
	ConsumerModulePath string              `json:"consumerModulePath"`
	SourceDigest       string              `json:"sourceDigest"`
	Sources            []wireSource        `json:"sources"`
	Operations         []wireOperation     `json:"operations"`
	Types              []wireProjectedType `json:"types"`
}

func (value wireDocumentV1) document() wireDocument {
	return wireDocument{
		APIVersion: value.APIVersion, Kind: value.Kind, CoreServiceID: value.CoreServiceID,
		ConsumerModulePath: value.ConsumerModulePath, SourceDigest: value.SourceDigest,
		Sources: value.Sources, Operations: value.Operations,
	}
}

func (value wireDocumentV2) document() wireDocument {
	types := value.Types
	return wireDocument{
		APIVersion: value.APIVersion, Kind: value.Kind, CoreServiceID: value.CoreServiceID,
		ConsumerModulePath: value.ConsumerModulePath, SourceDigest: value.SourceDigest,
		Sources: value.Sources, Operations: value.Operations, Types: &types,
	}
}

func CanonicalJSON(document Document) ([]byte, error) {
	if document.state == nil {
		return nil, invalid("document_invalid", "", "/document", "composition document is invalid")
	}
	wire, err := compositionWire(document)
	if err != nil {
		return nil, err
	}
	return canonicalize(wire)
}

func compositionWire(document Document) (wireDocument, error) {
	types := make([]wireProjectedType, len(document.state.types))
	result := wireDocument{APIVersion: CurrentAPIVersion, Kind: Kind, CoreServiceID: document.state.coreServiceID, ConsumerModulePath: document.state.consumerModulePath, Operations: make([]wireOperation, len(document.state.operations)), Types: &types}
	sourceSet := map[string]provenance.Source{}
	for index, projected := range document.state.types {
		item := wireProjectedType{Name: projected.name, ServiceID: projected.serviceID, MessageFullName: projected.messageFullName, MessageSourceRef: projected.message.Source().Ref.String(), Provenance: wireProvenanceOf(projected.provenance), Fields: make([]wireProjectedField, len(projected.fields))}
		addNodeSources(sourceSet, projected.provenance)
		for fieldIndex, field := range projected.fields {
			item.Fields[fieldIndex] = wireProjectedField{ID: field.id, ProtoName: field.protoName, JSONName: field.jsonName, Number: field.number, ProtoType: wireProtoType{Kind: field.field.Type().Kind(), Name: field.field.Type().Name()}, ValueType: wireValueOf(field.valueType), Required: field.required, FieldSourceRef: field.field.Source().Ref.String(), Provenance: wireProvenanceOf(field.provenance)}
			addNodeSources(sourceSet, field.provenance)
		}
		resultTypes := *result.Types
		resultTypes[index] = item
		*result.Types = resultTypes
	}
	for index, operation := range document.state.operations {
		item := wireOperation{ID: operation.proxy.OperationID(), ServiceID: operation.serviceID, MethodFullName: operation.methodName, InputName: operation.inputName, OutputName: operation.outputName, RequestType: operation.requestType, ResponseType: operation.responseType, Method: operation.proxy.Method(), Path: operation.proxy.Path(), Permission: operation.proxy.Permission(), Auth: wireAuth{Credentials: []wireCredential{}}, RequestFields: []wireFieldBinding{}, ContextFields: []wireContextBinding{}, ResponseFields: []wireFieldBinding{}, Errors: []wireError{}, OperationProvenance: wireProvenanceOf(operation.operationProvenance), RequestProvenance: wireProvenanceOf(operation.requestProvenance), ResponseProvenance: wireProvenanceOf(operation.responseProvenance)}
		addNodeSources(sourceSet, operation.operationProvenance)
		addNodeSources(sourceSet, operation.requestProvenance)
		addNodeSources(sourceSet, operation.responseProvenance)
		auth := operation.proxy.Auth()
		item.Auth.Mode = auth.Mode()
		for _, credential := range auth.Credentials() {
			item.Auth.Credentials = append(item.Auth.Credentials, wireCredential{ID: credential.ID(), Type: credential.Type(), Location: credential.Location(), Name: credential.Name()})
		}
		sort.Slice(item.Auth.Credentials, func(i, j int) bool { return item.Auth.Credentials[i].ID < item.Auth.Credentials[j].ID })
		for _, binding := range operation.requestFields {
			value, err := wireFieldBindingOf(operation, binding)
			if err != nil {
				return wireDocument{}, err
			}
			item.RequestFields = append(item.RequestFields, value)
			addBindingSources(sourceSet, operation, binding)
		}
		for _, binding := range operation.contextFields {
			owner, err := fieldProvenance(operation, binding)
			if err != nil {
				return wireDocument{}, err
			}
			item.ContextFields = append(item.ContextFields, wireContextBinding{Source: binding.context, RPCPath: wirePathOf(binding), ValueType: wireValueOf(binding.valueType), Required: binding.required, Provenance: wireProvenanceOf(owner)})
			addBindingSources(sourceSet, operation, binding)
		}
		for _, binding := range operation.responseFields {
			value, err := wireFieldBindingOf(operation, binding)
			if err != nil {
				return wireDocument{}, err
			}
			item.ResponseFields = append(item.ResponseFields, value)
			addBindingSources(sourceSet, operation, binding)
		}
		sort.Slice(item.RequestFields, func(i, j int) bool {
			return fieldBindingKey(item.RequestFields[i]) < fieldBindingKey(item.RequestFields[j])
		})
		sort.Slice(item.ContextFields, func(i, j int) bool {
			return contextBindingKey(item.ContextFields[i]) < contextBindingKey(item.ContextFields[j])
		})
		sort.Slice(item.ResponseFields, func(i, j int) bool {
			return fieldBindingKey(item.ResponseFields[i]) < fieldBindingKey(item.ResponseFields[j])
		})
		for _, projection := range operation.errorProjections {
			item.Errors = append(item.Errors, wireError{Match: wireErrorMatch{Domain: projection.Match.Domain, Code: projection.Match.Code}, Project: wireErrorTarget{Domain: projection.Project.Domain, Code: projection.Project.Code, HTTPStatus: projection.Project.HTTPStatus}})
		}
		sort.Slice(item.Errors, func(i, j int) bool { return errorKey(item.Errors[i]) < errorKey(item.Errors[j]) })
		result.Operations[index] = item
	}
	sort.Slice(result.Operations, func(i, j int) bool { return result.Operations[i].ID < result.Operations[j].ID })
	for _, source := range sourceSet {
		result.Sources = append(result.Sources, wireSource{Ref: source.Ref.String(), Digest: source.Digest.String()})
	}
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].Ref < result.Sources[j].Ref })
	sources := make([]provenance.Source, len(result.Sources))
	for index, item := range result.Sources {
		sources[index] = sourceSet[item.Ref]
	}
	digest, err := api.ComputeSourceDigest(sources)
	if err != nil {
		return wireDocument{}, invalid("source_digest_invalid", "", "/sources", "composition source set is invalid")
	}
	result.SourceDigest = digest.String()
	return result, nil
}

func wireFieldBindingOf(operation *operationState, binding resolvedBinding) (wireFieldBinding, error) {
	owner, err := fieldProvenance(operation, binding)
	if err != nil {
		return wireFieldBinding{}, err
	}
	return wireFieldBinding{HTTPField: binding.httpField, RPCPath: wirePathOf(binding), ValueType: wireValueOf(binding.valueType), Required: binding.required, Provenance: wireProvenanceOf(owner)}, nil
}
func wirePathOf(binding resolvedBinding) []wirePathSegment {
	result := make([]wirePathSegment, len(binding.fields))
	for index, field := range binding.fields {
		result[index] = wirePathSegment{ID: binding.typedPath[index], FullName: field.FullName(), Number: field.Number(), SourceRef: field.Source().Ref.String(), JSONName: field.JSONName(), Cardinality: field.Cardinality(), Presence: field.Presence(), TypeKind: field.Type().Kind(), TypeName: field.Type().Name()}
	}
	return result
}
func wireValueOf(value httpapi.ValueTypeSpec) wireValue {
	result := wireValue{Kind: value.Kind, Name: value.Name}
	if value.Element != nil {
		item := wireValueOf(*value.Element)
		result.Element = &item
	}
	return result
}
func wireProvenanceOf(value httpapi.NodeProvenance) wireProvenance {
	result := wireProvenance{}
	for _, source := range value.Sources() {
		result.Sources = append(result.Sources, source.Ref.String())
	}
	sort.Strings(result.Sources)
	return result
}
func addNodeSources(set map[string]provenance.Source, value httpapi.NodeProvenance) {
	for _, source := range value.Sources() {
		set[source.Ref.String()] = source
	}
}
func addBindingSources(set map[string]provenance.Source, operation *operationState, binding resolvedBinding) {
	set[operation.method.Source().Ref.String()] = operation.method.Source()
	set[operation.bindingSource.Ref.String()] = operation.bindingSource
	for _, field := range binding.fields {
		set[field.Source().Ref.String()] = field.Source()
	}
}
func fieldBindingKey(value wireFieldBinding) string {
	return value.HTTPField + "\x00" + pathKey(value.RPCPath)
}
func contextBindingKey(value wireContextBinding) string {
	return string(value.Source) + "\x00" + pathKey(value.RPCPath)
}
func pathKey(value []wirePathSegment) string {
	var ids []string
	for _, item := range value {
		ids = append(ids, item.ID)
	}
	return strings.Join(ids, "\x00")
}
func errorKey(value wireError) string {
	return value.Match.Domain + "\x00" + value.Match.Code + "\x00" + value.Project.Domain + "\x00" + value.Project.Code
}
func canonicalize(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}
