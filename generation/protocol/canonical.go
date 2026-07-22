package protocol

import (
	"encoding/json"
	"sort"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	messageNodeAPIVersion = "nexa.dev/proto-message-node/v1"
	fieldNodeAPIVersion   = "nexa.dev/proto-field-node/v1"
	methodNodeAPIVersion  = "nexa.dev/proto-method-node/v2"
)

type canonicalSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type canonicalType struct {
	Kind  TypeKind       `json:"kind"`
	Name  string         `json:"name,omitempty"`
	Key   *canonicalType `json:"key,omitempty"`
	Value *canonicalType `json:"value,omitempty"`
}
type canonicalMessageNode struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	FullName   string `json:"fullName"`
}
type canonicalFieldNode struct {
	APIVersion  string        `json:"apiVersion"`
	Kind        string        `json:"kind"`
	FullName    string        `json:"fullName"`
	Number      int           `json:"number"`
	JSONName    string        `json:"jsonName"`
	Cardinality Cardinality   `json:"cardinality"`
	Presence    Presence      `json:"presence"`
	Type        canonicalType `json:"type"`
	Oneof       string        `json:"oneof,omitempty"`
}
type canonicalMethodNode struct {
	APIVersion      string               `json:"apiVersion"`
	Kind            string               `json:"kind"`
	FullName        string               `json:"fullName"`
	Input           string               `json:"input"`
	Output          string               `json:"output"`
	ClientStreaming bool                 `json:"clientStreaming"`
	ServerStreaming bool                 `json:"serverStreaming"`
	HTTPProxy       *canonicalHTTPProxy  `json:"httpProxy,omitempty"`
	RPCContext      *canonicalRPCContext `json:"rpcContext,omitempty"`
}
type canonicalHTTPProxy struct {
	OperationID    string                     `json:"operationId"`
	Method         HTTPMethod                 `json:"method"`
	Path           string                     `json:"path"`
	Auth           canonicalAuth              `json:"auth"`
	Permission     string                     `json:"permission"`
	RequestFields  []canonicalRequestField    `json:"requestFields"`
	ResponseFields []canonicalResponseField   `json:"responseFields"`
	Errors         []canonicalErrorProjection `json:"errors"`
}
type canonicalRPCContext struct {
	ContextFields []canonicalContextField `json:"contextFields"`
}
type canonicalAuth struct {
	Mode        AuthMode              `json:"mode"`
	Credentials []canonicalCredential `json:"credentials"`
}
type canonicalCredential struct {
	ID   string             `json:"id"`
	Type CredentialType     `json:"type"`
	In   CredentialLocation `json:"in"`
	Name string             `json:"name"`
}
type canonicalRequestField struct {
	HTTPField string   `json:"httpField"`
	RPCPath   []string `json:"rpcPath"`
}
type canonicalContextField struct {
	Source  ContextValue `json:"source"`
	RPCPath []string     `json:"rpcPath"`
}
type canonicalResponseField struct {
	RPCPath   []string `json:"rpcPath"`
	HTTPField string   `json:"httpField"`
}
type canonicalErrorProjection struct {
	Match   canonicalErrorMatch  `json:"match"`
	Project canonicalErrorTarget `json:"project"`
}
type canonicalErrorMatch struct {
	Domain string `json:"domain"`
	Code   string `json:"code"`
}
type canonicalErrorTarget struct {
	Domain     string `json:"domain"`
	Code       string `json:"code"`
	HTTPStatus int    `json:"httpStatus"`
}

