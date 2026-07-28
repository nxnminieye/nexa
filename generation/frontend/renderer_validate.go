package frontend

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gowebpki/jcs"
	generationapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

type rendererAPIValue struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name,omitempty"`
	Element *rendererAPIValue `json:"element,omitempty"`
}
type rendererAPIField struct {
	Path      []string            `json:"path"`
	Required  bool                `json:"required"`
	ValueType rendererAPIValue    `json:"valueType"`
	Binding   *rendererAPIBinding `json:"binding,omitempty"`
}
type rendererAPIBinding struct {
	Location string `json:"in"`
	Name     string `json:"name"`
}
type rendererAPIType struct {
	Name   string             `json:"name"`
	Fields []rendererAPIField `json:"fields"`
}
type rendererAPIOperation struct {
	ID           string `json:"id"`
	Permission   string `json:"permission"`
	RequestType  string `json:"requestType"`
	ResponseBody string `json:"responseBody"`
	ResponseType string `json:"responseType"`
}
type rendererAPIIndex struct {
	Types      []rendererAPIType      `json:"types"`
	Operations []rendererAPIOperation `json:"operations"`
	Sources    []wireSource           `json:"sources"`
}

// ValidateRendererInput is the renderer-side v1 reference validation pipeline.
// It performs size, schema, canonical JSON, scope, embedded API, and projected
// IR semantic checks without writing files.
func ValidateRendererInput(data []byte) error {
	if len(data) > maxRenderRequestBytes {
		return renderError("request_too_large", "", "frontend render request exceeds 1 MiB")
	}
	document, err := strictdoc.ParseJSON("frontend-render-request.json", data)
	if err != nil {
		return renderError("document_invalid", "", "frontend render request is not valid JSON")
	}
	var normalized any
	if err := json.Unmarshal(document.JSON(), &normalized); err != nil {
		return renderError("document_invalid", "", "frontend render request is not valid JSON")
	}
	if err := validateWireSchema(renderSchemaURL, RenderRequestSchema(), normalized); err != nil {
		return renderError("schema_invalid", "", "frontend render request does not match its schema")
	}
	canonical, err := jcs.Transform(data)
	if err != nil || !bytes.Equal(canonical, data) {
		return renderError("noncanonical_input", "", "frontend render request must be canonical JSON")
	}
	var request wireRenderRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return renderError("document_invalid", "", "frontend render request is invalid")
	}
	if request.RepositoryRoot == "" || !filepath.IsAbs(request.RepositoryRoot) || filepath.Clean(request.RepositoryRoot) != request.RepositoryRoot {
		return renderError("repository_root_invalid", "/repositoryRoot", "repository root must be absolute")
	}
	if !validWireScope(request.GeneratedScope) {
		return renderError("generated_scope_invalid", "/generatedScope", "generated scope is invalid")
	}
	for index, scope := range request.ExtensionScopes {
		if !validWireScope(scope) {
			return renderError("extension_scope_invalid", "/extensionScopes/"+itoa(index), "extension scope is invalid")
		}
		if relation := scopeRelation(request.GeneratedScope, scope); relation != "" {
			return renderError("scope_"+relation, "/extensionScopes/"+itoa(index), "generated and extension scopes overlap")
		}
		for previous := 0; previous < index; previous++ {
			if relation := scopeRelation(request.ExtensionScopes[previous], scope); relation != "" {
				return renderError("scope_"+relation, "/extensionScopes/"+itoa(index), "extension scopes overlap")
			}
		}
	}
	if _, err := provenance.ParseDigest(request.FrontendSourceLockDigest); err != nil {
		return renderError("frontend_source_lock_digest_invalid", "/frontendSourceLockDigest", "frontend source lock digest is invalid")
	}
	var ir wireDocument
	if err := json.Unmarshal(request.FrontendIR, &ir); err != nil {
		return renderError("frontend_ir_invalid", "/frontendIR", "frontend IR is invalid")
	}
	source, _ := provenance.ParseDomainSource("renderer/frontend-api.json")
	if _, err := httpapi.ParseSnapshot(source, ir.API); err != nil {
		return renderError("api_snapshot_invalid", "/frontendIR/api", "embedded HTTP API snapshot is invalid")
	}
	if provenance.SHA256(ir.API).String() != ir.APIDigest {
		return renderError("api_digest_mismatch", "/frontendIR/apiDigest", "API digest does not match embedded API bytes")
	}
	sources, sourceIndex, failure := validateRendererSources(ir)
	if failure != nil {
		return failure
	}
	apiDigest, _ := provenance.ParseDigest(ir.APIDigest)
	sourceDigest, err := computeSourceDigest(apiDigest, sources)
	if err != nil || sourceDigest.String() != ir.SourceDigest {
		return renderError("source_digest_mismatch", "/frontendIR/sourceDigest", "source digest does not match the canonical source set")
	}
	var api rendererAPIIndex
	if err := json.Unmarshal(ir.API, &api); err != nil {
		return renderError("api_snapshot_invalid", "/frontendIR/api", "embedded HTTP API snapshot is invalid")
	}
	for _, item := range api.Sources {
		if digest, ok := sourceIndex[item.Ref]; !ok || digest != item.Digest {
			return renderError("api_source_missing", "/frontendIR/sources", "embedded API source is absent or has a different digest")
		}
	}
	for pageIndex, page := range ir.Pages {
		if _, ok := sourceIndex[page.SpecSourceRef]; !ok {
			return renderError("source_ref_unresolved", "/frontendIR/pages/"+itoa(pageIndex)+"/specSourceRef", "page source ref is absent from IR sources")
		}
	}
	for localeIndex, locale := range ir.Locales {
		if _, ok := sourceIndex[locale.SourceRef]; !ok {
			return renderError("source_ref_unresolved", "/frontendIR/locales/"+itoa(localeIndex)+"/sourceRef", "locale source ref is absent from IR sources")
		}
	}
	expectedSources := map[string]bool{}
	for _, item := range api.Sources {
		expectedSources[item.Ref] = true
	}
	for _, page := range ir.Pages {
		expectedSources[page.SpecSourceRef] = true
	}
	for _, locale := range ir.Locales {
		expectedSources[locale.SourceRef] = true
	}
	if len(expectedSources) != len(sourceIndex) {
		return renderError("source_union_mismatch", "/frontendIR/sources", "IR sources must equal the API, page, and locale source union")
	}
	for ref := range sourceIndex {
		if !expectedSources[ref] {
			return renderError("source_union_mismatch", "/frontendIR/sources", "IR sources contain an unexpected ref")
		}
	}
	if failure := validateRendererOrder(ir, request.ExtensionScopes); failure != nil {
		return failure
	}
	if failure := validateRendererPageGraphAndLocales(ir); failure != nil {
		return failure
	}
	return validateRendererSemantics(ir)
}

