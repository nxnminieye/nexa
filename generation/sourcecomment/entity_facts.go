package sourcecomment

import (
	"fmt"
	"strings"
)

// LocalizedText is a resolved projection of localized Source Comment facts.
// Key is derived from the semantic node; it is never authored independently.
type LocalizedText struct {
	Key  string
	ZhCN string
	EnUS string
}

type RecordScope string

const (
	ScopeGlobal RecordScope = "global"
	ScopeTenant RecordScope = "tenant"
)

type UIControl string

const (
	UIControlText        UIControl = "text"
	UIControlTextarea    UIControl = "textarea"
	UIControlNumber      UIControl = "number"
	UIControlSwitch      UIControl = "switch"
	UIControlSelect      UIControl = "select"
	UIControlMultiSelect UIControl = "multi-select"
	UIControlDatetime    UIControl = "datetime"
	UIControlReadonly    UIControl = "readonly"
	UIControlSensitive   UIControl = "sensitive"
	UIControlMember      UIControl = "member"
	UIControlReference   UIControl = "reference"
	UIControlAttachment  UIControl = "attachment"
	UIControlTags        UIControl = "tags"
	UIControlComponent   UIControl = "component"
	UIControlI18n        UIControl = "i18n"
	UIControlIconify     UIControl = "iconify"
	UIControlPermission  UIControl = "permission"
	UIControlRoute       UIControl = "route"
	UIControlScope       UIControl = "scope"
	UIControlHTTPMethod  UIControl = "http-method"
	UIControlHTTPPath    UIControl = "http-path"
	UIControlModule      UIControl = "module"
	UIControlLocale      UIControl = "locale"
	UIControlTimezone    UIControl = "timezone"
)

type FieldVisibility string

const (
	VisibilityPublic    FieldVisibility = "public"
	VisibilityInternal  FieldVisibility = "internal"
	VisibilitySensitive FieldVisibility = "sensitive"
)

type ReadPolicy string

const (
	ReadInclude ReadPolicy = "include"
	ReadExclude ReadPolicy = "exclude"
)

type MutationPolicy string

const (
	MutationNone         MutationPolicy = "none"
	MutationCreate       MutationPolicy = "create"
	MutationUpdate       MutationPolicy = "update"
	MutationCreateUpdate MutationPolicy = "create-update"
)

type CRUDFieldPolicy struct {
	Read     ReadPolicy     `json:"read"`
	Mutation MutationPolicy `json:"mutation"`
}

type ResolvedReference struct {
	Target  string `json:"target"`
	Display string `json:"display"`
}

type SchemaFacts struct {
	Label       LocalizedText
	Description LocalizedText
	Scope       RecordScope
}

type FieldFacts struct {
	Label       LocalizedText
	Description LocalizedText
	Control     UIControl
	Reference   *ResolvedReference
	Visibility  FieldVisibility
	CRUD        *CRUDFieldPolicy
}

type CRUDOperation string

const (
	CRUDList   CRUDOperation = "list"
	CRUDGet    CRUDOperation = "get"
	CRUDCreate CRUDOperation = "create"
	CRUDUpdate CRUDOperation = "update"
	CRUDDelete CRUDOperation = "delete"
)

type CRUDOperations struct{ operations []CRUDOperation }

func NewCRUDOperations(values ...CRUDOperation) (CRUDOperations, error) {
	rank := map[CRUDOperation]int{CRUDList: 0, CRUDGet: 1, CRUDCreate: 2, CRUDUpdate: 3, CRUDDelete: 4}
	seen := make(map[CRUDOperation]bool, len(values))
	canonical := make([]CRUDOperation, 0, len(values))
	for _, operation := range []CRUDOperation{CRUDList, CRUDGet, CRUDCreate, CRUDUpdate, CRUDDelete} {
		for _, value := range values {
			if _, ok := rank[value]; !ok {
				return CRUDOperations{}, fmt.Errorf("unknown CRUD operation %q", value)
			}
			if value != operation {
				continue
			}
			if seen[value] {
				return CRUDOperations{}, fmt.Errorf("duplicate CRUD operation %q", value)
			}
			seen[value] = true
			canonical = append(canonical, value)
		}
	}
	if len(canonical) == 0 {
		return CRUDOperations{}, fmt.Errorf("CRUD operations are empty")
	}
	return CRUDOperations{operations: canonical}, nil
}

