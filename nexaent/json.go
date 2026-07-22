package nexaent

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/internal/strictdoc"
)

type wireLocalizedText struct {
	Key  string `json:"key"`
	ZhCN string `json:"zhCN"`
	EnUS string `json:"enUS"`
}

type wirePhysicalDisplay struct {
	Field string `json:"field"`
}

type wireLogicalReference struct {
	Target  string `json:"target"`
	Display string `json:"display"`
}

type wireCRUDFieldPolicy struct {
	Read     ReadPolicy     `json:"read"`
	Mutation MutationPolicy `json:"mutation"`
}

type wireSchemaPayload struct {
	Label       wireLocalizedText `json:"label"`
	Description wireLocalizedText `json:"description"`
	Identity    IdentityStrategy  `json:"identity"`
	Scope       RecordScope       `json:"scope"`
}

type wireFieldPayload struct {
	Label            wireLocalizedText     `json:"label"`
	Description      wireLocalizedText     `json:"description"`
	UIHint           UIHint                `json:"uiHint"`
	PhysicalDisplay  *wirePhysicalDisplay  `json:"physicalDisplay,omitempty"`
	LogicalReference *wireLogicalReference `json:"logicalReference,omitempty"`
	Visibility       FieldVisibility       `json:"visibility"`
	CRUD             *wireCRUDFieldPolicy  `json:"crud,omitempty"`
}

type wireCRUDPayload struct {
	Operations []CRUDOperation `json:"operations"`
}

type wireNormalEnvelope[T any] struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Payload    T      `json:"payload"`
}

type wireDuplicateEnvelope struct {
	APIVersion string `json:"apiVersion"`
	Duplicate  bool   `json:"duplicate"`
	Kind       string `json:"kind"`
}

type wireInvalid struct {
	Reason  string `json:"reason"`
	Pointer string `json:"pointer"`
}

type wireInvalidEnvelope struct {
	APIVersion string      `json:"apiVersion"`
	Invalid    wireInvalid `json:"invalid"`
	Kind       string      `json:"kind"`
}

