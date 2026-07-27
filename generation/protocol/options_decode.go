package protocol

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	stableMetadataID      = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	operationPermissionID = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	httpFieldName         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	literalPathSegment    = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
)

func decodeHTTPProxy(value protoreflect.Message, method protoreflect.MethodDescriptor) (*httpProxyState, error) {
	state := &httpProxyState{
		operationID:    stringValue(value, "operation_id"),
		path:           stringValue(value, "path"),
		permission:     stringValue(value, "permission"),
		requestFields:  make([]*requestFieldState, 0),
		responseFields: make([]*responseFieldState, 0),
		errors:         make([]*errorProjectionState, 0),
	}
	if !operationPermissionID.MatchString(state.operationID) {
		return nil, optionError(method, "operation_id_invalid")
	}
	var ok bool
	if state.method, ok = decodeHTTPMethod(enumName(value, "method")); !ok {
		return nil, optionError(method, "method_invalid")
	}
	pathVariables, routeOK := parseRouteVariables(state.path)
	if !routeOK {
		return nil, optionError(method, "path_invalid")
	}
	authValue, present := messageValue(value, "auth")
	if !present {
		return nil, optionError(method, "auth_missing")
	}
	auth, err := decodeAuth(authValue, method)
	if err != nil {
		return nil, err
	}
	state.auth = auth
	if state.permission != "" && !operationPermissionID.MatchString(state.permission) {
		return nil, optionError(method, "permission_invalid")
	}
	if state.auth.mode == AuthNone && state.permission != "" {
		return nil, optionError(method, "permission_auth_conflict")
	}
	if state.auth.mode != AuthNone && state.permission == "" {
		return nil, optionError(method, "permission_auth_conflict")
	}
	if state.requestFields, err = decodeRequestFields(value, method); err != nil {
		return nil, err
	}
	boundFields := make(map[string]struct{}, len(state.requestFields))
	for _, binding := range state.requestFields {
		boundFields[binding.httpField] = struct{}{}
		field, fieldErr := resolveRPCField(method.Input(), rpcPathName(method.Input(), binding.rpcPath))
		if fieldErr != nil {
			return nil, optionError(method, "rpc_field_unresolved")
		}
		complex := field.Cardinality() == protoreflect.Repeated || field.Kind() == protoreflect.MessageKind
		_, pathBound := pathVariables[binding.httpField]
		if complex && (pathBound || state.method == MethodGET || state.method == MethodDELETE) {
			return nil, optionError(method, "body_binding_required")
		}
	}
	if !pathVariablesBound(pathVariables, boundFields) {
		return nil, optionError(method, "path_binding_mismatch")
	}
	if state.responseFields, err = decodeResponseFields(value, method); err != nil {
		return nil, err
	}
	if state.errors, err = decodeErrors(value, method); err != nil {
		return nil, err
	}
	return state, nil
}

func decodeRPCContext(value protoreflect.Message, method protoreflect.MethodDescriptor) (*rpcContextState, error) {
	fields, err := decodeContextFields(value, method)
	if err != nil {
		return nil, err
	}
	return &rpcContextState{contextFields: fields}, nil
}

func decodeAuth(value protoreflect.Message, method protoreflect.MethodDescriptor) (authState, error) {
	mode, ok := decodeAuthMode(enumName(value, "mode"))
	if !ok {
		return authState{}, optionError(method, "auth_mode_invalid")
	}
	result := authState{mode: mode, credentials: make([]*credentialState, 0)}
	seenID := map[string]struct{}{}
	seenWire := map[string]struct{}{}
	for _, item := range repeatedMessages(value, "credentials") {
		credential := &credentialState{id: stringValue(item, "id"), name: stringValue(item, "name")}
		if !stableMetadataID.MatchString(credential.id) {
			return authState{}, optionError(method, "credential_id_invalid")
		}
		if _, duplicate := seenID[credential.id]; duplicate {
			return authState{}, optionError(method, "credential_duplicate")
		}
		seenID[credential.id] = struct{}{}
		if credential.typeID, ok = decodeCredentialType(enumName(item, "type")); !ok {
			return authState{}, optionError(method, "credential_type_invalid")
		}
		if credential.location, ok = decodeCredentialLocation(enumName(item, "location")); !ok {
			return authState{}, optionError(method, "credential_location_invalid")
		}
		if !validCredentialName(credential.location, credential.name) {
			return authState{}, optionError(method, "credential_name_invalid")
		}
		if credential.location == CredentialHeader {
			credential.name = strings.ToLower(credential.name)
			if credential.name == "content-type" {
				return authState{}, optionError(method, "header_name_reserved")
			}
		}
		if credential.typeID == CredentialBearer && (credential.location != CredentialHeader || credential.name != "authorization") || credential.typeID == CredentialSessionCookie && credential.location != CredentialCookie {
			return authState{}, optionError(method, "credential_combination_invalid")
		}
		wire := string(credential.location) + "\x00" + credential.name
		if _, duplicate := seenWire[wire]; duplicate {
			return authState{}, optionError(method, "credential_duplicate")
		}
		seenWire[wire] = struct{}{}
		result.credentials = append(result.credentials, credential)
	}
	if mode == AuthNone && len(result.credentials) != 0 || mode != AuthNone && len(result.credentials) == 0 {
		return authState{}, optionError(method, "credential_combination_invalid")
	}
	sort.Slice(result.credentials, func(i, j int) bool { return result.credentials[i].id < result.credentials[j].id })
	return result, nil
}