func (s CRUDOperations) Operations() []CRUDOperation {
	return append([]CRUDOperation(nil), s.operations...)
}

func ValidateSchemaFacts(value SchemaFacts) error {
	if err := validateLocalized(value.Label); err != nil {
		return fmt.Errorf("schema label: %w", err)
	}
	if err := validateLocalized(value.Description); err != nil {
		return fmt.Errorf("schema description: %w", err)
	}
	if value.Scope != ScopeGlobal && value.Scope != ScopeTenant {
		return fmt.Errorf("schema scope %q is invalid", value.Scope)
	}
	return nil
}

func ValidateFieldFacts(value FieldFacts) error {
	if err := validateLocalized(value.Label); err != nil {
		return fmt.Errorf("field label: %w", err)
	}
	if err := validateLocalized(value.Description); err != nil {
		return fmt.Errorf("field description: %w", err)
	}
	registry := StandardRegistry()
	control := StringValue(string(value.Control))
	if code, _ := registry.entries["ui.control"].validate(Target{SemanticID: "Field", Kind: NodeField, Stage: StageEnt}, control); code != "" {
		return fmt.Errorf("field control %q is invalid", value.Control)
	}
	visibility := StringValue(string(value.Visibility))
	if code, _ := registry.entries["visibility"].validate(Target{SemanticID: "Field", Kind: NodeField, Stage: StageEnt}, visibility); code != "" {
		return fmt.Errorf("field visibility %q is invalid", value.Visibility)
	}
	if value.CRUD != nil {
		if code, _ := registry.entries["crud.read"].validate(Target{SemanticID: "Field", Kind: NodeField, Stage: StageEnt}, StringValue(string(value.CRUD.Read))); code != "" {
			return fmt.Errorf("field CRUD read policy %q is invalid", value.CRUD.Read)
		}
		if code, _ := registry.entries["crud.mutation"].validate(Target{SemanticID: "Field", Kind: NodeField, Stage: StageEnt}, StringValue(string(value.CRUD.Mutation))); code != "" {
			return fmt.Errorf("field CRUD mutation policy %q is invalid", value.CRUD.Mutation)
		}
	}
	if value.Reference != nil {
		if !canonicalIdentifierPattern.MatchString(value.Reference.Target) || !fieldIdentifierPattern.MatchString(value.Reference.Display) {
			return fmt.Errorf("field reference is invalid")
		}
	}
	return nil
}

func ValidateCRUDOperations(value CRUDOperations) error {
	_, err := NewCRUDOperations(value.Operations()...)
	return err
}

func validateLocalized(value LocalizedText) error {
	if strings.TrimSpace(value.Key) == "" || strings.TrimSpace(value.ZhCN) == "" || strings.TrimSpace(value.EnUS) == "" {
		return fmt.Errorf("localized text is incomplete")
	}
	return nil
}

func (g FactGraph) SchemaFacts(semanticID string) (SchemaFacts, error) {
	label, err := g.localized(semanticID, "label")
	if err != nil {
		return SchemaFacts{}, err
	}
	description, err := g.localized(semanticID, "description")
	if err != nil {
		return SchemaFacts{}, err
	}
	scope, err := g.requiredString(semanticID, "scope")
	if err != nil {
		return SchemaFacts{}, err
	}
	return SchemaFacts{Label: label, Description: description, Scope: RecordScope(scope)}, nil
}

