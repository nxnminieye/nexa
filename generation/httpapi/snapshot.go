package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

const Kind = "HTTPAPIIR"

type wireSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type wireProvenance struct {
	Kind    NodeFactKind `json:"kind"`
	Sources []string     `json:"sources"`
}
type wireValue struct {
	Kind    ValueKind  `json:"kind"`
	Name    string     `json:"name,omitempty"`
	Element *wireValue `json:"element,omitempty"`
	Key     *wireValue `json:"key,omitempty"`
	Value   *wireValue `json:"value,omitempty"`
}
type wireBinding struct {
	Location api.RequestBindingLocation `json:"in"`
	Name     string                     `json:"name"`
}
type wireOrigin struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type wireField struct {
	Path       []string       `json:"path"`
	Required   bool           `json:"required"`
	ValueType  wireValue      `json:"valueType"`
	Binding    *wireBinding   `json:"binding,omitempty"`
	Origin     *wireOrigin    `json:"origin,omitempty"`
	Provenance wireProvenance `json:"provenance"`
}
type wireType struct {
	Name       string         `json:"name"`
	Shape      wireValue      `json:"shape"`
	Fields     []wireField    `json:"fields"`
	Provenance wireProvenance `json:"provenance"`
}
type wireCredential struct {
	ID       string                 `json:"id"`
	Type     api.CredentialType     `json:"type"`
	Location api.CredentialLocation `json:"in"`
	Name     string                 `json:"name"`
}
type wireAuth struct {
	Mode        api.AuthMode     `json:"mode"`
	Credentials []wireCredential `json:"credentials"`
}
type wireCapability struct {
	ID         string `json:"id"`
	APIVersion string `json:"apiVersion"`
}
type wireOperation struct {
	ID               string                    `json:"id"`
	Method           api.HTTPMethod            `json:"method"`
	Path             string                    `json:"path"`
	RequestType      string                    `json:"requestType"`
	ResponseBody     api.ResponseBodyMode      `json:"responseBody"`
	ResponseType     string                    `json:"responseType,omitempty"`
	Auth             wireAuth                  `json:"auth"`
	Permission       string                    `json:"permission"`
	Capability       *wireCapability           `json:"capability,omitempty"`
	ErrorProjections []api.ErrorProjectionSpec `json:"errorProjections"`
	Provenance       wireProvenance            `json:"provenance"`
}
type wireDocument struct {
	APIVersion   string          `json:"apiVersion"`
	Kind         string          `json:"kind"`
	SourceDigest string          `json:"sourceDigest"`
	Sources      []wireSource    `json:"sources"`
	Types        []wireType      `json:"types"`
	Operations   []wireOperation `json:"operations"`
}

func CanonicalJSON(document Document) ([]byte, error) {
	if document.state == nil {
		return nil, invalid("document_invalid", "", "", "HTTP API document is invalid")
	}
	wire, err := documentWire(document)
	if err != nil {
		return nil, err
	}
	return canonicalize(wire)
}