type canonicalDocument struct {
	APIVersion   string            `json:"apiVersion"`
	Kind         string            `json:"kind"`
	ServiceID    string            `json:"serviceId"`
	SourceDigest string            `json:"sourceDigest"`
	Sources      []canonicalSource `json:"sources"`
	Files        []canonicalFile   `json:"files"`
}
type canonicalFile struct {
	Path     string             `json:"path"`
	Messages []canonicalMessage `json:"messages"`
	Enums    []canonicalEnum    `json:"enums"`
	Services []canonicalService `json:"services"`
}
type canonicalMessage struct {
	FullName  string           `json:"fullName"`
	SourceRef string           `json:"sourceRef"`
	Fields    []canonicalField `json:"fields"`
}
type canonicalField struct {
	FullName    string        `json:"fullName"`
	Number      int           `json:"number"`
	JSONName    string        `json:"jsonName"`
	Cardinality Cardinality   `json:"cardinality"`
	Presence    Presence      `json:"presence"`
	Type        canonicalType `json:"type"`
	Oneof       string        `json:"oneof,omitempty"`
	SourceRef   string        `json:"sourceRef"`
}
type canonicalEnum struct {
	FullName string               `json:"fullName"`
	Values   []canonicalEnumValue `json:"values"`
}
type canonicalEnumValue struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
}
type canonicalService struct {
	FullName string            `json:"fullName"`
	Methods  []canonicalMethod `json:"methods"`
}
type canonicalMethod struct {
	FullName        string               `json:"fullName"`
	Input           string               `json:"input"`
	Output          string               `json:"output"`
	ClientStreaming bool                 `json:"clientStreaming"`
	ServerStreaming bool                 `json:"serverStreaming"`
	HTTPProxy       *canonicalHTTPProxy  `json:"httpProxy,omitempty"`
	RPCContext      *canonicalRPCContext `json:"rpcContext,omitempty"`
	SourceRef       string               `json:"sourceRef"`
}
type canonicalSourceSet struct {
	APIVersion string            `json:"apiVersion"`
	Sources    []canonicalSource `json:"sources"`
}