type strictEnvelopeProbe struct {
	APIVersion json.RawMessage `json:"apiVersion"`
	Kind       json.RawMessage `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Duplicate  json.RawMessage `json:"duplicate"`
	Invalid    json.RawMessage `json:"invalid"`
}

type strictEnvelope[T any] struct {
	APIVersion *string `json:"apiVersion"`
	Kind       *string `json:"kind"`
	Payload    *T      `json:"payload"`
}

type strictDuplicateEnvelope struct {
	APIVersion *string `json:"apiVersion"`
	Duplicate  *bool   `json:"duplicate"`
	Kind       *string `json:"kind"`
}

type strictInvalidValue struct {
	Reason  *string `json:"reason"`
	Pointer *string `json:"pointer"`
}

type strictInvalidEnvelope struct {
	APIVersion *string             `json:"apiVersion"`
	Invalid    *strictInvalidValue `json:"invalid"`
	Kind       *string             `json:"kind"`
}

type strictLocalizedText struct {
	Key  *string `json:"key"`
	ZhCN *string `json:"zhCN"`
	EnUS *string `json:"enUS"`
}

type strictPhysicalDisplay struct {
	Field *string `json:"field"`
}

type strictLogicalReference struct {
	Target  *string `json:"target"`
	Display *string `json:"display"`
}

type strictCRUDFieldPolicy struct {
	Read     *ReadPolicy     `json:"read"`
	Mutation *MutationPolicy `json:"mutation"`
}

type strictSchemaPayload struct {
	Label       *strictLocalizedText `json:"label"`
	Description *strictLocalizedText `json:"description"`
	Identity    *IdentityStrategy    `json:"identity"`
	Scope       *RecordScope         `json:"scope"`
}

type strictFieldPayload struct {
	Label            *strictLocalizedText `json:"label"`
	Description      *strictLocalizedText `json:"description"`
	UIHint           *UIHint              `json:"uiHint"`
	PhysicalDisplay  json.RawMessage      `json:"physicalDisplay,omitempty"`
	LogicalReference json.RawMessage      `json:"logicalReference,omitempty"`
	Visibility       *FieldVisibility     `json:"visibility"`
	CRUD             json.RawMessage      `json:"crud,omitempty"`
}

type strictCRUDPayload struct {
	Operations *[]CRUDOperation `json:"operations"`
}

func (a *annotationValue) MarshalJSON() ([]byte, error) {
	if a == nil {
		return nil, invalidError(ownerSchema, "document_invalid", "")
	}
	encoded, err := a.transportJSON()
	return append([]byte(nil), encoded...), err
}

func (a *annotationValue) CanonicalJSON() ([]byte, error) {
	if a == nil {
		return nil, invalidError(ownerSchema, "document_invalid", "")
	}
	if a.duplicate {
		return nil, duplicateError(a.owner)
	}
	if a.failure != nil {
		return nil, a.failure
	}
	transport, err := a.transportJSON()
	if err != nil {
		return nil, err
	}
	canonical, err := jcs.Transform(transport)
	if err != nil {
		return nil, invalidError(a.owner, "document_invalid", "")
	}
	return append([]byte(nil), canonical...), nil
}

func (a *annotationValue) transportJSON() ([]byte, error) {
	var value any
	switch {
	case a.duplicate:
		value = wireDuplicateEnvelope{APIVersion: a.owner.name(), Duplicate: true, Kind: a.owner.kind()}
	case a.failure != nil:
		value = wireInvalidEnvelope{APIVersion: a.owner.name(), Invalid: wireInvalid{Reason: a.failure.Reason(), Pointer: a.failure.Pointer()}, Kind: a.owner.kind()}
	case a.owner == ownerField:
		value = wireNormalEnvelope[wireFieldPayload]{APIVersion: a.owner.name(), Kind: a.owner.kind(), Payload: fieldPayloadToWire(a.fieldMeta)}
	case a.owner == ownerCRUD:
		value = wireNormalEnvelope[wireCRUDPayload]{APIVersion: a.owner.name(), Kind: a.owner.kind(), Payload: wireCRUDPayload{Operations: append([]CRUDOperation(nil), a.crudSpec.operations...)}}
	default:
		value = wireNormalEnvelope[wireSchemaPayload]{APIVersion: a.owner.name(), Kind: a.owner.kind(), Payload: schemaPayloadToWire(a.schemaMeta)}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, invalidError(a.owner, "document_invalid", "")
	}
	return encoded, nil
}

func schemaPayloadToWire(meta SchemaMeta) wireSchemaPayload {
	return wireSchemaPayload{Label: localizedTextToWire(meta.Label), Description: localizedTextToWire(meta.Description), Identity: meta.Identity, Scope: meta.Scope}
}

func fieldPayloadToWire(meta FieldMeta) wireFieldPayload {
	payload := wireFieldPayload{Label: localizedTextToWire(meta.Label), Description: localizedTextToWire(meta.Description), UIHint: meta.UIHint, Visibility: meta.Visibility}
	if meta.PhysicalDisplay != nil {
		payload.PhysicalDisplay = &wirePhysicalDisplay{Field: meta.PhysicalDisplay.Field}
	}
	if meta.LogicalReference != nil {
		payload.LogicalReference = &wireLogicalReference{Target: meta.LogicalReference.Target, Display: meta.LogicalReference.Display}
	}
	if meta.CRUD != nil {
		payload.CRUD = &wireCRUDFieldPolicy{Read: meta.CRUD.Read, Mutation: meta.CRUD.Mutation}
	}
	return payload
}

func localizedTextToWire(text LocalizedText) wireLocalizedText {
	return wireLocalizedText{Key: text.Key, ZhCN: text.ZhCN, EnUS: text.EnUS}
}

// DecodeSchema strictly decodes a schema annotation transport envelope.
func DecodeSchema(data []byte) (SchemaMeta, error) {
	var payload strictSchemaPayload
	if err := decodeNormal(ownerSchema, data, &payload); err != nil {
		return SchemaMeta{}, err
	}
	if err := requireLocalized(ownerSchema, payload.Label, "/payload/label"); err != nil {
		return SchemaMeta{}, err
	}
	if err := requireLocalized(ownerSchema, payload.Description, "/payload/description"); err != nil {
		return SchemaMeta{}, err
	}
	if payload.Identity == nil {
		return SchemaMeta{}, invalidError(ownerSchema, "document_required_missing", "/payload/identity")
	}
	if payload.Scope == nil {
		return SchemaMeta{}, invalidError(ownerSchema, "document_required_missing", "/payload/scope")
	}
	meta := SchemaMeta{Label: localizedFromStrict(payload.Label), Description: localizedFromStrict(payload.Description), Identity: *payload.Identity, Scope: *payload.Scope}
	if err := validateSchemaMeta(ownerSchema, meta); err != nil {
		return SchemaMeta{}, err
	}
	return meta, nil
}

// DecodeField strictly decodes a field annotation transport envelope.
func DecodeField(data []byte) (FieldMeta, error) {
	var payload strictFieldPayload
	if err := decodeNormal(ownerField, data, &payload); err != nil {
		return FieldMeta{}, err
	}
	if err := requireLocalized(ownerField, payload.Label, "/payload/label"); err != nil {
		return FieldMeta{}, err
	}
	if err := requireLocalized(ownerField, payload.Description, "/payload/description"); err != nil {
		return FieldMeta{}, err
	}
	if payload.UIHint == nil {
		return FieldMeta{}, invalidError(ownerField, "document_required_missing", "/payload/uiHint")
	}
	if payload.Visibility == nil {
		return FieldMeta{}, invalidError(ownerField, "document_required_missing", "/payload/visibility")
	}
	input := fieldSemanticInput{label: localizedFromStrict(payload.Label), description: localizedFromStrict(payload.Description), uiHint: *payload.UIHint, visibility: *payload.Visibility}
	if len(payload.PhysicalDisplay) != 0 {
		if bytes.Equal(bytes.TrimSpace(payload.PhysicalDisplay), []byte("null")) {
			return FieldMeta{}, invalidError(ownerField, "document_type_invalid", "/payload/physicalDisplay")
		}
		var physical strictPhysicalDisplay
		if err := strictdoc.DecodeJSONExact(ownerField.name(), payload.PhysicalDisplay, &physical); err != nil {
			return FieldMeta{}, projectNestedDocumentError(ownerField, "/payload/physicalDisplay", err)
		}
		if physical.Field == nil {
			return FieldMeta{}, invalidError(ownerField, "document_required_missing", "/payload/physicalDisplay/field")
		}
		input.physicalDisplay = &PhysicalDisplay{Field: *physical.Field}
	}
	if len(payload.LogicalReference) != 0 {
		if bytes.Equal(bytes.TrimSpace(payload.LogicalReference), []byte("null")) {
			return FieldMeta{}, invalidError(ownerField, "document_type_invalid", "/payload/logicalReference")
		}
		var logical strictLogicalReference
		if err := strictdoc.DecodeJSONExact(ownerField.name(), payload.LogicalReference, &logical); err != nil {
			return FieldMeta{}, projectNestedDocumentError(ownerField, "/payload/logicalReference", err)
		}
		if logical.Target == nil {
			return FieldMeta{}, invalidError(ownerField, "document_required_missing", "/payload/logicalReference/target")
		}
		if logical.Display == nil {
			return FieldMeta{}, invalidError(ownerField, "document_required_missing", "/payload/logicalReference/display")
		}
		input.logicalReference = &LogicalReference{Target: *logical.Target, Display: *logical.Display}
	}
	if len(payload.CRUD) != 0 {
		if bytes.Equal(bytes.TrimSpace(payload.CRUD), []byte("null")) {
			return FieldMeta{}, invalidError(ownerField, "document_type_invalid", "/payload/crud")
		}
		var crud strictCRUDFieldPolicy
		if err := strictdoc.DecodeJSONExact(ownerField.name(), payload.CRUD, &crud); err != nil {
			return FieldMeta{}, projectNestedDocumentError(ownerField, "/payload/crud", err)
		}
		if crud.Read == nil {
			return FieldMeta{}, invalidError(ownerField, "document_required_missing", "/payload/crud/read")
		}
		if crud.Mutation == nil {
			return FieldMeta{}, invalidError(ownerField, "document_required_missing", "/payload/crud/mutation")
		}
		input.crud = &CRUDFieldPolicy{Read: *crud.Read, Mutation: *crud.Mutation}
	}
	meta, semanticErr := validateFieldSemantic(ownerField, input)
	if semanticErr != nil {
		return FieldMeta{}, semanticErr
	}
	return meta, nil
}

// DecodeCRUD strictly decodes a CRUD annotation transport envelope.
func DecodeCRUD(data []byte) (CRUDSpec, error) {
	var payload strictCRUDPayload
	if err := decodeNormal(ownerCRUD, data, &payload); err != nil {
		return CRUDSpec{}, err
	}
	if payload.Operations == nil {
		return CRUDSpec{}, invalidError(ownerCRUD, "document_required_missing", "/payload/operations")
	}
	canonical, err := validateCRUDOperations(ownerCRUD, append([]CRUDOperation(nil), (*payload.Operations)...))
	if err != nil {
		return CRUDSpec{}, err
	}
	return CRUDSpec{operations: canonical}, nil
}

func decodeNormal[T any](owner annotationOwner, data []byte, payload *T) *Error {
	document, err := strictdoc.ParseJSON(owner.name(), data)
	if err != nil {
		return projectDocumentError(owner, err)
	}
	normalized := document.JSON()
	if len(normalized) == 0 || normalized[0] != '{' {
		return invalidError(owner, "document_invalid", "")
	}
	var probe strictEnvelopeProbe
	if err := document.DecodeExact(&probe); err != nil {
		return projectDocumentError(owner, err)
	}
	if len(probe.Invalid) != 0 {
		return decodeInvalid(owner, document, probe.Invalid)
	}
	if len(probe.Duplicate) != 0 {
		return decodeDuplicate(owner, document, probe.Duplicate)
	}
	var envelope strictEnvelope[T]
	if err := document.DecodeExact(&envelope); err != nil {
		return projectDocumentError(owner, err)
	}
	if err := validateEnvelopeIdentity(owner, envelope.APIVersion, envelope.Kind); err != nil {
		return err
	}
	if envelope.Payload == nil {
		return invalidError(owner, "document_required_missing", "/payload")
	}
	*payload = *envelope.Payload
	return nil
}

func decodeDuplicate(owner annotationOwner, document strictdoc.Document, raw json.RawMessage) *Error {
	var envelope strictDuplicateEnvelope
	if err := document.DecodeExact(&envelope); err != nil {
		return projectDocumentError(owner, err)
	}
	if err := validateEnvelopeIdentity(owner, envelope.APIVersion, envelope.Kind); err != nil {
		return err
	}
	if envelope.Duplicate == nil || !*envelope.Duplicate || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return invalidError(owner, "document_type_invalid", "/duplicate")
	}
	return duplicateError(owner)
}

func decodeInvalid(owner annotationOwner, document strictdoc.Document, raw json.RawMessage) *Error {
	var envelope strictInvalidEnvelope
	if err := document.DecodeExact(&envelope); err != nil {
		return projectDocumentError(owner, err)
	}
	if err := validateEnvelopeIdentity(owner, envelope.APIVersion, envelope.Kind); err != nil {
		return err
	}
	if envelope.Invalid == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return invalidError(owner, "document_type_invalid", "/invalid")
	}
	if envelope.Invalid.Reason == nil {
		return invalidError(owner, "document_required_missing", "/invalid/reason")
	}
	if envelope.Invalid.Pointer == nil {
		return invalidError(owner, "document_required_missing", "/invalid/pointer")
	}
	return validateInvalidSentinel(owner, *envelope.Invalid.Reason, *envelope.Invalid.Pointer)
}

func validateEnvelopeIdentity(owner annotationOwner, apiVersion, kind *string) *Error {
	if apiVersion == nil {
		return invalidError(owner, "document_required_missing", "/apiVersion")
	}
	if kind == nil {
		return invalidError(owner, "document_required_missing", "/kind")
	}
	if *apiVersion != owner.name() {
		return invalidError(owner, "version_unsupported", "/apiVersion")
	}
	if *kind != owner.kind() {
		return invalidError(owner, "kind_invalid", "/kind")
	}
	return nil
}

func requireLocalized(owner annotationOwner, value *strictLocalizedText, pointer string) *Error {
	if value == nil {
		return invalidError(owner, "document_required_missing", pointer)
	}
	for _, member := range []struct {
		name  string
		value *string
	}{{"key", value.Key}, {"zhCN", value.ZhCN}, {"enUS", value.EnUS}} {
		if member.value == nil {
			return invalidError(owner, "document_required_missing", pointer+"/"+member.name)
		}
	}
	return nil
}

func localizedFromStrict(value *strictLocalizedText) LocalizedText {
	return LocalizedText{Key: *value.Key, ZhCN: *value.ZhCN, EnUS: *value.EnUS}
}

func projectDocumentError(owner annotationOwner, err error) *Error {
	var documentError *strictdoc.Error
	if !errors.As(err, &documentError) {
		return invalidError(owner, "document_invalid", "")
	}
	switch documentError.Code {
	case "document_duplicate_key", "document_unknown_field":
		return invalidError(owner, documentError.Code, documentError.Pointer)
	case "document_trailing_input":
		return invalidError(owner, documentError.Code, "")
	case "document_unicode_invalid":
		return invalidError(owner, "unicode_invalid", "")
	case "document_invalid":
		if documentError.Pointer != "" {
			return invalidError(owner, "document_type_invalid", documentError.Pointer)
		}
	}
	return invalidError(owner, "document_invalid", "")
}

func projectNestedDocumentError(owner annotationOwner, base string, err error) *Error {
	projected := projectDocumentError(owner, err)
	if projected.Pointer() == "" {
		if projected.Reason() == "document_invalid" {
			return invalidError(owner, "document_type_invalid", base)
		}
		return invalidError(owner, projected.Reason(), base)
	}
	return invalidError(owner, projected.Reason(), base+projected.Pointer())
}