func decodeRequestFields(value protoreflect.Message, method protoreflect.MethodDescriptor) ([]*requestFieldState, error) {
	result := make([]*requestFieldState, 0)
	seenHTTP, seenRPC := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range repeatedMessages(value, "request_fields") {
		binding := &requestFieldState{httpField: stringValue(item, "http_field")}
		if !httpFieldName.MatchString(binding.httpField) {
			return nil, optionError(method, "http_field_invalid")
		}
		terminal, terminalErr := resolveRPCField(method.Input(), stringValue(item, "rpc_field"))
		if terminalErr != nil {
			return nil, optionError(method, "rpc_field_unresolved")
		}
		if !supportedBindingTerminal(terminal) {
			return nil, optionError(method, "binding_terminal_unsupported")
		}
		var err error
		binding.rpcPath, err = resolveRPCPath(method.Input(), stringValue(item, "rpc_field"))
		if err != nil {
			return nil, optionError(method, "rpc_field_unresolved")
		}
		key := strings.Join(binding.rpcPath, "\x00")
		if _, duplicate := seenHTTP[binding.httpField]; duplicate {
			return nil, optionError(method, "binding_destination_duplicate")
		}
		if _, duplicate := seenRPC[key]; duplicate {
			return nil, optionError(method, "binding_destination_duplicate")
		}
		seenHTTP[binding.httpField], seenRPC[key] = struct{}{}, struct{}{}
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].httpField != result[j].httpField {
			return result[i].httpField < result[j].httpField
		}
		return strings.Join(result[i].rpcPath, "\x00") < strings.Join(result[j].rpcPath, "\x00")
	})
	return result, nil
}

func expectedContextKind(source ContextValue) protoreflect.Kind {
	if source == ContextTenantID {
		return protoreflect.Int64Kind
	}
	return protoreflect.StringKind
}

func resolveRPCField(root protoreflect.MessageDescriptor, value string) (protoreflect.FieldDescriptor, error) {
	if value == "" {
		return nil, errProtocolInvalid
	}
	segments := strings.Split(value, ".")
	current := root
	for index, segment := range segments {
		if segment == "" {
			return nil, errProtocolInvalid
		}
		field := current.Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return nil, errProtocolInvalid
		}
		if index == len(segments)-1 {
			return field, nil
		}
		if field.Cardinality() == protoreflect.Repeated || field.IsMap() || field.Message() == nil {
			return nil, errProtocolInvalid
		}
		current = field.Message()
	}
	return nil, errProtocolInvalid
}

func decodeContextFields(value protoreflect.Message, method protoreflect.MethodDescriptor) ([]*contextFieldState, error) {
	result := make([]*contextFieldState, 0)
	seenSource, seenRPC := map[ContextValue]struct{}{}, map[string]struct{}{}
	for _, item := range repeatedMessages(value, "context_fields") {
		binding := &contextFieldState{}
		var ok bool
		if binding.source, ok = decodeContextValue(enumName(item, "source")); !ok {
			return nil, optionError(method, "context_source_invalid")
		}
		field, err := resolveRPCField(method.Input(), stringValue(item, "rpc_field"))
		if err != nil || field.Cardinality() != protoreflect.Optional || field.HasPresence() || field.Kind() != expectedContextKind(binding.source) {
			return nil, optionError(method, "context_field_type_invalid")
		}
		binding.rpcPath, err = resolveRPCPath(method.Input(), stringValue(item, "rpc_field"))
		if err != nil {
			return nil, optionError(method, "rpc_field_unresolved")
		}
		key := strings.Join(binding.rpcPath, "\x00")
		if _, duplicate := seenSource[binding.source]; duplicate {
			return nil, optionError(method, "binding_destination_duplicate")
		}
		if _, duplicate := seenRPC[key]; duplicate {
			return nil, optionError(method, "binding_destination_duplicate")
		}
		seenSource[binding.source], seenRPC[key] = struct{}{}, struct{}{}
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].source != result[j].source {
			return result[i].source < result[j].source
		}
		return strings.Join(result[i].rpcPath, "\x00") < strings.Join(result[j].rpcPath, "\x00")
	})
	return result, nil
}