func finalizeSources(state *documentState) error {
	var sources []provenance.Source
	for _, file := range state.files {
		for _, message := range file.messages {
			encoded, err := canonicalize(canonicalMessageNode{APIVersion: messageNodeAPIVersion, Kind: "message", FullName: message.fullName})
			if err != nil {
				return err
			}
			ref, err := provenance.RepositoryRef(file.path, "message:"+message.fullName)
			if err != nil {
				return err
			}
			message.canonicalSource, message.source = encoded, provenance.Source{Ref: ref, Digest: provenance.SHA256(encoded)}
			sources = append(sources, message.source)
			for _, field := range message.fields {
				node := canonicalFieldNode{APIVersion: fieldNodeAPIVersion, Kind: "field", FullName: field.fullName, Number: field.number, JSONName: field.jsonName, Cardinality: field.cardinality, Presence: field.presence, Type: canonicalTypeValue(field.typeValue), Oneof: field.oneof}
				encoded, err = canonicalize(node)
				if err != nil {
					return err
				}
				ref, err = provenance.RepositoryRef(file.path, "field:"+field.fullName)
				if err != nil {
					return err
				}
				field.canonicalSource, field.source = encoded, provenance.Source{Ref: ref, Digest: provenance.SHA256(encoded)}
				sources = append(sources, field.source)
			}
		}
		for _, service := range file.services {
			for _, method := range service.methods {
				node := canonicalMethodNode{APIVersion: methodNodeAPIVersion, Kind: "method", FullName: method.fullName, Input: method.input, Output: method.output, ClientStreaming: method.clientStreaming, ServerStreaming: method.serverStreaming, HTTPProxy: canonicalProxy(method.httpProxy), RPCContext: canonicalRPCContextValue(method.rpcContext)}
				encoded, err := canonicalize(node)
				if err != nil {
					return err
				}
				ref, err := provenance.RepositoryRef(file.path, "method:"+method.fullName)
				if err != nil {
					return err
				}
				method.canonicalSource, method.source = encoded, provenance.Source{Ref: ref, Digest: provenance.SHA256(encoded)}
				sources = append(sources, method.source)
			}
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Ref.String() < sources[j].Ref.String() })
	state.sources = sources
	state.sourceIndex = make(map[string]int, len(sources))
	canonicalSources := make([]canonicalSource, len(sources))
	for i, source := range sources {
		if _, duplicate := state.sourceIndex[source.Ref.String()]; duplicate {
			return protocolError("protocol_ir_invalid", "source_conflict", source.Ref.Path(), "", "Protocol owner source is duplicated")
		}
		state.sourceIndex[source.Ref.String()] = i
		canonicalSources[i] = canonicalSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	encoded, err := canonicalize(canonicalSourceSet{APIVersion: SourceSetAPIVersion, Sources: canonicalSources})
	if err != nil {
		return err
	}
	state.sourceDigest = provenance.SHA256(encoded)
	return nil
}

func canonicalTypeValue(value *typeState) canonicalType {
	if value == nil {
		return canonicalType{}
	}
	result := canonicalType{Kind: value.kind, Name: value.name}
	if value.kind == TypeMap {
		key, item := canonicalTypeValue(value.key), canonicalTypeValue(value.value)
		result.Key, result.Value = &key, &item
	}
	return result
}

func canonicalProxy(value *httpProxyState) *canonicalHTTPProxy {
	if value == nil {
		return nil
	}
	result := &canonicalHTTPProxy{OperationID: value.operationID, Method: value.method, Path: value.path, Auth: canonicalAuth{Mode: value.auth.mode, Credentials: make([]canonicalCredential, len(value.auth.credentials))}, Permission: value.permission, RequestFields: make([]canonicalRequestField, len(value.requestFields)), ResponseFields: make([]canonicalResponseField, len(value.responseFields)), Errors: make([]canonicalErrorProjection, len(value.errors))}
	for i, item := range value.auth.credentials {
		result.Auth.Credentials[i] = canonicalCredential{ID: item.id, Type: item.typeID, In: item.location, Name: item.name}
	}
	for i, item := range value.requestFields {
		result.RequestFields[i] = canonicalRequestField{HTTPField: item.httpField, RPCPath: append([]string(nil), item.rpcPath...)}
	}
	for i, item := range value.responseFields {
		result.ResponseFields[i] = canonicalResponseField{HTTPField: item.httpField, RPCPath: append([]string(nil), item.rpcPath...)}
	}
	for i, item := range value.errors {
		result.Errors[i] = canonicalErrorProjection{Match: canonicalErrorMatch{Domain: item.match.domain, Code: item.match.code}, Project: canonicalErrorTarget{Domain: item.project.domain, Code: item.project.code, HTTPStatus: item.project.httpStatus}}
	}
	return result
}

func canonicalRPCContextValue(value *rpcContextState) *canonicalRPCContext {
	if value == nil {
		return nil
	}
	result := &canonicalRPCContext{ContextFields: make([]canonicalContextField, len(value.contextFields))}
	for i, item := range value.contextFields {
		result.ContextFields[i] = canonicalContextField{Source: item.source, RPCPath: append([]string(nil), item.rpcPath...)}
	}
	return result
}

func CanonicalJSON(document Document) ([]byte, error) {
	if document.state == nil {
		return nil, protocolError("protocol_ir_invalid", "document_invalid", "", "/document", "Protocol document is invalid")
	}
	state := document.state
	wire := canonicalDocument{APIVersion: APIVersion, Kind: Kind, ServiceID: state.serviceID, SourceDigest: state.sourceDigest.String(), Sources: make([]canonicalSource, len(state.sources)), Files: make([]canonicalFile, len(state.files))}
	for i, source := range state.sources {
		wire.Sources[i] = canonicalSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	for i, file := range state.files {
		item := canonicalFile{Path: file.path, Messages: make([]canonicalMessage, len(file.messages)), Enums: make([]canonicalEnum, len(file.enums)), Services: make([]canonicalService, len(file.services))}
		for j, message := range file.messages {
			messageWire := canonicalMessage{FullName: message.fullName, SourceRef: message.source.Ref.String(), Fields: make([]canonicalField, len(message.fields))}
			for k, field := range message.fields {
				messageWire.Fields[k] = canonicalField{FullName: field.fullName, Number: field.number, JSONName: field.jsonName, Cardinality: field.cardinality, Presence: field.presence, Type: canonicalTypeValue(field.typeValue), Oneof: field.oneof, SourceRef: field.source.Ref.String()}
			}
			item.Messages[j] = messageWire
		}
		for j, enum := range file.enums {
			enumWire := canonicalEnum{FullName: enum.fullName, Values: make([]canonicalEnumValue, len(enum.values))}
			for k, value := range enum.values {
				enumWire.Values[k] = canonicalEnumValue{Name: value.name, Number: value.number}
			}
			item.Enums[j] = enumWire
		}
		for j, service := range file.services {
			serviceWire := canonicalService{FullName: service.fullName, Methods: make([]canonicalMethod, len(service.methods))}
			for k, method := range service.methods {
				serviceWire.Methods[k] = canonicalMethod{FullName: method.fullName, Input: method.input, Output: method.output, ClientStreaming: method.clientStreaming, ServerStreaming: method.serverStreaming, HTTPProxy: canonicalProxy(method.httpProxy), RPCContext: canonicalRPCContextValue(method.rpcContext), SourceRef: method.source.Ref.String()}
			}
			item.Services[j] = serviceWire
		}
		wire.Files[i] = item
	}
	return canonicalize(wire)
}

func canonicalize(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, protocolError("protocol_ir_invalid", "canonical_invalid", "", "", "Protocol value cannot be encoded")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, protocolError("protocol_ir_invalid", "canonical_invalid", "", "", "Protocol value cannot be canonicalized")
	}
	return canonical, nil
}
