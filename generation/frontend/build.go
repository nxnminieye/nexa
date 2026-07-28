package frontend

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/provenance"
)

var numericScalars = map[string]bool{"int": true, "int8": true, "int16": true, "int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "float": true, "float32": true, "float64": true, "number": true}
var integerScalars = map[string]bool{"int": true, "int8": true, "int16": true, "int32": true, "int64": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true}

func Build(apiDocument httpapi.Document, specs []PageSpec, locales ...Locale) (Document, error) {
	apiJSON, err := httpapi.CanonicalJSON(apiDocument)
	if err != nil {
		return Document{}, buildError("api_invalid", "/api", "HTTP API document is invalid")
	}
	apiDigest := provenance.SHA256(apiJSON)
	pages := make([]wirePage, len(specs))
	pageIDs, routePaths, routeNames, menuIDs, menuOrders := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]int{}, map[string]bool{}
	contextTypes := map[string]string{}
	validatedResponseTypes := map[string]bool{}
	for index, spec := range specs {
		base := "/pages/" + strconv.Itoa(index)
		if spec.state == nil {
			return Document{}, buildError("page_spec_invalid", base, "frontend page spec is invalid")
		}
		page := spec.state.document
		if pageIDs[page.ID] {
			return Document{}, buildError("page_id_duplicate", base+"/id", "frontend page id is duplicated")
		}
		pageIDs[page.ID] = true
		if routePaths[page.Route.Path] {
			return Document{}, buildError("route_path_duplicate", base+"/route/path", "frontend route path is duplicated")
		}
		routePaths[page.Route.Path] = true
		if routeNames[page.Route.Name] {
			return Document{}, buildError("route_name_duplicate", base+"/route/name", "frontend route name is duplicated")
		}
		routeNames[page.Route.Name] = true
		if page.Menu != nil {
			if _, ok := menuIDs[page.Menu.ID]; ok {
				return Document{}, buildError("menu_id_duplicate", base+"/menu/id", "frontend menu id is duplicated")
			}
			menuIDs[page.Menu.ID] = index
			key := page.Menu.ParentID + "\x00" + strconv.Itoa(page.Menu.Order)
			if menuOrders[key] {
				return Document{}, buildError("menu_order_duplicate", base+"/menu/order", "menu order is duplicated under the same parent")
			}
			menuOrders[key] = true
		}
		projected, failure := projectPage(apiDocument, spec, base, contextTypes, validatedResponseTypes)
		if failure != nil {
			return Document{}, failure
		}
		pages[index] = projected
	}
	if failure := validateMenuGraph(specs, menuIDs); failure != nil {
		return Document{}, failure
	}
	projectedLocales, err := validateAndProjectLocales(specs, locales)
	if err != nil {
		return Document{}, err
	}
	sources, err := mergeSources(apiDocument.Sources(), specs, locales)
	if err != nil {
		return Document{}, err
	}
	sourceDigest, err := computeSourceDigest(apiDigest, sources)
	if err != nil {
		return Document{}, buildError("source_digest_invalid", "/sourceDigest", "frontend source digest cannot be computed")
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].ID < pages[j].ID })
	wire := wireDocument{APIVersion: APIVersion, Kind: documentKind, APIDigest: apiDigest.String(), SourceDigest: sourceDigest.String(), Sources: wireSources(sources), API: append(json.RawMessage(nil), apiJSON...), Locales: projectedLocales, Pages: pages}
	return Document{state: &documentState{wire: cloneWireDocument(wire)}}, nil
}

type operationProjection struct {
	spec  operationDocument
	api   httpapi.Operation
	index int
}

