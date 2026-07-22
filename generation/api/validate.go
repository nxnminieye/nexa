package api

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

var stableIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
var fieldNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var literalPathSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
var positiveIntegerPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
var errorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

type schemaEdge struct {
	from, to string
	pointer  string
}

func validateSpec(source string, spec ManifestSpec) []*Error {
	failures, validSources := validateSources(source, spec.Sources)
	usedSources := make(map[string]struct{}, len(validSources))
	provenanceValid := true
	schemaIndexes := make(map[string]int, len(spec.Schemas))
	for index, schema := range spec.Schemas {
		base := "/schemas/" + strconv.Itoa(index)
		if !stableIDPattern.MatchString(schema.ID) {
			failures = append(failures, semanticError(source, base+"/id", "schema_id_invalid"))
		}
		if _, duplicate := schemaIndexes[schema.ID]; duplicate {
			failures = append(failures, semanticError(source, base+"/id", "schema_duplicate"))
		} else {
			schemaIndexes[schema.ID] = index
		}
	}
	var edges []schemaEdge
	for index, schema := range spec.Schemas {
		base := "/schemas/" + strconv.Itoa(index)
		switch schema.Kind {
		case SchemaString, SchemaInteger, SchemaNumber, SchemaBoolean:
			expected := map[SchemaKind]string{SchemaString: "scalar.string", SchemaInteger: "scalar.integer", SchemaNumber: "scalar.number", SchemaBoolean: "scalar.boolean"}[schema.Kind]
			if schema.ID != expected || schema.Provenance != nil || len(schema.Fields) != 0 || schema.ItemSchemaRef != "" {
				failures = append(failures, semanticError(source, base+"/id", "scalar_schema_invalid"))
			}
		case SchemaObject:
			if schema.ItemSchemaRef != "" {
				failures = append(failures, semanticError(source, base+"/itemSchemaRef", "schema_shape_invalid"))
			}
		case SchemaArray:
			if len(schema.Fields) != 0 {
				failures = append(failures, semanticError(source, base+"/fields", "schema_shape_invalid"))
			}
			if !stableIDPattern.MatchString(schema.ItemSchemaRef) {
				failures = append(failures, semanticError(source, base+"/itemSchemaRef", "item_schema_ref_invalid"))
			} else if _, exists := schemaIndexes[schema.ItemSchemaRef]; !exists {
				failures = append(failures, semanticError(source, base+"/itemSchemaRef", "item_schema_ref_unresolved"))
			} else {
				edges = append(edges, schemaEdge{from: schema.ID, to: schema.ItemSchemaRef, pointer: base + "/itemSchemaRef"})
			}
		default:
			failures = append(failures, semanticError(source, base+"/kind", "schema_kind_invalid"))
		}
		if schema.Kind == SchemaObject || schema.Kind == SchemaArray {
			value := NodeProvenanceSpec{}
			if schema.Provenance != nil {
				value = *schema.Provenance
			}
			provenanceFailures, refs := validateNodeProvenance(source, base, value, validSources)
			if len(provenanceFailures) > 0 {
				provenanceValid = false
			}
			failures = append(failures, provenanceFailures...)
			markUsedSources(usedSources, refs)
		}
		if schema.Kind != SchemaObject {
			continue
		}
		seenFields := make(map[string]struct{}, len(schema.Fields))
		for fieldIndex, field := range schema.Fields {
			fieldBase := base + "/fields/" + strconv.Itoa(fieldIndex)
			if !fieldNamePattern.MatchString(field.Name) {
				failures = append(failures, semanticError(source, fieldBase+"/name", "field_name_invalid"))
			}
			if _, duplicate := seenFields[field.Name]; duplicate {
				failures = append(failures, semanticError(source, fieldBase+"/name", "field_duplicate"))
			}
			seenFields[field.Name] = struct{}{}
			if !stableIDPattern.MatchString(field.SchemaRef) {
				failures = append(failures, semanticError(source, fieldBase+"/schemaRef", "field_schema_ref_invalid"))
			} else if _, exists := schemaIndexes[field.SchemaRef]; !exists {
				failures = append(failures, semanticError(source, fieldBase+"/schemaRef", "field_schema_ref_unresolved"))
			} else {
				edges = append(edges, schemaEdge{from: schema.ID, to: field.SchemaRef, pointer: fieldBase + "/schemaRef"})
			}
			provenanceFailures, refs := validateNodeProvenance(source, fieldBase, field.Provenance, validSources)
			if len(provenanceFailures) > 0 {
				provenanceValid = false
			}
			failures = append(failures, provenanceFailures...)
			markUsedSources(usedSources, refs)
			if field.Origin != nil {
				originFailures, ref := validateOriginBinding(source, fieldBase, *field.Origin, field.Provenance, validSources)
				if len(originFailures) > 0 {
					provenanceValid = false
				}
				failures = append(failures, originFailures...)
				if ref.String() != "" {
					usedSources[ref.String()] = struct{}{}
				}
			}
		}
	}
	failures = append(failures, schemaCycleFailures(source, edges)...)
	seenOperationIDs := make(map[string]struct{}, len(spec.Operations))
	seenRoutes := make(map[string]struct{}, len(spec.Operations))
	for index, operation := range spec.Operations {
		base := "/operations/" + strconv.Itoa(index)
		if !stableIDPattern.MatchString(operation.ID) {
			failures = append(failures, semanticError(source, base+"/id", "operation_id_invalid"))
		}
		if _, duplicate := seenOperationIDs[operation.ID]; duplicate {
			failures = append(failures, semanticError(source, base+"/id", "operation_duplicate"))
		}
		seenOperationIDs[operation.ID] = struct{}{}
		if !validMethod(operation.Method) {
			failures = append(failures, semanticError(source, base+"/method", "method_invalid"))
		}
		pathVariables, pathValid := parsePathVariables(operation.Path)
		if !pathValid {
			failures = append(failures, semanticError(source, base+"/path", "path_invalid"))
		}
		routeKey := string(operation.Method) + "\x00" + operation.Path
		if _, duplicate := seenRoutes[routeKey]; duplicate {
			failures = append(failures, semanticError(source, base+"/path", "route_duplicate"))
		}
		seenRoutes[routeKey] = struct{}{}
		provenanceFailures, refs := validateNodeProvenance(source, base, operation.Provenance, validSources)
		if len(provenanceFailures) > 0 {
			provenanceValid = false
		}
		failures = append(failures, provenanceFailures...)
		markUsedSources(usedSources, refs)
		requestIndex, requestExists := schemaIndexes[operation.RequestSchemaRef]
		requestRefValid := stableIDPattern.MatchString(operation.RequestSchemaRef)
		if !requestRefValid {
			failures = append(failures, semanticError(source, base+"/requestSchemaRef", "request_schema_ref_invalid"))
		} else if !requestExists {
			failures = append(failures, semanticError(source, base+"/requestSchemaRef", "request_schema_ref_unresolved"))
		} else if spec.Schemas[requestIndex].Kind != SchemaObject {
			failures = append(failures, semanticError(source, base+"/requestSchemaRef", "request_schema_kind_invalid"))
		}
		switch operation.ResponseBody {
		case ResponseBodyJSON:
			if !stableIDPattern.MatchString(operation.ResponseSchemaRef) {
				failures = append(failures, semanticError(source, base+"/responseSchemaRef", "response_schema_ref_invalid"))
			} else if _, exists := schemaIndexes[operation.ResponseSchemaRef]; !exists {
				failures = append(failures, semanticError(source, base+"/responseSchemaRef", "response_schema_ref_unresolved"))
			}
		case ResponseBodyNone:
			if operation.ResponseSchemaRef != "" {
				failures = append(failures, semanticError(source, base+"/responseSchemaRef", "response_schema_ref_forbidden"))
			}
		default:
			failures = append(failures, semanticError(source, base+"/responseBody", "response_body_invalid"))
		}

		var requestFields map[string]FieldSpec
		if requestExists && spec.Schemas[requestIndex].Kind == SchemaObject {
			requestFields = make(map[string]FieldSpec, len(spec.Schemas[requestIndex].Fields))
			for _, field := range spec.Schemas[requestIndex].Fields {
				requestFields[field.Name] = field
			}
		}
		bindingFailures, bindingKeys := validateBindings(source, base, operation.RequestBindings, requestFields, schemaIndexes, spec.Schemas, pathVariables, pathValid)
		failures = append(failures, bindingFailures...)
		credentialFailures := validateAuth(source, base, operation.Auth, operation.RequestBindings, bindingKeys)
		failures = append(failures, credentialFailures...)
		if operation.Permission != "" && !stableIDPattern.MatchString(operation.Permission) {
			failures = append(failures, semanticError(source, base+"/permission", "permission_invalid"))
		}
		if operation.Permission != "" && operation.Auth.Mode == AuthNone {
			failures = append(failures, semanticError(source, base+"/permission", "permission_auth_conflict"))
		}
		failures = append(failures, validateCapability(source, base, operation.Capability)...)
		failures = append(failures, validateErrorProjections(source, base, operation.ErrorProjections)...)
	}
	if provenanceValid {
		failures = append(failures, unreferencedSourceFailures(source, spec.Sources, usedSources)...)
	}
	return failures
}