func validateRendererSources(ir wireDocument) ([]provenance.Source, map[string]string, error) {
	sources := make([]provenance.Source, len(ir.Sources))
	index := make(map[string]string, len(ir.Sources))
	previous := ""
	for position, item := range ir.Sources {
		ref, refErr := provenance.ParseSourceRef(item.Ref)
		digest, digestErr := provenance.ParseDigest(item.Digest)
		if refErr != nil || digestErr != nil {
			return nil, nil, renderError("source_invalid", "/frontendIR/sources/"+itoa(position), "IR source ref or digest is invalid")
		}
		if previous != "" && item.Ref <= previous {
			return nil, nil, renderError("source_order_invalid", "/frontendIR/sources", "IR sources must be unique and sorted by ref")
		}
		previous = item.Ref
		index[item.Ref] = item.Digest
		sources[position] = provenance.Source{Ref: ref, Digest: digest}
	}
	return sources, index, nil
}

func validateRendererOrder(ir wireDocument, extensionScopes []string) error {
	if !strictStrings(extensionScopes) {
		return renderError("extension_scope_order_invalid", "/extensionScopes", "extension scopes must be unique and sorted")
	}
	pageIDs := make([]string, len(ir.Pages))
	for index, page := range ir.Pages {
		pageIDs[index] = page.ID
		operationIDs := make([]string, len(page.Operations))
		for operation, item := range page.Operations {
			operationIDs[operation] = item.ID
			contexts := make([]string, len(item.ContextBindings))
			for binding, context := range item.ContextBindings {
				contexts[binding] = context.Context
			}
			if !strictStrings(contexts) {
				return renderError("ir_order_invalid", "/frontendIR/pages/"+itoa(index)+"/operations/"+itoa(operation)+"/contextBindings", "context bindings must be uniquely sorted")
			}
		}
		fieldIDs := make([]string, len(page.Fields))
		for field, item := range page.Fields {
			fieldIDs[field] = item.ID
			if !strictStrings(item.Surfaces) {
				return renderError("ir_order_invalid", "/frontendIR/pages/"+itoa(index)+"/fields/"+itoa(field)+"/surfaces", "field surfaces must be uniquely sorted")
			}
		}
		actionIDs := make([]string, len(page.Actions))
		for action, item := range page.Actions {
			actionIDs[action] = item.ID
			if !strictStrings(item.Fields) {
				return renderError("ir_order_invalid", "/frontendIR/pages/"+itoa(index)+"/actions/"+itoa(action)+"/fields", "action fields must be uniquely sorted")
			}
		}
		if !strictStrings(operationIDs) || !strictStrings(fieldIDs) || !strictStrings(actionIDs) || !strictStrings(page.ExtensionPoints) {
			return renderError("ir_order_invalid", "/frontendIR/pages/"+itoa(index), "page collections must be uniquely sorted")
		}
	}
	locales := make([]string, len(ir.Locales))
	for index, locale := range ir.Locales {
		locales[index] = locale.Locale
	}
	if !strictStrings(pageIDs) || !strictStrings(locales) {
		return renderError("ir_order_invalid", "/frontendIR", "pages and locales must be uniquely sorted")
	}
	return nil
}

func strictStrings(values []string) bool {
	return sort.StringsAreSorted(values) && !hasAdjacentDuplicate(values)
}

func hasAdjacentDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}