func projectPage(document httpapi.Document, spec PageSpec, base string, globalContexts map[string]string, validatedResponseTypes map[string]bool) (wirePage, *Error) {
	page := clonePageDocument(spec.state.document)
	aliases := map[string]operationProjection{}
	roleCounts := map[string]int{}
	operations := make([]wireOperation, len(page.Operations))
	for index, item := range page.Operations {
		pointer := base + "/operations/" + strconv.Itoa(index)
		op, ok := document.Operation(item.OperationID)
		if !ok {
			return wirePage{}, buildError("operation_unresolved", pointer+"/operationId", "API operation does not exist")
		}
		if op.ResponseBody() == api.ResponseBodyJSON && !validatedResponseTypes[op.ResponseType()] {
			if failure := validateResponseWireClosure(document, op.ResponseType()); failure != nil {
				return wirePage{}, failure
			}
			validatedResponseTypes[op.ResponseType()] = true
		}
		aliases[item.ID] = operationProjection{item, op, index}
		roleCounts[item.Role]++
		if failure := validateResult(document, op, item, pointer); failure != nil {
			return wirePage{}, failure
		}
		if item.Role == "list" || item.Role == "options" {
			if item.Pagination == nil {
				return wirePage{}, buildError("pagination_required", pointer+"/pagination", "list and options operations require offset pagination")
			}
			if failure := validatePagination(document, op, item.Pagination, pointer+"/pagination"); failure != nil {
				return wirePage{}, failure
			}
			if !equalPath(item.Result.TotalPath, item.Pagination.TotalPath) {
				return wirePage{}, buildError("pagination_total_mismatch", pointer+"/pagination/totalPath", "pagination totalPath must match result totalPath")
			}
		} else if item.Pagination != nil {
			return wirePage{}, buildError("pagination_role_invalid", pointer+"/pagination", "pagination is only valid for list and options operations")
		}
		contexts := make([]wireContextBinding, len(item.ContextBindings))
		for bindingIndex, binding := range item.ContextBindings {
			_, value, required, ok := resolveDirectFieldPresence(document, op.RequestType(), binding.Path)
			if !ok || !required || !isContextScalar(value) {
				return wirePage{}, buildError("context_binding_type_invalid", pointer+"/contextBindings/"+strconv.Itoa(bindingIndex)+"/path", "context binding must target a required direct string or integer scalar")
			}
			typeID := exactType(value)
			if previous, exists := globalContexts[binding.Context]; exists && previous != typeID {
				return wirePage{}, buildError("context_type_inconsistent", pointer+"/contextBindings/"+strconv.Itoa(bindingIndex)+"/context", "context id must keep one exact scalar type across the IR")
			}
			globalContexts[binding.Context] = typeID
			contexts[bindingIndex] = wireContextBinding{Context: binding.Context, Path: clonePath(binding.Path), ValueType: wireValue(value)}
		}
		sort.Slice(contexts, func(i, j int) bool { return contexts[i].Context < contexts[j].Context })
		responseType := op.ResponseType()
		operations[index] = wireOperation{ID: item.ID, Role: item.Role, OperationID: item.OperationID, Permission: op.Permission(), RequestType: op.RequestType(), ResponseType: responseType, ContextBindings: contexts, Result: cloneResult(item.Result), Pagination: clonePaginationPtr(item.Pagination)}
	}
	if failure := validatePageMode(page, aliases, roleCounts, base); failure != nil {
		return wirePage{}, failure
	}
	access := aliases[page.AccessOperation]
	actionsByOperation := map[string]actionDocument{}
	fieldsByID := map[string]fieldDocument{}
	for _, field := range page.Fields {
		fieldsByID[field.ID] = field
	}
	for index, action := range page.Actions {
		pointer := base + "/actions/" + strconv.Itoa(index)
		projection, ok := aliases[action.Operation]
		if !ok || projection.spec.Role != "action" {
			return wirePage{}, buildError("action_operation_invalid", pointer+"/operation", "action must reference a role=action operation")
		}
		if _, exists := actionsByOperation[action.Operation]; exists {
			return wirePage{}, buildError("action_operation_duplicate", pointer+"/operation", "action operation must be referenced exactly once")
		}
		if action.Effect == "create" && action.Placement != "toolbar" || action.Effect != "create" && action.Placement != "row" {
			return wirePage{}, buildError("action_placement_invalid", pointer+"/placement", "create actions are toolbar actions; update and delete actions are row actions")
		}
		seen := map[string]bool{}
		for fieldIndex, fieldID := range action.Fields {
			field, exists := fieldsByID[fieldID]
			if !exists {
				return wirePage{}, buildError("action_field_unresolved", pointer+"/fields/"+strconv.Itoa(fieldIndex), "action field does not exist")
			}
			if seen[fieldID] {
				return wirePage{}, buildError("action_field_duplicate", pointer+"/fields/"+strconv.Itoa(fieldIndex), "action field is duplicated")
			}
			seen[fieldID] = true
			if field.Control == "" || !hasBinding(field, action.Operation, "request") {
				return wirePage{}, buildError("action_field_binding_missing", pointer+"/fields/"+strconv.Itoa(fieldIndex), "action field requires a control and request binding to the action operation")
			}
		}
		actionsByOperation[action.Operation] = action
	}
	for alias, projection := range aliases {
		if projection.spec.Role == "action" {
			if _, ok := actionsByOperation[alias]; !ok {
				return wirePage{}, buildError("action_operation_unreferenced", base+"/operations/"+strconv.Itoa(projection.index), "role=action operation must be referenced exactly once")
			}
		}
	}

	pageBindingPaths := map[string]bool{}
	optionRefs := map[string]bool{}
	actionFieldRefs := map[string]bool{}
	for _, action := range page.Actions {
		for _, fieldID := range action.Fields {
			actionFieldRefs[fieldID] = true
		}
	}
	wireFields := make([]wireField, len(page.Fields))
	for fieldIndex, field := range page.Fields {
		pointer := base + "/fields/" + strconv.Itoa(fieldIndex)
		if field.Options != nil && len(field.Surfaces) != 0 {
			return wirePage{}, buildError("options_surface_forbidden", pointer+"/surfaces", "dynamic options field is forbidden from search, list, and detail surfaces")
		}
		if len(field.Columns) != 0 && field.Control != "" {
			return wirePage{}, buildError("columns_control_forbidden", pointer+"/control", "object array columns cannot have a control")
		}
		bindingTypes := ""
		wireBindings := make([]wireBinding, len(field.Bindings))
		for bindingIndex, binding := range field.Bindings {
			bp := pointer + "/bindings/" + strconv.Itoa(bindingIndex)
			projection, ok := aliases[binding.Operation]
			if !ok {
				return wirePage{}, buildError("binding_operation_unresolved", bp+"/operation", "field binding operation alias does not exist")
			}
			key := binding.Operation + "\x00" + binding.Direction + "\x00" + strings.Join(binding.Path, "\x00")
			if pageBindingPaths[key] {
				return wirePage{}, buildError("binding_path_duplicate", bp+"/path", "operation direction path is bound more than once")
			}
			pageBindingPaths[key] = true
			var value httpapi.ValueType
			var found, required bool
			if binding.Direction == "request" {
				_, value, required, found = resolveDirectFieldPresence(document, projection.api.RequestType(), binding.Path)
				if !found || !isRequestLeaf(value) {
					return wirePage{}, buildError("request_binding_path_invalid", bp+"/path", "request binding must target a direct scalar or scalar array leaf")
				}
			} else {
				if projection.spec.Role != "list" && projection.spec.Role != "get" {
					return wirePage{}, buildError("response_binding_role_invalid", bp+"/operation", "response bindings are only allowed on primary list or singleton get")
				}
				if projection.api.ResponseBody() != api.ResponseBodyJSON {
					return wirePage{}, buildError("binding_response_missing", bp+"/path", "response binding requires a JSON response")
				}
				_, value, required, found = resolveFieldPathPresence(document, projection.api.ResponseType(), binding.Path, allowedArrayFor(projection.spec))
				if !found || !bindingWithinResult(binding.Path, projection.spec) {
					return wirePage{}, buildError("response_binding_scope_invalid", bp+"/path", "response binding must stay within the primary result root")
				}
			}
			identity := exactType(value)
			if !isIRValue(value) {
				return wirePage{}, buildError("binding_value_type_invalid", bp+"/path", "binding value type is not representable by closed frontend IR")
			}
			if bindingTypes == "" {
				bindingTypes = identity
			} else if bindingTypes != identity {
				return wirePage{}, buildError("binding_type_inconsistent", bp+"/path", "field bindings must have the same exact value type")
			}
			if failure := validateControlBinding(field.Control, value, bp+"/path"); failure != nil {
				return wirePage{}, failure
			}
			wireBindings[bindingIndex] = wireBinding{Operation: binding.Operation, Direction: binding.Direction, Path: clonePath(binding.Path), ValueType: wireValue(value), Required: required}
		}
		if failure := validateSurfaces(page, field, aliases, pointer); failure != nil {
			return wirePage{}, failure
		}
		if (field.Options != nil || len(field.Choices) > 0) && field.Control != "select" && field.Control != "multi-select" {
			return wirePage{}, buildError("selection_control_required", pointer+"/control", "choices and options require select or multi-select control")
		}
		if field.Options != nil && len(field.Choices) != 0 {
			return wirePage{}, buildError("selection_source_conflict", pointer, "choices and options are mutually exclusive")
		}
		if field.Options != nil {
			if !actionFieldRefs[field.ID] {
				return wirePage{}, buildError("options_action_required", pointer+"/options", "dynamic options field must be referenced by at least one action")
			}
			projection, ok := aliases[field.Options.Operation]
			if !ok || projection.spec.Role != "options" {
				return wirePage{}, buildError("options_operation_invalid", pointer+"/options/operation", "field options must reference a role=options operation")
			}
			optionRefs[field.Options.Operation] = true
			for _, candidate := range []struct {
				name string
				path []string
			}{{"valuePath", field.Options.ValuePath}, {"labelPath", field.Options.LabelPath}} {
				combined := append(clonePath(projection.spec.Result.ItemsPath), candidate.path...)
				_, value, found := resolveFieldPath(document, projection.api.ResponseType(), combined, len(projection.spec.Result.ItemsPath)-1)
				if !found || !isStringScalar(value) {
					return wirePage{}, buildError("options_value_type_invalid", pointer+"/options/"+candidate.name, "option value and label must be string scalar leaves")
				}
			}
		}
		wireColumns, failure := projectColumns(document, page, field, aliases, pointer)
		if failure != nil {
			return wirePage{}, failure
		}
		used := len(field.Surfaces) > 0
		for _, action := range page.Actions {
			if contains(action.Fields, field.ID) || hasBinding(field, action.Operation, "request") {
				used = true
			}
		}
		if !used {
			return wirePage{}, buildError("field_unused", pointer, "field must be consumed by a surface, action, options, or hidden action row source")
		}
		wireFields[fieldIndex] = wireField{ID: field.ID, LabelKey: field.LabelKey, Surfaces: sortedStrings(field.Surfaces), Control: field.Control, Bindings: wireBindings, Options: field.Options, Choices: field.Choices, Columns: wireColumns}
	}
	for alias, projection := range aliases {
		if projection.spec.Role == "options" && !optionRefs[alias] {
			return wirePage{}, buildError("options_operation_unreferenced", base+"/operations/"+strconv.Itoa(projection.index), "options operation must be referenced by a field")
		}
	}
	if failure := validateOperationClosure(document, page, aliases, actionsByOperation, base); failure != nil {
		return wirePage{}, failure
	}
	sort.Slice(operations, func(i, j int) bool { return operations[i].ID < operations[j].ID })
	sort.Slice(wireFields, func(i, j int) bool { return wireFields[i].ID < wireFields[j].ID })
	sort.Slice(page.Actions, func(i, j int) bool { return page.Actions[i].ID < page.Actions[j].ID })
	for i := range page.Actions {
		page.Actions[i].Fields = sortedStrings(page.Actions[i].Fields)
	}
	return wirePage{ID: page.ID, TitleKey: page.TitleKey, Mode: page.Mode, AccessOperation: page.AccessOperation, AccessPermission: access.api.Permission(), Route: page.Route, Menu: page.Menu, Operations: operations, Fields: wireFields, Actions: page.Actions, ExtensionPoints: sortedStrings(page.ExtensionPoints), SpecSourceRef: spec.state.sourceRef.String()}, nil
}

