package frontend

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

type resourceOperations struct {
	list, create, get, update, delete api.ClosureOperation
}

func Build(apiDocument api.Closure, specs []PageSpec) (Document, error) {
	return BuildApplication(apiDocument, specs, nil)
}

// BuildApplication builds one application-level FrontendIR. Page operations and
// explicitly selected shell operations share the same canonical API closure.
func BuildApplication(apiDocument api.Closure, specs []PageSpec, shellOperationIDs []string) (Document, error) {
	if apiDocument.Convention() != httpconvention.APIVersion {
		return Document{}, buildError("http_convention_invalid", "/httpConvention", "frontend API closure must use the Nexa HTTP Convention v1")
	}
	apiFacts := apiDocument.FactGraph()
	if !apiFacts.Valid() {
		return Document{}, buildError("source_graph_invalid", "/sourceFacts", "frontend API closure must carry one validated source FactGraph")
	}
	graphs := []sourcecomment.FactGraph{apiFacts}
	for index, spec := range specs {
		if spec.state == nil {
			return Document{}, buildError("page_spec_invalid", "/pages/"+strconv.Itoa(index), "frontend page spec is invalid")
		}
		graphs = append(graphs, spec.state.facts)
	}
	facts, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), graphs...)
	if len(diagnostics) != 0 {
		return Document{}, buildError("source_graph_invalid", "/sourceFacts", diagnostics[0].Suggestion)
	}
	pages := make([]wirePage, len(specs))
	pageIDs, routePaths, routeNames, menuOrders := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	operationPointers := map[string]string{}
	for index, spec := range specs {
		base := "/pages/" + strconv.Itoa(index)
		projected, selected, failure := projectPage(apiDocument, facts, spec, base)
		if failure != nil {
			return Document{}, failure
		}
		if pageIDs[projected.ID] {
			return Document{}, buildError("page_id_duplicate", base+"/id", "frontend page id is duplicated")
		}
		pageIDs[projected.ID] = true
		if routePaths[projected.Route.Path] {
			return Document{}, buildError("route_path_duplicate", base+"/route/path", "frontend route path is duplicated")
		}
		routePaths[projected.Route.Path] = true
		if routeNames[projected.Route.Name] {
			return Document{}, buildError("route_name_duplicate", base+"/route/name", "frontend route name is duplicated")
		}
		routeNames[projected.Route.Name] = true
		if projected.Menu != nil {
			key := projected.Menu.ParentID + "\x00" + strconv.Itoa(projected.Menu.Order)
			if menuOrders[key] {
				return Document{}, buildError("menu_order_duplicate", base+"/menu/order", "menu order is duplicated under the same parent")
			}
			menuOrders[key] = true
		}
		for operationID := range selected {
			if _, exists := operationPointers[operationID]; !exists {
				operationPointers[operationID] = base + "/entity"
			}
		}
		pages[index] = projected
	}
	shellPointers := make(map[string]string, len(shellOperationIDs))
	for index, operationID := range shellOperationIDs {
		pointer := "/shellOperationIds/" + strconv.Itoa(index)
		if strings.TrimSpace(operationID) == "" {
			return Document{}, buildError("operation_id_invalid", pointer, "selected shell operation id must not be empty")
		}
		if _, duplicate := shellPointers[operationID]; duplicate {
			return Document{}, buildError("operation_id_duplicate", pointer, "selected shell operation id is duplicated")
		}
		if _, usedByPage := operationPointers[operationID]; usedByPage {
			return Document{}, buildError("operation_id_duplicate", pointer, "selected shell operation is already owned by a page")
		}
		shellPointers[operationID] = pointer
		operationPointers[operationID] = pointer
	}
	if failure := validateMenuGraph(pages); failure != nil {
		return Document{}, failure
	}
	selectedClosure, failure := selectClosure(apiDocument, operationPointers)
	if failure != nil {
		return Document{}, failure
	}
	typeNames, failure := generatedTypeScriptNames(apiDocument)
	if failure != nil {
		return Document{}, failure
	}
	clientNames, failure := generatedClientNames(apiDocument)
	if failure != nil {
		return Document{}, failure
	}
	for operationID, pointer := range shellPointers {
		operation, ok := apiDocument.Operation(operationID)
		if !ok {
			continue
		}
		if failure := validateOperationConvention(apiDocument, operation, pointer); failure != nil {
			return Document{}, failure
		}
	}
	projectedLocales, err := projectFactLocales(pages, facts)
	if err != nil {
		return Document{}, err
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].ID < pages[j].ID })
	operations, failure := closureOperations(selectedClosure, clientNames)
	if failure != nil {
		return Document{}, failure
	}
	wire := wireDocument{
		APIVersion: APIVersion, Kind: documentKind, HTTPConvention: httpconvention.APIVersion,
		Types: closureTypes(selectedClosure, typeNames), Operations: operations, Locales: projectedLocales, Pages: pages,
	}
	return Document{state: &documentState{wire: cloneWireDocument(wire), facts: facts}}, nil
}

