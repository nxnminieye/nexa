package api

import (
	"sort"
	"strconv"
	"strings"

	generationapi "github.com/nxnminieye/nexa/generation/api"
)

type runtimeContractIssue struct {
	reason  string
	pointer string
}

func validateRuntimeModel(model *runtimeModel) *runtimeContractIssue {
	seenSchemas := make(map[string]int, len(model.schemas))
	for index, schema := range model.schemas {
		base := "/schemas/" + strconv.Itoa(index)
		switch schema.kind {
		case generationapi.SchemaArray:
			if schema.items < 0 || schema.items >= index {
				return runtimeIssue("runtime_schema_index_invalid", base+"/items")
			}
		case generationapi.SchemaObject:
			for _, name := range schema.fieldNames() {
				if field := schema.fields[name]; field.schema < 0 || field.schema >= index {
					return runtimeIssue("runtime_schema_index_invalid", base+"/fields/"+escapeJSONPointer(name)+"/schema")
				}
			}
		}
		key := runtimeSchemaSemanticKey(schema)
		if _, duplicate := seenSchemas[key]; duplicate {
			return runtimeIssue("runtime_schema_duplicate", base)
		}
		seenSchemas[key] = index
	}

	for _, id := range runtimeOperationIDs(model.operations) {
		operation := model.operations[id]
		base := "/operations/" + escapeJSONPointer(id)
		requestSchema, requestExists := model.schema(operation.request.schema)
		if !requestExists {
			return runtimeIssue("runtime_operation_schema_index_invalid", base+"/request/schema")
		}
		if operation.response.hasSchema {
			if _, exists := model.schema(operation.response.schema); !exists {
				return runtimeIssue("runtime_operation_schema_index_invalid", base+"/response/schema")
			}
		}
		if requestSchema.kind != generationapi.SchemaObject {
			return runtimeIssue("runtime_request_schema_kind_invalid", base+"/request/schema")
		}

		bindingNames := sortedRuntimeBindingNames(operation.request.bindings)
		for _, field := range bindingNames {
			if _, exists := requestSchema.field(field); !exists {
				return runtimeIssue("runtime_binding_field_unresolved", base+"/request/bindings/"+escapeJSONPointer(field))
			}
		}
		for _, field := range requestSchema.fieldNames() {
			if _, exists := operation.request.bindings[field]; !exists {
				return runtimeIssue("runtime_binding_field_missing", base+"/request/bindings")
			}
		}
		for _, field := range bindingNames {
			binding := operation.request.bindings[field]
			fieldSpec, _ := requestSchema.field(field)
			fieldSchema, _ := model.schema(fieldSpec.schema)
			if binding.location != generationapi.RequestBindingBody && !runtimeScalarKind(fieldSchema.kind) {
				return runtimeIssue("runtime_binding_schema_kind_invalid", base+"/request/bindings/"+escapeJSONPointer(field))
			}
		}
		for _, field := range bindingNames {
			binding := operation.request.bindings[field]
			fieldSpec, _ := requestSchema.field(field)
			if binding.location == generationapi.RequestBindingPath && !fieldSpec.required {
				return runtimeIssue("runtime_path_field_optional", base+"/request/bindings/"+escapeJSONPointer(field))
			}
		}
		pathFields, pathFieldsValid := runtimePathFieldSet(operation.pathSegments)
		bindingPathFields := make(map[string]struct{})
		for _, field := range bindingNames {
			if operation.request.bindings[field].location == generationapi.RequestBindingPath {
				bindingPathFields[field] = struct{}{}
			}
		}
		if !pathFieldsValid || !sameRuntimeStringSet(pathFields, bindingPathFields) {
			return runtimeIssue("runtime_path_binding_mismatch", base+"/pathSegments")
		}
		for _, field := range bindingNames {
			binding := operation.request.bindings[field]
			if binding.location == generationapi.RequestBindingPath && binding.name != field {
				return runtimeIssue("runtime_path_binding_name_invalid", base+"/request/bindings/"+escapeJSONPointer(field)+"/name")
			}
		}
		if !validRuntimePath(operation.pathSegments) {
			return runtimeIssue("runtime_path_invalid", base+"/pathSegments")
		}

		seenBindingTargets := make(map[string]struct{}, len(bindingNames))
		for _, field := range bindingNames {
			binding := operation.request.bindings[field]
			key := string(binding.location) + "\x00" + runtimeCanonicalWireName(string(binding.location), binding.name)
			if _, duplicate := seenBindingTargets[key]; duplicate {
				return runtimeIssue("runtime_binding_wire_target_duplicate", base+"/request/bindings/"+escapeJSONPointer(field)+"/name")
			}
			seenBindingTargets[key] = struct{}{}
			if binding.location == generationapi.RequestBindingHeader && strings.EqualFold(binding.name, generationapi.RequestContentTypeHeader) {
				return runtimeIssue("runtime_header_name_reserved", base+"/request/bindings/"+escapeJSONPointer(field)+"/name")
			}
		}

		credentials := sortedRuntimeCredentialIDs(operation.auth.credentials)
		if operation.auth.mode == generationapi.AuthNone && len(credentials) != 0 ||
			(operation.auth.mode == generationapi.AuthOptional || operation.auth.mode == generationapi.AuthRequired) && len(credentials) == 0 {
			return runtimeIssue("runtime_credential_combination_invalid", base+"/auth/credentials")
		}
		seenCredentialTargets := make(map[string]struct{}, len(credentials))
		for _, credentialID := range credentials {
			credential := operation.auth.credentials[credentialID]
			credentialBase := base + "/auth/credentials/" + escapeJSONPointer(credentialID)
			switch credential.typeID {
			case generationapi.CredentialBearer:
				if credential.location != generationapi.CredentialLocationHeader {
					return runtimeIssue("runtime_credential_combination_invalid", credentialBase+"/in")
				}
				if credential.name != "authorization" {
					return runtimeIssue("runtime_credential_combination_invalid", credentialBase+"/name")
				}
			case generationapi.CredentialSessionCookie:
				if credential.location != generationapi.CredentialLocationCookie {
					return runtimeIssue("runtime_credential_combination_invalid", credentialBase+"/in")
				}
			}
			directTarget := string(credential.location) + "\x00" + runtimeCanonicalWireName(string(credential.location), credential.name)
			if _, duplicate := seenCredentialTargets[directTarget]; duplicate {
				return runtimeIssue("runtime_credential_wire_target_duplicate", credentialBase+"/name")
			}
			seenCredentialTargets[directTarget] = struct{}{}
			if _, conflict := seenBindingTargets[directTarget]; conflict {
				return runtimeIssue("runtime_credential_binding_conflict", credentialBase+"/name")
			}
			if credential.location == generationapi.CredentialLocationCookie {
				cookieHeaderTarget := string(generationapi.CredentialLocationHeader) + "\x00cookie"
				if _, conflict := seenBindingTargets[cookieHeaderTarget]; conflict {
					return runtimeIssue("runtime_credential_binding_conflict", credentialBase+"/name")
				}
			}
		}
		if operation.permission != "" && operation.auth.mode == generationapi.AuthNone {
			return runtimeIssue("runtime_permission_auth_conflict", base+"/permission")
		}
		if operation.capability != nil && !validRuntimeCapabilityVersion(operation.capability.id, operation.capability.apiVersion) {
			return runtimeIssue("runtime_capability_version_invalid", base+"/capability/apiVersion")
		}
	}
	return nil
}

