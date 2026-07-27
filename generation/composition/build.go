package composition

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/mod/module"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
)

var serviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func Build(catalog servicecatalog.Catalog, protocols []protocol.Document, native httpapi.Document, options BuildOptions) (Document, error) {
	if !serviceIDPattern.MatchString(options.CoreServiceID) || module.CheckPath(options.ConsumerModulePath) != nil {
		return Document{}, invalid("build_options_invalid", "", "/options", "composition build options are invalid")
	}
	if catalog.APIVersion() != servicecatalog.APIVersion {
		return Document{}, invalid("catalog_invalid", "", "/catalog", "service catalog is invalid")
	}
	if _, ok := catalog.Lookup(options.CoreServiceID); catalog.Len() != 0 && !ok {
		return Document{}, invalid("core_service_missing", "", "/options/coreServiceId", "Core service is missing from the service catalog")
	}
	if _, err := httpapi.Merge(native); err != nil {
		return Document{}, invalid("native_api_invalid", "", "/native", "native HTTP API document is invalid")
	}
	byService := make(map[string]protocol.Document, len(protocols))
	for index, document := range protocols {
		serviceID := document.ServiceID()
		if !serviceIDPattern.MatchString(serviceID) {
			return Document{}, invalid("protocol_service_invalid", "", fmt.Sprintf("/protocols/%d", index), "Protocol service identity is invalid")
		}
		if _, duplicate := byService[serviceID]; duplicate {
			return Document{}, invalid("protocol_service_duplicate", "", fmt.Sprintf("/protocols/%d", index), "Protocol service is duplicated")
		}
		byService[serviceID] = document
	}
	nativeOperations, nativeRoutes, nativeTypes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, operation := range native.Operations() {
		nativeOperations[operation.ID()] = true
		nativeRoutes[string(operation.Method())+"\x00"+operation.Path()] = true
	}
	for _, value := range native.Types() {
		nativeTypes[value.Name()] = true
	}
	operationIDs, routes, typeNames, methodNames := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	state := &documentState{coreServiceID: options.CoreServiceID, consumerModulePath: options.ConsumerModulePath, operations: []*operationState{}, types: []*projectedTypeState{}}
	projector := &typeProjector{state: state, byKey: map[string]*projectedTypeState{}, visiting: map[string]bool{}, reserved: typeNames, native: nativeTypes}
	for _, service := range catalog.Services() {
		binding, selected := selectedBinding(service)
		if !selected {
			continue
		}
		protocolDocument, ok := byService[service.ID()]
		if !ok {
			return Document{}, invalid("selected_protocol_missing", "", "/protocols", "selected service ProtocolIR is missing")
		}
		for _, file := range protocolDocument.Files() {
			for _, protocolService := range file.Services() {
				for _, method := range protocolService.Methods() {
					proxy, hasProxy := method.HTTPProxy()
					if !hasProxy {
						continue
					}
					if methodNames[method.FullName()] {
						return Document{}, invalid("method_duplicate", method.FilePath(), "", "proxy method is duplicated")
					}
					methodNames[method.FullName()] = true
					operation, err := buildOperation(service.ID(), binding.Source(), protocolDocument, method, proxy, projector)
					if err != nil {
						return Document{}, err
					}
					routeKey := string(apiMethod(proxy.Method())) + "\x00" + proxy.Path()
					if nativeOperations[proxy.OperationID()] || nativeRoutes[routeKey] || operationIDs[proxy.OperationID()] || routes[routeKey] {
						return Document{}, invalid("native_operation_collision", method.FilePath(), "", "proxy operation collides with an HTTP API operation")
					}
					if nativeTypes[operation.requestType] || nativeTypes[operation.responseType] || typeNames[operation.requestType] || typeNames[operation.responseType] {
						return Document{}, invalid("native_type_collision", method.FilePath(), "", "proxy type collides with an HTTP API type")
					}
					operationIDs[proxy.OperationID()], routes[routeKey] = true, true
					typeNames[operation.requestType], typeNames[operation.responseType] = true, true
					state.operations = append(state.operations, operation)
				}
			}
		}
	}
	if _, err := tenantTypesByService(state.operations); err != nil {
		return Document{}, err
	}
	sort.Slice(state.operations, func(i, j int) bool {
		return state.operations[i].proxy.OperationID() < state.operations[j].proxy.OperationID()
	})
	sort.Slice(state.types, func(i, j int) bool { return state.types[i].name < state.types[j].name })
	return Document{state: state}, nil
}