func validateResponseWireClosure(document httpapi.Document, rootType string) *Error {
	types := document.Types()
	typeIndexes := make(map[string]int, len(types))
	for index, value := range types {
		typeIndexes[value.Name()] = index
	}
	visited := map[string]bool{}
	var validateObject func(string, []string) *Error
	validateObject = func(typeName string, prefix []string) *Error {
		objectKey := typeName + "\x00" + strings.Join(prefix, "\x00")
		if visited[objectKey] {
			return nil
		}
		visited[objectKey] = true
		typeValue, ok := document.Type(typeName)
		if !ok {
			return buildError("response_wire_type_unresolved", "/api/types", "JSON response type closure contains an unresolved type")
		}
		fields := typeValue.Fields()
		wireNames := map[string]bool{}
		for fieldIndex, field := range fields {
			path := field.Path()
			if len(path) != len(prefix)+1 || !samePathPrefix(path, prefix) {
				continue
			}
			fieldPointer := "/api/types/" + strconv.Itoa(typeIndexes[typeName]) + "/fields/" + strconv.Itoa(fieldIndex)
			binding, hasBinding := field.Binding()
			if !hasBinding {
				return buildError("response_wire_binding_missing", fieldPointer+"/binding", "JSON response object field requires an explicit body binding")
			}
			if binding.Location() != api.RequestBindingBody {
				return buildError("response_wire_binding_location_invalid", fieldPointer+"/binding/in", "JSON response object field binding must use body location")
			}
			if wireNames[binding.Name()] {
				return buildError("response_wire_name_duplicate", fieldPointer+"/binding/name", "JSON response object sibling wire name is duplicated")
			}
			wireNames[binding.Name()] = true
			value := unwrapResponseContainer(field.ValueType())
			switch value.Kind() {
			case httpapi.ValueObject:
				if failure := validateObject(typeName, path); failure != nil {
					return failure
				}
			case httpapi.ValueRef:
				if failure := validateObject(value.Name(), nil); failure != nil {
					return failure
				}
			}
		}
		return nil
	}
	return validateObject(rootType, nil)
}

