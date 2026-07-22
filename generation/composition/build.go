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
	state := &documentState{coreServiceID: options.CoreServiceID, consumerModulePath: options.ConsumerModulePath, operations: []*operationState{}}
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
					operation, err := buildOperation(service.ID(), binding.Source(), protocolDocument, method, proxy)
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
	sort.Slice(state.operations, func(i, j int) bool {
		return state.operations[i].proxy.OperationID() < state.operations[j].proxy.OperationID()
	})
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

func buildOperation(serviceID string, binding provenance.Source, document protocol.Document, method protocol.Method, proxy protocol.HTTPProxy) (*operationState, error) {
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
		resolved, err := resolveBinding(document, request, item.RPCPath(), item.HTTPField(), "")
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
		resolved, err := resolveBinding(document, request, item.RPCPath(), "", item.Source())
		if err != nil {
			return nil, err
		}
		expected := "string"
		if item.Source() == protocol.ContextTenantID {
			expected = "int64"
		}
		if resolved.valueType.Kind != httpapi.ValueScalar || resolved.valueType.Name != expected {
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
		resolved, err := resolveBinding(document, response, item.RPCPath(), item.HTTPField(), "")
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

func resolveBinding(document protocol.Document, root protocol.Message, path []string, httpField string, context protocol.ContextValue) (resolvedBinding, error) {
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
			if found.Type().Kind() != protocol.TypeMessage {
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
	value, err := apiValueType(last)
	if err != nil {
		return resolvedBinding{}, err
	}
	result.valueType = value
	result.required = last.Presence() != protocol.PresenceExplicit
	return result, nil
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