func validateRendererPageGraphAndLocales(ir wireDocument) error {
	routes, routeNames := map[string]bool{}, map[string]bool{}
	menus := map[string]*menuDocument{}
	menuOrders := map[string]bool{}
	labels := map[string]bool{}
	for pageIndex := range ir.Pages {
		page := &ir.Pages[pageIndex]
		if routes[page.Route.Path] || routeNames[page.Route.Name] {
			return renderError("route_duplicate", "/frontendIR/pages/"+itoa(pageIndex)+"/route", "route path or name is duplicated")
		}
		routes[page.Route.Path], routeNames[page.Route.Name] = true, true
		labels[page.TitleKey] = true
		if page.Menu != nil {
			if _, duplicate := menus[page.Menu.ID]; duplicate {
				return renderError("menu_id_duplicate", "/frontendIR/pages/"+itoa(pageIndex)+"/menu/id", "menu id is duplicated")
			}
			orderKey := page.Menu.ParentID + "\x00" + itoa(page.Menu.Order)
			if menuOrders[orderKey] {
				return renderError("menu_order_duplicate", "/frontendIR/pages/"+itoa(pageIndex)+"/menu/order", "menu order is duplicated under parent")
			}
			menuOrders[orderKey] = true
			menus[page.Menu.ID] = page.Menu
			labels[page.Menu.TitleKey] = true
		}
		for _, field := range page.Fields {
			labels[field.LabelKey] = true
			for _, choice := range field.Choices {
				labels[choice.LabelKey] = true
			}
			for _, column := range field.Columns {
				labels[column.LabelKey] = true
			}
		}
		for _, action := range page.Actions {
			labels[action.LabelKey] = true
			if action.ConfirmKey != "" {
				labels[action.ConfirmKey] = true
			}
		}
	}
	menuIDs := make([]string, 0, len(menus))
	for id := range menus {
		menuIDs = append(menuIDs, id)
	}
	sort.Strings(menuIDs)
	for _, id := range menuIDs {
		menu := menus[id]
		if menu.ParentID != "" {
			if menu.ParentID == id || menus[menu.ParentID] == nil {
				return renderError("menu_parent_invalid", "/frontendIR/pages", "menu parent is unresolved or self-referential")
			}
		}
	}
	for _, id := range menuIDs {
		menu := menus[id]
		if menu.ParentID != "" {
			seen := map[string]bool{id: true}
			for parent := menu.ParentID; parent != ""; {
				if seen[parent] {
					return renderError("menu_cycle", "/frontendIR/pages", "menu graph contains a cycle")
				}
				seen[parent] = true
				ancestor, ok := menus[parent]
				if !ok || ancestor == nil {
					return renderError("menu_parent_invalid", "/frontendIR/pages", "menu parent is unresolved or self-referential")
				}
				parent = ancestor.ParentID
			}
		}
	}
	if len(ir.Pages) != 0 && len(ir.Locales) == 0 {
		return renderError("locale_required", "/frontendIR/locales", "pages require a locale")
	}
	for key := range labels {
		parts := strings.Split(key, ".")
		for index := 1; index < len(parts); index++ {
			if labels[strings.Join(parts[:index], ".")] {
				return renderError("locale_message_prefix_collision", "/frontendIR/locales", "required locale keys collide by prefix")
			}
		}
	}
	for localeIndex, locale := range ir.Locales {
		for key := range labels {
			if strings.TrimSpace(locale.Messages[key]) == "" {
				return renderError("locale_message_missing", "/frontendIR/locales/"+itoa(localeIndex)+"/messages", "required locale message is missing")
			}
		}
	}
	return nil
}