func decodeResponseFields(value protoreflect.Message, method protoreflect.MethodDescriptor) ([]*responseFieldState, error) {
	result := make([]*responseFieldState, 0)
	seenHTTP, seenRPC := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range repeatedMessages(value, "response_fields") {
		binding := &responseFieldState{httpField: stringValue(item, "http_field")}
		if !httpFieldName.MatchString(binding.httpField) {
			return nil, optionError(method, "http_field_invalid")
		}
		terminal, terminalErr := resolveRPCField(method.Output(), stringValue(item, "rpc_field"))
		if terminalErr != nil {
			return nil, optionError(method, "rpc_field_unresolved")
		}
		if !supportedBindingTerminal(terminal) {
			return nil, optionError(method, "binding_terminal_unsupported")
		}
		var err error
		binding.rpcPath, err = resolveRPCPath(method.Output(), stringValue(item, "rpc_field"))
		if err != nil {
			return nil, optionError(method, "rpc_field_unresolved")
		}
		key := strings.Join(binding.rpcPath, "\x00")
		if _, duplicate := seenHTTP[binding.httpField]; duplicate {
			return nil, optionError(method, "binding_destination_duplicate")
		}
		if _, duplicate := seenRPC[key]; duplicate {
			return nil, optionError(method, "binding_destination_duplicate")
		}
		seenHTTP[binding.httpField], seenRPC[key] = struct{}{}, struct{}{}
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].httpField != result[j].httpField {
			return result[i].httpField < result[j].httpField
		}
		return strings.Join(result[i].rpcPath, "\x00") < strings.Join(result[j].rpcPath, "\x00")
	})
	return result, nil
}

func decodeErrors(value protoreflect.Message, method protoreflect.MethodDescriptor) ([]*errorProjectionState, error) {
	result := make([]*errorProjectionState, 0)
	seen := map[string]struct{}{}
	for _, item := range repeatedMessages(value, "errors") {
		match, matchOK := messageValue(item, "match")
		project, projectOK := messageValue(item, "project")
		if !matchOK || !projectOK {
			return nil, optionError(method, "error_projection_incomplete")
		}
		entry := &errorProjectionState{match: errorMatchState{domain: stringValue(match, "domain"), code: stringValue(match, "code")}, project: errorTargetState{domain: stringValue(project, "domain"), code: stringValue(project, "code"), httpStatus: int(uintValue(project, "http_status"))}}
		if !stableMetadataID.MatchString(entry.match.domain) || !stableMetadataID.MatchString(entry.match.code) || !stableMetadataID.MatchString(entry.project.domain) || !stableMetadataID.MatchString(entry.project.code) || entry.project.httpStatus < 400 || entry.project.httpStatus > 599 {
			return nil, optionError(method, "error_projection_invalid")
		}
		key := entry.match.domain + "\x00" + entry.match.code
		if _, duplicate := seen[key]; duplicate {
			return nil, optionError(method, "error_projection_duplicate")
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].match.domain != result[j].match.domain {
			return result[i].match.domain < result[j].match.domain
		}
		return result[i].match.code < result[j].match.code
	})
	return result, nil
}

func resolveRPCPath(root protoreflect.MessageDescriptor, value string) ([]string, error) {
	if value == "" {
		return nil, errProtocolInvalid
	}
	segments := strings.Split(value, ".")
	current := root
	result := make([]string, 0, len(segments))
	for index, segment := range segments {
		if segment == "" {
			return nil, errProtocolInvalid
		}
		field := current.Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return nil, errProtocolInvalid
		}
		result = append(result, string(current.FullName())+"#"+strconv.Itoa(int(field.Number())))
		if index < len(segments)-1 {
			if field.Cardinality() == protoreflect.Repeated || field.IsMap() || field.Message() == nil {
				return nil, errProtocolInvalid
			}
			current = field.Message()
		} else if !supportedBindingTerminal(field) {
			return nil, errProtocolInvalid
		}
	}
	return result, nil
}

func supportedBindingTerminal(field protoreflect.FieldDescriptor) bool {
	if field == nil || field.IsMap() {
		return false
	}
	if oneof := field.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
		return false
	}
	switch field.Kind() {
	case protoreflect.BoolKind, protoreflect.EnumKind, protoreflect.StringKind, protoreflect.BytesKind,
		protoreflect.DoubleKind, protoreflect.FloatKind, protoreflect.Int32Kind, protoreflect.Sint32Kind,
		protoreflect.Sfixed32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind,
		protoreflect.MessageKind:
		return true
	default:
		return false
	}
}

