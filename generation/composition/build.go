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
	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
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
	if err := httpapi.ValidateConvention(native); err != nil {
		return Document{}, invalid("native_api_invalid", "", "/native", err.Error())
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
	nativeOperations, nativeRoutes, reservedTypes := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, operation := range native.Operations() {
		nativeOperations[operation.ID()] = true
		nativeRoutes[string(operation.Method())+"\x00"+operation.Path()] = true
	}
	for _, value := range native.Types() {
		reservedTypes[value.Name()] = true
	}
	state := &documentState{coreServiceID: options.CoreServiceID, consumerModulePath: options.ConsumerModulePath}
	selectedGraphs := []sourcecomment.FactGraph{native.FactGraph()}
	projector := &typeProjector{state: state, byKey: map[string]*projectedTypeState{}, visiting: map[string]bool{}, reserved: reservedTypes}
	operationIDs, routes, methods := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, service := range catalog.Services() {
		binding, selected := selectedBinding(service)
		if !selected {
			continue
		}
		document, ok := byService[service.ID()]
		if !ok {
			return Document{}, invalid("selected_protocol_missing", "", "/protocols", "selected service ProtocolIR is missing")
		}
		selectedGraphs = append(selectedGraphs, document.FactGraph())
		for _, file := range document.Files() {
			for _, rpcService := range file.Services() {
				for _, method := range rpcService.Methods() {
					facts, selected, err := operationFacts(service.ID(), document.FactGraph(), method)
					if err != nil {
						return Document{}, err
					}
					if !selected {
						continue
					}
					if methods[method.FullName()] {
						return Document{}, invalid("method_duplicate", method.FilePath(), "", "proxy method is duplicated")
					}
					methods[method.FullName()] = true
					operation, err := buildOperation(service.ID(), binding.Source(), document, method, facts, projector)
					if err != nil {
						return Document{}, err
					}
					routeKey := string(operation.httpMethod) + "\x00" + operation.path
					if nativeOperations[operation.operationID] || nativeRoutes[routeKey] || operationIDs[operation.operationID] || routes[routeKey] {
						return Document{}, invalid("native_operation_collision", method.FilePath(), "", "proxy operation collides with an HTTP API operation")
					}
					operationIDs[operation.operationID], routes[routeKey] = true, true
					state.operations = append(state.operations, operation)
				}
			}
		}
	}
	sort.Slice(state.operations, func(i, j int) bool {
		return state.operations[i].operationID < state.operations[j].operationID
	})
	sort.Slice(state.types, func(i, j int) bool { return state.types[i].name < state.types[j].name })
	facts, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), selectedGraphs...)
	if len(diagnostics) > 0 {
		return Document{}, invalid("source_graph_invalid", diagnostics[0].File, "", diagnostics[0].Suggestion)
	}
	state.facts = facts
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

type typeProjector struct {
	state    *documentState
	byKey    map[string]*projectedTypeState
	visiting map[string]bool
	reserved map[string]bool
}

type projectedOperationFacts struct {
	method      api.HTTPMethod
	path        string
	auth        api.AuthMode
	permission  string
	firstSource sourcecomment.SourceRef
}

func operationFacts(serviceID string, graph sourcecomment.FactGraph, method protocol.Method) (projectedOperationFacts, bool, error) {
	operationID, err := sourcecomment.CanonicalRPCOperationID(serviceID, method.FullName())
	if err != nil {
		return projectedOperationFacts{}, false, invalid("operation_id_invalid", method.FilePath(), "", err.Error())
	}
	read := func(key string) (string, bool, error) {
		fact, ok := graph.Fact(sourcecomment.FactID{SemanticID: operationID, Key: key})
		if !ok {
			return "", false, nil
		}
		value, stringValue := fact.Value().String()
		if !stringValue {
			return "", true, invalid("http_fact_invalid", method.FilePath(), "", "RPC HTTP fact is not a string")
		}
		return value, true, nil
	}
	methodValue, hasMethod, err := read("http.method")
	if err != nil {
		return projectedOperationFacts{}, false, err
	}
	pathValue, hasPath, err := read("http.path")
	if err != nil {
		return projectedOperationFacts{}, false, err
	}
	if !hasMethod && !hasPath {
		return projectedOperationFacts{}, false, nil
	}
	if !hasMethod || !hasPath {
		return projectedOperationFacts{}, false, invalid("http_fact_incomplete", method.FilePath(), "", "RPC HTTP method and path facts must be declared together")
	}
	methodFact, _ := graph.Fact(sourcecomment.FactID{SemanticID: operationID, Key: "http.method"})
	httpMethod := api.HTTPMethod(methodValue)
	switch httpMethod {
	case api.MethodGET, api.MethodPOST, api.MethodPUT, api.MethodDELETE:
	default:
		return projectedOperationFacts{}, false, invalid("http_method_unsupported", method.FilePath(), "", "RPC proxy method is outside source-comment v1")
	}
	authValue, hasAuth, err := read("auth")
	if err != nil {
		return projectedOperationFacts{}, false, err
	}
	if !hasAuth {
		return projectedOperationFacts{}, false, invalid("auth_fact_missing", method.FilePath(), "", "HTTP-projected RPC must declare auth")
	}
	auth := api.AuthMode(authValue)
	if auth != api.AuthNone && auth != api.AuthRequired {
		return projectedOperationFacts{}, false, invalid("auth_fact_invalid", method.FilePath(), "", "RPC auth fact is invalid")
	}
	permission, hasPermission, err := read("permission")
	if err != nil {
		return projectedOperationFacts{}, false, err
	}
	if auth == api.AuthRequired && !hasPermission {
		return projectedOperationFacts{}, false, invalid("permission_fact_missing", method.FilePath(), "", "authenticated HTTP-projected RPC must declare permission")
	}
	if auth == api.AuthNone && hasPermission {
		return projectedOperationFacts{}, false, invalid("permission_fact_invalid", method.FilePath(), "", "unauthenticated HTTP-projected RPC cannot declare permission")
	}
	return projectedOperationFacts{method: httpMethod, path: pathValue, auth: auth, permission: permission, firstSource: methodFact.FirstSource()}, true, nil
}