type bindingKey struct {
	location string
	name     string
	pointer  string
}

func validateBindings(source, base string, bindings []RequestBindingSpec, requestFields map[string]FieldSpec, schemaIndexes map[string]int, schemas []SchemaSpec, pathVariables map[string]struct{}, pathValid bool) ([]*Error, []bindingKey) {
	var failures []*Error
	seenFields := make(map[string]struct{}, len(bindings))
	seenWire := make(map[string]string, len(bindings))
	boundValidFields := make(map[string]struct{}, len(bindings))
	pathBindings := make(map[string]struct{})
	keys := make([]bindingKey, 0, len(bindings))
	bindingsValidForCompleteness := requestFields != nil
	for index, binding := range bindings {
		bindingBase := base + "/requestBindings/" + strconv.Itoa(index)
		if !fieldNamePattern.MatchString(binding.Field) {
			failures = append(failures, semanticError(source, bindingBase+"/field", "binding_field_invalid"))
			bindingsValidForCompleteness = false
		}
		field, fieldExists := requestFields[binding.Field]
		if requestFields != nil && !fieldExists {
			failures = append(failures, semanticError(source, bindingBase+"/field", "binding_field_unresolved"))
			bindingsValidForCompleteness = false
		}
		if _, duplicate := seenFields[binding.Field]; duplicate {
			failures = append(failures, semanticError(source, bindingBase+"/field", "binding_duplicate"))
			bindingsValidForCompleteness = false
		} else if fieldExists {
			boundValidFields[binding.Field] = struct{}{}
		}
		seenFields[binding.Field] = struct{}{}
		if !validBindingLocation(binding.Location) {
			failures = append(failures, semanticError(source, bindingBase+"/in", "binding_location_invalid"))
			bindingsValidForCompleteness = false
		}
		canonicalName := canonicalBindingName(binding.Location, binding.Name)
		if !validBindingName(binding.Location, binding.Name) {
			failures = append(failures, semanticError(source, bindingBase+"/name", "binding_name_invalid"))
			bindingsValidForCompleteness = false
		}
		if binding.Location == RequestBindingHeader && canonicalName == RequestContentTypeHeader {
			failures = append(failures, semanticError(source, bindingBase+"/name", "header_name_reserved"))
		}
		wireKey := string(binding.Location) + "\x00" + canonicalName
		if _, duplicate := seenWire[wireKey]; duplicate {
			failures = append(failures, semanticError(source, bindingBase+"/name", "binding_wire_name_duplicate"))
		}
		seenWire[wireKey] = bindingBase + "/name"
		keys = append(keys, bindingKey{location: string(binding.Location), name: canonicalName, pointer: bindingBase + "/name"})
		if fieldExists && binding.Location != RequestBindingBody {
			if schemaIndex, exists := schemaIndexes[field.SchemaRef]; exists && !isScalarKind(schemas[schemaIndex].Kind) {
				failures = append(failures, semanticError(source, bindingBase+"/field", "binding_schema_kind_invalid"))
			}
		}
		if binding.Location == RequestBindingPath {
			pathBindings[canonicalName] = struct{}{}
			if fieldExists && !field.Required {
				failures = append(failures, semanticError(source, bindingBase+"/field", "binding_path_required"))
			}
		}
	}
	if requestFields != nil && bindingsValidForCompleteness && len(boundValidFields) != len(requestFields) {
		failures = append(failures, semanticError(source, base+"/requestBindings", "binding_field_unresolved"))
	}
	if pathValid && bindingsValidForCompleteness && !sameStringSet(pathVariables, pathBindings) {
		failures = append(failures, semanticError(source, base+"/path", "binding_path_mismatch"))
	}
	return failures, keys
}

