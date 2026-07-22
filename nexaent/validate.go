package nexaent

import (
	_ "embed"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed ent-schema-meta-v1.schema.json
var embeddedSchemaAnnotationSchema string

//go:embed ent-field-meta-v1.schema.json
var embeddedFieldAnnotationSchema string

//go:embed ent-crud-v1.schema.json
var embeddedCRUDAnnotationSchema string

// SchemaAnnotationSchema returns a fresh copy of the schema annotation JSON Schema.
func SchemaAnnotationSchema() []byte { return []byte(embeddedSchemaAnnotationSchema) }

// FieldAnnotationSchema returns a fresh copy of the field annotation JSON Schema.
func FieldAnnotationSchema() []byte { return []byte(embeddedFieldAnnotationSchema) }

// CRUDAnnotationSchema returns a fresh copy of the CRUD annotation JSON Schema.
func CRUDAnnotationSchema() []byte { return []byte(embeddedCRUDAnnotationSchema) }

func validateSchemaMeta(owner annotationOwner, meta SchemaMeta) *Error {
	if err := validateLocalizedText(owner, meta.Label, "/payload/label"); err != nil {
		return err
	}
	if err := validateLocalizedText(owner, meta.Description, "/payload/description"); err != nil {
		return err
	}
	if err := validateUnicode(owner, string(meta.Identity), "/payload/identity"); err != nil {
		return err
	}
	if meta.Identity != IdentityEntID {
		return invalidError(owner, "enum_invalid", "/payload/identity")
	}
	if err := validateUnicode(owner, string(meta.Scope), "/payload/scope"); err != nil {
		return err
	}
	if meta.Scope != ScopeGlobal && meta.Scope != ScopeTenant {
		return invalidError(owner, "enum_invalid", "/payload/scope")
	}
	return nil
}

type fieldSemanticInput struct {
	label            LocalizedText
	description      LocalizedText
	uiHint           UIHint
	physicalDisplay  *PhysicalDisplay
	logicalReference *LogicalReference
	visibility       FieldVisibility
	crud             *CRUDFieldPolicy
}

func normalizeFieldMeta(owner annotationOwner, meta FieldMeta) (FieldMeta, *Error) {
	input := fieldSemanticInput{
		label: meta.Label, description: meta.Description, uiHint: meta.UIHint,
		visibility: meta.Visibility,
	}
	if meta.PhysicalDisplay != nil {
		physical := *meta.PhysicalDisplay
		input.physicalDisplay = &physical
	}
	if meta.LogicalReference != nil {
		logical := *meta.LogicalReference
		input.logicalReference = &logical
	}
	if meta.CRUD != nil {
		crud := *meta.CRUD
		input.crud = &crud
	}
	return validateFieldSemantic(owner, input)
}

func validateFieldSemantic(owner annotationOwner, input fieldSemanticInput) (FieldMeta, *Error) {
	if err := validateLocalizedText(owner, input.label, "/payload/label"); err != nil {
		return FieldMeta{}, err
	}
	if err := validateLocalizedText(owner, input.description, "/payload/description"); err != nil {
		return FieldMeta{}, err
	}
	if err := validateUnicode(owner, string(input.uiHint), "/payload/uiHint"); err != nil {
		return FieldMeta{}, err
	}
	if !validUIHint(input.uiHint) {
		return FieldMeta{}, invalidError(owner, "enum_invalid", "/payload/uiHint")
	}
	if input.physicalDisplay != nil && input.logicalReference != nil {
		return FieldMeta{}, invalidError(owner, "reference_conflict", "/payload")
	}
	var physical *PhysicalDisplay
	if input.physicalDisplay != nil {
		copied := *input.physicalDisplay
		if err := validateUnicode(owner, copied.Field, "/payload/physicalDisplay/field"); err != nil {
			return FieldMeta{}, err
		}
		if blank(copied.Field) {
			return FieldMeta{}, invalidError(owner, "reference_invalid", "/payload/physicalDisplay/field")
		}
		physical = &copied
	}
	var logical *LogicalReference
	if input.logicalReference != nil {
		copied := *input.logicalReference
		for _, member := range []struct{ value, pointer string }{{copied.Target, "/payload/logicalReference/target"}, {copied.Display, "/payload/logicalReference/display"}} {
			if err := validateUnicode(owner, member.value, member.pointer); err != nil {
				return FieldMeta{}, err
			}
			if blank(member.value) {
				return FieldMeta{}, invalidError(owner, "reference_invalid", member.pointer)
			}
		}
		logical = &copied
	}
	if err := validateUnicode(owner, string(input.visibility), "/payload/visibility"); err != nil {
		return FieldMeta{}, err
	}
	if input.visibility != VisibilityPublic && input.visibility != VisibilityInternal && input.visibility != VisibilitySensitive {
		return FieldMeta{}, invalidError(owner, "enum_invalid", "/payload/visibility")
	}
	var crud *CRUDFieldPolicy
	if input.crud != nil {
		copied := *input.crud
		if err := validateUnicode(owner, string(copied.Read), "/payload/crud/read"); err != nil {
			return FieldMeta{}, err
		}
		if copied.Read != ReadInclude && copied.Read != ReadExclude {
			return FieldMeta{}, invalidError(owner, "enum_invalid", "/payload/crud/read")
		}
		if err := validateUnicode(owner, string(copied.Mutation), "/payload/crud/mutation"); err != nil {
			return FieldMeta{}, err
		}
		if copied.Mutation != MutationNone && copied.Mutation != MutationCreate && copied.Mutation != MutationUpdate && copied.Mutation != MutationCreateUpdate {
			return FieldMeta{}, invalidError(owner, "enum_invalid", "/payload/crud/mutation")
		}
		if input.visibility == VisibilitySensitive && copied.Read == ReadInclude ||
			input.visibility == VisibilityInternal && copied.Mutation != MutationNone {
			return FieldMeta{}, invalidError(owner, "policy_conflict", "/payload/crud")
		}
		crud = &copied
	}
	return FieldMeta{
		Label: input.label, Description: input.description, UIHint: input.uiHint,
		PhysicalDisplay: physical, LogicalReference: logical, Visibility: input.visibility, CRUD: crud,
	}, nil
}

func validateCRUDOperations(owner annotationOwner, operations []CRUDOperation) ([]CRUDOperation, *Error) {
	if len(operations) == 0 {
		return nil, invalidError(owner, "crud_operations_empty", "/payload/operations")
	}
	seen := make(map[CRUDOperation]struct{}, len(operations))
	for index, operation := range operations {
		pointer := "/payload/operations/" + strconv.Itoa(index)
		if err := validateUnicode(owner, string(operation), pointer); err != nil {
			return nil, err
		}
		if !validCRUDOperation(operation) {
			return nil, invalidError(owner, "enum_invalid", pointer)
		}
		if _, duplicate := seen[operation]; duplicate {
			return nil, invalidError(owner, "crud_operation_duplicate", "/payload/operations/"+strconv.Itoa(index))
		}
		seen[operation] = struct{}{}
	}
	canonical := make([]CRUDOperation, 0, len(operations))
	for _, operation := range AllCRUDOperations() {
		if _, exists := seen[operation]; exists {
			canonical = append(canonical, operation)
		}
	}
	return canonical, nil
}

func validateLocalizedText(owner annotationOwner, text LocalizedText, base string) *Error {
	for _, member := range []struct {
		value string
		name  string
	}{
		{text.Key, "key"},
		{text.ZhCN, "zhCN"},
		{text.EnUS, "enUS"},
	} {
		pointer := base + "/" + member.name
		if err := validateUnicode(owner, member.value, pointer); err != nil {
			return err
		}
		if blank(member.value) {
			return invalidError(owner, "localized_text_invalid", pointer)
		}
	}
	return nil
}

func blank(value string) bool { return strings.TrimSpace(value) == "" }

func validateUnicode(owner annotationOwner, value, pointer string) *Error {
	if !utf8.ValidString(value) {
		return invalidError(owner, "unicode_invalid", pointer)
	}
	return nil
}

func validUIHint(value UIHint) bool {
	switch value {
	case UIHintText, UIHintTextarea, UIHintNumber, UIHintSwitch, UIHintSelect, UIHintMultiSelect,
		UIHintDatetime, UIHintJSON, UIHintReadonly, UIHintSensitive, UIHintMember, UIHintReference,
		UIHintAttachment, UIHintTags, UIHintComponent, UIHintI18n, UIHintIconify, UIHintPermission,
		UIHintRoute, UIHintScope, UIHintHTTPMethod, UIHintHTTPPath, UIHintModule, UIHintLocale,
		UIHintTimezone:
		return true
	default:
		return false
	}
}

func validCRUDOperation(value CRUDOperation) bool {
	switch value {
	case CRUDList, CRUDGet, CRUDCreate, CRUDUpdate, CRUDDelete:
		return true
	default:
		return false
	}
}

func validateInvalidSentinel(owner annotationOwner, reason, pointer string) *Error {
	known, valid := allowedInvalidPair(owner, reason, pointer)
	if !known {
		return invalidError(owner, "invalid_sentinel_invalid", "/invalid/reason")
	}
	if !valid {
		return invalidError(owner, "invalid_sentinel_invalid", "/invalid/pointer")
	}
	return invalidError(owner, reason, pointer)
}

func allowedInvalidPair(owner annotationOwner, reason, pointer string) (known, valid bool) {
	switch owner {
	case ownerSchema:
		switch reason {
		case "localized_text_invalid":
			return true, localizedPointer(pointer)
		case "enum_invalid":
			return true, pointer == "/payload/identity" || pointer == "/payload/scope"
		case "unicode_invalid":
			return true, localizedPointer(pointer) || pointer == "/payload/identity" || pointer == "/payload/scope"
		default:
			return false, false
		}
	case ownerField:
		switch reason {
		case "localized_text_invalid":
			return true, localizedPointer(pointer)
		case "enum_invalid":
			switch pointer {
			case "/payload/uiHint",
				"/payload/visibility", "/payload/crud/read", "/payload/crud/mutation":
				return true, true
			}
			return true, false
		case "reference_invalid":
			return true, pointer == "/payload/physicalDisplay/field" || pointer == "/payload/logicalReference/target" || pointer == "/payload/logicalReference/display"
		case "reference_conflict":
			return true, pointer == "/payload"
		case "policy_conflict":
			return true, pointer == "/payload/crud"
		case "unicode_invalid":
			return true, fieldUnicodePointer(pointer)
		default:
			return false, false
		}
	case ownerCRUD:
		switch reason {
		case "crud_operations_empty":
			return true, pointer == "/payload/operations"
		case "enum_invalid":
			return true, canonicalOperationIndex(pointer, false)
		case "crud_operation_duplicate":
			return true, canonicalOperationIndex(pointer, true)
		case "unicode_invalid":
			return true, canonicalOperationIndex(pointer, false)
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func fieldUnicodePointer(pointer string) bool {
	if localizedPointer(pointer) {
		return true
	}
	switch pointer {
	case "/payload/uiHint", "/payload/physicalDisplay/field",
		"/payload/logicalReference/target", "/payload/logicalReference/display",
		"/payload/visibility", "/payload/crud/read", "/payload/crud/mutation":
		return true
	default:
		return false
	}
}

func localizedPointer(pointer string) bool {
	for _, object := range []string{"label", "description"} {
		for _, member := range []string{"key", "zhCN", "enUS"} {
			if pointer == "/payload/"+object+"/"+member {
				return true
			}
		}
	}
	return false
}

func canonicalOperationIndex(pointer string, positive bool) bool {
	const prefix = "/payload/operations/"
	if !strings.HasPrefix(pointer, prefix) {
		return false
	}
	index := strings.TrimPrefix(pointer, prefix)
	if index == "" || len(index) > 1 && index[0] == '0' {
		return false
	}
	for _, character := range index {
		if character < '0' || character > '9' {
			return false
		}
	}
	value, err := strconv.Atoi(index)
	if err != nil || value < 0 {
		return false
	}
	return !positive || value > 0
}