func unwrapResponseContainer(value httpapi.ValueType) httpapi.ValueType {
	for value.Kind() == httpapi.ValueOptional || value.Kind() == httpapi.ValueArray {
		next, ok := value.Element()
		if !ok {
			return httpapi.ValueType{}
		}
		value = next
	}
	return value
}

func samePathPrefix(path, prefix []string) bool {
	for index := range prefix {
		if path[index] != prefix[index] {
			return false
		}
	}
	return true
}

func projectColumns(document httpapi.Document, page pageDocument, field fieldDocument, aliases map[string]operationProjection, pointer string) ([]wireColumn, *Error) {
	if len(field.Columns) == 0 {
		if len(field.Surfaces) > 0 {
			for _, binding := range field.Bindings {
				projection, ok := aliases[binding.Operation]
				if ok && binding.Operation == page.AccessOperation && binding.Direction == "response" {
					_, value, found := resolveFieldPath(document, projection.api.ResponseType(), binding.Path, allowedArrayFor(projection.spec))
					if found && isObjectArray(value) {
						return nil, buildError("columns_required", pointer+"/columns", "displayed object arrays require nonempty columns")
					}
				}
			}
		}
		return nil, nil
	}
	if field.Control != "" {
		return nil, buildError("columns_control_forbidden", pointer+"/control", "object array columns cannot have a control")
	}
	if len(field.Bindings) != 1 {
		return nil, buildError("columns_binding_invalid", pointer+"/bindings", "columns require exactly one response binding to the page access operation")
	}
	binding := field.Bindings[0]
	projection, ok := aliases[binding.Operation]
	if !ok || binding.Operation != page.AccessOperation || binding.Direction != "response" {
		return nil, buildError("columns_binding_invalid", pointer+"/bindings", "columns require exactly one response binding to the page access operation")
	}
	_, value, bindingRequired, found := resolveFieldPathPresence(document, projection.api.ResponseType(), binding.Path, allowedArrayFor(projection.spec))
	if !found || !isObjectArray(value) {
		return nil, buildError("columns_type_invalid", pointer+"/bindings/0/path", "columns require an array of object or ref")
	}
	element, _ := unwrapOptional(value).Element()
	if element.Kind() == httpapi.ValueOptional {
		return nil, buildError("column_optional_element_forbidden", pointer+"/bindings/0/path", "column array elements cannot be optional")
	}
	result := make([]wireColumn, len(field.Columns))
	for index, column := range field.Columns {
		combined := append(clonePath(binding.Path), column.Path...)
		allowedArrays := map[int]bool{allowedArrayFor(projection.spec): true, len(binding.Path) - 1: true}
		_, terminal, required, ok := resolveFieldPathPresenceArrays(document, projection.api.ResponseType(), combined, allowedArrays)
		if !ok {
			return nil, buildError("column_path_invalid", pointer+"/columns/"+strconv.Itoa(index)+"/path", "column path must resolve relative to the array item without crossing map or array")
		}
		terminal = unwrapOptional(terminal)
		if terminal.Kind() == httpapi.ValueArray {
			item, exists := terminal.Element()
			if !exists || item.Kind() == httpapi.ValueOptional || unwrapOptional(item).Kind() != httpapi.ValueScalar {
				return nil, buildError("column_type_invalid", pointer+"/columns/"+strconv.Itoa(index)+"/path", "column terminal must be scalar or array of non-optional scalar")
			}
		} else if terminal.Kind() != httpapi.ValueScalar {
			return nil, buildError("column_type_invalid", pointer+"/columns/"+strconv.Itoa(index)+"/path", "column terminal must be scalar or array of non-optional scalar")
		}
		result[index] = wireColumn{ID: column.ID, LabelKey: column.LabelKey, Path: clonePath(column.Path), ValueType: wireValue(terminal), Required: bindingRequired && required}
	}
	return result, nil
}

func isObjectArray(value httpapi.ValueType) bool {
	value = unwrapOptional(value)
	if value.Kind() != httpapi.ValueArray {
		return false
	}
	element, ok := value.Element()
	if !ok {
		return false
	}
	element = unwrapOptional(element)
	return element.Kind() == httpapi.ValueObject || element.Kind() == httpapi.ValueRef
}

func validatePageMode(page pageDocument, aliases map[string]operationProjection, counts map[string]int, base string) *Error {
	access, ok := aliases[page.AccessOperation]
	if !ok {
		return buildError("access_operation_unresolved", base+"/accessOperation", "access operation alias does not exist")
	}
	if page.Mode == "collection" {
		if counts["list"] != 1 || counts["get"] != 0 {
			return buildError("collection_operation_shape_invalid", base+"/operations", "collection requires exactly one list and forbids get")
		}
		if access.spec.Role != "list" {
			return buildError("access_operation_role_invalid", base+"/accessOperation", "collection access operation must be its primary list")
		}
		return nil
	}
	if counts["get"] != 1 || len(page.Operations) != 1 || len(page.Actions) != 0 {
		return buildError("singleton_operation_shape_invalid", base+"/operations", "singleton requires exactly one get and no actions or other operations")
	}
	if access.spec.Role != "get" {
		return buildError("access_operation_role_invalid", base+"/accessOperation", "singleton access operation must be its get")
	}
	return nil
}

