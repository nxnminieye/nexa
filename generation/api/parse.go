package api

import (
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

type manifestDocument struct {
	APIVersion   string              `json:"apiVersion"`
	Kind         string              `json:"kind"`
	SourceDigest string              `json:"sourceDigest"`
	Sources      []sourceDocument    `json:"sources"`
	Schemas      []schemaDocument    `json:"schemas"`
	Operations   []operationDocument `json:"operations"`
}
type sourceDocument struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type schemaDocument struct {
	ID            string                  `json:"id"`
	Kind          SchemaKind              `json:"kind"`
	Provenance    *nodeProvenanceDocument `json:"provenance,omitempty"`
	Fields        []fieldDocument         `json:"fields,omitempty"`
	ItemSchemaRef string                  `json:"itemSchemaRef,omitempty"`
}
type fieldDocument struct {
	Name       string                  `json:"name"`
	SchemaRef  string                  `json:"schemaRef"`
	Required   bool                    `json:"required"`
	Provenance *nodeProvenanceDocument `json:"provenance"`
	Origin     *originBindingDocument  `json:"origin,omitempty"`
}
type nodeProvenanceDocument struct {
	Kind NodeProvenanceKind `json:"kind"`
	Refs []string           `json:"refs"`
}
type originBindingDocument struct {
	Ref string `json:"ref"`
}
type bindingDocument struct {
	Field    string                 `json:"field"`
	Location RequestBindingLocation `json:"in"`
	Name     string                 `json:"name"`
}
type credentialDocument struct {
	ID       string             `json:"id"`
	Type     CredentialType     `json:"type"`
	Location CredentialLocation `json:"in"`
	Name     string             `json:"name"`
}
type authDocument struct {
	Mode        AuthMode             `json:"mode"`
	Credentials []credentialDocument `json:"credentials"`
}
type capabilityDocument struct {
	ID         string `json:"id"`
	APIVersion string `json:"apiVersion"`
}
type errorMatchDocument struct {
	Domain string `json:"domain"`
	Code   string `json:"code"`
}
type errorTargetDocument struct {
	Domain     string `json:"domain"`
	Code       string `json:"code"`
	HTTPStatus int    `json:"httpStatus"`
}
type errorProjectionDocument struct {
	Match   errorMatchDocument  `json:"match"`
	Project errorTargetDocument `json:"project"`
}
type operationDocument struct {
	ID                string                    `json:"id"`
	Method            HTTPMethod                `json:"method"`
	Path              string                    `json:"path"`
	Provenance        *nodeProvenanceDocument   `json:"provenance"`
	RequestSchemaRef  string                    `json:"requestSchemaRef"`
	ResponseBody      ResponseBodyMode          `json:"responseBody"`
	ResponseSchemaRef string                    `json:"responseSchemaRef,omitempty"`
	RequestBindings   []bindingDocument         `json:"requestBindings"`
	Auth              authDocument              `json:"auth"`
	Permission        string                    `json:"permission,omitempty"`
	Capability        *capabilityDocument       `json:"capability,omitempty"`
	ErrorProjections  []errorProjectionDocument `json:"errorProjections"`
}

func Parse(source string, data []byte) (Manifest, error) {
	var strictDocument strictdoc.Document
	var err error
	if strings.EqualFold(path.Ext(source), ".json") {
		strictDocument, err = strictdoc.ParseJSON(source, data)
	} else {
		strictDocument, err = strictdoc.ParseYAML(source, data)
	}
	if err != nil {
		return Manifest{}, projectDocumentError(source, err)
	}
	documentJSON := strictDocument.JSON()
	normalized, err := normalizedDocument(documentJSON)
	if err != nil {
		return Manifest{}, sourceError("document_invalid", source, "", "API manifest document is invalid")
	}
	var failures []*Error
	if err := validateDocumentSchema(normalized); err != nil {
		failures = append(failures, schemaValidationErrors(source, err)...)
	}
	var wire manifestDocument
	if err := strictDocument.Decode(&wire); err != nil {
		failures = append(failures, projectDocumentError(source, err))
		_ = json.Unmarshal(documentJSON, &wire)
	}
	if wire.APIVersion != "" && wire.APIVersion != APIVersion {
		failures = append(failures, sourceError("version_unsupported", source, "/apiVersion", "API manifest version is not supported"))
	}
	if wire.Kind != "" && wire.Kind != Kind {
		failures = append(failures, sourceError("kind_invalid", source, "/kind", "API manifest kind is invalid"))
	}
	spec, semanticFailures := specFromDocument(source, wire)
	failures = append(failures, semanticFailures...)
	failures = append(failures, validateSpec(source, spec)...)
	storedDigest, digestErr := provenance.ParseDigest(wire.SourceDigest)
	if digestErr != nil {
		failures = append(failures, semanticError(source, "/sourceDigest", "source_digest_invalid"))
	}
	sourceFailures, _ := validateSources(source, spec.Sources)
	if digestErr == nil && len(sourceFailures) == 0 {
		computed, computeErr := computeSourceDigestUnchecked(spec.Sources)
		if computeErr == nil && computed != storedDigest {
			failures = append(failures, semanticError(source, "/sourceDigest", "source_digest_mismatch"))
		}
	}
	selected := selectError(failures, normalized)
	if selected != nil {
		applySourceLocation(selected, data)
		return Manifest{}, selected
	}
	return manifestFromSpec(spec, storedDigest), nil
}

func specFromDocument(source string, wire manifestDocument) (ManifestSpec, []*Error) {
	var failures []*Error
	spec := ManifestSpec{Sources: make([]provenance.Source, len(wire.Sources)), Schemas: make([]SchemaSpec, len(wire.Schemas)), Operations: make([]OperationSpec, len(wire.Operations))}
	for index, item := range wire.Sources {
		base := "/sources/" + strconv.Itoa(index)
		ref, err := provenance.ParseSourceRef(item.Ref)
		if err != nil {
			failures = append(failures, semanticError(source, base+"/ref", "source_ref_invalid"))
		}
		digest, err := provenance.ParseDigest(item.Digest)
		if err != nil {
			failures = append(failures, semanticError(source, base+"/digest", "source_digest_invalid"))
		}
		spec.Sources[index] = provenance.Source{Ref: ref, Digest: digest}
	}
	for index, item := range wire.Schemas {
		result := SchemaSpec{ID: item.ID, Kind: item.Kind, ItemSchemaRef: item.ItemSchemaRef, Fields: make([]FieldSpec, len(item.Fields))}
		if item.Provenance != nil {
			value := provenanceSpecFromDocument(*item.Provenance)
			result.Provenance = &value
		}
		for fieldIndex, field := range item.Fields {
			result.Fields[fieldIndex] = FieldSpec{Name: field.Name, SchemaRef: field.SchemaRef, Required: field.Required}
			if field.Provenance != nil {
				result.Fields[fieldIndex].Provenance = provenanceSpecFromDocument(*field.Provenance)
			}
			if field.Origin != nil {
				ref, _ := provenance.ParseSourceRef(field.Origin.Ref)
				result.Fields[fieldIndex].Origin = &OriginBindingSpec{Ref: ref}
			}
		}
		spec.Schemas[index] = result
	}
	for index, item := range wire.Operations {
		result := OperationSpec{ID: item.ID, Method: item.Method, Path: item.Path, RequestSchemaRef: item.RequestSchemaRef, ResponseBody: item.ResponseBody, ResponseSchemaRef: item.ResponseSchemaRef, Permission: item.Permission, RequestBindings: make([]RequestBindingSpec, len(item.RequestBindings)), Auth: AuthSpec{Mode: item.Auth.Mode, Credentials: make([]CredentialSpec, len(item.Auth.Credentials))}, ErrorProjections: make([]ErrorProjectionSpec, len(item.ErrorProjections))}
		if item.Provenance != nil {
			result.Provenance = provenanceSpecFromDocument(*item.Provenance)
		}
		for bindingIndex, binding := range item.RequestBindings {
			result.RequestBindings[bindingIndex] = RequestBindingSpec{Field: binding.Field, Location: binding.Location, Name: binding.Name}
		}
		for credentialIndex, credential := range item.Auth.Credentials {
			result.Auth.Credentials[credentialIndex] = CredentialSpec{ID: credential.ID, Type: credential.Type, Location: credential.Location, Name: credential.Name}
		}
		if item.Capability != nil {
			result.Capability = &CapabilitySpec{ID: item.Capability.ID, APIVersion: item.Capability.APIVersion}
		}
		for projectionIndex, projection := range item.ErrorProjections {
			result.ErrorProjections[projectionIndex] = ErrorProjectionSpec{Match: ErrorMatchSpec{Domain: projection.Match.Domain, Code: projection.Match.Code}, Project: ErrorTargetSpec{Domain: projection.Project.Domain, Code: projection.Project.Code, HTTPStatus: projection.Project.HTTPStatus}}
		}
		spec.Operations[index] = result
	}
	return spec, failures
}

func provenanceSpecFromDocument(document nodeProvenanceDocument) NodeProvenanceSpec {
	refs := make([]provenance.SourceRef, len(document.Refs))
	for index, raw := range document.Refs {
		refs[index], _ = provenance.ParseSourceRef(raw)
	}
	return NodeProvenanceSpec{Kind: document.Kind, Refs: refs}
}

func computeSourceDigestUnchecked(sources []provenance.Source) (provenance.Digest, error) {
	document := canonicalSourceSet{APIVersion: SourceSetAPIVersion, Sources: canonicalSources(sources)}
	encoded, err := json.Marshal(document)
	if err != nil {
		return provenance.Digest{}, err
	}
	return provenance.SHA256(encoded), nil
}

func projectDocumentError(source string, err error) *Error {
	var strictError *strictdoc.Error
	if !errors.As(err, &strictError) {
		return sourceError("document_invalid", source, "", "API manifest document is invalid")
	}
	result := sourceError(strictError.Code, strictError.Source, strictError.Pointer, "API manifest document is invalid")
	result.line, result.column = strictError.Line, strictError.Column
	return result
}
