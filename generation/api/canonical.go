package api

import (
	"encoding/json"
	"sort"

	"github.com/nxnminieye/nexa/provenance"
)

type canonicalSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type canonicalField struct {
	Name       string                  `json:"name"`
	SchemaRef  string                  `json:"schemaRef"`
	Required   bool                    `json:"required"`
	Provenance canonicalNodeProvenance `json:"provenance"`
	Origin     *canonicalOriginBinding `json:"origin,omitempty"`
}
type canonicalSchema struct {
	ID            string                   `json:"id"`
	Kind          SchemaKind               `json:"kind"`
	Provenance    *canonicalNodeProvenance `json:"provenance,omitempty"`
	Fields        *[]canonicalField        `json:"fields,omitempty"`
	ItemSchemaRef string                   `json:"itemSchemaRef,omitempty"`
}
type canonicalNodeProvenance struct {
	Kind NodeProvenanceKind `json:"kind"`
	Refs []string           `json:"refs"`
}
type canonicalOriginBinding struct {
	Ref string `json:"ref"`
}
type canonicalBinding struct {
	Field    string                 `json:"field"`
	Location RequestBindingLocation `json:"in"`
	Name     string                 `json:"name"`
}
type canonicalCredential struct {
	ID       string             `json:"id"`
	Type     CredentialType     `json:"type"`
	Location CredentialLocation `json:"in"`
	Name     string             `json:"name"`
}
type canonicalAuth struct {
	Mode        AuthMode              `json:"mode"`
	Credentials []canonicalCredential `json:"credentials"`
}
type canonicalCapability struct {
	ID         string `json:"id"`
	APIVersion string `json:"apiVersion"`
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
type canonicalErrorProjection struct {
	Match   canonicalErrorMatch  `json:"match"`
	Project canonicalErrorTarget `json:"project"`
}
type canonicalOperation struct {
	ID                string                     `json:"id"`
	Method            HTTPMethod                 `json:"method"`
	Path              string                     `json:"path"`
	Provenance        canonicalNodeProvenance    `json:"provenance"`
	RequestSchemaRef  string                     `json:"requestSchemaRef"`
	ResponseBody      ResponseBodyMode           `json:"responseBody"`
	ResponseSchemaRef string                     `json:"responseSchemaRef,omitempty"`
	RequestBindings   []canonicalBinding         `json:"requestBindings"`
	Auth              canonicalAuth              `json:"auth"`
	Permission        string                     `json:"permission,omitempty"`
	Capability        *canonicalCapability       `json:"capability,omitempty"`
	ErrorProjections  []canonicalErrorProjection `json:"errorProjections"`
}
type canonicalManifest struct {
	APIVersion   string               `json:"apiVersion"`
	Kind         string               `json:"kind"`
	SourceDigest string               `json:"sourceDigest"`
	Sources      []canonicalSource    `json:"sources"`
	Schemas      []canonicalSchema    `json:"schemas"`
	Operations   []canonicalOperation `json:"operations"`
}
type canonicalSourceSet struct {
	APIVersion string            `json:"apiVersion"`
	Sources    []canonicalSource `json:"sources"`
}

func ComputeSourceDigest(sources []provenance.Source) (provenance.Digest, error) {
	failures, _ := validateSources("", sources)
	if err := selectError(failures, map[string]any{"sources": make([]any, len(sources))}); err != nil {
		return provenance.Digest{}, err
	}
	document := canonicalSourceSet{APIVersion: SourceSetAPIVersion, Sources: canonicalSources(sources)}
	encoded, err := json.Marshal(document)
	if err != nil {
		return provenance.Digest{}, invalidError("document_invalid", "", "API source set cannot be encoded")
	}
	return provenance.SHA256(encoded), nil
}

func (m Manifest) CanonicalJSON() ([]byte, error) {
	if m.apiVersion != APIVersion {
		return nil, invalidError("version_unsupported", "/apiVersion", "API manifest version is not supported")
	}
	if _, err := provenance.ParseDigest(m.sourceDigest.String()); err != nil {
		return nil, invalidError("source_digest_invalid", "/sourceDigest", "API manifest source digest is invalid")
	}
	document := canonicalManifest{APIVersion: m.apiVersion, Kind: Kind, SourceDigest: m.sourceDigest.String(), Sources: canonicalSources(m.sources), Schemas: make([]canonicalSchema, len(m.schemas)), Operations: make([]canonicalOperation, len(m.operations))}
	for i, schema := range m.schemas {
		item := canonicalSchema{ID: schema.id, Kind: schema.kind, ItemSchemaRef: schema.itemSchemaRef}
		if schema.hasProvenance {
			value := canonicalProvenance(schema.provenance)
			item.Provenance = &value
		}
		if schema.kind == SchemaObject {
			fields := make([]canonicalField, len(schema.fields))
			item.Fields = &fields
			for j, field := range schema.fields {
				(*item.Fields)[j] = canonicalField{Name: field.name, SchemaRef: field.schemaRef, Required: field.required, Provenance: canonicalProvenance(field.provenance)}
				if field.hasOrigin {
					(*item.Fields)[j].Origin = &canonicalOriginBinding{Ref: field.origin.ref.String()}
				}
			}
		}
		document.Schemas[i] = item
	}
	for i, operation := range m.operations {
		item := canonicalOperation{ID: operation.id, Method: operation.method, Path: operation.path, Provenance: canonicalProvenance(operation.provenance), RequestSchemaRef: operation.requestSchemaRef, ResponseBody: operation.responseBody, ResponseSchemaRef: operation.responseSchemaRef, RequestBindings: make([]canonicalBinding, len(operation.requestBindings)), Auth: canonicalAuth{Mode: operation.auth.mode, Credentials: make([]canonicalCredential, len(operation.auth.credentials))}, Permission: operation.permission, ErrorProjections: make([]canonicalErrorProjection, len(operation.errorProjections))}
		for j, binding := range operation.requestBindings {
			item.RequestBindings[j] = canonicalBinding{Field: binding.field, Location: binding.location, Name: binding.name}
		}
		for j, credential := range operation.auth.credentials {
			item.Auth.Credentials[j] = canonicalCredential{ID: credential.id, Type: credential.typeID, Location: credential.location, Name: credential.name}
		}
		if operation.hasCapability {
			item.Capability = &canonicalCapability{ID: operation.capability.id, APIVersion: operation.capability.apiVersion}
		}
		for j, projection := range operation.errorProjections {
			item.ErrorProjections[j] = canonicalErrorProjection{Match: canonicalErrorMatch{Domain: projection.match.domain, Code: projection.match.code}, Project: canonicalErrorTarget{Domain: projection.project.domain, Code: projection.project.code, HTTPStatus: projection.project.httpStatus}}
		}
		document.Operations[i] = item
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, invalidError("document_invalid", "", "API manifest cannot be encoded")
	}
	return append(encoded, '\n'), nil
}

func canonicalProvenance(value NodeProvenance) canonicalNodeProvenance {
	refs := make([]string, len(value.refs))
	for index, ref := range value.refs {
		refs[index] = ref.String()
	}
	return canonicalNodeProvenance{Kind: value.kind, Refs: refs}
}

func canonicalSources(sources []provenance.Source) []canonicalSource {
	result := make([]canonicalSource, len(sources))
	for i, source := range sources {
		result[i] = canonicalSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	return result
}