func validateResult(document httpapi.Document, op httpapi.Operation, spec operationDocument, pointer string) *Error {
	r := spec.Result
	if spec.Role == "list" || spec.Role == "options" {
		if r == nil || len(r.ItemsPath) == 0 || len(r.TotalPath) == 0 {
			return buildError("collection_result_required", pointer+"/result", "list and options require itemsPath and totalPath")
		}
		if spec.Role == "list" && len(r.RowKeyPath) == 0 {
			return buildError("row_key_required", pointer+"/result/rowKeyPath", "list requires rowKeyPath")
		}
		if spec.Role == "options" && len(r.RowKeyPath) > 0 {
			return buildError("row_key_forbidden", pointer+"/result/rowKeyPath", "rowKeyPath is only valid for list")
		}
	} else if spec.Role == "get" {
		if r != nil && (len(r.ItemsPath) > 0 || len(r.TotalPath) > 0 || len(r.RowKeyPath) > 0) {
			return buildError("get_result_invalid", pointer+"/result", "get result may only declare itemPath")
		}
	} else if r != nil {
		return buildError("action_result_forbidden", pointer+"/result", "action operations cannot declare result")
	}
	if r == nil {
		return nil
	}
	if op.ResponseBody() != api.ResponseBodyJSON || op.ResponseType() == "" {
		return buildError("result_response_missing", pointer+"/result", "operation result requires a JSON response")
	}
	if len(r.ItemsPath) > 0 {
		_, v, ok := resolveFieldPath(document, op.ResponseType(), r.ItemsPath, -1)
		if !ok || unwrapOptional(v).Kind() != httpapi.ValueArray {
			return buildError("result_items_type_invalid", pointer+"/result/itemsPath", "itemsPath must resolve without crossing an array or map to an array")
		}
	}
	if len(r.ItemPath) > 0 {
		_, v, ok := resolveFieldPath(document, op.ResponseType(), r.ItemPath, -1)
		k := unwrapOptional(v).Kind()
		if !ok || (k != httpapi.ValueObject && k != httpapi.ValueRef) {
			return buildError("result_item_type_invalid", pointer+"/result/itemPath", "itemPath must resolve to object or ref")
		}
	}
	if len(r.TotalPath) > 0 {
		_, v, ok := resolveFieldPath(document, op.ResponseType(), r.TotalPath, -1)
		if !ok || !isIntegerScalar(v) {
			return buildError("result_total_type_invalid", pointer+"/result/totalPath", "totalPath must resolve to an integer scalar")
		}
	}
	if len(r.RowKeyPath) > 0 {
		combined := append(clonePath(r.ItemsPath), r.RowKeyPath...)
		_, v, required, ok := resolveFieldPathPresence(document, op.ResponseType(), combined, len(r.ItemsPath)-1)
		if !ok || !required || (!isStringScalar(v) && !isIntegerScalar(v)) {
			return buildError("row_key_type_invalid", pointer+"/result/rowKeyPath", "rowKeyPath must resolve relative to itemsPath to a required string or integer scalar")
		}
	}
	return nil
}

func validatePagination(document httpapi.Document, op httpapi.Operation, p *paginationDocument, pointer string) *Error {
	if equalPath(p.LimitPath, p.OffsetPath) {
		return buildError("pagination_path_duplicate", pointer+"/offsetPath", "limitPath and offsetPath must differ")
	}
	for _, c := range []struct {
		name string
		path []string
	}{{"limitPath", p.LimitPath}, {"offsetPath", p.OffsetPath}} {
		_, v, ok := resolveDirectField(document, op.RequestType(), c.path)
		if !ok || !isIntegerScalar(v) {
			return buildError("pagination_request_type_invalid", pointer+"/"+c.name, "pagination request path must be a direct integer scalar")
		}
	}
	if op.ResponseBody() != api.ResponseBodyJSON {
		return buildError("pagination_response_missing", pointer+"/totalPath", "pagination requires JSON response")
	}
	_, v, ok := resolveFieldPath(document, op.ResponseType(), p.TotalPath, -1)
	if !ok || !isIntegerScalar(v) {
		return buildError("pagination_total_type_invalid", pointer+"/totalPath", "pagination total must be an integer scalar")
	}
	return nil
}

func validateSurfaces(page pageDocument, field fieldDocument, aliases map[string]operationProjection, pointer string) *Error {
	for i, s := range field.Surfaces {
		switch s {
		case "search":
			if field.Control == "" || !hasBinding(field, page.AccessOperation, "request") {
				return buildError("search_surface_binding_invalid", pointer+"/surfaces/"+strconv.Itoa(i), "search surface requires a control and primary list request binding")
			}
		case "list":
			if !hasBinding(field, page.AccessOperation, "response") {
				return buildError("list_surface_binding_invalid", pointer+"/surfaces/"+strconv.Itoa(i), "list surface requires primary list response binding")
			}
		case "detail":
			if page.Mode != "singleton" || !hasBinding(field, page.AccessOperation, "response") {
				return buildError("detail_surface_binding_invalid", pointer+"/surfaces/"+strconv.Itoa(i), "detail surface requires singleton get response binding")
			}
		}
	}
	for _, b := range field.Bindings {
		if b.Operation == page.AccessOperation && b.Direction == "request" && !contains(field.Surfaces, "search") {
			return buildError("binding_surface_mismatch", pointer+"/bindings", "primary list request binding requires search surface")
		}
	}
	return nil
}

