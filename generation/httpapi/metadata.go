package httpapi

import (
	"fmt"
	"path"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
	goctlast "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/ast"
)

var serverKeys = map[string]struct{}{
	"nexaOperationId": {}, "nexaAuthMode": {}, "nexaCredentialId": {}, "nexaCredentialType": {}, "nexaCredentialLocation": {}, "nexaCredentialName": {}, "nexaPermission": {}, "nexaCapabilityId": {}, "nexaCapabilityVersion": {},
}

func parseRawServerMetadata(value *goctlast.AtServerStmt, source string) (map[string]string, string, error) {
	result := map[string]string{}
	if value == nil {
		return result, "", nil
	}
	for _, pair := range value.Values {
		key := strings.TrimSuffix(pair.Key.RawText(), ":")
		if _, duplicate := result[key]; duplicate {
			return nil, "", invalid("server_metadata_duplicate", source, "", "@server metadata key is duplicated")
		}
		if strings.HasPrefix(key, "nexa") {
			if _, known := serverKeys[key]; !known {
				return nil, "", invalid("server_metadata_unknown", source, "", "unknown Nexa @server metadata key")
			}
		}
		result[key] = pair.Value.RawText()
	}
	return result, result["prefix"], nil
}

func normalizeRoutePath(prefix, route string) (string, error) {
	value := route
	if prefix != "" {
		value = path.Join("/", prefix, route)
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("route path must be absolute")
	}
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			name := strings.TrimPrefix(segment, ":")
			if name == "" {
				return "", fmt.Errorf("path variable name is required")
			}
			segments[index] = "{" + name + "}"
		}
	}
	return strings.Join(segments, "/"), nil
}

func projectNativeOperations(groups []spec.Group, index authoredIndex, types []*typeState) ([]*operationState, error) {
	typeNames := map[string]bool{}
	for _, item := range types {
		typeNames[item.name] = true
	}
	var result []*operationState
	seenID, seenRoute := map[string]bool{}, map[string]bool{}
	for _, group := range groups {
		prefix := group.Annotation.Properties["prefix"]
		for _, route := range group.Routes {
			method := api.HTTPMethod(strings.ToUpper(route.Method))
			pathValue, err := normalizeRoutePath(prefix, route.Path)
			if err != nil {
				return nil, invalid("route_path_invalid", "", "", err.Error())
			}
			key := string(method) + "\x00" + pathValue
			metadata := index.metadata[key]
			source := index.routeFiles[key]
			if source == "" {
				return nil, invalid("route_source_unresolved", "", "", "HTTP API route declaring file cannot be resolved")
			}
			operation, err := operationFromMetadata(metadata)
			if err != nil {
				return nil, invalid("server_metadata_invalid", source, "", err.Error())
			}
			operation.method, operation.path = method, pathValue
			if route.RequestType != nil {
				operation.requestType = route.RequestType.Name()
				if !typeNames[operation.requestType] {
					return nil, invalid("request_type_unresolved", source, "", "request type cannot be resolved")
				}
			}
			if route.ResponseType == nil {
				operation.responseBody = api.ResponseBodyNone
			} else {
				operation.responseBody, operation.responseType = api.ResponseBodyJSON, route.ResponseType.Name()
				if !typeNames[operation.responseType] {
					return nil, invalid("response_type_unresolved", source, "", "response type cannot be resolved")
				}
			}
			if seenID[operation.id] {
				return nil, invalid("operation_collision", source, "", "operation id is duplicated")
			}
			if seenRoute[key] {
				return nil, invalid("route_collision", source, "", "route is duplicated")
			}
			seenID[operation.id], seenRoute[key] = true, true
			envelope := canonicalRouteNode{APIVersion: routeNodeVersion, Kind: "route", OperationID: operation.id, Method: string(operation.method), Path: operation.path, RequestType: operation.requestType, ResponseBody: string(operation.responseBody), ResponseType: operation.responseType, Auth: canonicalAuth{Mode: string(operation.auth.mode), Credentials: []canonicalCredential{}}, Permission: operation.permission, ErrorProjections: []any{}}
			for _, credential := range operation.auth.credentials {
				envelope.Auth.Credentials = append(envelope.Auth.Credentials, canonicalCredential{ID: credential.id, Type: string(credential.typeID), Location: string(credential.location), Name: credential.name})
			}
			if operation.hasCapability {
				envelope.Capability = &canonicalCapability{ID: operation.capability.id, APIVersion: operation.capability.apiVersion}
			}
			provenanceValue, err := nativeProvenance(source, "route:"+string(method)+" "+pathValue, envelope)
			if err != nil {
				return nil, invalid("route_source_invalid", source, "", err.Error())
			}
			operation.provenance = provenanceValue
			result = append(result, operation)
		}
	}
	return result, nil
}