func documentWire(document Document) (wireDocument, error) {
	digest, err := api.ComputeSourceDigest(document.state.sources)
	if err != nil {
		return wireDocument{}, invalid("source_digest_invalid", "", "", err.Error())
	}
	result := wireDocument{APIVersion: APIVersion, Kind: Kind, SourceDigest: digest.String(), Sources: make([]wireSource, len(document.state.sources)), Types: make([]wireType, len(document.state.types)), Operations: make([]wireOperation, len(document.state.operations))}
	for index, source := range document.state.sources {
		result.Sources[index] = wireSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	for index, item := range document.state.types {
		value := wireType{Name: item.name, Shape: wireValueOf(item.shape), Fields: make([]wireField, len(item.fields)), Provenance: wireProvenanceOf(item.provenance)}
		for fieldIndex, field := range item.fields {
			entry := wireField{Path: append([]string(nil), field.path...), Required: field.required, ValueType: wireValueOf(field.valueType), Provenance: wireProvenanceOf(field.provenance)}
			if field.hasBinding {
				entry.Binding = &wireBinding{Location: field.binding.location, Name: field.binding.name}
			}
			if field.hasOrigin {
				entry.Origin = &wireOrigin{Ref: field.origin.Ref.String(), Digest: field.origin.Digest.String()}
			}
			value.Fields[fieldIndex] = entry
		}
		result.Types[index] = value
	}
	for index, item := range document.state.operations {
		value := wireOperation{ID: item.id, Method: item.method, Path: item.path, RequestType: item.requestType, ResponseBody: item.responseBody, ResponseType: item.responseType, Auth: wireAuth{Mode: item.auth.mode, Credentials: make([]wireCredential, len(item.auth.credentials))}, Permission: item.permission, ErrorProjections: append([]api.ErrorProjectionSpec{}, item.errorProjections...), Provenance: wireProvenanceOf(item.provenance)}
		for credentialIndex, credential := range item.auth.credentials {
			value.Auth.Credentials[credentialIndex] = wireCredential{ID: credential.id, Type: credential.typeID, Location: credential.location, Name: credential.name}
		}
		if item.hasCapability {
			value.Capability = &wireCapability{ID: item.capability.id, APIVersion: item.capability.apiVersion}
		}
		result.Operations[index] = value
	}
	return result, nil
}
func wireValueOf(value ValueType) wireValue {
	result := wireValue{Kind: value.kind, Name: value.name}
	if value.element != nil {
		item := wireValueOf(*value.element)
		result.Element = &item
	}
	if value.key != nil {
		item := wireValueOf(*value.key)
		result.Key = &item
	}
	if value.value != nil {
		item := wireValueOf(*value.value)
		result.Value = &item
	}
	return result
}
func wireProvenanceOf(value NodeProvenance) wireProvenance {
	result := wireProvenance{Kind: value.kind, Sources: make([]string, len(value.sources))}
	for index, source := range value.sources {
		result.Sources[index] = source.Ref.String()
	}
	sort.Strings(result.Sources)
	return result
}

type Snapshot struct {
	state  *snapshotState
	marker snapshotMarker
}
type snapshotMarker struct{ _ [0]func() }
type snapshotState struct {
	canonical  []byte
	types      []string
	operations []string
}
type SnapshotType struct{ name string }
type SnapshotOperation struct{ id string }

func (s Snapshot) CanonicalJSON() ([]byte, error) {
	if s.state == nil {
		return nil, invalid("snapshot_invalid", "", "", "HTTP API snapshot is invalid")
	}
	return append([]byte(nil), s.state.canonical...), nil
}
func (s Snapshot) Types() []SnapshotType {
	if s.state == nil {
		return nil
	}
	result := make([]SnapshotType, len(s.state.types))
	for index, name := range s.state.types {
		result[index] = SnapshotType{name: name}
	}
	return result
}
func (s Snapshot) Operations() []SnapshotOperation {
	if s.state == nil {
		return nil
	}
	result := make([]SnapshotOperation, len(s.state.operations))
	for index, id := range s.state.operations {
		result[index] = SnapshotOperation{id: id}
	}
	return result
}
func (t SnapshotType) Name() string    { return t.name }
func (o SnapshotOperation) ID() string { return o.id }

func ParseSnapshot(source provenance.DomainSource, data []byte) (Snapshot, error) {
	if source.String() == "" {
		return Snapshot{}, invalid("snapshot_source_invalid", "", "", "snapshot source is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire wireDocument
	if err := decoder.Decode(&wire); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", err.Error())
	}
	if err := ensureEOF(decoder); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", err.Error())
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", err.Error())
	}
	if err := validateSnapshotSchema(schemaValue); err != nil {
		return Snapshot{}, invalid("document_invalid", source.String(), "", err.Error())
	}
	if wire.APIVersion != APIVersion {
		return Snapshot{}, invalid("version_unsupported", source.String(), "/apiVersion", "HTTP API snapshot version is unsupported")
	}
	if wire.Kind != Kind {
		return Snapshot{}, invalid("kind_invalid", source.String(), "/kind", "HTTP API snapshot kind is invalid")
	}
	canonical, err := canonicalize(wire)
	if err != nil || !bytes.Equal(canonical, data) {
		return Snapshot{}, invalid("canonical_order_invalid", source.String(), "", "HTTP API snapshot is not canonical")
	}
	sources := make([]provenance.Source, len(wire.Sources))
	previous := ""
	for index, item := range wire.Sources {
		ref, refErr := provenance.ParseSourceRef(item.Ref)
		digest, digestErr := provenance.ParseDigest(item.Digest)
		if refErr != nil || digestErr != nil || (previous != "" && item.Ref <= previous) {
			return Snapshot{}, invalid("source_invalid", source.String(), "/sources", "HTTP API snapshot source is invalid")
		}
		previous = item.Ref
		sources[index] = provenance.Source{Ref: ref, Digest: digest}
	}
	digest, err := api.ComputeSourceDigest(sources)
	if err != nil || digest.String() != wire.SourceDigest {
		return Snapshot{}, invalid("source_digest_mismatch", source.String(), "/sourceDigest", "HTTP API snapshot source digest does not match")
	}
	if err := validateSnapshotSemantics(wire, sources); err != nil {
		return Snapshot{}, err
	}
	state := &snapshotState{canonical: append([]byte(nil), canonical...)}
	for _, item := range wire.Types {
		state.types = append(state.types, item.Name)
	}
	for _, item := range wire.Operations {
		state.operations = append(state.operations, item.ID)
	}
	return Snapshot{state: state}, nil
}
func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else {
		return err
	}
}