func validateOperationClosure(document httpapi.Document, page pageDocument, aliases map[string]operationProjection, actions map[string]actionDocument, base string) *Error {
	for alias, p := range aliases {
		covered := map[string]string{}
		add := func(path []string, source, pointer string) *Error {
			key := strings.Join(path, "\x00")
			if previous := covered[key]; previous != "" {
				return buildError("request_binding_conflict", pointer, "request path is covered by both "+previous+" and "+source)
			}
			covered[key] = source
			return nil
		}
		for i, c := range p.spec.ContextBindings {
			if e := add(c.Path, "context", base+"/operations/"+strconv.Itoa(p.index)+"/contextBindings/"+strconv.Itoa(i)+"/path"); e != nil {
				return e
			}
		}
		if p.spec.Pagination != nil {
			if e := add(p.spec.Pagination.LimitPath, "pagination", base+"/operations/"+strconv.Itoa(p.index)+"/pagination/limitPath"); e != nil {
				return e
			}
			if e := add(p.spec.Pagination.OffsetPath, "pagination", base+"/operations/"+strconv.Itoa(p.index)+"/pagination/offsetPath"); e != nil {
				return e
			}
		}
		for fi, f := range page.Fields {
			for bi, b := range f.Bindings {
				if b.Operation != alias || b.Direction != "request" {
					continue
				}
				if e := add(b.Path, "field", base+"/fields/"+strconv.Itoa(fi)+"/bindings/"+strconv.Itoa(bi)+"/path"); e != nil {
					return e
				}
				if p.spec.Role == "action" {
					a := actions[alias]
					controlled := contains(a.Fields, f.ID)
					if controlled {
						if f.Control == "" {
							return buildError("controlled_field_control_missing", base+"/fields/"+strconv.Itoa(fi)+"/control", "action input field requires a control")
						}
					} else {
						if a.Effect == "create" {
							return buildError("create_row_source_forbidden", base+"/fields/"+strconv.Itoa(fi)+"/bindings/"+strconv.Itoa(bi), "create action cannot use row source")
						}
						source, ok := bindingFor(f, page.AccessOperation, "response")
						if !ok {
							return buildError("row_source_missing", base+"/fields/"+strconv.Itoa(fi)+"/bindings/"+strconv.Itoa(bi), "uncontrolled row action binding requires primary list response source")
						}
						_, _, sourceRequired, _ := resolveFieldPathPresence(document, aliases[page.AccessOperation].api.ResponseType(), source.Path, allowedArrayFor(aliases[page.AccessOperation].spec))
						_, _, targetRequired, _ := resolveDirectFieldPresence(document, p.api.RequestType(), b.Path)
						if !sourceRequired && targetRequired {
							return buildError("optional_row_source_required_target", base+"/fields/"+strconv.Itoa(fi)+"/bindings/"+strconv.Itoa(bi), "optional row source cannot populate required action input")
						}
					}
				}
			}
		}
		t, ok := document.Type(p.api.RequestType())
		if !ok {
			return buildError("request_type_unresolved", base+"/operations/"+strconv.Itoa(p.index)+"/operationId", "request type does not exist")
		}
		for _, f := range t.Fields() {
			if len(f.Path()) != 1 {
				continue
			}
			v := unwrapOptional(f.ValueType())
			if !isRequestLeaf(v) {
				return buildError("request_leaf_type_invalid", base+"/operations/"+strconv.Itoa(p.index)+"/operationId", "v1 request DTO direct fields must be scalar or scalar arrays")
			}
			key := f.Path()[0]
			if f.Required() && covered[key] == "" {
				return buildError("required_request_binding_missing", base+"/operations/"+strconv.Itoa(p.index)+"/operationId", "required direct request field "+key+" is not covered")
			}
		}
		if p.spec.Role == "get" {
			for _, f := range page.Fields {
				if hasBinding(f, alias, "request") {
					return buildError("singleton_request_field_forbidden", base+"/fields", "singleton get request fields must come only from context bindings")
				}
			}
		}
	}
	return nil
}