func buildOperation(serviceID string, binding provenance.Source, document protocol.Document, method protocol.Method, facts projectedOperationFacts, projector *typeProjector) (*operationState, error) {
	if method.ClientStreaming() || method.ServerStreaming() {
		return nil, invalid("streaming_proxy_unsupported", method.FilePath(), "", "streaming RPC methods cannot be projected")
	}
	if _, err := httpconvention.ValidateRoute(facts.path); err != nil {
		return nil, invalid("route_invalid", method.FilePath(), "", err.Error())
	}
	request, ok := document.Message(method.Input())
	if !ok {
		return nil, invalid("request_message_missing", method.FilePath(), "", "proxy request message is missing")
	}
	response, ok := document.Message(method.Output())
	if !ok {
		return nil, invalid("response_message_missing", method.FilePath(), "", "proxy response message is missing")
	}
	requestType, err := projector.project(serviceID, binding, document, request)
	if err != nil {
		return nil, err
	}
	var responseType *projectedTypeState
	responseType, err = projector.project(serviceID, binding, document, response)
	if err != nil {
		return nil, err
	}
	owner, err := httpapi.NewGeneratedProvenance([]provenance.Source{binding, method.Source()})
	if err != nil {
		return nil, invalid("provenance_invalid", method.FilePath(), "", err.Error())
	}
	operationID, err := canonicalOperationID(serviceID, method.FullName())
	if err != nil {
		return nil, invalid("operation_id_invalid", method.FilePath(), "", err.Error())
	}
	result := &operationState{serviceID: serviceID, methodFullName: method.FullName(), inputName: method.Input(), outputName: method.Output(), requestType: requestType.name, operationID: operationID, httpMethod: facts.method, path: facts.path, auth: facts.auth, permission: facts.permission, firstSource: facts.firstSource, method: method, bindingSource: binding, request: requestType, response: responseType, provenance: owner}
	if responseType != nil {
		result.responseType = responseType.name
	}
	return result, nil
}

