package httpapi

import (
	"encoding/json"

	"github.com/nxnminieye/nexa/generation/api"
)

// The first public alpha releases encoded these three structs without JSON
// tags. Keep this read-only wire shape separate from the current v1 emitter.
type legacyWireErrorMatch struct {
	Domain string `json:"Domain"`
	Code   string `json:"Code"`
}
type legacyWireErrorTarget struct {
	Domain     string `json:"Domain"`
	Code       string `json:"Code"`
	HTTPStatus int    `json:"HTTPStatus"`
}
type legacyWireErrorProjection struct {
	Match   legacyWireErrorMatch  `json:"Match"`
	Project legacyWireErrorTarget `json:"Project"`
}
type legacyWireOperation struct {
	ID               string                      `json:"id"`
	Method           api.HTTPMethod              `json:"method"`
	Path             string                      `json:"path"`
	RequestType      string                      `json:"requestType"`
	ResponseBody     api.ResponseBodyMode        `json:"responseBody"`
	ResponseType     string                      `json:"responseType,omitempty"`
	Auth             wireAuth                    `json:"auth"`
	Permission       string                      `json:"permission"`
	Capability       *wireCapability             `json:"capability,omitempty"`
	ErrorProjections []legacyWireErrorProjection `json:"errorProjections"`
	Provenance       wireProvenance              `json:"provenance"`
}
type legacyWireDocument struct {
	APIVersion   string                `json:"apiVersion"`
	Kind         string                `json:"kind"`
	SourceDigest string                `json:"sourceDigest"`
	Sources      []wireSource          `json:"sources"`
	Types        []wireType            `json:"types"`
	Operations   []legacyWireOperation `json:"operations"`
}

func decodeLegacyWire(data []byte, schemaValue any) (wireDocument, []byte, bool, error) {
	normalized, legacy, err := normalizeLegacySchemaValue(schemaValue)
	if err != nil || !legacy {
		return wireDocument{}, nil, legacy, err
	}
	if err := validateSnapshotSchema(normalized); err != nil {
		return wireDocument{}, nil, true, err
	}
	var legacyWire legacyWireDocument
	if err := decodeStrictJSON(data, &legacyWire); err != nil {
		return wireDocument{}, nil, true, err
	}
	canonical, err := canonicalize(legacyWire)
	if err != nil {
		return wireDocument{}, nil, true, err
	}
	return currentWire(legacyWire), canonical, true, nil
}

func normalizeLegacySchemaValue(input any) (any, bool, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, false, err
	}
	var cloned any
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil, false, err
	}
	root, ok := cloned.(map[string]any)
	if !ok {
		return nil, false, nil
	}
	operations, ok := root["operations"].([]any)
	if !ok {
		return nil, false, nil
	}
	found := false
	for _, operationValue := range operations {
		operation, ok := operationValue.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		projections, ok := operation["errorProjections"].([]any)
		if !ok {
			return nil, false, nil
		}
		for _, projectionValue := range projections {
			projection, ok := projectionValue.(map[string]any)
			if !ok || !hasExactKeys(projection, "Match", "Project") {
				return nil, false, nil
			}
			match, matchOK := projection["Match"].(map[string]any)
			project, projectOK := projection["Project"].(map[string]any)
			if !matchOK || !projectOK || !hasExactKeys(match, "Code", "Domain") || !hasExactKeys(project, "Code", "Domain", "HTTPStatus") {
				return nil, false, nil
			}
			projection["match"] = map[string]any{"code": match["Code"], "domain": match["Domain"]}
			projection["project"] = map[string]any{"code": project["Code"], "domain": project["Domain"], "httpStatus": project["HTTPStatus"]}
			delete(projection, "Match")
			delete(projection, "Project")
			found = true
		}
	}
	if !found {
		return nil, false, nil
	}
	return cloned, true, nil
}

func hasExactKeys(value map[string]any, keys ...string) bool {
	if len(value) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func currentWire(legacy legacyWireDocument) wireDocument {
	result := wireDocument{
		APIVersion:   legacy.APIVersion,
		Kind:         legacy.Kind,
		SourceDigest: legacy.SourceDigest,
		Sources:      append([]wireSource(nil), legacy.Sources...),
		Types:        append([]wireType(nil), legacy.Types...),
		Operations:   make([]wireOperation, len(legacy.Operations)),
	}
	for index, operation := range legacy.Operations {
		errors := make([]wireErrorProjection, len(operation.ErrorProjections))
		for projectionIndex, projection := range operation.ErrorProjections {
			errors[projectionIndex] = wireErrorProjection{
				Match:   wireErrorMatch{Domain: projection.Match.Domain, Code: projection.Match.Code},
				Project: wireErrorTarget{Domain: projection.Project.Domain, Code: projection.Project.Code, HTTPStatus: projection.Project.HTTPStatus},
			}
		}
		result.Operations[index] = wireOperation{
			ID: operation.ID, Method: operation.Method, Path: operation.Path,
			RequestType: operation.RequestType, ResponseBody: operation.ResponseBody, ResponseType: operation.ResponseType,
			Auth: operation.Auth, Permission: operation.Permission, Capability: operation.Capability,
			ErrorProjections: errors, Provenance: operation.Provenance,
		}
	}
	return result
}
