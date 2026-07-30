package httpapi

import (
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
	goctlast "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/ast"
)

func parseServerProperties(value *goctlast.AtServerStmt, source string) (map[string]string, string, error) {
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
			return nil, "", invalid("server_metadata_forbidden", source, "", "Nexa facts must use source-comment v1 on the operation")
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

func projectNativeOperations(groups []spec.Group, index authoredIndex, types []*typeState) ([]*operationState, []*typeState, error) {
	typeNames := map[string]bool{}
	for _, item := range types {
		typeNames[item.name] = true
	}
	var result []*operationState
	var responseTypes []*typeState
	seenID, seenRoute := map[string]bool{}, map[string]bool{}
	for _, group := range groups {
		prefix := group.Annotation.Properties["prefix"]
		groupName := group.Annotation.Properties["group"]
		for _, route := range group.Routes {
			method := api.HTTPMethod(strings.ToUpper(route.Method))
			pathValue, err := normalizeRoutePath(prefix, route.Path)
			if err != nil {
				return nil, nil, invalid("route_path_invalid", "", "", err.Error())
			}
			key := string(method) + "\x00" + pathValue
			source := index.routeFiles[key]
			if source == "" {
				return nil, nil, invalid("route_source_unresolved", "", "", "HTTP API route declaring file cannot be resolved")
			}
			operationID, err := sourcecomment.CanonicalAPIOperationID(groupName, route.Handler)
			if err != nil {
				return nil, nil, invalid("operation_id_invalid", source, "", err.Error())
			}
			operationSource, sourceErr := sourcecomment.ParseSourceRef("api://" + source + "#" + operationID)
			if sourceErr != nil {
				return nil, nil, invalid("operation_source_invalid", source, "", sourceErr.Error())
			}
			operationID = index.projectedSemanticID(operationSource, operationID, sourcecomment.NodeAPIOperation)
			operation, err := operationFromFacts(index.facts, operationID)
			if err != nil {
				return nil, nil, invalid("operation_facts_invalid", source, "", err.Error())
			}
			operation.method, operation.path = method, pathValue
			if route.RequestType != nil {
				operation.requestType = route.RequestType.Name()
				if !typeNames[operation.requestType] {
					return nil, nil, invalid("request_type_unresolved", source, "", "request type cannot be resolved")
				}
			}
			if route.ResponseType == nil {
				operation.responseType = ""
			} else {
				operation.responseType = route.ResponseType.Name()
				if !typeNames[operation.responseType] {
					shape, shapeErr := projectValueType(route.ResponseType)
					if shapeErr != nil || shape.kind != ValueArray {
						return nil, nil, invalid("response_type_unresolved", source, "", "response type cannot be resolved")
					}
					alias := anonymousResponseTypeName(operation.id)
					if typeNames[alias] {
						return nil, nil, invalid("response_type_collision", source, "", "anonymous response type collides with an authored type")
					}
					state := &typeState{name: alias, shape: shape, fieldIndex: map[string]int{}}
					provenanceValue, provenanceErr := nativeProvenance(source, "type:"+alias, canonicalTypeNode{APIVersion: typeNodeVersion, Kind: "type", Name: alias, Shape: canonicalValueOf(shape)})
					if provenanceErr != nil {
						return nil, nil, invalid("type_source_invalid", source, "", provenanceErr.Error())
					}
					state.provenance = provenanceValue
					responseTypes = append(responseTypes, state)
					typeNames[alias] = true
					operation.responseType = alias
				}
			}
			if seenID[operation.id] {
				return nil, nil, invalid("operation_collision", source, "", "operation id is duplicated")
			}
			if seenRoute[key] {
				return nil, nil, invalid("route_collision", source, "", "route is duplicated")
			}
			seenID[operation.id], seenRoute[key] = true, true
			envelope := canonicalRouteNode{APIVersion: routeNodeVersion, Kind: "route", OperationID: operation.id, Method: string(operation.method), Path: operation.path, RequestType: operation.requestType, ResponseType: operation.responseType, Auth: canonicalAuth{Mode: string(operation.auth.mode)}, Permission: operation.permission}
			provenanceValue, err := nativeProvenance(source, "route:"+string(method)+" "+pathValue, envelope)
			if err != nil {
				return nil, nil, invalid("route_source_invalid", source, "", err.Error())
			}
			operation.provenance = provenanceValue
			result = append(result, operation)
		}
	}
	return result, responseTypes, nil
}

func anonymousResponseTypeName(operationID string) string {
	var result strings.Builder
	upper := true
	for _, character := range operationID {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			upper = true
			continue
		}
		if upper {
			character = unicode.ToUpper(character)
			upper = false
		}
		result.WriteRune(character)
	}
	return result.String() + "Response"
}

func operationFromFacts(graph sourcecomment.FactGraph, id string) (*operationState, error) {
	read := func(key string) (string, bool, error) {
		fact, ok := graph.Fact(sourcecomment.FactID{SemanticID: id, Key: key})
		if !ok {
			return "", false, nil
		}
		value, stringValue := fact.Value().String()
		if !stringValue {
			return "", true, fmt.Errorf("%s fact must be a string", key)
		}
		return value, true, nil
	}
	authValue, hasAuth, err := read("auth")
	if err != nil {
		return nil, err
	}
	if !hasAuth {
		return nil, fmt.Errorf("auth fact is required")
	}
	mode := api.AuthMode(authValue)
	if mode != api.AuthNone && mode != api.AuthRequired {
		return nil, fmt.Errorf("auth fact is invalid")
	}
	permission, hasPermission, err := read("permission")
	if err != nil {
		return nil, err
	}
	if mode == api.AuthRequired && !hasPermission {
		return nil, fmt.Errorf("permission fact is required for authenticated operations")
	}
	if mode == api.AuthNone && hasPermission {
		return nil, fmt.Errorf("unauthenticated operation cannot declare permission")
	}
	return &operationState{id: id, permission: permission, auth: Auth{mode: mode}}, nil
}