func selectedBinding(service servicecatalog.Service) (servicecatalog.CapabilityBinding, bool) {
	for _, binding := range service.CapabilityBindings() {
		if binding.ID() == CapabilityID && binding.APIVersion() == CapabilityVersion {
			return binding, true
		}
	}
	return servicecatalog.CapabilityBinding{}, false
}

func buildOperation(serviceID string, binding provenance.Source, document protocol.Document, method protocol.Method, proxy protocol.HTTPProxy, projector *typeProjector) (*operationState, error) {
	if method.ClientStreaming() || method.ServerStreaming() {
		return nil, invalid("streaming_proxy_unsupported", method.FilePath(), "", "streaming RPC methods cannot be projected")
	}
	request, ok := document.Message(method.Input())
	if !ok {
		return nil, invalid("request_message_missing", method.FilePath(), "", "proxy request message is missing")
	}
	response, ok := document.Message(method.Output())
	if !ok {
		return nil, invalid("response_message_missing", method.FilePath(), "", "proxy response message is missing")
	}
	methodOwner, err := httpapi.NewGeneratedProvenance([]provenance.Source{method.Source(), binding})
	if err != nil {
		return nil, invalid("provenance_invalid", method.FilePath(), "", "proxy operation provenance is invalid")
	}
	requestOwner, err := httpapi.NewGeneratedProvenance([]provenance.Source{method.Source(), request.Source(), binding})
	if err != nil {
		return nil, invalid("provenance_invalid", method.FilePath(), "", "proxy request provenance is invalid")
	}
	responseOwner, err := httpapi.NewGeneratedProvenance([]provenance.Source{method.Source(), response.Source(), binding})
	if err != nil {
		return nil, invalid("provenance_invalid", method.FilePath(), "", "proxy response provenance is invalid")
	}
	prefix := exportedIdentifier(proxy.OperationID())
	result := &operationState{serviceID: serviceID, methodName: method.FullName(), inputName: method.Input(), outputName: method.Output(), requestType: prefix + "Request", responseType: prefix + "Response", proxy: proxy, method: method, bindingSource: binding, requestMessage: request, responseMessage: response, operationProvenance: methodOwner, requestProvenance: requestOwner, responseProvenance: responseOwner}
	boundRequest := map[string]bool{}
	requestDestinations := map[string]bool{}
	requestHTTPFields := map[string]bool{}
	contextSources := map[protocol.ContextValue]bool{}
	for _, item := range proxy.RequestFields() {
		resolved, err := resolveBinding(serviceID, binding, document, request, item.RPCPath(), item.HTTPField(), "", projector)
		if err != nil {
			return nil, err
		}
		key := strings.Join(resolved.typedPath, "\x00")
		if requestDestinations[key] || requestHTTPFields[item.HTTPField()] {
			return nil, invalid("many_to_one_mapping", method.FilePath(), "", "multiple bindings target the same RPC input field")
		}
		requestDestinations[key], requestHTTPFields[item.HTTPField()] = true, true
		result.requestFields = append(result.requestFields, resolved)
		boundRequest[resolved.fields[0].FullName()] = true
	}
	for _, item := range method.RPCContext().ContextFields() {
		resolved, err := resolveBinding(serviceID, binding, document, request, item.RPCPath(), "", item.Source(), projector)
		if err != nil {
			return nil, err
		}
		if !validContextValueType(item.Source(), resolved.valueType) {
			return nil, invalid("context_binding_type_invalid", method.FilePath(), "", "context binding target has an invalid type")
		}
		key := strings.Join(resolved.typedPath, "\x00")
		if requestDestinations[key] {
			return nil, invalid("many_to_one_mapping", method.FilePath(), "", "multiple bindings target the same RPC input field")
		}
		requestDestinations[key] = true
		result.contextFields = append(result.contextFields, resolved)
		contextSources[item.Source()] = true
		boundRequest[resolved.fields[0].FullName()] = true
	}
	if len(proxy.Errors()) > 0 && (!contextSources[protocol.ContextRequestID] || !contextSources[protocol.ContextTraceID]) {
		return nil, invalid("error_context_binding_missing", method.FilePath(), "", "error projection requires request-id and trace-id context bindings")
	}
	for _, field := range request.Fields() {
		if !boundRequest[field.FullName()] {
			return nil, invalid("required_input_unbound", field.FilePath(), "", "RPC input field has no request or context binding")
		}
	}
	boundResponse := map[string]bool{}
	responseDestinations := map[string]bool{}
	responseHTTPFields := map[string]bool{}
	for _, item := range proxy.ResponseFields() {
		resolved, err := resolveBinding(serviceID, binding, document, response, item.RPCPath(), item.HTTPField(), "", projector)
		if err != nil {
			return nil, err
		}
		key := strings.Join(resolved.typedPath, "\x00")
		if responseDestinations[key] || responseHTTPFields[item.HTTPField()] {
			return nil, invalid("many_to_one_mapping", method.FilePath(), "", "multiple mappings read the same RPC response field")
		}
		responseDestinations[key], responseHTTPFields[item.HTTPField()] = true, true
		result.responseFields = append(result.responseFields, resolved)
		boundResponse[resolved.fields[0].FullName()] = true
	}
	for _, field := range response.Fields() {
		if !boundResponse[field.FullName()] {
			if _, err := apiValueType(field); err != nil {
				return nil, err
			}
			return nil, invalid("response_field_unmapped", field.FilePath(), "", "RPC response field has no HTTP mapping")
		}
	}
	for _, item := range proxy.Errors() {
		result.errorProjections = append(result.errorProjections, api.ErrorProjectionSpec{Match: api.ErrorMatchSpec{Domain: item.Match().Domain(), Code: item.Match().Code()}, Project: api.ErrorTargetSpec{Domain: item.Project().Domain(), Code: item.Project().Code(), HTTPStatus: item.Project().HTTPStatus()}})
	}
	if generatedIdentifierCollision(append(append([]resolvedBinding(nil), result.requestFields...), result.contextFields...), true) ||
		generatedIdentifierCollision(result.requestFields, false) ||
		generatedIdentifierCollision(result.responseFields, true) ||
		generatedIdentifierCollision(result.responseFields, false) {
		return nil, invalid("generated_identifier_collision", method.FilePath(), "", "bindings produce duplicate generated Go identifiers")
	}
	return result, nil
}

