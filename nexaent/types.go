// Package nexaent defines closed, typed Ent schema metadata annotations.
// Constructors preserve invalid input as transportable typed sentinels, while
// Annotation.CanonicalJSON is the only semantic-byte projection.
package nexaent

const (
	// SchemaAnnotationName is the Ent annotation name for schema metadata v1.
	SchemaAnnotationName = "nexa.dev/ent-schema-meta/v1"
	// FieldAnnotationName is the Ent annotation name for field metadata v1.
	FieldAnnotationName = "nexa.dev/ent-field-meta/v1"
	// CRUDAnnotationName is the Ent annotation name for schema CRUD opt-in v1.
	CRUDAnnotationName = "nexa.dev/ent-crud/v1"
	// SchemaAnnotationKind is the wire discriminator for schema metadata.
	SchemaAnnotationKind = "EntSchemaMeta"
	// FieldAnnotationKind is the wire discriminator for field metadata.
	FieldAnnotationKind = "EntFieldMeta"
	// CRUDAnnotationKind is the wire discriminator for schema CRUD opt-in.
	CRUDAnnotationKind = "EntCRUD"
)

// LocalizedText is a required localization key with Chinese and English text.
type LocalizedText struct {
	// Key identifies the localization entry.
	Key string
	// ZhCN is the Simplified Chinese text.
	ZhCN string
	// EnUS is the US English text.
	EnUS string
}

// IdentityStrategy identifies how an Ent schema obtains record identity.
type IdentityStrategy string

// IdentityEntID uses the Ent-managed ID as record identity.
const IdentityEntID IdentityStrategy = "ent-id"

// RecordScope identifies whether records are shared or tenant-owned.
type RecordScope string

const (
	// ScopeGlobal marks records that are shared across tenants.
	ScopeGlobal RecordScope = "global"
	// ScopeTenant marks records that belong to a tenant scope.
	ScopeTenant RecordScope = "tenant"
)

// UIHint identifies the intended field presentation or editor category.
type UIHint string

const (
	// UIHintText identifies a single-line text field.
	UIHintText UIHint = "text"
	// UIHintTextarea identifies a multiline text field.
	UIHintTextarea UIHint = "textarea"
	// UIHintNumber identifies a numeric field.
	UIHintNumber UIHint = "number"
	// UIHintSwitch identifies a boolean switch field.
	UIHintSwitch UIHint = "switch"
	// UIHintSelect identifies a single-choice field.
	UIHintSelect UIHint = "select"
	// UIHintMultiSelect identifies a multiple-choice field.
	UIHintMultiSelect UIHint = "multi-select"
	// UIHintDatetime identifies a date-time field.
	UIHintDatetime UIHint = "datetime"
	// UIHintJSON identifies a structured JSON field.
	UIHintJSON UIHint = "json"
	// UIHintReadonly identifies a read-only field.
	UIHintReadonly UIHint = "readonly"
	// UIHintSensitive identifies a sensitive-value field.
	UIHintSensitive UIHint = "sensitive"
	// UIHintMember identifies a member-selection field.
	UIHintMember UIHint = "member"
	// UIHintReference identifies a reference-selection field.
	UIHintReference UIHint = "reference"
	// UIHintAttachment identifies an attachment field.
	UIHintAttachment UIHint = "attachment"
	// UIHintTags identifies a tag-list field.
	UIHintTags UIHint = "tags"
	// UIHintComponent identifies a component-selection field.
	UIHintComponent UIHint = "component"
	// UIHintI18n identifies a localized-content field.
	UIHintI18n UIHint = "i18n"
	// UIHintIconify identifies an Iconify icon-selection field.
	UIHintIconify UIHint = "iconify"
	// UIHintPermission identifies a permission-selection field.
	UIHintPermission UIHint = "permission"
	// UIHintRoute identifies a route-selection field.
	UIHintRoute UIHint = "route"
	// UIHintScope identifies a scope-selection field.
	UIHintScope UIHint = "scope"
	// UIHintHTTPMethod identifies an HTTP method field.
	UIHintHTTPMethod UIHint = "http-method"
	// UIHintHTTPPath identifies an HTTP path field.
	UIHintHTTPPath UIHint = "http-path"
	// UIHintModule identifies a module-selection field.
	UIHintModule UIHint = "module"
	// UIHintLocale identifies a locale-selection field.
	UIHintLocale UIHint = "locale"
	// UIHintTimezone identifies a timezone-selection field.
	UIHintTimezone UIHint = "timezone"
)

