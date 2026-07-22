package httpapi

import (
	"encoding/json"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	typeNodeVersion  = "nexa.dev/http-api-type-node/v1"
	fieldNodeVersion = "nexa.dev/http-api-field-node/v1"
	routeNodeVersion = "nexa.dev/http-api-route-node/v1"
)

type canonicalValue struct {
	Kind    ValueKind       `json:"kind"`
	Name    string          `json:"name,omitempty"`
	Element *canonicalValue `json:"element,omitempty"`
	Key     *canonicalValue `json:"key,omitempty"`
	Value   *canonicalValue `json:"value,omitempty"`
}
type canonicalBinding struct {
	Location apiLocation `json:"in"`
	Name     string      `json:"name"`
}
type apiLocation = string
type canonicalOrigin struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type canonicalTypeNode struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Shape      canonicalValue `json:"shape"`
}
type canonicalFieldNode struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	OwnerType  string            `json:"ownerType"`
	Path       []string          `json:"path"`
	Required   bool              `json:"required"`
	ValueType  canonicalValue    `json:"valueType"`
	Binding    *canonicalBinding `json:"binding,omitempty"`
	Origin     *canonicalOrigin  `json:"origin,omitempty"`
}
type canonicalCredential struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Location string `json:"in"`
	Name     string `json:"name"`
}
type canonicalAuth struct {
	Mode        string                `json:"mode"`
	Credentials []canonicalCredential `json:"credentials"`
}
type canonicalCapability struct {
	ID         string `json:"id"`
	APIVersion string `json:"apiVersion"`
}
type canonicalRouteNode struct {
	APIVersion       string               `json:"apiVersion"`
	Kind             string               `json:"kind"`
	OperationID      string               `json:"operationId"`
	Method           string               `json:"method"`
	Path             string               `json:"path"`
	RequestType      string               `json:"requestType"`
	ResponseBody     string               `json:"responseBody"`
	ResponseType     string               `json:"responseType,omitempty"`
	Auth             canonicalAuth        `json:"auth"`
	Permission       string               `json:"permission"`
	Capability       *canonicalCapability `json:"capability,omitempty"`
	ErrorProjections []any                `json:"errorProjections"`
}

func canonicalize(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

func canonicalValueOf(value ValueType) canonicalValue {
	result := canonicalValue{Kind: value.kind, Name: value.name}
	if value.element != nil {
		item := canonicalValueOf(*value.element)
		result.Element = &item
	}
	if value.key != nil {
		item := canonicalValueOf(*value.key)
		result.Key = &item
	}
	if value.value != nil {
		item := canonicalValueOf(*value.value)
		result.Value = &item
	}
	return result
}

func nativeProvenance(path, fragment string, envelope any) (NodeProvenance, error) {
	canonical, err := canonicalize(envelope)
	if err != nil {
		return NodeProvenance{}, err
	}
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		return NodeProvenance{}, err
	}
	source := provenance.Source{Ref: ref, Digest: provenance.SHA256(canonical)}
	return NodeProvenance{kind: NodeFactNative, sources: []provenance.Source{source}, canonical: canonical}, nil
}