func operationFromMetadata(values map[string]string) (*operationState, error) {
	for key := range values {
		if strings.HasPrefix(key, "nexa") {
			if _, known := serverKeys[key]; !known {
				return nil, fmt.Errorf("unknown Nexa @server metadata key %q", key)
			}
		}
	}
	id := values["nexaOperationId"]
	if id == "" {
		return nil, fmt.Errorf("nexaOperationId is required")
	}
	mode := api.AuthMode(values["nexaAuthMode"])
	if mode != api.AuthNone && mode != api.AuthOptional && mode != api.AuthRequired {
		return nil, fmt.Errorf("nexaAuthMode is invalid")
	}
	credentialValues := []string{values["nexaCredentialId"], values["nexaCredentialType"], values["nexaCredentialLocation"], values["nexaCredentialName"]}
	credentialCount := 0
	for _, value := range credentialValues {
		if value != "" {
			credentialCount++
		}
	}
	if credentialCount != 0 && credentialCount != len(credentialValues) {
		return nil, fmt.Errorf("credential metadata must be declared together")
	}
	result := &operationState{id: id, permission: values["nexaPermission"], auth: Auth{mode: mode, credentials: []Credential{}}}
	if credentialCount == 0 {
		if mode != api.AuthNone {
			return nil, fmt.Errorf("authenticated route requires one credential")
		}
	} else {
		if mode == api.AuthNone {
			return nil, fmt.Errorf("auth none cannot declare credentials")
		}
		credential := Credential{id: credentialValues[0], typeID: api.CredentialType(credentialValues[1]), location: api.CredentialLocation(credentialValues[2]), name: credentialValues[3]}
		if credential.typeID != api.CredentialBearer && credential.typeID != api.CredentialAPIKey && credential.typeID != api.CredentialSessionCookie {
			return nil, fmt.Errorf("credential type is invalid")
		}
		if credential.location != api.CredentialLocationHeader && credential.location != api.CredentialLocationQuery && credential.location != api.CredentialLocationCookie {
			return nil, fmt.Errorf("credential location is invalid")
		}
		if credential.location == api.CredentialLocationHeader {
			credential.name = strings.ToLower(credential.name)
		}
		result.auth.credentials = []Credential{credential}
	}
	capabilityID, capabilityVersion := values["nexaCapabilityId"], values["nexaCapabilityVersion"]
	if (capabilityID == "") != (capabilityVersion == "") {
		return nil, fmt.Errorf("capability metadata must be declared together")
	}
	if capabilityID != "" {
		result.capability, result.hasCapability = Capability{id: capabilityID, apiVersion: capabilityVersion}, true
	}
	return result, nil
}

func validateTypeCycles(types []*typeState) error {
	graph := map[string][]string{}
	for _, item := range types {
		refs := graph[item.name]
		for _, field := range item.fields {
			collectRefs(field.valueType, &refs)
		}
		graph[item.name] = refs
	}
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return invalid("type_reference_cycle", "", "", "HTTP API type reference cycle detected")
		}
		if state[name] == 2 {
			return nil
		}
		state[name] = 1
		for _, next := range graph[name] {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for name := range graph {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func collectRefs(value ValueType, output *[]string) {
	if value.kind == ValueRef {
		*output = append(*output, value.name)
	}
	if value.element != nil {
		collectRefs(*value.element, output)
	}
	if value.key != nil {
		collectRefs(*value.key, output)
	}
	if value.value != nil {
		collectRefs(*value.value, output)
	}
}
