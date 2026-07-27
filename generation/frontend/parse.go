package frontend

import (
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

const messageKeyExpression = `^[a-z][A-Za-z0-9]*(?:[.-][A-Za-z0-9]+)*$`

var messageKeyPattern = regexp.MustCompile(messageKeyExpression)

func ParsePageSpec(source string, data []byte) (PageSpec, error) {
	domainSource, err := provenance.ParseDomainSource(source)
	if err != nil {
		return PageSpec{}, pageError("source_invalid", source, "", "frontend page spec source must be a canonical repository-relative path")
	}
	sourceRef, err := provenance.RepositoryRef(domainSource.String(), "")
	if err != nil {
		return PageSpec{}, pageError("source_invalid", source, "", "frontend page spec source must be a canonical repository-relative path")
	}
	var strict strictdoc.Document
	switch extension := strings.ToLower(path.Ext(source)); extension {
	case ".json":
		strict, err = strictdoc.ParseJSON(source, data)
	case ".yaml", ".yml":
		strict, err = strictdoc.ParseYAML(source, data)
	default:
		return PageSpec{}, pageError("format_unsupported", source, "", "frontend page spec must use .json, .yaml, or .yml")
	}
	if err != nil {
		return PageSpec{}, projectDocumentError(source, err)
	}
	var normalized any
	if err := json.Unmarshal(strict.JSON(), &normalized); err != nil {
		return PageSpec{}, pageError("document_invalid", source, "", "frontend page spec document is invalid")
	}
	if fields, ok := normalized.(map[string]any)["fields"].([]any); ok {
		for index, raw := range fields {
			field, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			_, hasOptions := field["options"]
			_, hasChoices := field["choices"]
			if hasOptions && hasChoices {
				return PageSpec{}, pageError("selection_source_conflict", source, "/fields/"+strconv.Itoa(index), "choices and options are mutually exclusive")
			}
		}
	}
	if err := validatePageSchema(normalized); err != nil {
		failures := schemaFailures(source, err)
		if len(failures) > 0 {
			return PageSpec{}, withLocation(strict, failures[0])
		}
	}
	var document pageDocument
	if err := strict.DecodeExact(&document); err != nil {
		return PageSpec{}, projectDocumentError(source, err)
	}
	if failure := validatePageDocument(source, document); failure != nil {
		return PageSpec{}, withLocation(strict, failure)
	}
	canonical, err := jcs.Transform(strict.JSON())
	if err != nil {
		return PageSpec{}, pageError("canonical_invalid", source, "", "frontend page spec cannot be canonicalized")
	}
	return PageSpec{state: &pageSpecState{document: clonePageDocument(document), sourceRef: sourceRef, digest: provenance.SHA256(canonical)}}, nil
}

func ParseLocale(source string, data []byte) (Locale, error) {
	domainSource, err := provenance.ParseDomainSource(source)
	if err != nil {
		return Locale{}, localeError("source_invalid", source, "", "frontend locale source must be a canonical repository-relative path")
	}
	sourceRef, err := provenance.RepositoryRef(domainSource.String(), "")
	if err != nil {
		return Locale{}, localeError("source_invalid", source, "", "frontend locale source must be a canonical repository-relative path")
	}
	var strict strictdoc.Document
	switch strings.ToLower(path.Ext(source)) {
	case ".json":
		strict, err = strictdoc.ParseJSON(source, data)
	case ".yaml", ".yml":
		strict, err = strictdoc.ParseYAML(source, data)
	default:
		return Locale{}, localeError("format_unsupported", source, "", "frontend locale must use .json, .yaml, or .yml")
	}
	if err != nil {
		return Locale{}, projectLocaleDocumentError(source, err)
	}
	var normalized any
	if err := json.Unmarshal(strict.JSON(), &normalized); err != nil {
		return Locale{}, localeError("document_invalid", source, "", "frontend locale document is invalid")
	}
	if err := validateLocaleSchema(normalized); err != nil {
		failures := localeSchemaFailures(source, err)
		if len(failures) > 0 {
			return Locale{}, withLocation(strict, failures[0])
		}
	}
	var document localeDocument
	if err := strict.DecodeExact(&document); err != nil {
		return Locale{}, projectLocaleDocumentError(source, err)
	}
	for key := range document.Messages {
		if !messageKeyPattern.MatchString(key) {
			return Locale{}, withLocation(strict, localeError("message_key_invalid", source, "/messages/"+escapePointer(key), "frontend locale message key is invalid"))
		}
	}
	canonical, err := jcs.Transform(strict.JSON())
	if err != nil {
		return Locale{}, localeError("canonical_invalid", source, "", "frontend locale cannot be canonicalized")
	}
	return Locale{state: &localeState{document: cloneLocaleDocument(document), sourceRef: sourceRef, digest: provenance.SHA256(canonical)}}, nil
}

func validatePageDocument(source string, document pageDocument) *Error {
	keys := []struct{ value, pointer string }{{document.TitleKey, "/titleKey"}}
	if document.Menu != nil {
		keys = append(keys, struct{ value, pointer string }{document.Menu.TitleKey, "/menu/titleKey"})
	}
	for index, field := range document.Fields {
		keys = append(keys, struct{ value, pointer string }{field.LabelKey, "/fields/" + strconv.Itoa(index) + "/labelKey"})
		for choiceIndex, choice := range field.Choices {
			keys = append(keys, struct{ value, pointer string }{choice.LabelKey, "/fields/" + strconv.Itoa(index) + "/choices/" + strconv.Itoa(choiceIndex) + "/labelKey"})
		}
		for columnIndex, column := range field.Columns {
			keys = append(keys, struct{ value, pointer string }{column.LabelKey, "/fields/" + strconv.Itoa(index) + "/columns/" + strconv.Itoa(columnIndex) + "/labelKey"})
		}
	}
	for index, action := range document.Actions {
		keys = append(keys, struct{ value, pointer string }{action.LabelKey, "/actions/" + strconv.Itoa(index) + "/labelKey"})
		if action.ConfirmKey != "" {
			keys = append(keys, struct{ value, pointer string }{action.ConfirmKey, "/actions/" + strconv.Itoa(index) + "/confirmKey"})
		}
	}
	for _, key := range keys {
		if !messageKeyPattern.MatchString(key.value) {
			return pageError("message_key_invalid", source, key.pointer, "frontend message key is invalid")
		}
	}
	seenOperations := map[string]int{}
	seenRoles := map[string]int{}
	for index, operation := range document.Operations {
		base := "/operations/" + strconv.Itoa(index)
		if _, ok := seenOperations[operation.ID]; ok {
			return pageError("operation_id_duplicate", source, base+"/id", "frontend operation alias is duplicated")
		}
		seenOperations[operation.ID] = index
		if operation.Role == "list" || operation.Role == "get" {
			if _, ok := seenRoles[operation.Role]; ok {
				return pageError("operation_role_duplicate", source, base+"/role", "standard operation role is duplicated")
			}
			seenRoles[operation.Role] = index
		}
		seenContexts := map[string]struct{}{}
		for bindingIndex, binding := range operation.ContextBindings {
			if _, ok := seenContexts[binding.Context]; ok {
				return pageError("context_binding_duplicate", source, base+"/contextBindings/"+strconv.Itoa(bindingIndex)+"/context", "operation context binding is duplicated")
			}
			seenContexts[binding.Context] = struct{}{}
		}
	}
	seenFields := map[string]struct{}{}
	seenColumns := map[string]struct{}{}
	for fieldIndex, field := range document.Fields {
		base := "/fields/" + strconv.Itoa(fieldIndex)
		if _, ok := seenFields[field.ID]; ok {
			return pageError("field_id_duplicate", source, base+"/id", "frontend field alias is duplicated")
		}
		seenFields[field.ID] = struct{}{}
		seenSurfaces := map[string]struct{}{}
		for index, surface := range field.Surfaces {
			if _, ok := seenSurfaces[surface]; ok {
				return pageError("surface_duplicate", source, base+"/surfaces/"+strconv.Itoa(index), "field surface is duplicated")
			}
			seenSurfaces[surface] = struct{}{}
		}
		seenBindings := map[string]struct{}{}
		for index, binding := range field.Bindings {
			key := binding.Operation + "\x00" + binding.Direction
			if _, ok := seenBindings[key]; ok {
				return pageError("field_binding_duplicate", source, base+"/bindings/"+strconv.Itoa(index), "field binding is duplicated")
			}
			seenBindings[key] = struct{}{}
		}
		seenChoices := map[string]struct{}{}
		for index, choice := range field.Choices {
			if _, ok := seenChoices[choice.Value]; ok {
				return pageError("choice_value_duplicate", source, base+"/choices/"+strconv.Itoa(index)+"/value", "choice value is duplicated")
			}
			seenChoices[choice.Value] = struct{}{}
		}
		for index, column := range field.Columns {
			if _, ok := seenColumns[column.ID]; ok {
				return pageError("column_id_duplicate", source, base+"/columns/"+strconv.Itoa(index)+"/id", "column id is duplicated in the page")
			}
			seenColumns[column.ID] = struct{}{}
		}
	}
	seenActions := map[string]struct{}{}
	for index, action := range document.Actions {
		if _, ok := seenActions[action.ID]; ok {
			return pageError("action_id_duplicate", source, "/actions/"+strconv.Itoa(index)+"/id", "frontend action alias is duplicated")
		}
		seenActions[action.ID] = struct{}{}
		seenActionFields := map[string]struct{}{}
		for fieldIndex, field := range action.Fields {
			if _, ok := seenActionFields[field]; ok {
				return pageError("action_field_duplicate", source, "/actions/"+strconv.Itoa(index)+"/fields/"+strconv.Itoa(fieldIndex), "action field is duplicated")
			}
			seenActionFields[field] = struct{}{}
		}
	}
	seenExtensions := map[string]struct{}{}
	for index, extension := range document.ExtensionPoints {
		if _, ok := seenExtensions[extension]; ok {
			return pageError("extension_point_duplicate", source, "/extensionPoints/"+strconv.Itoa(index), "extension point is duplicated")
		}
		seenExtensions[extension] = struct{}{}
	}
	return nil
}

func projectDocumentError(source string, err error) *Error {
	var strict *strictdoc.Error
	if !errors.As(err, &strict) {
		return pageError("document_invalid", source, "", "frontend page spec document is invalid")
	}
	result := pageError(strict.Code, strict.Source, strict.Pointer, "frontend page spec document is invalid")
	result.line, result.column = strict.Line, strict.Column
	return result
}

func projectLocaleDocumentError(source string, err error) *Error {
	var strict *strictdoc.Error
	if !errors.As(err, &strict) {
		return localeError("document_invalid", source, "", "frontend locale document is invalid")
	}
	result := localeError(strict.Code, strict.Source, strict.Pointer, "frontend locale document is invalid")
	result.line, result.column = strict.Line, strict.Column
	return result
}

func withLocation(document strictdoc.Document, err *Error) *Error {
	if err == nil {
		return nil
	}
	if line, column, ok := document.Location(err.pointer); ok {
		err.line, err.column = line, column
	}
	return err
}

func clonePageDocument(input pageDocument) pageDocument {
	result := input
	if input.Menu != nil {
		value := *input.Menu
		result.Menu = &value
	}
	result.Operations = make([]operationDocument, len(input.Operations))
	copy(result.Operations, input.Operations)
	for index := range result.Operations {
		if input.Operations[index].Result != nil {
			value := *input.Operations[index].Result
			value.ItemsPath = append([]string(nil), value.ItemsPath...)
			value.ItemPath = append([]string(nil), value.ItemPath...)
			value.TotalPath = append([]string(nil), value.TotalPath...)
			value.RowKeyPath = append([]string(nil), value.RowKeyPath...)
			result.Operations[index].Result = &value
		}
		result.Operations[index].ContextBindings = append([]contextBindingDocument(nil), input.Operations[index].ContextBindings...)
		for binding := range result.Operations[index].ContextBindings {
			result.Operations[index].ContextBindings[binding].Path = append([]string(nil), input.Operations[index].ContextBindings[binding].Path...)
		}
		if input.Operations[index].Pagination != nil {
			value := clonePagination(input.Operations[index].Pagination)
			result.Operations[index].Pagination = &value
		}
	}
	result.Fields = make([]fieldDocument, len(input.Fields))
	copy(result.Fields, input.Fields)
	for index := range result.Fields {
		result.Fields[index].Surfaces = make([]string, len(input.Fields[index].Surfaces))
		copy(result.Fields[index].Surfaces, input.Fields[index].Surfaces)
		result.Fields[index].Bindings = make([]bindingDocument, len(input.Fields[index].Bindings))
		copy(result.Fields[index].Bindings, input.Fields[index].Bindings)
		for binding := range result.Fields[index].Bindings {
			result.Fields[index].Bindings[binding].Path = append([]string(nil), input.Fields[index].Bindings[binding].Path...)
		}
		if input.Fields[index].Options != nil {
			value := *input.Fields[index].Options
			value.ValuePath = append([]string(nil), value.ValuePath...)
			value.LabelPath = append([]string(nil), value.LabelPath...)
			result.Fields[index].Options = &value
		}
		result.Fields[index].Choices = append([]choiceDocument(nil), input.Fields[index].Choices...)
		result.Fields[index].Columns = append([]columnDocument(nil), input.Fields[index].Columns...)
		for column := range result.Fields[index].Columns {
			result.Fields[index].Columns[column].Path = append([]string(nil), input.Fields[index].Columns[column].Path...)
		}
	}
	result.Actions = make([]actionDocument, len(input.Actions))
	copy(result.Actions, input.Actions)
	for index := range result.Actions {
		result.Actions[index].Fields = append([]string(nil), input.Actions[index].Fields...)
	}
	result.ExtensionPoints = make([]string, len(input.ExtensionPoints))
	copy(result.ExtensionPoints, input.ExtensionPoints)
	return result
}

func cloneLocaleDocument(input localeDocument) localeDocument {
	result := input
	result.Messages = make(map[string]string, len(input.Messages))
	for key, value := range input.Messages {
		result.Messages[key] = value
	}
	return result
}

func clonePagination(input *paginationDocument) paginationDocument {
	result := *input
	result.LimitPath = append([]string(nil), input.LimitPath...)
	result.OffsetPath = append([]string(nil), input.OffsetPath...)
	result.TotalPath = append([]string(nil), input.TotalPath...)
	return result
}

func sortedStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	sort.Strings(result)
	return result
}