func rpcPathName(root protoreflect.MessageDescriptor, path []string) string {
	current := root
	segments := make([]string, 0, len(path))
	for _, typed := range path {
		_, numberText, ok := strings.Cut(typed, "#")
		number, err := strconv.Atoi(numberText)
		if !ok || err != nil || current == nil {
			return ""
		}
		field := current.Fields().ByNumber(protoreflect.FieldNumber(number))
		if field == nil {
			return ""
		}
		segments = append(segments, string(field.Name()))
		current = field.Message()
	}
	return strings.Join(segments, ".")
}

func parseRouteVariables(value string) (map[string]struct{}, bool) {
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
			if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' || !httpFieldName.MatchString(segment[1:len(segment)-1]) {
				return nil, false
			}
			name := segment[1 : len(segment)-1]
			if _, duplicate := variables[name]; duplicate {
				return nil, false
			}
			variables[name] = struct{}{}
		} else if !literalPathSegment.MatchString(segment) {
			return nil, false
		}
	}
	return variables, true
}

func validRoute(value string) bool { _, ok := parseRouteVariables(value); return ok }
func pathVariablesBound(variables, boundFields map[string]struct{}) bool {
	for value := range variables {
		if _, ok := boundFields[value]; !ok {
			return false
		}
	}
	return true
}
func validCredentialName(location CredentialLocation, name string) bool {
	switch location {
	case CredentialHeader, CredentialCookie:
		return validHTTPToken(name)
	case CredentialQuery:
		return httpFieldName.MatchString(name)
	default:
		return false
	}
}
func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			continue
		}
		return false
	}
	return true
}

func optionError(method protoreflect.MethodDescriptor, reason string) *Error {
	err := protocolError("protocol_ir_invalid", reason, method.ParentFile().Path(), "", "Proto HTTP proxy metadata is invalid")
	location := method.ParentFile().SourceLocations().ByDescriptor(method)
	err.line, err.column = location.StartLine+1, location.StartColumn+1
	return err
}

func fieldByName(message protoreflect.Message, name protoreflect.Name) protoreflect.FieldDescriptor {
	return message.Descriptor().Fields().ByName(name)
}
func stringValue(message protoreflect.Message, name protoreflect.Name) string {
	return message.Get(fieldByName(message, name)).String()
}
func uintValue(message protoreflect.Message, name protoreflect.Name) uint64 {
	return message.Get(fieldByName(message, name)).Uint()
}
func enumName(message protoreflect.Message, name protoreflect.Name) string {
	field := fieldByName(message, name)
	value := message.Get(field).Enum()
	descriptor := field.Enum().Values().ByNumber(value)
	if descriptor == nil {
		return ""
	}
	return string(descriptor.Name())
}
func messageValue(message protoreflect.Message, name protoreflect.Name) (protoreflect.Message, bool) {
	field := fieldByName(message, name)
	if !message.Has(field) {
		return nil, false
	}
	return message.Get(field).Message(), true
}
func repeatedMessages(message protoreflect.Message, name protoreflect.Name) []protoreflect.Message {
	list := message.Get(fieldByName(message, name)).List()
	result := make([]protoreflect.Message, list.Len())
	for i := 0; i < list.Len(); i++ {
		result[i] = list.Get(i).Message()
	}
	return result
}

func decodeHTTPMethod(value string) (HTTPMethod, bool) {
	result := map[string]HTTPMethod{"GET": MethodGET, "POST": MethodPOST, "PUT": MethodPUT, "PATCH": MethodPATCH, "DELETE": MethodDELETE}[value]
	return result, result != ""
}
func decodeAuthMode(value string) (AuthMode, bool) {
	result := map[string]AuthMode{"NONE": AuthNone, "OPTIONAL": AuthOptional, "REQUIRED": AuthRequired}[value]
	return result, result != ""
}
func decodeCredentialType(value string) (CredentialType, bool) {
	result := map[string]CredentialType{"BEARER": CredentialBearer, "API_KEY": CredentialAPIKey, "SESSION_COOKIE": CredentialSessionCookie}[value]
	return result, result != ""
}
func decodeCredentialLocation(value string) (CredentialLocation, bool) {
	result := map[string]CredentialLocation{"HEADER": CredentialHeader, "QUERY": CredentialQuery, "COOKIE": CredentialCookie}[value]
	return result, result != ""
}
func decodeContextValue(value string) (ContextValue, bool) {
	result := map[string]ContextValue{"SUBJECT_ID": ContextSubjectID, "TENANT_ID": ContextTenantID, "REQUEST_ID": ContextRequestID, "TRACE_ID": ContextTraceID}[value]
	return result, result != ""
}