func validateRendererSemantics(ir wireDocument) error {
	var api rendererAPIIndex
	if err := json.Unmarshal(ir.API, &api); err != nil {
		return renderError("api_snapshot_invalid", "/frontendIR/api", "embedded HTTP API snapshot is invalid")
	}
	types := map[string]rendererAPIType{}
	for _, candidate := range api.Types {
		types[candidate.Name] = candidate
	}
	operations := map[string]rendererAPIOperation{}
	for _, operation := range api.Operations {
		operations[operation.ID] = operation
	}
	globalContexts := map[string]string{}
	for pageIndex, page := range ir.Pages {
		base := "/frontendIR/pages/" + itoa(pageIndex)
		aliases := map[string]wireOperation{}
		roleCounts := map[string]int{}
		for operationIndex, operation := range page.Operations {
			apiOperation, ok := operations[operation.OperationID]
			if !ok || operation.RequestType != apiOperation.RequestType || operation.ResponseType != apiOperation.ResponseType || operation.Permission != apiOperation.Permission {
				return renderError("operation_projection_invalid", base+"/operations/"+itoa(operationIndex), "operation projection does not match embedded API")
			}
			if apiOperation.ResponseBody == string(generationapi.ResponseBodyJSON) {
				if failure := validateRendererResponseWireClosure(types, operation.ResponseType); failure != nil {
					return failure
				}
			}
			aliases[operation.ID] = operation
			roleCounts[operation.Role]++
			for contextIndex, context := range operation.ContextBindings {
				value, required, found := resolveRendererPath(types, operation.RequestType, context.Path)
				if !found || len(context.Path) != 1 || !required || !rendererContextScalar(value) || !equalRendererValue(value, context.ValueType) {
					return renderError("context_binding_type_invalid", base+"/operations/"+itoa(operationIndex)+"/contextBindings/"+itoa(contextIndex), "context binding does not match a required direct scalar")
				}
				typeID := rendererExactType(value)
				if previous, exists := globalContexts[context.Context]; exists && previous != typeID {
					return renderError("context_type_inconsistent", base+"/operations/"+itoa(operationIndex)+"/contextBindings/"+itoa(contextIndex), "context id changes exact type")
				}
				globalContexts[context.Context] = typeID
			}
			if failure := validateRendererResult(types, operation, base+"/operations/"+itoa(operationIndex)); failure != nil {
				return failure
			}
		}
		access, ok := aliases[page.AccessOperation]
		if !ok || page.Mode == "collection" && (access.Role != "list" || roleCounts["list"] != 1 || roleCounts["get"] != 0) || page.Mode == "singleton" && (access.Role != "get" || roleCounts["get"] != 1 || len(page.Operations) != 1 || len(page.Actions) != 0) {
			return renderError("access_operation_invalid", base+"/accessOperation", "page mode and access operation are inconsistent")
		}
		if page.AccessPermission != access.Permission {
			return renderError("access_permission_mismatch", base+"/accessPermission", "access permission does not match the access operation")
		}
		fieldsByID := map[string]wireField{}
		for _, field := range page.Fields {
			fieldsByID[field.ID] = field
		}
		actionsByOperation := map[string]actionDocument{}
		actionFields := map[string]bool{}
		for actionIndex, action := range page.Actions {
			operation, ok := aliases[action.Operation]
			if !ok || operation.Role != "action" {
				return renderError("action_operation_invalid", base+"/actions/"+itoa(actionIndex)+"/operation", "action must reference a role=action operation")
			}
			if _, duplicate := actionsByOperation[action.Operation]; duplicate {
				return renderError("action_operation_duplicate", base+"/actions/"+itoa(actionIndex)+"/operation", "action operation is referenced more than once")
			}
			if action.Effect == "create" && action.Placement != "toolbar" || action.Effect != "create" && action.Placement != "row" {
				return renderError("action_placement_invalid", base+"/actions/"+itoa(actionIndex)+"/placement", "action effect and placement are inconsistent")
			}
			for fieldIndex, fieldID := range action.Fields {
				field, exists := fieldsByID[fieldID]
				if !exists || field.Control == "" || !wireHasBinding(field, action.Operation, "request") {
					return renderError("action_field_binding_missing", base+"/actions/"+itoa(actionIndex)+"/fields/"+itoa(fieldIndex), "action field requires control and request binding")
				}
				actionFields[fieldID] = true
			}
			actionsByOperation[action.Operation] = action
		}
		for _, operation := range page.Operations {
			if operation.Role == "action" {
				if _, ok := actionsByOperation[operation.ID]; !ok {
					return renderError("action_operation_unreferenced", base+"/operations", "role=action operation is unreferenced")
				}
			}
		}
		pageBindingPaths := map[string]bool{}
		optionRefs := map[string]bool{}
		columnIDs := map[string]bool{}
		for fieldIndex, field := range page.Fields {
			fieldBase := base + "/fields/" + itoa(fieldIndex)
			if (field.Options != nil || len(field.Choices) != 0) && field.Control != "select" && field.Control != "multi-select" {
				return renderError("selection_control_required", fieldBase+"/control", "selection source requires select or multi-select")
			}
			if field.Options != nil && len(field.Choices) != 0 {
				return renderError("selection_source_conflict", fieldBase, "choices and options are mutually exclusive")
			}
			choiceValues := map[string]bool{}
			for choiceIndex, choice := range field.Choices {
				if choiceValues[choice.Value] {
					return renderError("choice_value_duplicate", fieldBase+"/choices/"+itoa(choiceIndex)+"/value", "choice value is duplicated")
				}
				choiceValues[choice.Value] = true
			}
			if field.Options != nil && !actionFields[field.ID] {
				return renderError("options_action_required", fieldBase+"/options", "dynamic options field is orphaned")
			}
			if field.Options != nil && len(field.Surfaces) != 0 {
				return renderError("options_surface_forbidden", fieldBase+"/surfaces", "dynamic options field cannot have display surfaces")
			}
			fieldBindingDirections := map[string]bool{}
			bindingType := ""
			for bindingIndex, binding := range field.Bindings {
				operation, ok := aliases[binding.Operation]
				if !ok {
					return renderError("binding_operation_unresolved", fieldBase+"/bindings/"+itoa(bindingIndex), "binding operation is unresolved")
				}
				if page.Mode == "singleton" && binding.Direction == "request" {
					return renderError("singleton_request_field_forbidden", fieldBase+"/bindings/"+itoa(bindingIndex), "singleton request fields must come only from context bindings")
				}
				fieldKey := binding.Operation + "\x00" + binding.Direction
				pathKey := fieldKey + "\x00" + strings.Join(binding.Path, "\x00")
				if fieldBindingDirections[fieldKey] || pageBindingPaths[pathKey] {
					return renderError("binding_path_duplicate", fieldBase+"/bindings/"+itoa(bindingIndex), "field binding is duplicated")
				}
				fieldBindingDirections[fieldKey], pageBindingPaths[pathKey] = true, true
				typeName := operation.RequestType
				if binding.Direction == "response" {
					if operation.Role != "list" && operation.Role != "get" {
						return renderError("response_binding_role_invalid", fieldBase+"/bindings/"+itoa(bindingIndex), "response binding role is invalid")
					}
					typeName = operation.ResponseType
				}
				allowedArray := -1
				if binding.Direction == "response" && operation.Role == "list" && operation.Result != nil {
					allowedArray = len(operation.Result.ItemsPath) - 1
				}
				value, required, ok := resolveRendererPathAllowed(types, typeName, binding.Path, allowedArray)
				if !ok || !rendererIRValue(value) || !equalRendererValue(value, binding.ValueType) || required != binding.Required {
					return renderError("binding_path_unresolved", fieldBase+"/bindings/"+itoa(bindingIndex), "binding does not match embedded API")
				}
				if binding.Direction == "request" && !rendererRequestLeaf(value) {
					return renderError("request_binding_path_invalid", fieldBase+"/bindings/"+itoa(bindingIndex), "request binding is not a direct scalar/scalar-array leaf")
				}
				exact := rendererExactType(value)
				if bindingType != "" && bindingType != exact {
					return renderError("binding_type_inconsistent", fieldBase+"/bindings/"+itoa(bindingIndex), "field bindings change exact type")
				}
				bindingType = exact
				if binding.Direction == "request" && len(binding.Path) != 1 || binding.Direction == "response" && !rendererBindingWithinResult(binding.Path, operation) {
					return renderError("binding_scope_invalid", fieldBase+"/bindings/"+itoa(bindingIndex), "binding is outside the operation contract scope")
				}
				if !rendererControlCompatible(field.Control, value) {
					return renderError("control_binding_type_invalid", fieldBase+"/bindings/"+itoa(bindingIndex), "control is incompatible with binding type")
				}
			}
			for surfaceIndex, surface := range field.Surfaces {
				if surface == "search" && (field.Control == "" || !wireHasBinding(field, page.AccessOperation, "request")) || surface == "list" && !wireHasBinding(field, page.AccessOperation, "response") || surface == "detail" && (page.Mode != "singleton" || !wireHasBinding(field, page.AccessOperation, "response")) {
					return renderError("surface_binding_invalid", fieldBase+"/surfaces/"+itoa(surfaceIndex), "field surface lacks its required binding")
				}
			}
			if wireHasBinding(field, page.AccessOperation, "request") && !contains(field.Surfaces, "search") {
				return renderError("binding_surface_mismatch", fieldBase+"/bindings", "primary request binding lacks search surface")
			}
			if field.Options != nil {
				operation, ok := aliases[field.Options.Operation]
				if !ok || operation.Role != "options" || field.Control != "select" && field.Control != "multi-select" {
					return renderError("options_operation_invalid", fieldBase+"/options", "field options projection is invalid")
				}
				for _, path := range [][]string{field.Options.ValuePath, field.Options.LabelPath} {
					combined := append(append([]string(nil), operation.Result.ItemsPath...), path...)
					value, _, found := resolveRendererPathAllowed(types, operation.ResponseType, combined, len(operation.Result.ItemsPath)-1)
					if !found || value.Kind != "scalar" || value.Name != "string" {
						return renderError("options_value_type_invalid", fieldBase+"/options", "option value and label paths must resolve to string")
					}
				}
				optionRefs[field.Options.Operation] = true
			}
			for _, column := range field.Columns {
				if columnIDs[column.ID] {
					return renderError("column_id_duplicate", fieldBase+"/columns", "column id is duplicated in the page")
				}
				columnIDs[column.ID] = true
				if len(field.Bindings) != 1 || field.Control != "" {
					return renderError("column_path_invalid", fieldBase+"/columns", "column source is invalid")
				}
				binding := field.Bindings[0]
				operation := aliases[binding.Operation]
				if binding.Direction != "response" || binding.Operation != page.AccessOperation {
					return renderError("column_path_invalid", fieldBase+"/columns", "column source is not the access response")
				}
				array, _, ok := resolveRendererPath(types, operation.ResponseType, binding.Path)
				if !ok || array.Kind != "array" || array.Element == nil || array.Element.Kind != "ref" && array.Element.Kind != "object" {
					return renderError("column_path_invalid", fieldBase+"/columns", "column source is invalid")
				}
				combined := append(append([]string(nil), binding.Path...), column.Path...)
				allowedArrays := map[int]bool{len(operation.Result.ItemsPath) - 1: true, len(binding.Path) - 1: true}
				value, required, ok := resolveRendererPathAllowedArrays(types, operation.ResponseType, combined, allowedArrays, false)
				if !ok || !rendererColumnTerminal(value) || !equalRendererValue(value, column.ValueType) || column.Required != (binding.Required && required) {
					return renderError("column_path_invalid", fieldBase+"/columns", "column path does not match embedded API")
				}
			}
			if len(field.Surfaces) > 0 && len(field.Columns) == 0 {
				for _, binding := range field.Bindings {
					operation := aliases[binding.Operation]
					value, _, ok := resolveRendererPath(types, operation.ResponseType, binding.Path)
					if binding.Operation == page.AccessOperation && binding.Direction == "response" && ok && value.Kind == "array" && value.Element != nil && (value.Element.Kind == "ref" || value.Element.Kind == "object") {
						return renderError("columns_required", fieldBase+"/columns", "displayed object array requires columns")
					}
				}
			}
			used := len(field.Surfaces) > 0 || actionFields[field.ID]
			for _, action := range page.Actions {
				if wireHasBinding(field, action.Operation, "request") {
					used = true
				}
			}
			if !used {
				return renderError("field_unused", fieldBase, "field is not consumed by a surface or action")
			}
		}
		for _, operation := range page.Operations {
			if operation.Role == "options" && !optionRefs[operation.ID] {
				return renderError("options_operation_unreferenced", base+"/operations", "role=options operation is unreferenced")
			}
			if failure := validateRendererRequestClosure(types, page, operation, actionsByOperation); failure != nil {
				return failure
			}
		}
	}
	return nil
}