func validContextValueType(source protocol.ContextValue, value httpapi.ValueTypeSpec) bool {
	if value.Kind != httpapi.ValueScalar {
		return false
	}
	if source == protocol.ContextTenantID {
		return value.Name == "string" || value.Name == "int64"
	}
	return value.Name == "string"
}

func tenantTypesByService(operations []*operationState) (map[string]string, error) {
	result := make(map[string]string)
	for _, operation := range operations {
		for _, binding := range operation.contextFields {
			if binding.context != protocol.ContextTenantID {
				continue
			}
			if existing := result[operation.serviceID]; existing != "" && existing != binding.valueType.Name {
				return nil, invalid("tenant_context_type_mixed", operation.method.FilePath(), "", "tenant context type is inconsistent within the service")
			}
			result[operation.serviceID] = binding.valueType.Name
		}
	}
	return result, nil
}

func generatedIdentifierCollision(bindings []resolvedBinding, rpc bool) bool {
	seen := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		identifier := exportedIdentifier(binding.httpField)
		if rpc {
			identifier = rpcFieldName(binding)
		}
		if seen[identifier] {
			return true
		}
		seen[identifier] = true
	}
	return false
}

func resolveBinding(serviceID string, bindingSource provenance.Source, document protocol.Document, root protocol.Message, path []string, httpField string, context protocol.ContextValue, projector *typeProjector) (resolvedBinding, error) {
	if len(path) == 0 {
		return resolvedBinding{}, invalid("field_path_invalid", root.FilePath(), "", "RPC field path is empty")
	}
	current := root
	result := resolvedBinding{httpField: httpField, context: context, typedPath: append([]string(nil), path...)}
	for index, segment := range path {
		var found protocol.Field
		for _, field := range current.Fields() {
			if typedFieldID(field) == segment {
				found = field
				break
			}
		}
		if found.FullName() == "" {
			return resolvedBinding{}, invalid("field_path_unresolved", root.FilePath(), "", "RPC field path cannot be resolved")
		}
		if found.Presence() == protocol.PresenceOneof {
			return resolvedBinding{}, invalid("oneof_mapping_unsupported", found.FilePath(), "", "oneof mapping is not supported")
		}
		result.fields = append(result.fields, found)
		if index+1 < len(path) {
			if found.Cardinality() != protocol.CardinalitySingular || found.Type().Kind() != protocol.TypeMessage {
				return resolvedBinding{}, invalid("field_path_unresolved", found.FilePath(), "", "RPC field path crosses a non-message field")
			}
			var ok bool
			current, ok = document.Message(found.Type().Name())
			if !ok {
				return resolvedBinding{}, invalid("field_path_unresolved", found.FilePath(), "", "RPC field path message is missing")
			}
		}
	}
	last := result.fields[len(result.fields)-1]
	value, err := bindingValueType(serviceID, bindingSource, document, last, projector)
	if err != nil {
		return resolvedBinding{}, err
	}
	result.valueType = value
	result.required = last.Presence() != protocol.PresenceExplicit || last.Type().Kind() == protocol.TypeMessage || last.Cardinality() == protocol.CardinalityRepeated
	return result, nil
}