func validateAuth(source, base string, auth AuthSpec, bindings []RequestBindingSpec, bindingKeys []bindingKey) []*Error {
	var failures []*Error
	if auth.Mode != AuthNone && auth.Mode != AuthOptional && auth.Mode != AuthRequired {
		failures = append(failures, semanticError(source, base+"/auth/mode", "auth_mode_invalid"))
	}
	if auth.Mode == AuthNone && len(auth.Credentials) != 0 || (auth.Mode == AuthOptional || auth.Mode == AuthRequired) && len(auth.Credentials) == 0 {
		failures = append(failures, semanticError(source, base+"/auth/credentials", "credential_combination_invalid"))
	}
	seenIDs := make(map[string]struct{}, len(auth.Credentials))
	seenWire := make(map[string]struct{}, len(auth.Credentials))
	hasCookie := false
	for index, credential := range auth.Credentials {
		credentialBase := base + "/auth/credentials/" + strconv.Itoa(index)
		if !stableIDPattern.MatchString(credential.ID) {
			failures = append(failures, semanticError(source, credentialBase+"/id", "credential_id_invalid"))
		}
		if _, duplicate := seenIDs[credential.ID]; duplicate {
			failures = append(failures, semanticError(source, credentialBase+"/id", "credential_duplicate"))
		}
		seenIDs[credential.ID] = struct{}{}
		if !validCredentialType(credential.Type) {
			failures = append(failures, semanticError(source, credentialBase+"/type", "credential_type_invalid"))
		}
		if !validCredentialLocation(credential.Location) {
			failures = append(failures, semanticError(source, credentialBase+"/in", "credential_location_invalid"))
		}
		canonicalName := canonicalCredentialName(credential.Location, credential.Name)
		if !validCredentialName(credential.Location, credential.Name) {
			failures = append(failures, semanticError(source, credentialBase+"/name", "credential_name_invalid"))
		}
		if credential.Location == CredentialLocationHeader && canonicalName == RequestContentTypeHeader {
			failures = append(failures, semanticError(source, credentialBase+"/name", "header_name_reserved"))
		}
		if validCredentialType(credential.Type) && validCredentialLocation(credential.Location) && validCredentialName(credential.Location, credential.Name) {
			switch credential.Type {
			case CredentialBearer:
				if credential.Location != CredentialLocationHeader {
					failures = append(failures, semanticError(source, credentialBase+"/in", "credential_combination_invalid"))
				} else if canonicalName != "authorization" {
					failures = append(failures, semanticError(source, credentialBase+"/name", "credential_combination_invalid"))
				}
			case CredentialSessionCookie:
				if credential.Location != CredentialLocationCookie {
					failures = append(failures, semanticError(source, credentialBase+"/in", "credential_combination_invalid"))
				}
			}
		}
		wireKey := string(credential.Location) + "\x00" + canonicalName
		if _, duplicate := seenWire[wireKey]; duplicate {
			failures = append(failures, semanticError(source, credentialBase+"/name", "credential_binding_conflict"))
		}
		seenWire[wireKey] = struct{}{}
		if credential.Location == CredentialLocationCookie {
			hasCookie = true
		}
		for _, binding := range bindingKeys {
			if binding.location == string(credential.Location) && binding.name == canonicalName {
				failures = append(failures, semanticError(source, credentialBase+"/name", "credential_binding_conflict"), semanticError(source, binding.pointer, "credential_binding_conflict"))
			}
		}
	}
	if hasCookie {
		for index, binding := range bindings {
			if binding.Location == RequestBindingHeader && strings.EqualFold(binding.Name, "cookie") {
				bindingPointer := base + "/requestBindings/" + strconv.Itoa(index) + "/name"
				for credentialIndex, credential := range auth.Credentials {
					if credential.Location == CredentialLocationCookie {
						credentialPointer := base + "/auth/credentials/" + strconv.Itoa(credentialIndex) + "/name"
						failures = append(failures, semanticError(source, credentialPointer, "credential_binding_conflict"), semanticError(source, bindingPointer, "credential_binding_conflict"))
					}
				}
			}
		}
	}
	return failures
}