func validateRendererResult(types map[string]rendererAPIType, operation wireOperation, pointer string) error {
	if operation.Role == "list" || operation.Role == "options" {
		if operation.Result == nil || operation.Pagination == nil || len(operation.Result.ItemsPath) == 0 || len(operation.Result.TotalPath) == 0 || operation.Role == "list" && len(operation.Result.RowKeyPath) == 0 {
			return renderError("collection_result_invalid", pointer+"/result", "list/options result and pagination projection is incomplete")
		}
		if operation.Role == "options" && len(operation.Result.RowKeyPath) != 0 {
			return renderError("row_key_forbidden", pointer+"/result/rowKeyPath", "options result cannot project row key")
		}
	} else if operation.Role == "get" {
		if operation.Pagination != nil || operation.Result != nil && (len(operation.Result.ItemsPath) != 0 || len(operation.Result.TotalPath) != 0 || len(operation.Result.RowKeyPath) != 0) {
			return renderError("get_result_invalid", pointer+"/result", "get may only project itemPath")
		}
	} else if operation.Role == "action" && (operation.Result != nil || operation.Pagination != nil) {
		return renderError("action_result_invalid", pointer, "action cannot project result or pagination")
	}
	if operation.Result == nil {
		return nil
	}
	result := operation.Result
	if len(result.ItemsPath) > 0 {
		value, _, ok := resolveRendererPathAllowed(types, operation.ResponseType, result.ItemsPath, -1)
		if !ok || value.Kind != "array" {
			return renderError("result_items_type_invalid", pointer+"/result/itemsPath", "items path must resolve to array")
		}
	}
	if len(result.ItemPath) > 0 {
		value, _, ok := resolveRendererPathAllowed(types, operation.ResponseType, result.ItemPath, -1)
		if !ok || value.Kind != "object" && value.Kind != "ref" {
			return renderError("result_item_type_invalid", pointer+"/result/itemPath", "item path must resolve to object/ref")
		}
	}
	if len(result.TotalPath) > 0 {
		value, _, ok := resolveRendererPathAllowed(types, operation.ResponseType, result.TotalPath, -1)
		if !ok || !rendererInteger(value) {
			return renderError("result_total_type_invalid", pointer+"/result/totalPath", "total path must resolve to integer")
		}
	}
	if len(result.RowKeyPath) > 0 {
		value, required, ok := resolveRendererPathAllowed(types, operation.ResponseType, append(append([]string(nil), result.ItemsPath...), result.RowKeyPath...), len(result.ItemsPath)-1)
		if !ok || !required || !rendererRowKey(value) {
			return renderError("row_key_type_invalid", pointer+"/result/rowKeyPath", "row key must be required string/integer")
		}
	}
	if operation.Pagination != nil {
		pagination := operation.Pagination
		if equalPath(pagination.LimitPath, pagination.OffsetPath) || !equalPath(pagination.TotalPath, result.TotalPath) || pagination.PageSize < 1 || pagination.PageSize > 200 {
			return renderError("pagination_projection_invalid", pointer+"/pagination", "pagination paths, total, or page size are invalid")
		}
		for _, path := range [][]string{pagination.LimitPath, pagination.OffsetPath} {
			value, _, ok := resolveRendererPathAllowed(types, operation.RequestType, path, -1)
			if !ok || len(path) != 1 || !rendererInteger(value) {
				return renderError("pagination_request_type_invalid", pointer+"/pagination", "pagination request path must be direct integer")
			}
		}
		value, _, ok := resolveRendererPathAllowed(types, operation.ResponseType, pagination.TotalPath, -1)
		if !ok || !rendererInteger(value) {
			return renderError("pagination_total_type_invalid", pointer+"/pagination/totalPath", "pagination total must be integer")
		}
	}
	return nil
}