func resolveDirectField(d httpapi.Document, typeName string, path []string) (httpapi.Field, httpapi.ValueType, bool) {
	field, value, _, ok := resolveDirectFieldPresence(d, typeName, path)
	return field, value, ok
}
func resolveDirectFieldPresence(d httpapi.Document, typeName string, path []string) (httpapi.Field, httpapi.ValueType, bool, bool) {
	if len(path) != 1 {
		return httpapi.Field{}, httpapi.ValueType{}, false, false
	}
	return resolveFieldPathPresence(d, typeName, path, -1)
}
func resolveFieldPath(d httpapi.Document, typeName string, path []string, allowedArray int) (httpapi.Field, httpapi.ValueType, bool) {
	field, value, _, ok := resolveFieldPathPresence(d, typeName, path, allowedArray)
	return field, value, ok
}
func resolveFieldPathPresence(d httpapi.Document, typeName string, path []string, allowedArray int) (httpapi.Field, httpapi.ValueType, bool, bool) {
	allowed := map[int]bool{}
	if allowedArray >= 0 {
		allowed[allowedArray] = true
	}
	return resolveFieldPathPresenceArrays(d, typeName, path, allowed)
}
func resolveFieldPathPresenceArrays(d httpapi.Document, typeName string, path []string, allowedArrays map[int]bool) (httpapi.Field, httpapi.ValueType, bool, bool) {
	if typeName == "" || len(path) == 0 || invalidTypedPathSegment(path) >= 0 {
		return httpapi.Field{}, httpapi.ValueType{}, false, false
	}
	current, ok := d.Type(typeName)
	if !ok {
		return httpapi.Field{}, httpapi.ValueType{}, false, false
	}
	prefix := []string{}
	required := true
	for i, s := range path {
		prefix = append(prefix, s)
		field, found := current.Field(strings.Join(prefix, "."))
		if !found {
			return httpapi.Field{}, httpapi.ValueType{}, false, false
		}
		raw := field.ValueType()
		required = required && field.Required() && raw.Kind() != httpapi.ValueOptional
		v := unwrapOptional(raw)
		if i == len(path)-1 {
			return field, v, required, true
		}
		switch v.Kind() {
		case httpapi.ValueObject:
		case httpapi.ValueRef:
			current, ok = d.Type(v.Name())
			if !ok {
				return httpapi.Field{}, httpapi.ValueType{}, false, false
			}
			prefix = nil
		case httpapi.ValueArray:
			if !allowedArrays[i] {
				return httpapi.Field{}, httpapi.ValueType{}, false, false
			}
			e, x := v.Element()
			if !x {
				return httpapi.Field{}, httpapi.ValueType{}, false, false
			}
			required = required && e.Kind() != httpapi.ValueOptional
			e = unwrapOptional(e)
			if e.Kind() == httpapi.ValueRef {
				current, ok = d.Type(e.Name())
				if !ok {
					return httpapi.Field{}, httpapi.ValueType{}, false, false
				}
				prefix = nil
			} else if e.Kind() != httpapi.ValueObject {
				return httpapi.Field{}, httpapi.ValueType{}, false, false
			}
		default:
			return httpapi.Field{}, httpapi.ValueType{}, false, false
		}
	}
	return httpapi.Field{}, httpapi.ValueType{}, false, false
}
func invalidTypedPathSegment(path []string) int {
	for i, s := range path {
		if strings.Contains(s, ".") {
			return i
		}
	}
	return -1
}
func unwrapOptional(v httpapi.ValueType) httpapi.ValueType {
	for v.Kind() == httpapi.ValueOptional {
		n, ok := v.Element()
		if !ok {
			break
		}
		v = n
	}
	return v
}
func exactType(v httpapi.ValueType) string {
	v = unwrapOptional(v)
	if v.Kind() == httpapi.ValueArray {
		e, ok := v.Element()
		if !ok {
			return "array:?"
		}
		return "array:" + exactType(e)
	}
	return string(v.Kind()) + ":" + v.Name()
}
func wireValue(v httpapi.ValueType) wireValueType {
	v = unwrapOptional(v)
	r := wireValueType{Kind: string(v.Kind()), Name: v.Name()}
	if v.Kind() == httpapi.ValueArray {
		e, _ := v.Element()
		x := wireValue(e)
		r.Element = &x
	}
	return r
}
func isIRValue(v httpapi.ValueType) bool {
	v = unwrapOptional(v)
	if v.Kind() == httpapi.ValueScalar || v.Kind() == httpapi.ValueRef || v.Kind() == httpapi.ValueObject {
		return true
	}
	if v.Kind() != httpapi.ValueArray {
		return false
	}
	element, ok := v.Element()
	return ok && element.Kind() != httpapi.ValueOptional && isIRValue(element)
}
func isRequestLeaf(v httpapi.ValueType) bool {
	v = unwrapOptional(v)
	if v.Kind() == httpapi.ValueScalar {
		return true
	}
	if v.Kind() != httpapi.ValueArray {
		return false
	}
	e, ok := v.Element()
	return ok && unwrapOptional(e).Kind() == httpapi.ValueScalar
}
func isIntegerScalar(v httpapi.ValueType) bool {
	v = unwrapOptional(v)
	return v.Kind() == httpapi.ValueScalar && integerScalars[v.Name()]
}
func isStringScalar(v httpapi.ValueType) bool {
	v = unwrapOptional(v)
	return v.Kind() == httpapi.ValueScalar && v.Name() == "string"
}
func isContextScalar(v httpapi.ValueType) bool { return isStringScalar(v) || isIntegerScalar(v) }
func validateControlBinding(control string, v httpapi.ValueType, p string) *Error {
	v = unwrapOptional(v)
	valid := true
	switch control {
	case "toggle":
		valid = v.Kind() == httpapi.ValueScalar && v.Name() == "bool"
	case "select", "text", "password", "textarea":
		valid = isStringScalar(v)
	case "multi-select":
		valid = false
		if v.Kind() == httpapi.ValueArray {
			e, ok := v.Element()
			valid = ok && isStringScalar(e)
		}
	case "number":
		valid = v.Kind() == httpapi.ValueScalar && numericScalars[v.Name()]
	}
	if control != "" && !valid {
		return buildError("control_binding_type_invalid", p, "control is incompatible with exact binding type")
	}
	return nil
}
func allowedArrayFor(spec operationDocument) int {
	if spec.Result != nil && len(spec.Result.ItemsPath) > 0 {
		return len(spec.Result.ItemsPath) - 1
	}
	return -1
}
func bindingWithinResult(path []string, spec operationDocument) bool {
	if spec.Role == "list" {
		return len(path) > len(spec.Result.ItemsPath) && hasPrefix(path, spec.Result.ItemsPath)
	}
	if spec.Role == "get" {
		if spec.Result == nil || len(spec.Result.ItemPath) == 0 {
			return true
		}
		return len(path) > len(spec.Result.ItemPath) && hasPrefix(path, spec.Result.ItemPath)
	}
	return false
}
func hasPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}
func hasBinding(f fieldDocument, op, dir string) bool { _, ok := bindingFor(f, op, dir); return ok }
func bindingFor(f fieldDocument, op, dir string) (bindingDocument, bool) {
	for _, b := range f.Bindings {
		if b.Operation == op && b.Direction == dir {
			return b, true
		}
	}
	return bindingDocument{}, false
}
func contains(v []string, w string) bool {
	for _, x := range v {
		if x == w {
			return true
		}
	}
	return false
}
func equalPath(a, b []string) bool  { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }
func clonePath(v []string) []string { return append([]string(nil), v...) }
func clonePaginationPtr(v *paginationDocument) *paginationDocument {
	if v == nil {
		return nil
	}
	x := clonePagination(v)
	return &x
}