func validateCapability(source, base string, capability *CapabilitySpec) []*Error {
	if capability == nil {
		return nil
	}
	if capability.ID == "" {
		return []*Error{semanticError(source, base+"/capability/id", "capability_incomplete")}
	}
	if capability.APIVersion == "" {
		return []*Error{semanticError(source, base+"/capability/apiVersion", "capability_incomplete")}
	}
	var failures []*Error
	idValid := validCapabilityID(capability.ID)
	if !idValid {
		failures = append(failures, semanticError(source, base+"/capability/id", "capability_id_invalid"))
	}
	if idValid && !validCapabilityVersion(capability.ID, capability.APIVersion) {
		failures = append(failures, semanticError(source, base+"/capability/apiVersion", "capability_version_invalid"))
	}
	return failures
}

func validCapabilityID(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && stableIDPattern.MatchString(parts[0]) && stableIDPattern.MatchString(parts[1])
}

func validCapabilityVersion(id, value string) bool {
	prefix := id + "/v"
	return strings.HasPrefix(value, prefix) && positiveIntegerPattern.MatchString(strings.TrimPrefix(value, prefix))
}

func validateErrorProjections(source, base string, projections []ErrorProjectionSpec) []*Error {
	var failures []*Error
	seen := make(map[string]struct{}, len(projections))
	for index, projection := range projections {
		projectionBase := base + "/errorProjections/" + strconv.Itoa(index)
		if !errorIDPattern.MatchString(projection.Match.Domain) {
			failures = append(failures, semanticError(source, projectionBase+"/match/domain", "error_match_domain_invalid"))
		}
		if !errorIDPattern.MatchString(projection.Match.Code) {
			failures = append(failures, semanticError(source, projectionBase+"/match/code", "error_match_code_invalid"))
		}
		if !errorIDPattern.MatchString(projection.Project.Domain) {
			failures = append(failures, semanticError(source, projectionBase+"/project/domain", "error_project_domain_invalid"))
		}
		if !errorIDPattern.MatchString(projection.Project.Code) {
			failures = append(failures, semanticError(source, projectionBase+"/project/code", "error_project_code_invalid"))
		}
		if projection.Project.HTTPStatus < 400 || projection.Project.HTTPStatus > 599 {
			failures = append(failures, semanticError(source, projectionBase+"/project/httpStatus", "error_http_status_invalid"))
		}
		key := projection.Match.Domain + "\x00" + projection.Match.Code
		if _, duplicate := seen[key]; duplicate {
			failures = append(failures, semanticError(source, projectionBase+"/match", "error_projection_duplicate"))
		}
		seen[key] = struct{}{}
	}
	return failures
}