func validateRendererRequestClosure(types map[string]rendererAPIType, page wirePage, operation wireOperation, actions map[string]actionDocument) error {
	covered := map[string]bool{}
	add := func(path []string) bool {
		key := strings.Join(path, "\x00")
		if covered[key] {
			return false
		}
		covered[key] = true
		return true
	}
	for _, context := range operation.ContextBindings {
		if !add(context.Path) {
			return renderError("request_binding_conflict", "/frontendIR/pages", "request path is covered more than once")
		}
	}
	if operation.Pagination != nil && (!add(operation.Pagination.LimitPath) || !add(operation.Pagination.OffsetPath)) {
		return renderError("request_binding_conflict", "/frontendIR/pages", "pagination request path is covered more than once")
	}
	for _, field := range page.Fields {
		for _, binding := range field.Bindings {
			if binding.Operation != operation.ID || binding.Direction != "request" {
				continue
			}
			if !add(binding.Path) {
				return renderError("request_binding_conflict", "/frontendIR/pages", "field request path is covered more than once")
			}
			if operation.Role == "action" {
				action := actions[operation.ID]
				controlled := contains(action.Fields, field.ID)
				if controlled && field.Control == "" {
					return renderError("controlled_field_control_missing", "/frontendIR/pages", "controlled action field lacks control")
				}
				if !controlled {
					if action.Effect == "create" {
						return renderError("create_row_source_forbidden", "/frontendIR/pages", "create action cannot use row source")
					}
					source, ok := wireBindingFor(field, page.AccessOperation, "response")
					if !ok || !source.Required && binding.Required {
						return renderError("row_source_invalid", "/frontendIR/pages", "row source is missing or too optional")
					}
				}
			}
		}
	}
	typeDef, ok := types[operation.RequestType]
	if !ok {
		return renderError("request_type_unresolved", "/frontendIR/pages", "operation request type is unresolved")
	}
	for _, field := range typeDef.Fields {
		if len(field.Path) == 1 && !rendererRequestLeaf(field.ValueType) {
			return renderError("request_leaf_type_invalid", "/frontendIR/pages", "request DTO contains an unsupported direct leaf")
		}
		if len(field.Path) == 1 && field.Required && field.ValueType.Kind != "optional" && !covered[field.Path[0]] {
			return renderError("required_request_binding_missing", "/frontendIR/pages", "required request field is uncovered")
		}
	}
	return nil
}