func (g FactGraph) CRUD(semanticID string) (CRUDOperations, bool, error) {
	fact, ok := g.Fact(FactID{SemanticID: semanticID, Key: "crud.operations"})
	if !ok {
		return CRUDOperations{}, false, nil
	}
	items, ok := fact.Value().Elements()
	if !ok {
		return CRUDOperations{}, true, fmt.Errorf("fact %s:crud.operations is not a list", semanticID)
	}
	operations := make([]CRUDOperation, len(items))
	for index, item := range items {
		value, ok := item.String()
		if !ok {
			return CRUDOperations{}, true, fmt.Errorf("fact %s:crud.operations contains a non-string value", semanticID)
		}
		operations[index] = CRUDOperation(value)
	}
	result, err := NewCRUDOperations(operations...)
	return result, true, err
}

func (g FactGraph) FieldFacts(semanticID string) (FieldFacts, error) {
	label, err := g.localized(semanticID, "label")
	if err != nil {
		return FieldFacts{}, err
	}
	description, err := g.localized(semanticID, "description")
	if err != nil {
		return FieldFacts{}, err
	}
	control, err := g.requiredString(semanticID, "ui.control")
	if err != nil {
		return FieldFacts{}, err
	}
	visibility, err := g.requiredString(semanticID, "visibility")
	if err != nil {
		return FieldFacts{}, err
	}
	result := FieldFacts{Label: label, Description: description, Control: UIControl(control), Visibility: FieldVisibility(visibility)}
	read, hasRead := g.optionalString(semanticID, "crud.read")
	mutation, hasMutation := g.optionalString(semanticID, "crud.mutation")
	if hasRead != hasMutation {
		return FieldFacts{}, fmt.Errorf("facts %s:crud.read and crud.mutation must be declared together", semanticID)
	}
	if hasRead {
		result.CRUD = &CRUDFieldPolicy{Read: ReadPolicy(read), Mutation: MutationPolicy(mutation)}
	}
	if fact, ok := g.Fact(FactID{SemanticID: semanticID, Key: "ui.reference"}); ok {
		reference, valid := fact.Value().Reference()
		if !valid {
			return FieldFacts{}, fmt.Errorf("fact %s:ui.reference is invalid", semanticID)
		}
		result.Reference = &ResolvedReference{Target: reference.Target, Display: reference.Display}
	}
	return result, nil
}

func (g FactGraph) localized(semanticID, prefix string) (LocalizedText, error) {
	zhCN, err := g.requiredString(semanticID, prefix+".zh-CN")
	if err != nil {
		return LocalizedText{}, err
	}
	enUS, err := g.requiredString(semanticID, prefix+".en-US")
	if err != nil {
		return LocalizedText{}, err
	}
	return LocalizedText{Key: localeKey(semanticID, prefix), ZhCN: zhCN, EnUS: enUS}, nil
}

func (g FactGraph) requiredString(semanticID, key string) (string, error) {
	value, ok := g.optionalString(semanticID, key)
	if !ok {
		return "", fmt.Errorf("fact %s:%s is required", semanticID, key)
	}
	return value, nil
}

func (g FactGraph) optionalString(semanticID, key string) (string, bool) {
	fact, ok := g.Fact(FactID{SemanticID: semanticID, Key: key})
	if !ok {
		return "", false
	}
	value, ok := fact.Value().String()
	return value, ok
}

func localeKey(semanticID, suffix string) string {
	var builder strings.Builder
	for index, character := range semanticID {
		if character == '.' || character == '/' || character == ':' {
			builder.WriteByte('.')
			continue
		}
		if character >= 'A' && character <= 'Z' {
			if index > 0 && builder.Len() > 0 {
				builder.WriteByte('_')
			}
			builder.WriteByte(byte(character - 'A' + 'a'))
			continue
		}
		builder.WriteRune(character)
	}
	return strings.Trim(builder.String(), ".") + "." + suffix
}