type typeProjector struct {
	state            *documentState
	byKey            map[string]*projectedTypeState
	visiting         map[string]bool
	reserved, native map[string]bool
}

func bindingValueType(serviceID string, binding provenance.Source, document protocol.Document, field protocol.Field, projector *typeProjector) (httpapi.ValueTypeSpec, error) {
	if field.Type().Kind() != protocol.TypeMessage {
		return apiValueType(field)
	}
	projected, err := projector.project(serviceID, binding, document, field.Type().Name())
	if err != nil {
		return httpapi.ValueTypeSpec{}, err
	}
	value := httpapi.ValueTypeSpec{Kind: httpapi.ValueRef, Name: projected.name}
	if field.Cardinality() == protocol.CardinalityRepeated {
		item := value
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueArray, Element: &item}
	}
	return value, nil
}

func (p *typeProjector) project(serviceID string, binding provenance.Source, document protocol.Document, fullName string) (*projectedTypeState, error) {
	key := serviceID + "\x00" + fullName
	if p.visiting[key] {
		return nil, invalid("message_graph_recursive", "", "", "projected message graph is recursive")
	}
	if existing := p.byKey[key]; existing != nil {
		return existing, nil
	}
	message, ok := document.Message(fullName)
	if !ok {
		return nil, invalid("message_reference_unresolved", "", "", "projected message reference is missing")
	}
	name := exportedIdentifier(serviceID + "." + fullName)
	if p.native[name] || p.reserved[name] {
		return nil, invalid("projected_type_collision", message.FilePath(), "", "projected message type name collides")
	}
	p.reserved[name] = true
	owner, err := httpapi.NewGeneratedProvenance([]provenance.Source{binding, message.Source()})
	if err != nil {
		return nil, invalid("provenance_invalid", message.FilePath(), "", "projected message provenance is invalid")
	}
	state := &projectedTypeState{name: name, serviceID: serviceID, messageFullName: fullName, message: message, provenance: owner}
	p.visiting[key] = true
	for _, field := range message.Fields() {
		if field.Type().Kind() == protocol.TypeMap || field.Presence() == protocol.PresenceMap {
			delete(p.visiting, key)
			return nil, invalid("map_mapping_unrepresentable", field.FilePath(), "", "map field cannot enter projected object closure")
		}
		if field.Presence() == protocol.PresenceOneof {
			delete(p.visiting, key)
			return nil, invalid("oneof_mapping_unsupported", field.FilePath(), "", "oneof field cannot enter projected object closure")
		}
		value, valueErr := p.projectedFieldValue(serviceID, binding, document, field)
		if valueErr != nil {
			delete(p.visiting, key)
			return nil, valueErr
		}
		fieldOwner, ownerErr := httpapi.NewGeneratedProvenance([]provenance.Source{binding, message.Source(), field.Source()})
		if ownerErr != nil {
			delete(p.visiting, key)
			return nil, invalid("provenance_invalid", field.FilePath(), "", "projected field provenance is invalid")
		}
		state.fields = append(state.fields, &projectedFieldState{id: typedFieldID(field), protoName: field.Name(), jsonName: field.JSONName(), number: field.Number(), valueType: value, required: field.Presence() != protocol.PresenceExplicit, field: field, provenance: fieldOwner})
	}
	delete(p.visiting, key)
	sort.Slice(state.fields, func(i, j int) bool { return state.fields[i].number < state.fields[j].number })
	p.byKey[key] = state
	p.state.types = append(p.state.types, state)
	return state, nil
}