func generatedClientNames(closure api.Closure) (map[string]string, *Error) {
	baseNames := map[string]string{}
	baseCounts := map[string]int{}
	for _, operation := range closure.Operations {
		parts := strings.Split(operation.ID(), ".")
		base, ok := generatedClientIdentifier(parts[len(parts)-1])
		if !ok {
			return nil, buildError("generated_operation_name_invalid", "/operations", "operation id cannot become a TypeScript function identifier")
		}
		baseNames[operation.ID()] = base
		baseCounts[base]++
	}
	operationNames := map[string]string{}
	result := make(map[string]string, len(closure.Operations))
	for _, operation := range closure.Operations {
		name := baseNames[operation.ID()]
		if baseCounts[name] > 1 {
			var prefixed strings.Builder
			for _, part := range strings.Split(operation.ID(), ".") {
				identifier, ok := generatedIdentifier(part)
				if !ok {
					return nil, buildError("generated_operation_name_invalid", "/operations", "operation id cannot become a TypeScript function identifier")
				}
				prefixed.WriteString(identifier)
			}
			name = prefixed.String()
		}
		if typeScriptReservedWords[name] {
			return nil, buildError("generated_operation_name_reserved", "/operations", "operation client name is a reserved TypeScript word")
		}
		if previous, exists := operationNames[name]; exists && previous != operation.ID() {
			return nil, buildError("generated_operation_name_collision", "/operations", "operation ids collide after TypeScript function generation")
		}
		operationNames[name] = operation.ID()
		result[operation.ID()] = name
	}
	return result, nil
}

func generatedTypeScriptNames(closure api.Closure) (map[string]string, *Error) {
	typeNames := map[string]string{}
	result := make(map[string]string, len(closure.Types))
	for _, value := range closure.Types {
		generated, ok := generatedIdentifier(value.Name())
		if !ok {
			return nil, buildError("generated_type_name_invalid", "/types", "API type cannot become a TypeScript identifier")
		}
		if previous, exists := typeNames[generated]; exists && previous != value.Name() {
			return nil, buildError("generated_type_name_collision", "/types", "API types collide after TypeScript identifier generation")
		}
		typeNames[generated] = value.Name()
		result[value.Name()] = generated
	}
	return result, nil
}

var typeScriptReservedWords = map[string]bool{
	"as": true, "async": true, "await": true, "break": true, "case": true, "catch": true,
	"class": true, "const": true, "continue": true, "debugger": true, "default": true, "delete": true,
	"do": true, "else": true, "enum": true, "export": true, "extends": true, "false": true,
	"finally": true, "for": true, "from": true, "function": true, "if": true,
	"implements": true, "import": true, "in": true, "instanceof": true, "interface": true, "let": true,
	"new": true, "null": true, "of": true, "package": true, "private": true, "protected": true,
	"public": true, "return": true, "static": true, "super": true, "switch": true,
	"this": true, "throw": true, "true": true, "try": true, "type": true, "typeof": true,
	"var": true, "void": true, "while": true, "with": true, "yield": true,
}

func generatedClientIdentifier(value string) (string, bool) {
	generated, ok := generatedIdentifier(value)
	if !ok {
		return "", false
	}
	characters := []rune(generated)
	characters[0] = unicode.ToLower(characters[0])
	return string(characters), true
}