// PhysicalDisplay selects a display field on the target of a real Ent edge.
type PhysicalDisplay struct {
	Field string
}

// LogicalReference describes a business reference that is not a local Ent edge.
type LogicalReference struct {
	Target  string
	Display string
}

// FieldVisibility identifies the exposure class of a field.
type FieldVisibility string

const (
	// VisibilityPublic permits public exposure subject to field policy.
	VisibilityPublic FieldVisibility = "public"
	// VisibilityInternal restricts the field to internal use and forbids mutation policy.
	VisibilityInternal FieldVisibility = "internal"
	// VisibilitySensitive marks a sensitive field and requires read exclusion.
	VisibilitySensitive FieldVisibility = "sensitive"
)

// ReadPolicy controls whether generated read projections include a field.
type ReadPolicy string

const (
	// ReadInclude includes the field in read projections.
	ReadInclude ReadPolicy = "include"
	// ReadExclude excludes the field from read projections.
	ReadExclude ReadPolicy = "exclude"
)

// MutationPolicy identifies generated mutation support for a field.
type MutationPolicy string

const (
	// MutationNone excludes the field from create and update mutations.
	MutationNone MutationPolicy = "none"
	// MutationCreate includes the field only in create mutations.
	MutationCreate MutationPolicy = "create"
	// MutationUpdate includes the field only in update mutations.
	MutationUpdate MutationPolicy = "update"
	// MutationCreateUpdate includes the field in create and update mutations.
	MutationCreateUpdate MutationPolicy = "create-update"
)

// CRUDFieldPolicy controls generated read and mutation handling for one field.
// Internal fields require MutationNone; sensitive fields require ReadExclude.
type CRUDFieldPolicy struct {
	// Read controls read projection inclusion.
	Read ReadPolicy
	// Mutation controls create and update mutation inclusion.
	Mutation MutationPolicy
}

// SchemaMeta is the complete typed metadata authored on an Ent schema.
type SchemaMeta struct {
	// Label is the schema's localized display name.
	Label LocalizedText
	// Description is the schema's localized description.
	Description LocalizedText
	// Identity is the schema's record identity strategy.
	Identity IdentityStrategy
	// Scope is the schema's record ownership scope.
	Scope RecordScope
}

// FieldMeta is the complete typed metadata authored on an Ent field.
type FieldMeta struct {
	// Label is the field's localized display name.
	Label LocalizedText
	// Description is the field's localized description.
	Description LocalizedText
	// UIHint identifies the intended field presentation category.
	UIHint UIHint
	// PhysicalDisplay optionally selects the display field for a bound Ent edge.
	PhysicalDisplay *PhysicalDisplay
	// LogicalReference optionally describes a non-database business reference.
	LogicalReference *LogicalReference
	// Visibility identifies the field's exposure class.
	Visibility FieldVisibility
	// CRUD optionally controls this field within schema-level CRUD generation.
	// Nil means that no field policy was authored.
	CRUD *CRUDFieldPolicy
}

// CRUDOperation identifies one schema-level generated CRUD operation.
type CRUDOperation string

const (
	// CRUDList opts in to list generation.
	CRUDList CRUDOperation = "list"
	// CRUDGet opts in to get generation.
	CRUDGet CRUDOperation = "get"
	// CRUDCreate opts in to create generation.
	CRUDCreate CRUDOperation = "create"
	// CRUDUpdate opts in to update generation.
	CRUDUpdate CRUDOperation = "update"
	// CRUDDelete opts in to delete generation.
	CRUDDelete CRUDOperation = "delete"
)

// CRUDSpec is the validated, canonicalized schema CRUD operation set.
type CRUDSpec struct {
	operations []CRUDOperation
}

// Operations returns a fresh slice in canonical list/get/create/update/delete order.
func (s CRUDSpec) Operations() []CRUDOperation {
	return append([]CRUDOperation(nil), s.operations...)
}