func validMethod(value HTTPMethod) bool {
	switch value {
	case MethodGET, MethodPOST, MethodPUT, MethodPATCH, MethodDELETE, MethodHEAD, MethodOPTIONS:
		return true
	default:
		return false
	}
}

func parsePathVariables(value string) (map[string]struct{}, bool) {
	variables := map[string]struct{}{}
	if value == "/" {
		return variables, true
	}
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#%") {
		return nil, false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return nil, false
		}
		if strings.HasPrefix(segment, "{") || strings.HasSuffix(segment, "}") {
			if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' || !fieldNamePattern.MatchString(segment[1:len(segment)-1]) {
				return nil, false
			}
			name := segment[1 : len(segment)-1]
			if _, duplicate := variables[name]; duplicate {
				return nil, false
			}
			variables[name] = struct{}{}
		} else if !literalPathSegmentPattern.MatchString(segment) {
			return nil, false
		}
	}
	return variables, true
}

func validBindingLocation(value RequestBindingLocation) bool {
	return value == RequestBindingPath || value == RequestBindingQuery || value == RequestBindingHeader || value == RequestBindingBody
}
func canonicalBindingName(location RequestBindingLocation, name string) string {
	if location == RequestBindingHeader {
		return strings.ToLower(name)
	}
	return name
}
func validBindingName(location RequestBindingLocation, name string) bool {
	if location == RequestBindingHeader {
		return validHTTPToken(name)
	}
	return fieldNamePattern.MatchString(name)
}
func validCredentialType(value CredentialType) bool {
	return value == CredentialBearer || value == CredentialAPIKey || value == CredentialSessionCookie
}
func validCredentialLocation(value CredentialLocation) bool {
	return value == CredentialLocationHeader || value == CredentialLocationQuery || value == CredentialLocationCookie
}
func canonicalCredentialName(location CredentialLocation, name string) string {
	if location == CredentialLocationHeader {
		return strings.ToLower(name)
	}
	return name
}
func validCredentialName(location CredentialLocation, name string) bool {
	switch location {
	case CredentialLocationHeader, CredentialLocationCookie:
		return validHTTPToken(name)
	case CredentialLocationQuery:
		return fieldNamePattern.MatchString(name)
	default:
		return name != ""
	}
}
func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}
func isScalarKind(kind SchemaKind) bool {
	return kind == SchemaString || kind == SchemaInteger || kind == SchemaNumber || kind == SchemaBoolean
}
func sameStringSet(left, right map[string]struct{}) bool {
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

func schemaCycleFailures(source string, edges []schemaEdge) []*Error {
	graph := make(map[string][]string)
	for _, edge := range edges {
		graph[edge.from] = append(graph[edge.from], edge.to)
	}
	var failures []*Error
	for _, edge := range edges {
		if schemaReachable(graph, edge.to, edge.from, map[string]bool{}) {
			failures = append(failures, semanticError(source, edge.pointer, "schema_cycle"))
		}
	}
	return failures
}

func schemaReachable(graph map[string][]string, current, target string, visiting map[string]bool) bool {
	if current == target {
		return true
	}
	if visiting[current] {
		return false
	}
	visiting[current] = true
	defer delete(visiting, current)
	for _, next := range graph[current] {
		if schemaReachable(graph, next, target, visiting) {
			return true
		}
	}
	return false
}

func validateSources(source string, sources []provenance.Source) ([]*Error, map[string]struct{}) {
	var failures []*Error
	valid := make(map[string]struct{}, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for index, item := range sources {
		base := "/sources/" + strconv.Itoa(index)
		ref := item.Ref.String()
		if !validSourceRef(item.Ref) {
			failures = append(failures, semanticError(source, base+"/ref", "source_ref_invalid"))
		} else {
			valid[ref] = struct{}{}
		}
		if _, duplicate := seen[ref]; duplicate {
			failures = append(failures, semanticError(source, base+"/ref", "source_duplicate"))
		}
		seen[ref] = struct{}{}
		if _, err := provenance.ParseDigest(item.Digest.String()); err != nil {
			failures = append(failures, semanticError(source, base+"/digest", "source_digest_invalid"))
		}
	}
	return failures, valid
}

func validSourceRef(ref provenance.SourceRef) bool {
	_, err := provenance.ParseSourceRef(ref.String())
	return err == nil
}

func validateNodeProvenance(source, base string, value NodeProvenanceSpec, sources map[string]struct{}) ([]*Error, []provenance.SourceRef) {
	switch value.Kind {
	case NodeCanonical:
		if len(value.Refs) != 1 {
			return []*Error{semanticError(source, base+"/provenance/refs", "node_provenance_shape_invalid")}, nil
		}
	case NodeDerived:
		if len(value.Refs) == 0 {
			return []*Error{semanticError(source, base+"/provenance/refs", "node_provenance_refs_empty")}, nil
		}
	default:
		return []*Error{semanticError(source, base+"/provenance/kind", "node_provenance_kind_invalid")}, nil
	}

	var failures []*Error
	validRefs := make([]provenance.SourceRef, 0, len(value.Refs))
	validIndexes := make([]int, 0, len(value.Refs))
	seen := make(map[string]struct{}, len(value.Refs))
	for index, ref := range value.Refs {
		pointer := base + "/provenance/refs/" + strconv.Itoa(index)
		if !validSourceRef(ref) {
			failures = append(failures, semanticError(source, pointer, "node_provenance_ref_invalid"))
			continue
		}
		key := ref.String()
		if _, duplicate := seen[key]; duplicate {
			failures = append(failures, semanticError(source, pointer, "node_provenance_ref_duplicate"))
			continue
		}
		seen[key] = struct{}{}
		validRefs = append(validRefs, ref)
		validIndexes = append(validIndexes, index)
	}
	if len(failures) > 0 {
		return failures, nil
	}
	for index, ref := range validRefs {
		if _, exists := sources[ref.String()]; !exists {
			failures = append(failures, semanticError(source, base+"/provenance/refs/"+strconv.Itoa(validIndexes[index]), "node_provenance_ref_unresolved"))
		}
	}
	if len(failures) > 0 {
		return failures, nil
	}
	return nil, validRefs
}

func validateOriginBinding(source, base string, origin OriginBindingSpec, node NodeProvenanceSpec, sources map[string]struct{}) ([]*Error, provenance.SourceRef) {
	pointer := base + "/origin/ref"
	if !validSourceRef(origin.Ref) {
		return []*Error{semanticError(source, pointer, "origin_ref_invalid")}, provenance.SourceRef{}
	}
	for _, ref := range node.Refs {
		if ref == origin.Ref {
			return []*Error{semanticError(source, pointer, "origin_ref_redundant")}, provenance.SourceRef{}
		}
	}
	if _, exists := sources[origin.Ref.String()]; !exists {
		return []*Error{semanticError(source, pointer, "origin_ref_unresolved")}, provenance.SourceRef{}
	}
	return nil, origin.Ref
}

func markUsedSources(used map[string]struct{}, refs []provenance.SourceRef) {
	for _, ref := range refs {
		used[ref.String()] = struct{}{}
	}
}

func unreferencedSourceFailures(source string, sources []provenance.Source, used map[string]struct{}) []*Error {
	ordered := sources
	if source == "" {
		ordered = append([]provenance.Source(nil), sources...)
		sort.Slice(ordered, func(left, right int) bool { return ordered[left].Ref.String() < ordered[right].Ref.String() })
	}
	for index, item := range ordered {
		if _, exists := used[item.Ref.String()]; !exists {
			return []*Error{semanticError(source, "/sources/"+strconv.Itoa(index)+"/ref", "source_unreferenced")}
		}
	}
	return nil
}

func semanticError(source, pointer, reason string) *Error {
	messages := map[string]string{
		"source_ref_invalid":             "API source reference is invalid",
		"source_digest_invalid":          "API source digest is invalid",
		"source_digest_mismatch":         "API manifest source digest does not match sources",
		"source_duplicate":               "API source reference is duplicated",
		"source_unreferenced":            "API source is not referenced by any manifest node",
		"node_provenance_kind_invalid":   "API node provenance kind is invalid",
		"node_provenance_shape_invalid":  "API canonical node provenance must contain exactly one reference",
		"node_provenance_refs_empty":     "API derived node provenance references are empty",
		"node_provenance_ref_invalid":    "API node provenance reference is invalid",
		"node_provenance_ref_duplicate":  "API node provenance reference is duplicated",
		"node_provenance_ref_unresolved": "API node provenance reference is unresolved",
		"origin_ref_invalid":             "API field origin reference is invalid",
		"origin_ref_unresolved":          "API field origin reference is unresolved",
		"origin_ref_redundant":           "API field origin duplicates node provenance",
		"schema_id_invalid":              "API schema identifier is invalid",
		"schema_duplicate":               "API schema identifier is duplicated",
		"schema_kind_invalid":            "API schema kind is invalid",
		"schema_shape_invalid":           "API schema shape is invalid",
		"scalar_schema_invalid":          "API scalar schema is not a built-in pair",
		"schema_cycle":                   "API schema graph contains a cycle",
		"field_name_invalid":             "API field name is invalid",
		"field_duplicate":                "API field name is duplicated",
		"field_schema_ref_invalid":       "API field schema reference is invalid",
		"field_schema_ref_unresolved":    "API field schema reference is unresolved",
		"item_schema_ref_invalid":        "API array item schema reference is invalid",
		"item_schema_ref_unresolved":     "API array item schema reference is unresolved",
		"operation_id_invalid":           "API operation identifier is invalid",
		"operation_duplicate":            "API operation identifier is duplicated",
		"method_invalid":                 "API operation method is invalid",
		"path_invalid":                   "API operation path is invalid",
		"route_duplicate":                "API operation route is duplicated",
		"request_schema_ref_invalid":     "API request schema reference is invalid",
		"request_schema_ref_unresolved":  "API request schema reference is unresolved",
		"request_schema_kind_invalid":    "API request schema must be an object",
		"response_body_invalid":          "API response body mode is invalid",
		"response_schema_ref_invalid":    "API response schema reference is invalid",
		"response_schema_ref_unresolved": "API response schema reference is unresolved",
		"response_schema_ref_forbidden":  "API response schema reference is forbidden",
		"binding_location_invalid":       "API request binding location is invalid",
		"binding_field_invalid":          "API request binding field is invalid",
		"binding_field_unresolved":       "API request binding field is unresolved",
		"binding_name_invalid":           "API request binding name is invalid",
		"binding_duplicate":              "API request field is bound more than once",
		"binding_wire_name_duplicate":    "API request wire name is duplicated",
		"binding_schema_kind_invalid":    "API request binding schema kind is invalid",
		"binding_path_required":          "API path binding field must be required",
		"binding_path_mismatch":          "API path variables do not match path bindings",
		"header_name_reserved":           "API header name is reserved by the framework",
		"auth_mode_invalid":              "API authentication mode is invalid",
		"credential_id_invalid":          "API credential identifier is invalid",
		"credential_duplicate":           "API credential identifier is duplicated",
		"credential_type_invalid":        "API credential type is invalid",
		"credential_location_invalid":    "API credential location is invalid",
		"credential_name_invalid":        "API credential name is invalid",
		"credential_combination_invalid": "API credential combination is invalid",
		"credential_binding_conflict":    "API credential binding conflicts with another wire binding",
		"permission_invalid":             "API permission is invalid",
		"permission_auth_conflict":       "API permission requires authentication",
		"capability_id_invalid":          "API capability identifier is invalid",
		"capability_version_invalid":     "API capability version is invalid",
		"capability_incomplete":          "API capability is incomplete",
		"error_match_domain_invalid":     "API error match domain is invalid",
		"error_match_code_invalid":       "API error match code is invalid",
		"error_project_domain_invalid":   "API error projection domain is invalid",
		"error_project_code_invalid":     "API error projection code is invalid",
		"error_http_status_invalid":      "API error HTTP status is invalid",
		"error_projection_duplicate":     "API error projection match is duplicated",
	}
	return sourceError(reason, source, pointer, messages[reason])
}

func selectError(failures []*Error, normalized any) *Error {
	if len(failures) == 0 {
		return nil
	}
	selected := failures[0]
	for _, failure := range failures[1:] {
		comparison := compareLocations(pointerLocation(failure.pointer), pointerLocation(selected.pointer), normalized)
		if comparison < 0 || comparison == 0 && errorPriority(failure) < errorPriority(selected) {
			selected = failure
		}
	}
	return selected
}

func pointerLocation(pointer string) []string {
	if pointer == "" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for index, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		parts[index] = strings.ReplaceAll(part, "~0", "~")
	}
	return parts
}

func errorPriority(err *Error) int {
	switch err.reason {
	case "version_unsupported", "kind_invalid":
		return 0
	case "document_invalid", "document_unknown_field", "document_duplicate_key", "document_trailing_input", "document_alias_forbidden", "document_merge_key_forbidden", "document_tag_forbidden":
		return 1
	default:
		return 2
	}
}

func compareLocations(left, right []string, normalized any) int {
	limit := min(len(left), len(right))
	parent := normalized
	for index := 0; index < limit; index++ {
		if _, array := parent.([]any); array {
			leftNumber, leftErr := strconv.Atoi(left[index])
			rightNumber, rightErr := strconv.Atoi(right[index])
			if leftErr != nil || rightErr != nil {
				return strings.Compare(left[index], right[index])
			}
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
		} else if comparison := strings.Compare(left[index], right[index]); comparison != 0 {
			return comparison
		}
		parent = childAt(parent, left[index])
	}
	return len(left) - len(right)
}

func childAt(parent any, component string) any {
	switch value := parent.(type) {
	case map[string]any:
		return value[component]
	case []any:
		index, err := strconv.Atoi(component)
		if err == nil && index >= 0 && index < len(value) {
			return value[index]
		}
	}
	return nil
}

func normalizedSpec(spec ManifestSpec) any {
	schemas := make([]any, len(spec.Schemas))
	for i, schema := range spec.Schemas {
		fields := make([]any, len(schema.Fields))
		schemas[i] = map[string]any{"fields": fields}
	}
	operations := make([]any, len(spec.Operations))
	for i, operation := range spec.Operations {
		operations[i] = map[string]any{"requestBindings": make([]any, len(operation.RequestBindings)), "auth": map[string]any{"credentials": make([]any, len(operation.Auth.Credentials))}, "errorProjections": make([]any, len(operation.ErrorProjections))}
	}
	sources := make([]any, len(spec.Sources))
	return map[string]any{"sources": sources, "schemas": schemas, "operations": operations}
}