func wireHasBinding(field wireField, operation, direction string) bool {
	_, ok := wireBindingFor(field, operation, direction)
	return ok
}

func wireBindingFor(field wireField, operation, direction string) (wireBinding, bool) {
	for _, binding := range field.Bindings {
		if binding.Operation == operation && binding.Direction == direction {
			return binding, true
		}
	}
	return wireBinding{}, false
}

func rendererBindingWithinResult(path []string, operation wireOperation) bool {
	if operation.Result == nil {
		return operation.Role == "get"
	}
	if operation.Role == "list" {
		return len(path) > len(operation.Result.ItemsPath) && hasPrefix(path, operation.Result.ItemsPath)
	}
	if operation.Role == "get" {
		return len(operation.Result.ItemPath) == 0 || len(path) > len(operation.Result.ItemPath) && hasPrefix(path, operation.Result.ItemPath)
	}
	return false
}

func validateRendererResponseWireClosure(types map[string]rendererAPIType, rootType string) error {
	typeIndexes := make(map[string]int, len(types))
	ordered := make([]string, 0, len(types))
	for name := range types {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for index, name := range ordered {
		typeIndexes[name] = index
	}
	visited := map[string]bool{}
	var validateObject func(string, []string) error
	validateObject = func(typeName string, prefix []string) error {
		key := typeName + "\x00" + strings.Join(prefix, "\x00")
		if visited[key] {
			return nil
		}
		visited[key] = true
		typeValue, ok := types[typeName]
		if !ok {
			return renderError("response_wire_type_unresolved", "/frontendIR/api/types", "JSON response type closure contains an unresolved type")
		}
		wireNames := map[string]bool{}
		for fieldIndex, field := range typeValue.Fields {
			if len(field.Path) != len(prefix)+1 || !equalPathPrefix(field.Path, prefix) {
				continue
			}
			fieldPointer := "/frontendIR/api/types/" + itoa(typeIndexes[typeName]) + "/fields/" + itoa(fieldIndex)
			if field.Binding == nil {
				return renderError("response_wire_binding_missing", fieldPointer+"/binding", "JSON response object field requires an explicit body binding")
			}
			if field.Binding.Location != string(generationapi.RequestBindingBody) {
				return renderError("response_wire_binding_location_invalid", fieldPointer+"/binding/in", "JSON response object field binding must use body location")
			}
			if wireNames[field.Binding.Name] {
				return renderError("response_wire_name_duplicate", fieldPointer+"/binding/name", "JSON response object sibling wire name is duplicated")
			}
			wireNames[field.Binding.Name] = true
			value := rendererUnwrapResponseContainer(field.ValueType)
			switch value.Kind {
			case "object":
				if failure := validateObject(typeName, field.Path); failure != nil {
					return failure
				}
			case "ref":
				if failure := validateObject(value.Name, nil); failure != nil {
					return failure
				}
			}
		}
		return nil
	}
	return validateObject(rootType, nil)
}

func rendererUnwrapResponseContainer(value rendererAPIValue) rendererAPIValue {
	for value.Kind == "optional" || value.Kind == "array" {
		if value.Element == nil {
			return rendererAPIValue{}
		}
		value = *value.Element
	}
	return value
}

func equalPathPrefix(path, prefix []string) bool {
	for index := range prefix {
		if path[index] != prefix[index] {
			return false
		}
	}
	return true
}

func rendererContextScalar(value rendererAPIValue) bool {
	return value.Kind == "scalar" && (value.Name == "string" || integerScalars[value.Name])
}

func rendererInteger(value rendererAPIValue) bool {
	value = rendererUnwrap(value)
	return value.Kind == "scalar" && integerScalars[value.Name]
}

func rendererRowKey(value rendererAPIValue) bool {
	value = rendererUnwrap(value)
	return value.Kind == "scalar" && (value.Name == "string" || integerScalars[value.Name])
}

func rendererControlCompatible(control string, value rendererAPIValue) bool {
	value = rendererUnwrap(value)
	if control == "" {
		return true
	}
	switch control {
	case "toggle":
		return value.Kind == "scalar" && value.Name == "bool"
	case "select", "text", "password", "textarea":
		return value.Kind == "scalar" && value.Name == "string"
	case "number":
		return value.Kind == "scalar" && numericScalars[value.Name]
	case "multi-select":
		return value.Kind == "array" && value.Element != nil && rendererUnwrap(*value.Element).Kind == "scalar" && rendererUnwrap(*value.Element).Name == "string"
	default:
		return false
	}
}

func rendererColumnTerminal(value rendererAPIValue) bool {
	value = rendererUnwrap(value)
	if value.Kind == "scalar" {
		return true
	}
	return value.Kind == "array" && value.Element != nil && value.Element.Kind != "optional" && rendererUnwrap(*value.Element).Kind == "scalar"
}

func rendererRequestLeaf(value rendererAPIValue) bool {
	value = rendererUnwrap(value)
	if value.Kind == "scalar" {
		return true
	}
	return value.Kind == "array" && value.Element != nil && rendererUnwrap(*value.Element).Kind == "scalar"
}

func rendererIRValue(value rendererAPIValue) bool {
	value = rendererUnwrap(value)
	if value.Kind == "scalar" || value.Kind == "ref" || value.Kind == "object" {
		return true
	}
	return value.Kind == "array" && value.Element != nil && value.Element.Kind != "optional" && rendererIRValue(*value.Element)
}

func rendererExactType(value rendererAPIValue) string {
	value = rendererUnwrap(value)
	if value.Kind == "array" && value.Element != nil {
		return "array:" + rendererExactType(*value.Element)
	}
	return value.Kind + ":" + value.Name
}

func resolveRendererPath(types map[string]rendererAPIType, typeName string, path []string) (rendererAPIValue, bool, bool) {
	return resolveRendererPathAllowed(types, typeName, path, -2)
}

func resolveRendererPathAllowed(types map[string]rendererAPIType, typeName string, path []string, allowedArray int) (rendererAPIValue, bool, bool) {
	allowed := map[int]bool{}
	allowAny := allowedArray == -2
	if allowedArray >= 0 {
		allowed[allowedArray] = true
	}
	return resolveRendererPathAllowedArrays(types, typeName, path, allowed, allowAny)
}

func resolveRendererPathAllowedArrays(types map[string]rendererAPIType, typeName string, path []string, allowedArrays map[int]bool, allowAny bool) (rendererAPIValue, bool, bool) {
	required := true
	prefix := []string{}
	for index, segment := range path {
		typeDef, ok := types[typeName]
		if !ok {
			return rendererAPIValue{}, false, false
		}
		prefix = append(prefix, segment)
		var field rendererAPIField
		found := false
		for _, candidate := range typeDef.Fields {
			if equalPath(candidate.Path, prefix) {
				field, found = candidate, true
				break
			}
		}
		if !found {
			return rendererAPIValue{}, false, false
		}
		required = required && field.Required && field.ValueType.Kind != "optional"
		value := rendererUnwrap(field.ValueType)
		if index == len(path)-1 {
			return value, required, true
		}
		if value.Kind == "array" && value.Element != nil {
			if !allowAny && !allowedArrays[index] {
				return rendererAPIValue{}, false, false
			}
			element := *value.Element
			required = required && element.Kind != "optional"
			value = rendererUnwrap(element)
		}
		if value.Kind == "ref" {
			typeName = value.Name
			prefix = nil
			continue
		}
		if value.Kind != "object" {
			return rendererAPIValue{}, false, false
		}
	}
	return rendererAPIValue{}, false, false
}

func rendererUnwrap(value rendererAPIValue) rendererAPIValue {
	for value.Kind == "optional" && value.Element != nil {
		value = *value.Element
	}
	return value
}

func equalRendererValue(api rendererAPIValue, projected wireValueType) bool {
	api = rendererUnwrap(api)
	if api.Kind != projected.Kind || api.Name != projected.Name {
		return false
	}
	if api.Kind != "array" {
		return true
	}
	return api.Element != nil && projected.Element != nil && equalRendererValue(*api.Element, *projected.Element)
}