func (p *typeProjector) project(serviceID string, binding provenance.Source, document protocol.Document, message protocol.Message) (*projectedTypeState, error) {
	key := serviceID + "\x00" + message.FullName()
	if value := p.byKey[key]; value != nil {
		return value, nil
	}
	if p.visiting[key] {
		return nil, invalid("message_graph_recursive", message.FilePath(), "", "recursive message cannot enter HTTP Convention v1")
	}
	name := exportedIdentifier(serviceID + "." + message.FullName())
	if p.reserved[name] {
		return nil, invalid("projected_type_collision", message.FilePath(), "", "projected type name collides after canonical conversion")
	}
	owner, err := httpapi.NewGeneratedProvenance([]provenance.Source{binding, message.Source()})
	if err != nil {
		return nil, invalid("provenance_invalid", message.FilePath(), "", err.Error())
	}
	messageSource, err := sourcecomment.ParseSourceRef("proto://" + message.FilePath() + "#" + message.FullName())
	if err != nil {
		return nil, invalid("message_source_invalid", message.FilePath(), "", err.Error())
	}
	state := &projectedTypeState{name: name, serviceID: serviceID, messageFullName: message.FullName(), semanticID: message.FullName(), firstSource: messageSource, message: message, provenance: owner}
	p.reserved[name], p.visiting[key] = true, true
	p.byKey[key] = state
	for _, field := range message.Fields() {
		if field.Presence() == protocol.PresenceOneof {
			return nil, invalid("oneof_mapping_unsupported", field.FilePath(), "", "oneof field cannot enter HTTP Convention v1")
		}
		value, valueErr := p.fieldValue(serviceID, binding, document, field)
		if valueErr != nil {
			return nil, valueErr
		}
		jsonName, nameErr := httpconvention.CanonicalName(field.Name())
		if nameErr != nil {
			return nil, invalid("field_name_invalid", field.FilePath(), "", nameErr.Error())
		}
		for _, existing := range state.fields {
			if existing.jsonName == jsonName {
				return nil, invalid("generated_identifier_collision", field.FilePath(), "", "fields collide after canonical naming")
			}
		}
		fieldOwner, ownerErr := httpapi.NewGeneratedProvenance([]provenance.Source{binding, message.Source(), field.Source()})
		if ownerErr != nil {
			return nil, invalid("provenance_invalid", field.FilePath(), "", ownerErr.Error())
		}
		fieldSource, sourceErr := sourcecomment.ParseSourceRef("proto://" + field.FilePath() + "#" + field.FullName())
		if sourceErr != nil {
			return nil, invalid("field_source_invalid", field.FilePath(), "", sourceErr.Error())
		}
		state.fields = append(state.fields, &projectedFieldState{protoName: field.Name(), jsonName: jsonName, semanticID: field.FullName(), firstSource: fieldSource, number: field.Number(), valueType: value, required: field.Presence() != protocol.PresenceExplicit, field: field, provenance: fieldOwner})
	}
	delete(p.visiting, key)
	sort.Slice(state.fields, func(i, j int) bool { return state.fields[i].number < state.fields[j].number })
	p.state.types = append(p.state.types, state)
	return state, nil
}

func (p *typeProjector) fieldValue(serviceID string, binding provenance.Source, document protocol.Document, field protocol.Field) (httpapi.ValueTypeSpec, error) {
	value, err := p.protocolValue(serviceID, binding, document, field.FilePath(), field.Type())
	if err != nil {
		return httpapi.ValueTypeSpec{}, err
	}
	if field.Type().Kind() != protocol.TypeMap && field.Cardinality() == protocol.CardinalityRepeated {
		item := value
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueArray, Element: &item}
	} else if field.Presence() == protocol.PresenceExplicit {
		item := value
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueOptional, Element: &item}
	}
	return value, nil
}

func (p *typeProjector) protocolValue(serviceID string, binding provenance.Source, document protocol.Document, filePath string, fieldType protocol.Type) (httpapi.ValueTypeSpec, error) {
	var value httpapi.ValueTypeSpec
	switch fieldType.Kind() {
	case protocol.TypeMessage:
		message, ok := document.Message(fieldType.Name())
		if !ok {
			return value, invalid("message_mapping_unsupported", filePath, "", "message field type is unresolved")
		}
		nested, err := p.project(serviceID, binding, document, message)
		if err != nil {
			return value, err
		}
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueRef, Name: nested.name}
	case protocol.TypeEnum:
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}
	case protocol.TypeScalar:
		name := fieldType.Name()
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
		case "bool", "string", "int32", "int64", "uint32", "uint64":
		default:
			return value, invalid("scalar_mapping_unsupported", filePath, "", "RPC scalar has no HTTP Convention v1 wire type")
		}
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: name}
	case protocol.TypeMap:
		key, err := p.protocolValue(serviceID, binding, document, filePath, fieldType.Key())
		if err != nil {
			return value, err
		}
		mapValue, err := p.protocolValue(serviceID, binding, document, filePath, fieldType.Value())
		if err != nil {
			return value, err
		}
		value = httpapi.ValueTypeSpec{Kind: httpapi.ValueMap, Key: &key, Value: &mapValue}
	default:
		return value, invalid("field_type_unsupported", filePath, "", "RPC field type is unsupported")
	}
	return value, nil
}

func canonicalOperationID(serviceID, methodFullName string) (string, error) {
	return sourcecomment.CanonicalRPCOperationID(serviceID, methodFullName)
}

func exportedIdentifier(value string) string {
	var result []rune
	upper := true
	for _, current := range value {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			upper = true
			continue
		}
		if upper {
			current = unicode.ToUpper(current)
			upper = false
		}
		result = append(result, current)
	}
	return string(result)
}

func packageName(serviceID string) string { return strings.ReplaceAll(serviceID, "-", "") + "client" }