func runtimeIssue(reason, pointer string) *runtimeContractIssue {
	return &runtimeContractIssue{reason: reason, pointer: pointer}
}

func runtimeScalarKind(kind generationapi.SchemaKind) bool {
	return kind == generationapi.SchemaString || kind == generationapi.SchemaInteger ||
		kind == generationapi.SchemaNumber || kind == generationapi.SchemaBoolean
}

func sortedRuntimeBindingNames(bindings map[string]runtimeBinding) []string {
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedRuntimeCredentialIDs(credentials map[string]runtimeCredential) []string {
	ids := make([]string, 0, len(credentials))
	for id := range credentials {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func runtimePathFieldSet(segments []runtimePathSegment) (map[string]struct{}, bool) {
	result := make(map[string]struct{})
	for _, segment := range segments {
		if segment.field == "" {
			continue
		}
		if _, duplicate := result[segment.field]; duplicate {
			return result, false
		}
		result[segment.field] = struct{}{}
	}
	return result, true
}

func sameRuntimeStringSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, exists := right[value]; !exists {
			return false
		}
	}
	return true
}

func validRuntimePath(segments []runtimePathSegment) bool {
	if len(segments) == 0 || segments[0].field != "" || !strings.HasPrefix(segments[0].literal, "/") {
		return false
	}
	var path strings.Builder
	for index, segment := range segments {
		if segment.field != "" {
			if index == 0 || !strings.HasSuffix(segments[index-1].literal, "/") {
				return false
			}
			if index+1 < len(segments) && (segments[index+1].field != "" || !strings.HasPrefix(segments[index+1].literal, "/")) {
				return false
			}
			path.WriteString("x")
			continue
		}
		path.WriteString(segment.literal)
	}
	value := path.String()
	if value == "/" {
		return true
	}
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#%{}") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for index := 0; index < len(segment); index++ {
			character := segment[index]
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || strings.ContainsRune("._~-", rune(character)) {
				continue
			}
			return false
		}
	}
	return true
}

func runtimeCanonicalWireName(location, name string) string {
	if location == string(generationapi.RequestBindingHeader) || location == string(generationapi.CredentialLocationHeader) {
		return strings.ToLower(name)
	}
	return name
}

func validRuntimeCapabilityVersion(id, version string) bool {
	prefix := id + "/v"
	if !strings.HasPrefix(version, prefix) {
		return false
	}
	value := strings.TrimPrefix(version, prefix)
	if value == "" || value[0] == '0' {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}
