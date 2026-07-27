package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

type Snapshot struct {
	state  *snapshotState
	marker snapshotMarker
}
type snapshotMarker struct{ _ [0]func() }
type SnapshotMethod struct{ state *snapshotMethodState }
type snapshotState struct {
	canonical    []byte
	methods      map[string]*snapshotMethodState
	sources      []provenance.Source
	sourceDigest provenance.Digest
	usedSources  map[string]struct{}
	operationIDs map[string]struct{}
	routes       map[string]struct{}
	descriptors  *snapshotDescriptorIndex
	tenantType   string
}
type snapshotMethodState struct {
	fullName   string
	proxy      *httpProxyState
	rpcContext *rpcContextState
}

func ParseSnapshot(source provenance.DomainSource, data []byte) (Snapshot, error) {
	if source.String() == "" {
		return Snapshot{}, snapshotError("document_invalid", "", "")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire canonicalDocument
	if err := decoder.Decode(&wire); err != nil {
		return Snapshot{}, snapshotError("document_invalid", source.String(), "")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Snapshot{}, snapshotError("document_invalid", source.String(), "")
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil || validateSnapshotSchema(schemaValue) != nil {
		return Snapshot{}, snapshotError("document_invalid", source.String(), "")
	}
	if wire.APIVersion != APIVersion {
		return Snapshot{}, snapshotError("version_unsupported", source.String(), "/apiVersion")
	}
	if wire.Kind != Kind {
		return Snapshot{}, snapshotError("kind_invalid", source.String(), "/kind")
	}
	canonical, err := canonicalize(wire)
	if err != nil || !bytes.Equal(canonical, data) {
		return Snapshot{}, snapshotError("canonical_order_invalid", source.String(), "")
	}
	storedDigest, err := provenance.ParseDigest(wire.SourceDigest)
	if err != nil {
		return Snapshot{}, snapshotError("source_digest_invalid", source.String(), "/sourceDigest")
	}
	sources := make([]provenance.Source, len(wire.Sources))
	sourceIndex := make(map[string]provenance.Source, len(wire.Sources))
	previous := ""
	for i, item := range wire.Sources {
		ref, refErr := provenance.ParseSourceRef(item.Ref)
		digest, digestErr := provenance.ParseDigest(item.Digest)
		if refErr != nil || digestErr != nil || previous != "" && item.Ref <= previous {
			return Snapshot{}, snapshotError("source_invalid", source.String(), "/sources")
		}
		previous = item.Ref
		sources[i] = provenance.Source{Ref: ref, Digest: digest}
		sourceIndex[item.Ref] = sources[i]
	}
	setBytes, err := canonicalize(canonicalSourceSet{APIVersion: SourceSetAPIVersion, Sources: wire.Sources})
	if err != nil || provenance.SHA256(setBytes) != storedDigest {
		return Snapshot{}, snapshotError("source_digest_mismatch", source.String(), "/sourceDigest")
	}
	descriptors, err := buildSnapshotDescriptorIndex(wire)
	if err != nil {
		return Snapshot{}, snapshotError(err.(*Error).Reason(), source.String(), err.(*Error).Pointer())
	}
	state := &snapshotState{canonical: append([]byte(nil), canonical...), methods: map[string]*snapshotMethodState{}, sources: sources, sourceDigest: storedDigest, usedSources: map[string]struct{}{}, operationIDs: map[string]struct{}{}, routes: map[string]struct{}{}, descriptors: descriptors}
	previousFile := ""
	for _, file := range wire.Files {
		if previousFile != "" && file.Path <= previousFile {
			return Snapshot{}, snapshotError("canonical_order_invalid", source.String(), "/files")
		}
		previousFile = file.Path
		if err := validateSnapshotFile(file, sourceIndex, state); err != nil {
			return Snapshot{}, snapshotError(err.(*Error).Reason(), source.String(), err.(*Error).Pointer())
		}
	}
	if len(state.usedSources) != len(sources) {
		return Snapshot{}, snapshotError("source_closure_invalid", source.String(), "/sources")
	}
	return Snapshot{state: state}, nil
}

func validateSnapshotFile(file canonicalFile, sources map[string]provenance.Source, state *snapshotState) error {
	previous := ""
	for _, message := range file.Messages {
		if previous != "" && message.FullName <= previous {
			return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/messages", "Protocol snapshot order is invalid")
		}
		previous = message.FullName
		if err := validateProjectedSource(file.Path, "message:"+message.FullName, message.SourceRef, canonicalMessageNode{APIVersion: messageNodeAPIVersion, Kind: "message", FullName: message.FullName}, sources, state); err != nil {
			return err
		}
		lastNumber := 0
		for _, field := range message.Fields {
			if field.Number <= lastNumber {
				return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/messages/fields", "Protocol snapshot order is invalid")
			}
			lastNumber = field.Number
			node := canonicalFieldNode{APIVersion: fieldNodeAPIVersion, Kind: "field", FullName: field.FullName, Number: field.Number, JSONName: field.JSONName, Cardinality: field.Cardinality, Presence: field.Presence, Type: field.Type, Oneof: field.Oneof}
			if err := validateSnapshotField(message.FullName, field, state.descriptors, file.Path); err != nil {
				return err
			}
			if err := validateProjectedSource(file.Path, "field:"+field.FullName, field.SourceRef, node, sources, state); err != nil {
				return err
			}
		}
	}
	previous = ""
	for _, enum := range file.Enums {
		if previous != "" && enum.FullName <= previous {
			return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/enums", "Protocol snapshot order is invalid")
		}
		previous = enum.FullName
	}
	previous = ""
	for _, service := range file.Services {
		if previous != "" && service.FullName <= previous {
			return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/services", "Protocol snapshot order is invalid")
		}
		previous = service.FullName
		previousMethod := ""
		for _, method := range service.Methods {
			if previousMethod != "" && method.FullName <= previousMethod {
				return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", file.Path, "/services/methods", "Protocol snapshot order is invalid")
			}
			previousMethod = method.FullName
			if !strings.HasPrefix(method.FullName, service.FullName+".") || state.descriptors.messages[method.Input] == nil || state.descriptors.messages[method.Output] == nil {
				return protocolError("protocol_snapshot_invalid", "method_descriptor_invalid", file.Path, "/services/methods", "Protocol snapshot method descriptor is invalid")
			}
			if err := validateSnapshotProxy(method, state, file.Path); err != nil {
				return err
			}
			if err := validateSnapshotRPCContext(method, state, file.Path); err != nil {
				return err
			}
			node := canonicalMethodNode{APIVersion: methodNodeAPIVersion, Kind: "method", FullName: method.FullName, Input: method.Input, Output: method.Output, ClientStreaming: method.ClientStreaming, ServerStreaming: method.ServerStreaming, HTTPProxy: method.HTTPProxy, RPCContext: method.RPCContext}
			if err := validateProjectedSource(file.Path, "method:"+method.FullName, method.SourceRef, node, sources, state); err != nil {
				return err
			}
			if _, duplicate := state.methods[method.FullName]; duplicate {
				return protocolError("protocol_snapshot_invalid", "method_duplicate", file.Path, "/services/methods", "Protocol snapshot method is duplicated")
			}
			state.methods[method.FullName] = &snapshotMethodState{fullName: method.FullName, proxy: proxyStateFromCanonical(method.HTTPProxy), rpcContext: rpcContextStateFromCanonical(method.RPCContext)}
		}
	}
	return nil
}

func validateProjectedSource(filePath, fragment, sourceRef string, node any, sources map[string]provenance.Source, state *snapshotState) error {
	ref, err := provenance.RepositoryRef(filePath, fragment)
	if err != nil || ref.String() != sourceRef {
		return protocolError("protocol_snapshot_invalid", "source_ref_invalid", filePath, "/sourceRef", "Protocol snapshot source reference is invalid")
	}
	source, ok := sources[sourceRef]
	if !ok {
		return protocolError("protocol_snapshot_invalid", "source_closure_invalid", filePath, "/sourceRef", "Protocol snapshot source closure is invalid")
	}
	state.usedSources[sourceRef] = struct{}{}
	encoded, err := canonicalize(node)
	if err != nil || provenance.SHA256(encoded) != source.Digest {
		return protocolError("protocol_snapshot_invalid", "source_digest_mismatch", filePath, "/sourceRef", "Protocol snapshot owner digest is invalid")
	}
	return nil
}

func validateSnapshotProxy(method canonicalMethod, state *snapshotState, filePath string) error {
	proxy := method.HTTPProxy
	if proxy == nil {
		return nil
	}
	if method.ClientStreaming || method.ServerStreaming {
		return protocolError("protocol_snapshot_invalid", "streaming_proxy_invalid", filePath, "", "Protocol snapshot HTTP proxy is invalid")
	}
	pathVariables, routeOK := parseRouteVariables(proxy.Path)
	if !operationPermissionID.MatchString(proxy.OperationID) || !routeOK || !validHTTPMethod(proxy.Method) {
		return protocolError("protocol_snapshot_invalid", "http_proxy_invalid", filePath, "", "Protocol snapshot HTTP proxy is invalid")
	}
	if _, duplicate := state.operationIDs[proxy.OperationID]; duplicate {
		return protocolError("protocol_snapshot_invalid", "operation_id_duplicate", filePath, "", "Protocol snapshot operation id is duplicated")
	}
	state.operationIDs[proxy.OperationID] = struct{}{}
	route := string(proxy.Method) + "\x00" + proxy.Path
	if _, duplicate := state.routes[route]; duplicate {
		return protocolError("protocol_snapshot_invalid", "route_duplicate", filePath, "", "Protocol snapshot route is duplicated")
	}
	state.routes[route] = struct{}{}
	if proxy.Auth.Mode != AuthNone && proxy.Auth.Mode != AuthOptional && proxy.Auth.Mode != AuthRequired || proxy.Auth.Mode == AuthNone && (len(proxy.Auth.Credentials) != 0 || proxy.Permission != "") || proxy.Auth.Mode != AuthNone && (len(proxy.Auth.Credentials) == 0 || proxy.Permission == "" || !operationPermissionID.MatchString(proxy.Permission)) {
		return protocolError("protocol_snapshot_invalid", "auth_invalid", filePath, "", "Protocol snapshot auth is invalid")
	}
	previous := ""
	wires := map[string]struct{}{}
	for _, credential := range proxy.Auth.Credentials {
		if !stableMetadataID.MatchString(credential.ID) || previous != "" && credential.ID <= previous || !validCredential(credential) {
			return protocolError("protocol_snapshot_invalid", "credential_invalid", filePath, "", "Protocol snapshot credential is invalid")
		}
		previous = credential.ID
		wire := string(credential.In) + "\x00" + credential.Name
		if _, duplicate := wires[wire]; duplicate {
			return protocolError("protocol_snapshot_invalid", "credential_duplicate", filePath, "", "Protocol snapshot credential is duplicated")
		}
		wires[wire] = struct{}{}
	}
	boundFields := make(map[string]struct{}, len(proxy.RequestFields))
	for _, binding := range proxy.RequestFields {
		boundFields[binding.HTTPField] = struct{}{}
	}
	if !pathVariablesBound(pathVariables, boundFields) {
		return protocolError("protocol_snapshot_invalid", "path_binding_mismatch", filePath, "", "Protocol snapshot route binding is invalid")
	}
	if !sortedRequestFields(proxy.RequestFields) || !sortedResponseFields(proxy.ResponseFields) || !sortedErrors(proxy.Errors) {
		return protocolError("protocol_snapshot_invalid", "canonical_order_invalid", filePath, "", "Protocol snapshot metadata order is invalid")
	}
	for _, binding := range proxy.RequestFields {
		if !validateSnapshotRPCPath(method.Input, binding.RPCPath, state.descriptors) {
			return protocolError("protocol_snapshot_invalid", "rpc_path_invalid", filePath, "", "Protocol snapshot request path is invalid")
		}
		terminal, _ := snapshotRPCPathTerminal(method.Input, binding.RPCPath, state.descriptors)
		_, pathBound := pathVariables[binding.HTTPField]
		complex := terminal.Cardinality == CardinalityRepeated || terminal.Type.Kind == TypeMessage
		if complex && (pathBound || proxy.Method == MethodGET || proxy.Method == MethodDELETE) {
			return protocolError("protocol_snapshot_invalid", "body_binding_required", filePath, "", "Protocol snapshot object or collection binding requires an HTTP body")
		}
	}
	for _, binding := range proxy.ResponseFields {
		if !validateSnapshotRPCPath(method.Output, binding.RPCPath, state.descriptors) {
			return protocolError("protocol_snapshot_invalid", "rpc_path_invalid", filePath, "", "Protocol snapshot response path is invalid")
		}
	}
	return nil
}

func validateSnapshotRPCContext(method canonicalMethod, state *snapshotState, filePath string) error {
	if method.RPCContext == nil {
		return nil
	}
	if !sortedContextFields(method.RPCContext.ContextFields) {
		return protocolError("protocol_snapshot_invalid", "context_fields_invalid", filePath, "", "Protocol snapshot RPC context is invalid")
	}
	seenSources := make(map[ContextValue]struct{}, len(method.RPCContext.ContextFields))
	seenPaths := make(map[string]struct{}, len(method.RPCContext.ContextFields))
	for _, binding := range method.RPCContext.ContextFields {
		if !validateSnapshotContextPath(method.Input, binding, state.descriptors) {
			return protocolError("protocol_snapshot_invalid", "rpc_path_invalid", filePath, "", "Protocol snapshot context path is invalid")
		}
		if binding.Source == ContextTenantID {
			terminal, ok := snapshotRPCPathTerminal(method.Input, binding.RPCPath, state.descriptors)
			if !ok {
				return protocolError("protocol_snapshot_invalid", "rpc_path_invalid", filePath, "", "Protocol snapshot context path is invalid")
			}
			if state.tenantType != "" && state.tenantType != terminal.Type.Name {
				return protocolError("protocol_snapshot_invalid", "tenant_context_type_mixed", filePath, "", "Tenant context type is inconsistent within the service")
			}
			state.tenantType = terminal.Type.Name
		}
		pathKey := strings.Join(binding.RPCPath, "\x00")
		if _, duplicate := seenSources[binding.Source]; duplicate {
			return protocolError("protocol_snapshot_invalid", "binding_destination_duplicate", filePath, "", "Protocol snapshot context source is duplicated")
		}
		if _, duplicate := seenPaths[pathKey]; duplicate {
			return protocolError("protocol_snapshot_invalid", "binding_destination_duplicate", filePath, "", "Protocol snapshot context RPC path is duplicated")
		}
		seenSources[binding.Source] = struct{}{}
		seenPaths[pathKey] = struct{}{}
	}
	return nil
}

func validateSnapshotContextPath(root string, binding canonicalContextField, index *snapshotDescriptorIndex) bool {
	if !validateSnapshotRPCPath(root, binding.RPCPath, index) {
		return false
	}
	current := root
	var leaf canonicalField
	for _, segment := range binding.RPCPath {
		messageName, numberText, ok := strings.Cut(segment, "#")
		number, err := strconv.Atoi(numberText)
		if !ok || err != nil || messageName != current || index.messages[current] == nil {
			return false
		}
		var exists bool
		leaf, exists = index.messages[current].fields[number]
		if !exists {
			return false
		}
		if leaf.Type.Kind == TypeMessage {
			current = leaf.Type.Name
		}
	}
	validType := leaf.Type.Name == "string"
	if binding.Source == ContextTenantID {
		validType = leaf.Type.Name == "string" || leaf.Type.Name == "int64"
	}
	return leaf.Cardinality == CardinalitySingular && leaf.Presence == PresenceImplicit && leaf.Type.Kind == TypeScalar && validType
}

func validHTTPMethod(value HTTPMethod) bool {
	return value == MethodGET || value == MethodPOST || value == MethodPUT || value == MethodPATCH || value == MethodDELETE
}
func validCredential(value canonicalCredential) bool {
	if value.Type != CredentialBearer && value.Type != CredentialAPIKey && value.Type != CredentialSessionCookie || value.In != CredentialHeader && value.In != CredentialQuery && value.In != CredentialCookie || !validCredentialName(value.In, value.Name) {
		return false
	}
	if value.In == CredentialHeader && value.Name == "content-type" {
		return false
	}
	if value.In == CredentialHeader && value.Name != strings.ToLower(value.Name) {
		return false
	}
	switch value.Type {
	case CredentialBearer:
		return value.In == CredentialHeader && value.Name == "authorization"
	case CredentialSessionCookie:
		return value.In == CredentialCookie
	default:
		return true
	}
}
func sortedRequestFields(values []canonicalRequestField) bool {
	previous := ""
	for _, value := range values {
		key := value.HTTPField + "\x00" + strings.Join(value.RPCPath, "\x00")
		if !httpFieldName.MatchString(value.HTTPField) || previous != "" && key <= previous {
			return false
		}
		previous = key
	}
	return true
}
func sortedContextFields(values []canonicalContextField) bool {
	previous := ""
	for _, value := range values {
		key := string(value.Source) + "\x00" + strings.Join(value.RPCPath, "\x00")
		if value.Source != ContextSubjectID && value.Source != ContextTenantID && value.Source != ContextRequestID && value.Source != ContextTraceID || previous != "" && key <= previous {
			return false
		}
		previous = key
	}
	return true
}
func sortedResponseFields(values []canonicalResponseField) bool {
	previous := ""
	for _, value := range values {
		key := value.HTTPField + "\x00" + strings.Join(value.RPCPath, "\x00")
		if !httpFieldName.MatchString(value.HTTPField) || previous != "" && key <= previous {
			return false
		}
		previous = key
	}
	return true
}
func sortedErrors(values []canonicalErrorProjection) bool {
	previous := ""
	for _, value := range values {
		key := value.Match.Domain + "\x00" + value.Match.Code
		if !stableMetadataID.MatchString(value.Match.Domain) || !stableMetadataID.MatchString(value.Match.Code) || !stableMetadataID.MatchString(value.Project.Domain) || !stableMetadataID.MatchString(value.Project.Code) || value.Project.HTTPStatus < 400 || value.Project.HTTPStatus > 599 || previous != "" && key <= previous {
			return false
		}
		previous = key
	}
	return true
}

func proxyStateFromCanonical(value *canonicalHTTPProxy) *httpProxyState {
	if value == nil {
		return nil
	}
	result := &httpProxyState{operationID: value.OperationID, method: value.Method, path: value.Path, permission: value.Permission, auth: authState{mode: value.Auth.Mode, credentials: make([]*credentialState, len(value.Auth.Credentials))}, requestFields: make([]*requestFieldState, len(value.RequestFields)), responseFields: make([]*responseFieldState, len(value.ResponseFields)), errors: make([]*errorProjectionState, len(value.Errors))}
	for i, item := range value.Auth.Credentials {
		result.auth.credentials[i] = &credentialState{id: item.ID, typeID: item.Type, location: item.In, name: item.Name}
	}
	for i, item := range value.RequestFields {
		result.requestFields[i] = &requestFieldState{httpField: item.HTTPField, rpcPath: append([]string(nil), item.RPCPath...)}
	}
	for i, item := range value.ResponseFields {
		result.responseFields[i] = &responseFieldState{httpField: item.HTTPField, rpcPath: append([]string(nil), item.RPCPath...)}
	}
	for i, item := range value.Errors {
		result.errors[i] = &errorProjectionState{match: errorMatchState{domain: item.Match.Domain, code: item.Match.Code}, project: errorTargetState{domain: item.Project.Domain, code: item.Project.Code, httpStatus: item.Project.HTTPStatus}}
	}
	return result
}

func rpcContextStateFromCanonical(value *canonicalRPCContext) *rpcContextState {
	if value == nil {
		return nil
	}
	result := &rpcContextState{contextFields: make([]*contextFieldState, len(value.ContextFields))}
	for i, item := range value.ContextFields {
		result.contextFields[i] = &contextFieldState{source: item.Source, rpcPath: append([]string(nil), item.RPCPath...)}
	}
	return result
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return err
	}
	return nil
}
func (s Snapshot) CanonicalJSON() ([]byte, error) {
	if s.state == nil {
		return nil, snapshotError("document_invalid", "", "")
	}
	return append([]byte(nil), s.state.canonical...), nil
}
func (s Snapshot) ProjectedSources() []provenance.Source {
	if s.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), s.state.sources...)
}
func (s Snapshot) SourceDigest() provenance.Digest {
	if s.state == nil {
		return provenance.Digest{}
	}
	return s.state.sourceDigest
}
func (s Snapshot) Method(fullName string) (SnapshotMethod, bool) {
	if s.state == nil {
		return SnapshotMethod{}, false
	}
	value, ok := s.state.methods[fullName]
	return SnapshotMethod{state: value}, ok
}
func (m SnapshotMethod) FullName() string {
	if m.state == nil {
		return ""
	}
	return m.state.fullName
}
func (m SnapshotMethod) HTTPProxy() HTTPProxy {
	if m.state == nil {
		return HTTPProxy{}
	}
	return HTTPProxy{state: m.state.proxy}
}
func (m SnapshotMethod) RPCContext() RPCContext {
	if m.state == nil {
		return RPCContext{}
	}
	return RPCContext{state: m.state.rpcContext}
}

func snapshotError(reason, source, pointer string) *Error {
	return protocolError("protocol_snapshot_invalid", reason, source, pointer, "Protocol snapshot is invalid")
}