func generatedIdentifier(value string) (string, bool) {
	parts := strings.FieldsFunc(value, func(character rune) bool {
		return character > unicode.MaxASCII || !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	if len(parts) == 0 {
		return "", false
	}
	var result strings.Builder
	for _, part := range parts {
		characters := []rune(part)
		characters[0] = unicode.ToUpper(characters[0])
		result.WriteString(string(characters))
	}
	generated := result.String()
	if generated == "" || !unicode.IsLetter([]rune(generated)[0]) {
		return "", false
	}
	return generated, true
}

func projectPage(document api.Closure, facts sourcecomment.FactGraph, spec PageSpec, base string) (wirePage, map[string]bool, *Error) {
	pageID := pageFactID(spec.state.facts)
	entity, ok := spec.state.facts.PageEntity(pageID)
	if !ok {
		return wirePage{}, nil, buildError("page_entity_missing", base+"/entity", "frontend page must declare ui.entity")
	}
	operations, itemType, failure := resolveResource(document, entity, base+"/entity")
	if failure != nil {
		return wirePage{}, nil, failure
	}
	declared, exists, err := facts.CRUD(entity)
	if err != nil || !exists {
		return wirePage{}, nil, buildError("crud_facts_missing", base+"/entity", "page entity must have canonical crud.operations facts")
	}
	wanted := map[sourcecomment.CRUDOperation]bool{}
	for _, operation := range declared.Operations() {
		wanted[operation] = true
	}
	if !wanted[sourcecomment.CRUDList] {
		return wirePage{}, nil, buildError("list_operation_missing", base+"/entity", "frontend page entity must expose the standard list operation")
	}
	for role, operation := range map[sourcecomment.CRUDOperation]*api.ClosureOperation{
		sourcecomment.CRUDCreate: &operations.create, sourcecomment.CRUDGet: &operations.get,
		sourcecomment.CRUDUpdate: &operations.update, sourcecomment.CRUDDelete: &operations.delete,
	} {
		if !wanted[role] {
			*operation = api.ClosureOperation{}
		} else if operation.ID() == "" {
			return wirePage{}, nil, buildError("resource_operation_missing", base+"/entity", "declared CRUD operation is missing from the canonical HTTP closure")
		}
	}

	fields := make([]wireField, 0)
	fieldSurfaces := map[string]map[string]bool{}
	shapeFields := map[string][]struct {
		surface, typeName string
		value             api.ClosureValue
	}{}
	addShape := func(typeName, surface string, automatic map[string]bool) *Error {
		if typeName == "" {
			return nil
		}
		typeValue, exists := document.Type(typeName)
		if !exists {
			return buildError("type_unresolved", base+"/entity", "canonical CRUD type is unresolved")
		}
		for _, field := range typeValue.Fields() {
			path := field.Path()
			if len(path) != 1 || automatic[path[0]] {
				continue
			}
			shapeFields[path[0]] = append(shapeFields[path[0]], struct {
				surface, typeName string
				value             api.ClosureValue
			}{surface, typeName, unwrapOptional(field.ValueType())})
		}
		return nil
	}
	if failure := addShape(itemType.Name(), "list", nil); failure != nil {
		return wirePage{}, nil, failure
	}
	if failure := addShape(operations.list.RequestType(), "search", map[string]bool{"limit": true, "offset": true}); failure != nil {
		return wirePage{}, nil, failure
	}
	if failure := addShape(operations.create.RequestType(), "create", nil); failure != nil {
		return wirePage{}, nil, failure
	}
	if failure := addShape(operations.update.RequestType(), "edit", map[string]bool{"id": true}); failure != nil {
		return wirePage{}, nil, failure
	}
	names := make([]string, 0, len(shapeFields))
	for name := range shapeFields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		shapes := shapeFields[name]
		semanticID, fieldFacts, err := resolveFieldFacts(facts, entity, name, shapes)
		if err != nil {
			return wirePage{}, nil, buildError("field_facts_missing", base+"/fields/"+name, err.Error())
		}
		surfaces := map[string]bool{}
		var canonicalType api.ClosureValue
		for _, shape := range shapes {
			if canonicalType.Kind() == "" {
				canonicalType = shape.value
			}
			if exactValueType(canonicalType) != exactValueType(shape.value) {
				return wirePage{}, nil, buildError("field_type_inconsistent", base+"/fields/"+name, "same-name fields must keep one exact canonical type")
			}
			if includeSurface(fieldFacts, shape.surface) {
				surfaces[shape.surface] = true
			}
		}
		if len(surfaces) == 0 {
			continue
		}
		control := string(fieldFacts.Control)
		if !controlCompatible(control, canonicalType) {
			return wirePage{}, nil, buildError("field_control_invalid", base+"/fields/"+name, "field fact control is incompatible with its canonical type")
		}
		ordered := make([]string, 0, len(surfaces))
		for _, surface := range []string{"search", "list", "create", "edit"} {
			if surfaces[surface] {
				ordered = append(ordered, surface)
			}
		}
		fields = append(fields, wireField{Name: name, LabelKey: fieldFacts.Label.Key, Surfaces: ordered, Control: control})
		fieldSurfaces[name] = surfaces
		_ = semanticID
	}

	if failure := validateRequestCoverage(document, operations.list, map[string]bool{"limit": true, "offset": true}, fieldSurfaces, "search", base+"/entity"); failure != nil {
		return wirePage{}, nil, failure
	}
	extensionComponent, _ := spec.state.facts.PageString(pageID, "ui.extensionComponent")
	if extensionComponent == "" && hasFieldSurface(fieldSurfaces, "create") {
		if operations.create.ID() == "" {
			return wirePage{}, nil, buildError("create_operation_missing", base+"/fields", "create fields require a standard create operation")
		}
		if failure := validateRequestCoverage(document, operations.create, map[string]bool{}, fieldSurfaces, "create", base+"/entity"); failure != nil {
			return wirePage{}, nil, failure
		}
	}
	if extensionComponent == "" && hasFieldSurface(fieldSurfaces, "edit") {
		if operations.get.ID() == "" || operations.update.ID() == "" {
			return wirePage{}, nil, buildError("edit_operation_missing", base+"/fields", "edit fields require standard get and update operations")
		}
		if failure := validateIDOnlyRequest(document, operations.get, base+"/entity"); failure != nil {
			return wirePage{}, nil, failure
		}
		if failure := validateRequestCoverage(document, operations.update, map[string]bool{"id": true}, fieldSurfaces, "edit", base+"/entity"); failure != nil {
			return wirePage{}, nil, failure
		}
	}
	if extensionComponent == "" && operations.delete.ID() != "" {
		if failure := validateIDOnlyRequest(document, operations.delete, base+"/entity"); failure != nil {
			return wirePage{}, nil, failure
		}
	}

	pageSize, ok := spec.state.facts.PageSize(pageID)
	if !ok {
		pageSize = httpconvention.DefaultPageSize
	}
	routePath, ok := spec.state.facts.PageString(pageID, "route.path")
	if !ok {
		return wirePage{}, nil, buildError("route_path_missing", base+"/route/path", "frontend page must declare route.path")
	}
	routeNameValue, ok := spec.state.facts.PageString(pageID, "route.name")
	if !ok {
		routeNameValue = routeName(pageID)
	}
	icon, hasIcon := spec.state.facts.PageString(pageID, "route.icon")
	order, hasOrder := spec.state.facts.PageMenuOrder(pageID)
	var menu *wireMenu
	entityMeta, err := facts.SchemaFacts(entity)
	if err != nil {
		return wirePage{}, nil, buildError("schema_facts_missing", base+"/entity", err.Error())
	}
	if hasIcon || hasOrder {
		menu = &wireMenu{ID: pageID, TitleKey: entityMeta.Label.Key, Path: routePath, Icon: icon, Order: order}
	}
	projected := wirePage{
		ID: pageID, TitleKey: entityMeta.Label.Key, Route: wireRoute{Path: routePath, Name: routeNameValue}, ExtensionComponent: extensionComponent,
		Menu: menu, PageSize: pageSize, Fields: fields,
		Operations: wirePageOperations{List: operations.list.ID(), Create: operations.create.ID(), Get: operations.get.ID(), Update: operations.update.ID(), Delete: operations.delete.ID()},
	}
	sort.Slice(projected.Fields, func(i, j int) bool { return projected.Fields[i].Name < projected.Fields[j].Name })
	selected := map[string]bool{operations.list.ID(): true}
	for _, operation := range []api.ClosureOperation{operations.create, operations.get, operations.update, operations.delete} {
		if operation.ID() != "" {
			selected[operation.ID()] = true
		}
	}
	return projected, selected, nil
}

func hasFieldSurface(surfaces map[string]map[string]bool, target string) bool {
	for _, fieldSurfaces := range surfaces {
		if fieldSurfaces[target] {
			return true
		}
	}
	return false
}

func resolveFieldFacts(facts sourcecomment.FactGraph, entity, name string, shapes []struct {
	surface, typeName string
	value             api.ClosureValue
}) (string, sourcecomment.FieldFacts, error) {
	candidates := []string{entity + "." + name}
	seen := map[string]bool{candidates[0]: true}
	for _, shape := range shapes {
		candidate := shape.typeName + "." + name
		if !seen[candidate] {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	for _, candidate := range candidates {
		if _, ok := facts.Fact(sourcecomment.FactID{SemanticID: candidate, Key: "label.zh-CN"}); !ok {
			continue
		}
		value, err := facts.FieldFacts(candidate)
		if err != nil {
			return "", sourcecomment.FieldFacts{}, err
		}
		return candidate, value, nil
	}
	return "", sourcecomment.FieldFacts{}, fmt.Errorf("field %s has no upstream schema or operation field facts", name)
}

func includeSurface(facts sourcecomment.FieldFacts, surface string) bool {
	if facts.Visibility != sourcecomment.VisibilityPublic {
		return false
	}
	if surface == "search" && facts.CRUD == nil {
		return true
	}
	if facts.CRUD == nil {
		return false
	}
	switch surface {
	case "list":
		return facts.CRUD.Read == sourcecomment.ReadInclude
	case "search":
		return facts.CRUD.Read == sourcecomment.ReadInclude
	case "create":
		return facts.CRUD.Mutation == sourcecomment.MutationCreate || facts.CRUD.Mutation == sourcecomment.MutationCreateUpdate
	case "edit":
		return facts.CRUD.Mutation == sourcecomment.MutationUpdate || facts.CRUD.Mutation == sourcecomment.MutationCreateUpdate
	default:
		return false
	}
}

func resolveResource(document api.Closure, entity, pointer string) (resourceOperations, api.ClosureType, *Error) {
	var list api.ClosureOperation
	for _, candidate := range document.Operations {
		if candidate.Method() != api.MethodGET || candidate.ResponseType() == "" {
			continue
		}
		_, items, _, ok := directField(document, candidate.ResponseType(), "items")
		if !ok {
			continue
		}
		items = unwrapOptional(items)
		element, ok := items.Element()
		if !ok {
			continue
		}
		if value := unwrapOptional(element); value.Kind() == api.ValueRef && value.Name() == entity {
			if list.ID() != "" {
				return resourceOperations{}, api.ClosureType{}, buildError("resource_operation_ambiguous", pointer, "entity has more than one canonical list operation")
			}
			list = candidate
		}
	}
	if list.ID() == "" {
		return resourceOperations{}, api.ClosureType{}, buildError("operation_unresolved", pointer, "entity has no canonical list operation")
	}
	if list.Method() != api.MethodGET || list.ResponseType() == "" {
		return resourceOperations{}, api.ClosureType{}, buildError("list_operation_invalid", pointer, "list operation must be GET with a JSON response")
	}
	placeholders, err := httpconvention.ValidateRoute(list.Path())
	if err != nil || len(placeholders) != 0 {
		return resourceOperations{}, api.ClosureType{}, buildError("list_route_invalid", pointer, "list operation must use a canonical collection route")
	}
	if failure := validateOperationConvention(document, list, pointer); failure != nil {
		return resourceOperations{}, api.ClosureType{}, failure
	}
	_, items, itemsRequired, itemsOK := directField(document, list.ResponseType(), "items")
	_, total, totalRequired, totalOK := directField(document, list.ResponseType(), "total")
	items = unwrapOptional(items)
	listResponse, listResponseOK := document.Type(list.ResponseType())
	if !listResponseOK || len(listResponse.Fields()) != 2 || !itemsOK || !itemsRequired || items.Kind() != api.ValueArray || !totalOK || !totalRequired || !safeNumberScalar(total) {
		return resourceOperations{}, api.ClosureType{}, buildError("list_response_invalid", pointer, "list response must be exact required {items,total}")
	}
	element, ok := items.Element()
	if !ok || unwrapOptional(element).Kind() != api.ValueRef {
		return resourceOperations{}, api.ClosureType{}, buildError("list_item_type_invalid", pointer, "list items must reference one canonical resource type")
	}
	element = unwrapOptional(element)
	itemType, ok := document.Type(element.Name())
	if !ok {
		return resourceOperations{}, api.ClosureType{}, buildError("list_item_type_invalid", pointer, "list resource type is unresolved")
	}
	_, id, idRequired, idOK := directField(document, itemType.Name(), "id")
	if !idOK || !idRequired || unwrapOptional(id).Kind() != api.ValueScalar || !resourceIDScalar(unwrapOptional(id).Name()) {
		return resourceOperations{}, api.ClosureType{}, buildError("resource_id_invalid", pointer, "resource row key must be a required PDCL scalar id")
	}
	for _, name := range []string{"limit", "offset"} {
		_, value, _, exists := directField(document, list.RequestType(), name)
		if !exists || !safeNumberScalar(value) {
			return resourceOperations{}, api.ClosureType{}, buildError("pagination_request_invalid", pointer, "list request must contain canonical limit and offset numbers")
		}
	}

	operations := resourceOperations{list: list}
	itemPath := list.Path() + "/{id}"
	for _, candidate := range document.Operations {
		if candidate.ID() == list.ID() {
			continue
		}
		var target *api.ClosureOperation
		switch {
		case candidate.Path() == list.Path() && candidate.Method() == api.MethodPOST:
			target = &operations.create
		case candidate.Path() == itemPath && candidate.Method() == api.MethodGET:
			target = &operations.get
		case candidate.Path() == itemPath && (candidate.Method() == api.MethodPUT || candidate.Method() == api.MethodPATCH):
			target = &operations.update
		case candidate.Path() == itemPath && candidate.Method() == api.MethodDELETE:
			target = &operations.delete
		default:
			continue
		}
		if target.ID() != "" {
			return resourceOperations{}, api.ClosureType{}, buildError("resource_operation_ambiguous", pointer, "resource has more than one operation for a standard CRUD role")
		}
		*target = candidate
		if failure := validateOperationConvention(document, candidate, pointer); failure != nil {
			return resourceOperations{}, api.ClosureType{}, failure
		}
	}
	return operations, itemType, nil
}

func validateOperationConvention(document api.Closure, operation api.ClosureOperation, pointer string) *Error {
	if operation.Auth() != api.AuthNone && operation.Auth() != api.AuthRequired {
		return buildError("auth_convention_invalid", pointer, "operation auth must be required or none")
	}
	typeValue, ok := document.Type(operation.RequestType())
	if !ok {
		return buildError("request_type_unresolved", pointer, "operation request type is unresolved")
	}
	fields := make([]string, 0, len(typeValue.Fields()))
	for _, field := range typeValue.Fields() {
		if len(field.Path()) != 1 {
			return buildError("request_shape_invalid", pointer, "frontend CRUD request fields must be direct canonical fields")
		}
		fields = append(fields, field.Path()[0])
	}
	if _, err := httpconvention.ClassifyRequest(string(operation.Method()), operation.Path(), fields); err != nil {
		return buildError("request_convention_invalid", pointer, err.Error())
	}
	hasRepresentation := operation.ResponseType() != ""
	if _, err := httpconvention.SuccessStatus(string(operation.Method()), operation.Path(), hasRepresentation); err != nil {
		return buildError("success_convention_invalid", pointer, err.Error())
	}
	return nil
}

func validateRequestCoverage(document api.Closure, operation api.ClosureOperation, automatic map[string]bool, surfaces map[string]map[string]bool, surface, pointer string) *Error {
	typeValue, ok := document.Type(operation.RequestType())
	if !ok {
		return buildError("request_type_unresolved", pointer, "operation request type is unresolved")
	}
	for _, field := range typeValue.Fields() {
		path := field.Path()
		if len(path) != 1 {
			return buildError("request_shape_invalid", pointer, "frontend CRUD request fields must be direct")
		}
		value := field.ValueType()
		if !requestLeaf(value) {
			return buildError("request_field_unsupported", pointer, "frontend CRUD request fields must be scalar or scalar arrays")
		}
		covered := automatic[path[0]] || surfaces[path[0]][surface]
		if field.Required() && value.Kind() != api.ValueOptional && !covered {
			return buildError("required_request_field_uncovered", pointer, "required request field "+path[0]+" has no fixed or UI source")
		}
	}
	return nil
}

func validateIDOnlyRequest(document api.Closure, operation api.ClosureOperation, pointer string) *Error {
	typeValue, ok := document.Type(operation.RequestType())
	if !ok {
		return buildError("request_type_unresolved", pointer, "operation request type is unresolved")
	}
	if len(typeValue.Fields()) != 1 {
		return buildError("standard_action_request_invalid", pointer, "standard get and delete requests must contain only id")
	}
	field := typeValue.Fields()[0]
	if len(field.Path()) != 1 || field.Path()[0] != "id" || !field.Required() || unwrapOptional(field.ValueType()).Kind() != api.ValueScalar {
		return buildError("standard_action_request_invalid", pointer, "standard get and delete requests must contain one required scalar id")
	}
	return nil
}

func directField(document api.Closure, typeName, name string) (api.ClosureField, api.ClosureValue, bool, bool) {
	typeValue, ok := document.Type(typeName)
	if !ok {
		return api.ClosureField{}, api.ClosureValue{}, false, false
	}
	field, ok := typeValue.Field(name)
	if !ok || len(field.Path()) != 1 {
		return api.ClosureField{}, api.ClosureValue{}, false, false
	}
	value := field.ValueType()
	required := field.Required() && value.Kind() != api.ValueOptional
	return field, unwrapOptional(value), required, true
}

func unwrapOptional(value api.ClosureValue) api.ClosureValue {
	for value.Kind() == api.ValueOptional {
		element, ok := value.Element()
		if !ok {
			return api.ClosureValue{}
		}
		value = element
	}
	return value
}

func requestLeaf(value api.ClosureValue) bool {
	value = unwrapOptional(value)
	if value.Kind() == api.ValueScalar {
		return supportedScalar(value.Name())
	}
	if value.Kind() != api.ValueArray {
		return false
	}
	element, ok := value.Element()
	return ok && unwrapOptional(element).Kind() == api.ValueScalar && supportedScalar(unwrapOptional(element).Name())
}

func supportedScalar(name string) bool {
	switch name {
	case "string", "bool", "int32", "uint32", "int64", "uint64", "float32", "float64":
		return true
	default:
		return false
	}
}

func safeNumberScalar(value api.ClosureValue) bool {
	value = unwrapOptional(value)
	return value.Kind() == api.ValueScalar && (value.Name() == "int32" || value.Name() == "uint32" || value.Name() == "int64" || value.Name() == "uint64")
}

func resourceIDScalar(name string) bool {
	return name == "string" || name == "int32" || name == "uint32" || name == "int64" || name == "uint64"
}

func exactValueType(value api.ClosureValue) string {
	value = unwrapOptional(value)
	if element, ok := value.Element(); ok {
		return value.Kind() + ":" + exactValueType(element)
	}
	return value.Kind() + ":" + value.Name()
}

func controlCompatible(control string, value api.ClosureValue) bool {
	value = unwrapOptional(value)
	if control == "" {
		return true
	}
	switch control {
	case "switch":
		return value.Kind() == api.ValueScalar && value.Name() == "bool"
	case "select", "text", "textarea", "readonly", "sensitive", "member", "reference", "attachment", "tags", "component", "i18n", "iconify", "permission", "route", "scope", "http-method", "http-path", "module", "locale", "timezone":
		return value.Kind() == api.ValueScalar && (value.Name() == "string" || value.Name() == "int64" || value.Name() == "uint64")
	case "number":
		return value.Kind() == api.ValueScalar && (value.Name() == "int32" || value.Name() == "uint32" || value.Name() == "int64" || value.Name() == "uint64" || value.Name() == "float32" || value.Name() == "float64")
	case "multi-select":
		element, ok := value.Element()
		return value.Kind() == api.ValueArray && ok && unwrapOptional(element).Kind() == api.ValueScalar && unwrapOptional(element).Name() == "string"
	default:
		return false
	}
}

func validateMenuGraph(pages []wirePage) *Error {
	parents := map[string]string{}
	for _, page := range pages {
		if page.Menu != nil {
			parents[page.Menu.ID] = page.Menu.ParentID
		}
	}
	for id := range parents {
		seen := map[string]bool{}
		for current := id; current != "" && parents[current] != ""; current = parents[current] {
			if seen[current] {
				return buildError("menu_cycle", "/pages", "frontend menu graph contains a cycle")
			}
			seen[current] = true
		}
	}
	return nil
}

func projectFactLocales(pages []wirePage, facts sourcecomment.FactGraph) ([]wireLocale, error) {
	keys := map[string]bool{}
	for _, page := range pages {
		keys[page.TitleKey] = true
		for _, field := range page.Fields {
			keys[field.LabelKey] = true
		}
		for name, enabled := range map[string]bool{"create": page.Operations.Create != "", "update": page.Operations.Update != "", "delete": page.Operations.Delete != ""} {
			if enabled {
				keys[page.ID+".actions."+name] = true
			}
		}
		if page.Operations.Delete != "" {
			keys[page.ID+".actions.deleteConfirm"] = true
		}
	}
	orderedKeys := make([]string, 0, len(keys))
	for key := range keys {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	for _, key := range orderedKeys {
		parts := strings.Split(key, ".")
		for index := 1; index < len(parts); index++ {
			if keys[strings.Join(parts[:index], ".")] {
				return nil, buildError("locale_message_prefix_collision", "/locales", "required locale keys collide by prefix")
			}
		}
	}
	messages := map[string]map[string]string{"zh-CN": {}, "en-US": {}}
	semanticIDs := map[string]bool{}
	for _, fact := range facts.Facts() {
		if fact.ID().Key == "label.zh-CN" || fact.ID().Key == "label.en-US" {
			semanticIDs[fact.ID().SemanticID] = true
		}
	}
	for semanticID := range semanticIDs {
		if value, err := facts.SchemaFacts(semanticID); err == nil {
			messages["zh-CN"][value.Label.Key], messages["en-US"][value.Label.Key] = value.Label.ZhCN, value.Label.EnUS
			continue
		}
		if value, err := facts.FieldFacts(semanticID); err == nil {
			messages["zh-CN"][value.Label.Key], messages["en-US"][value.Label.Key] = value.Label.ZhCN, value.Label.EnUS
		}
	}
	actions := map[string][2]string{
		"create": {"新建", "Create"}, "update": {"编辑", "Edit"}, "delete": {"删除", "Delete"}, "deleteConfirm": {"确认删除？", "Confirm deletion?"},
	}
	for _, page := range pages {
		for action, values := range actions {
			key := page.ID + ".actions." + action
			if keys[key] {
				messages["zh-CN"][key], messages["en-US"][key] = values[0], values[1]
			}
		}
	}
	for _, key := range orderedKeys {
		for _, locale := range []string{"zh-CN", "en-US"} {
			if strings.TrimSpace(messages[locale][key]) == "" {
				return nil, buildError("locale_message_missing", "/locales/"+locale+"/messages/"+escapePointer(key), "required locale message is missing from upstream facts")
			}
		}
	}
	if len(pages) == 0 {
		return []wireLocale{}, nil
	}
	return []wireLocale{{Locale: "en-US", Messages: messages["en-US"]}, {Locale: "zh-CN", Messages: messages["zh-CN"]}}, nil
}

func selectClosure(document api.Closure, operationPointers map[string]string) (api.Closure, *Error) {
	operationIDs := make([]string, 0, len(operationPointers))
	for operationID := range operationPointers {
		operationIDs = append(operationIDs, operationID)
	}
	sort.Strings(operationIDs)
	selected := api.Closure{ConventionValue: document.Convention(), FactGraphValue: document.FactGraph()}
	types := map[string]api.ClosureType{}
	sources := map[string]provenance.Source{}
	addSources := func(values []provenance.Source, pointer string) *Error {
		for _, source := range values {
			key := source.Ref.String()
			if previous, exists := sources[key]; exists && previous.Digest != source.Digest {
				return buildError("source_digest_conflict", pointer, "referenced API facts have conflicting source digests")
			}
			sources[key] = source
		}
		return nil
	}
	var visitType func(string, string) *Error
	var visitValue func(api.ClosureValue, string) *Error
	visitValue = func(value api.ClosureValue, pointer string) *Error {
		if value.Kind() == api.ValueRef {
			return visitType(value.Name(), pointer)
		}
		if element, ok := value.Element(); ok {
			return visitValue(element, pointer)
		}
		return nil
	}
	visitType = func(name, pointer string) *Error {
		if name == "" {
			return nil
		}
		if _, exists := types[name]; exists {
			return nil
		}
		value, ok := document.Type(name)
		if !ok {
			return buildError("type_unresolved", pointer, "referenced API type does not exist")
		}
		types[name] = value
		if failure := addSources(value.Sources(), pointer); failure != nil {
			return failure
		}
		for _, field := range value.Fields() {
			if failure := addSources(field.Sources(), pointer); failure != nil {
				return failure
			}
			if failure := visitValue(field.ValueType(), pointer); failure != nil {
				return failure
			}
		}
		if failure := visitValue(value.Shape(), pointer); failure != nil {
			return failure
		}
		return nil
	}
	for _, operationID := range operationIDs {
		pointer := operationPointers[operationID]
		operation, ok := document.Operation(operationID)
		if !ok {
			return api.Closure{}, buildError("operation_unresolved", pointer, "API operation does not exist")
		}
		selected.Operations = append(selected.Operations, operation)
		if failure := addSources(operation.Sources(), pointer); failure != nil {
			return api.Closure{}, failure
		}
		if failure := visitType(operation.RequestType(), pointer); failure != nil {
			return api.Closure{}, failure
		}
		if failure := visitType(operation.ResponseType(), pointer); failure != nil {
			return api.Closure{}, failure
		}
	}
	typeNames := make([]string, 0, len(types))
	for name := range types {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		selected.Types = append(selected.Types, types[name])
	}
	sourceRefs := make([]string, 0, len(sources))
	for ref := range sources {
		sourceRefs = append(sourceRefs, ref)
	}
	sort.Strings(sourceRefs)
	for _, ref := range sourceRefs {
		selected.Sources = append(selected.Sources, sources[ref])
	}
	return selected, nil
}

func closureTypes(closure api.Closure, typeNames map[string]string) []wireClosureType {
	result := make([]wireClosureType, len(closure.Types))
	for index, value := range closure.Types {
		result[index] = wireClosureType{Name: value.Name(), TypeScriptName: typeNames[value.Name()], Fields: make([]wireClosureField, len(value.Fields()))}
		if shape := value.Shape(); shape.Kind() != "" && shape.Kind() != api.ValueObject {
			converted := closureValue(shape)
			result[index].Shape = &converted
		}
		for fieldIndex, field := range value.Fields() {
			result[index].Fields[fieldIndex] = wireClosureField{Path: field.Path(), Required: field.Required(), ValueType: closureValue(field.ValueType())}
		}
		sort.Slice(result[index].Fields, func(left, right int) bool {
			return strings.Join(result[index].Fields[left].Path, "\x00") < strings.Join(result[index].Fields[right].Path, "\x00")
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func closureOperations(closure api.Closure, clientNames map[string]string) ([]wireClosureOperation, *Error) {
	result := make([]wireClosureOperation, len(closure.Operations))
	for index, operation := range closure.Operations {
		clientName, ok := clientNames[operation.ID()]
		if !ok {
			return nil, buildError("generated_operation_name_missing", "/operations", "operation client name is missing")
		}
		result[index] = wireClosureOperation{ClientName: clientName, ID: operation.ID(), Method: string(operation.Method()), Path: operation.Path(), Auth: string(operation.Auth()), Permission: operation.Permission(), RequestType: operation.RequestType(), ResponseType: operation.ResponseType()}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func closureValue(value api.ClosureValue) wireClosureValue {
	result := wireClosureValue{Kind: value.Kind(), Name: value.Name()}
	if element, ok := value.Element(); ok {
		converted := closureValue(element)
		result.Element = &converted
	}
	return result
}

func routeName(id string) string {
	var result strings.Builder
	upper := true
	for _, character := range id {
		if character == '-' || character == '.' {
			upper = true
			continue
		}
		if upper {
			character = unicode.ToUpper(character)
			upper = false
		}
		result.WriteRune(character)
	}
	return result.String()
}