func (p *typeProjector) projectedFieldValue(serviceID string, binding provenance.Source, document protocol.Document, field protocol.Field) (httpapi.ValueTypeSpec, error) {
	var value httpapi.ValueTypeSpec
	if field.Type().Kind() == protocol.TypeMessage {
		nested, err := p.project(serviceID, binding, document, field.Type().Name())
		if err != nil {
			return httpapi.ValueTypeSpec{}, err
		}
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueRef, Name: nested.name}
	} else {
		var err error
		value, err = apiValueType(field)
		if err != nil {
			return httpapi.ValueTypeSpec{}, err
		}
		// apiValueType already applies repeated/optional wrappers for scalar fields.
		return value, nil
	}
	if field.Cardinality() == protocol.CardinalityRepeated {
		item := value
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueArray, Element: &item}
	} else if field.Presence() == protocol.PresenceExplicit {
		item := value
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueOptional, Element: &item}
	}
	return value, nil
}

func apiValueType(field protocol.Field) (httpapi.ValueTypeSpec, error) {
	if field.Type().Kind() == protocol.TypeMap || field.Presence() == protocol.PresenceMap {
		return httpapi.ValueTypeSpec{}, invalid("map_mapping_unrepresentable", field.FilePath(), "", "map field cannot enter HTTP API Manifest v1")
	}
	var value httpapi.ValueTypeSpec
	switch field.Type().Kind() {
	case protocol.TypeScalar:
		name := field.Type().Name()
		switch name {
		case "double":
			name = "float64"
		case "float":
			name = "float32"
		case "sint32", "sfixed32":
			name = "int32"
		case "sint64", "sfixed64":
			name = "int64"
		case "fixed32":
			name = "uint32"
		case "fixed64":
			name = "uint64"
		case "bool", "string", "bytes", "int32", "int64", "uint32", "uint64":
		default:
			return httpapi.ValueTypeSpec{}, invalid("scalar_mapping_unsupported", field.FilePath(), "", "RPC scalar cannot be projected to the HTTP API")
		}
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: name}
	case protocol.TypeEnum:
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "int32"}
	case protocol.TypeMessage:
		return httpapi.ValueTypeSpec{}, invalid("message_mapping_unsupported", field.FilePath(), "", "message field mapping requires an explicit leaf path")
	default:
		return httpapi.ValueTypeSpec{}, invalid("field_type_unsupported", field.FilePath(), "", "RPC field type is unsupported")
	}
	if field.Cardinality() == protocol.CardinalityRepeated {
		item := value
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueArray, Element: &item}
	}
	if field.Presence() == protocol.PresenceExplicit {
		item := value
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueOptional, Element: &item}
	}
	return value, nil
}

func typedFieldID(field protocol.Field) string {
	owner := strings.TrimSuffix(field.FullName(), "."+field.Name())
	return fmt.Sprintf("%s#%d", owner, field.Number())
}

func exportedIdentifier(value string) string {
	var output []rune
	upper := true
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			upper = true
			continue
		}
		if upper {
			character = unicode.ToUpper(character)
			upper = false
		}
		output = append(output, character)
	}
	return string(output)
}

func apiMethod(value protocol.HTTPMethod) api.HTTPMethod { return api.HTTPMethod(value) }

func pathBinding(path, field string) bool { return strings.Contains(path, "{"+field+"}") }