func validateMenuGraph(specs []PageSpec, menus map[string]int) *Error {
	for i, s := range specs {
		if s.state == nil || s.state.document.Menu == nil {
			continue
		}
		m := s.state.document.Menu
		if m.ParentID == "" {
			continue
		}
		if m.ParentID == m.ID {
			return buildError("menu_self_parent", "/pages/"+strconv.Itoa(i)+"/menu/parentId", "menu cannot be its own parent")
		}
		if _, ok := menus[m.ParentID]; !ok {
			return buildError("menu_parent_unresolved", "/pages/"+strconv.Itoa(i)+"/menu/parentId", "menu parent does not exist")
		}
		seen := map[string]bool{m.ID: true}
		p := m.ParentID
		for p != "" {
			if seen[p] {
				return buildError("menu_cycle", "/pages/"+strconv.Itoa(i)+"/menu/parentId", "menu parent graph contains a cycle")
			}
			seen[p] = true
			p = specs[menus[p]].state.document.Menu.ParentID
		}
	}
	return nil
}

func validateAndProjectLocales(specs []PageSpec, locales []Locale) ([]wireLocale, error) {
	if len(specs) > 0 && len(locales) == 0 {
		return nil, buildError("locale_required", "/locales", "pages require at least one frontend locale")
	}
	labels := map[string]bool{}
	for _, s := range specs {
		p := s.state.document
		labels[p.TitleKey] = true
		if p.Menu != nil {
			labels[p.Menu.TitleKey] = true
		}
		for _, f := range p.Fields {
			labels[f.LabelKey] = true
			for _, c := range f.Choices {
				labels[c.LabelKey] = true
			}
			for _, c := range f.Columns {
				labels[c.LabelKey] = true
			}
		}
		for _, a := range p.Actions {
			labels[a.LabelKey] = true
			if a.ConfirmKey != "" {
				labels[a.ConfirmKey] = true
			}
		}
	}
	for key := range labels {
		parts := strings.Split(key, ".")
		for index := 1; index < len(parts); index++ {
			if labels[strings.Join(parts[:index], ".")] {
				return nil, buildError("locale_message_prefix_collision", "/locales", "required locale message keys cannot be both a leaf and a namespace")
			}
		}
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	out := make([]wireLocale, len(locales))
	for i, l := range locales {
		base := "/locales/" + strconv.Itoa(i)
		if l.state == nil {
			return nil, buildError("locale_invalid", base, "frontend locale is invalid")
		}
		name := l.state.document.Locale
		if seen[name] {
			return nil, buildError("locale_duplicate", base+"/locale", "frontend locale is duplicated")
		}
		seen[name] = true
		for _, k := range keys {
			if strings.TrimSpace(l.state.document.Messages[k]) == "" {
				return nil, buildError("locale_message_missing", base+"/messages/"+escapePointer(k), "required locale message is missing or empty")
			}
		}
		out[i] = wireLocale{Locale: name, Messages: cloneLocaleDocument(l.state.document).Messages, SourceRef: l.state.sourceRef.String()}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locale < out[j].Locale })
	return out, nil
}
func escapePointer(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, "~", "~0"), "/", "~1")
}
func mergeSources(apiSources []provenance.Source, specs []PageSpec, locales []Locale) ([]provenance.Source, error) {
	by := map[string]provenance.Source{}
	for _, s := range apiSources {
		by[s.Ref.String()] = s
	}
	for i, s := range specs {
		x := provenance.Source{Ref: s.state.sourceRef, Digest: s.state.digest}
		if p, ok := by[x.Ref.String()]; ok && p.Digest != x.Digest {
			return nil, buildError("source_digest_conflict", "/pages/"+strconv.Itoa(i)+"/specSourceRef", "source reference has conflicting digests")
		}
		by[x.Ref.String()] = x
	}
	for i, l := range locales {
		x := provenance.Source{Ref: l.state.sourceRef, Digest: l.state.digest}
		if p, ok := by[x.Ref.String()]; ok && p.Digest != x.Digest {
			return nil, buildError("source_digest_conflict", "/locales/"+strconv.Itoa(i)+"/sourceRef", "source reference has conflicting digests")
		}
		by[x.Ref.String()] = x
	}
	out := make([]provenance.Source, 0, len(by))
	for _, s := range by {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.String() < out[j].Ref.String() })
	return out, nil
}
func computeSourceDigest(d provenance.Digest, s []provenance.Source) (provenance.Digest, error) {
	b, e := json.Marshal(sourceSetEnvelope{APIVersion: sourceSetAPI, APIDigest: d.String(), Sources: wireSources(s)})
	if e != nil {
		return provenance.Digest{}, e
	}
	b, e = jcs.Transform(b)
	if e != nil {
		return provenance.Digest{}, e
	}
	return provenance.SHA256(b), nil
}
func cloneResult(v *resultDocument) *resultDocument {
	if v == nil {
		return nil
	}
	r := *v
	r.ItemsPath = clonePath(v.ItemsPath)
	r.ItemPath = clonePath(v.ItemPath)
	r.TotalPath = clonePath(v.TotalPath)
	r.RowKeyPath = clonePath(v.RowKeyPath)
	return &r
}
func bindingKey(v bindingDocument) string {
	return v.Operation + "\x00" + v.Direction + "\x00" + strings.Join(v.Path, "\x00")
}
