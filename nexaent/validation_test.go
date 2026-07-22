package nexaent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStrictEnvelopeAndBranchErrorMatrix(t *testing.T) {
	const apiVersion = CRUDAnnotationName
	const kind = CRUDAnnotationKind
	normal := `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":["get"]}}`
	tests := []struct {
		name    string
		data    string
		code    string
		reason  string
		pointer string
	}{
		{name: "empty", data: ``, code: "annotation_invalid", reason: "document_invalid"},
		{name: "malformed", data: `{`, code: "annotation_invalid", reason: "document_invalid"},
		{name: "null root", data: `null`, code: "annotation_invalid", reason: "document_invalid"},
		{name: "array root", data: `[]`, code: "annotation_invalid", reason: "document_invalid"},
		{name: "scalar root", data: `"value"`, code: "annotation_invalid", reason: "document_invalid"},
		{name: "duplicate top key", data: `{"apiVersion":"` + apiVersion + `","apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "document_duplicate_key", pointer: "/apiVersion"},
		{name: "duplicate nested key", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":["get"],"operations":["list"]}}`, code: "annotation_invalid", reason: "document_duplicate_key", pointer: "/payload/operations"},
		{name: "trailing document", data: normal + `{}`, code: "annotation_invalid", reason: "document_trailing_input"},
		{name: "unknown field", data: `{"z":1,"a":2,"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/a"},
		{name: "unknown huge number keeps unknown priority", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":["get"]},"extra":1e1000}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/extra"},
		{name: "top unknown before nested", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"inner":1,"operations":["get"]},"outer":1}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/outer"},
		{name: "nested unknown field", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"z":1,"a":2,"operations":["get"]}}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/payload/a"},
		{name: "missing api version", data: `{}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/apiVersion"},
		{name: "null api version", data: `{"apiVersion":null,"kind":"` + kind + `","payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/apiVersion"},
		{name: "missing kind", data: `{"apiVersion":"` + apiVersion + `","payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/kind"},
		{name: "null kind", data: `{"apiVersion":"` + apiVersion + `","kind":null,"payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/kind"},
		{name: "missing normal payload", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `"}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/payload"},
		{name: "null normal payload", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":null}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/payload"},
		{name: "scalar normal payload", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":false}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/payload"},
		{name: "array normal payload", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":[]}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/payload"},
		{name: "wrong api version type precedes kind", data: `{"apiVersion":1,"kind":1,"payload":[]}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/apiVersion"},
		{name: "wrong kind type precedes payload", data: `{"apiVersion":"` + apiVersion + `","kind":1,"payload":[]}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/kind"},
		{name: "unsupported version", data: `{"apiVersion":"nexa.dev/ent-crud/v2","kind":"` + kind + `","payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "version_unsupported", pointer: "/apiVersion"},
		{name: "wrong kind", data: `{"apiVersion":"` + apiVersion + `","kind":"Wrong","payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "kind_invalid", pointer: "/kind"},
		{name: "duplicate false", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","duplicate":false}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/duplicate"},
		{name: "duplicate null", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","duplicate":null}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/duplicate"},
		{name: "duplicate scalar", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","duplicate":"true"}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/duplicate"},
		{name: "duplicate with payload", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","duplicate":true,"payload":{"operations":["get"]}}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/payload"},
		{name: "duplicate exact", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","duplicate":true}`, code: "annotation_duplicate", reason: "duplicate_annotation", pointer: "/duplicate"},
		{name: "invalid null", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":null}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/invalid"},
		{name: "invalid false", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":false}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/invalid"},
		{name: "invalid array", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":[]}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/invalid"},
		{name: "invalid string", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":"bad"}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/invalid"},
		{name: "invalid selects over duplicate", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":"crud_operations_empty","pointer":"/payload/operations"},"duplicate":true}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/duplicate"},
		{name: "invalid forbidden members", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{},"duplicate":true,"invalid":{"reason":"crud_operations_empty","pointer":"/payload/operations"}}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/duplicate"},
		{name: "invalid nested unknown", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"unknown":1},"payload":{}}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/invalid/unknown"},
		{name: "invalid unknown", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"z":1,"a":2}}`, code: "annotation_invalid", reason: "document_unknown_field", pointer: "/invalid/a"},
		{name: "invalid missing reason", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{}}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/invalid/reason"},
		{name: "invalid null reason", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":null,"pointer":"/payload/operations"}}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/invalid/reason"},
		{name: "invalid missing pointer", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":"crud_operations_empty"}}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/invalid/pointer"},
		{name: "invalid null pointer", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":"crud_operations_empty","pointer":null}}`, code: "annotation_invalid", reason: "document_required_missing", pointer: "/invalid/pointer"},
		{name: "invalid reason type", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":1,"pointer":"/payload/operations"}}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/invalid/reason"},
		{name: "invalid pointer type", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":"crud_operations_empty","pointer":1}}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/invalid/pointer"},
		{name: "invalid unsupported reason", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":"document_invalid","pointer":""}}`, code: "annotation_invalid", reason: "invalid_sentinel_invalid", pointer: "/invalid/reason"},
		{name: "invalid mismatched pointer", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","invalid":{"reason":"crud_operations_empty","pointer":"/payload/operations/0"}}`, code: "annotation_invalid", reason: "invalid_sentinel_invalid", pointer: "/invalid/pointer"},
		{name: "operations wrong type", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":{}}}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/payload/operations"},
		{name: "operation member wrong type", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":[1]}}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/payload/operations"},
		{name: "huge operation number keeps type priority", data: `{"apiVersion":"` + apiVersion + `","kind":"` + kind + `","payload":{"operations":[1e1000]}}`, code: "annotation_invalid", reason: "document_type_invalid", pointer: "/payload/operations"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeCRUD([]byte(test.data))
			assertTypedError(t, err, test.code, test.reason, test.pointer, CRUDAnnotationName)
		})
	}
}

func TestOwnerSpecificNormalPayloadStructuralMatrix(t *testing.T) {
	tests := []struct {
		name    string
		decode  func([]byte) error
		data    string
		reason  string
		pointer string
		source  string
	}{
		{name: "schema label type", decode: decodeSchemaError, data: schemaEnvelope(`{"label":[],"description":{"key":"d","zhCN":"d","enUS":"d"},"identity":"ent-id","scope":"tenant"}`), reason: "document_type_invalid", pointer: "/payload/label", source: SchemaAnnotationName},
		{name: "schema nested null", decode: decodeSchemaError, data: schemaEnvelope(`{"label":{"key":null,"zhCN":"l","enUS":"l"},"description":{"key":"d","zhCN":"d","enUS":"d"},"identity":"ent-id","scope":"tenant"}`), reason: "document_required_missing", pointer: "/payload/label/key", source: SchemaAnnotationName},
		{name: "field physical display null", decode: decodeFieldError, data: fieldEnvelope(fieldPayloadWith(`"physicalDisplay":null,`)), reason: "document_type_invalid", pointer: "/payload/physicalDisplay", source: FieldAnnotationName},
		{name: "field legacy source unknown", decode: decodeFieldError, data: fieldEnvelope(fieldPayloadWith(`"sourceBinding":null,`)), reason: "document_unknown_field", pointer: "/payload/sourceBinding", source: FieldAnnotationName},
		{name: "field crud type", decode: decodeFieldError, data: fieldEnvelope(strings.Replace(validFieldPayload(), `"crud":{"read":"include","mutation":"create-update"}`, `"crud":[]`, 1)), reason: "document_type_invalid", pointer: "/payload/crud", source: FieldAnnotationName},
		{name: "field missing physical display field", decode: decodeFieldError, data: fieldEnvelope(fieldPayloadWith(`"physicalDisplay":{},`)), reason: "document_required_missing", pointer: "/payload/physicalDisplay/field", source: FieldAnnotationName},
		{name: "field missing crud read", decode: decodeFieldError, data: fieldEnvelope(strings.Replace(validFieldPayload(), `"crud":{"read":"include","mutation":"create-update"}`, `"crud":{"mutation":"create-update"}`, 1)), reason: "document_required_missing", pointer: "/payload/crud/read", source: FieldAnnotationName},
		{name: "crud missing operations", decode: decodeCRUDError, data: crudEnvelope(`{}`), reason: "document_required_missing", pointer: "/payload/operations", source: CRUDAnnotationName},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTypedError(t, test.decode([]byte(test.data)), "annotation_invalid", test.reason, test.pointer, test.source)
		})
	}

	meta, err := DecodeField([]byte(fieldEnvelope(validFieldPayload())))
	if err != nil {
		t.Fatalf("DecodeField(optional fields absent) error = %v", err)
	}
	if meta.PhysicalDisplay != nil || meta.LogicalReference != nil {
		t.Fatalf("optional references = %#v %#v, want nil", meta.PhysicalDisplay, meta.LogicalReference)
	}
}

func TestEveryRequiredMemberMissingAndNullUsesItsExactPointer(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		decode   func([]byte) error
		source   string
		pointers []string
	}{
		{
			name: "schema", base: validSchemaEnvelope(), decode: decodeSchemaError, source: SchemaAnnotationName,
			pointers: []string{
				"/payload/label", "/payload/label/key", "/payload/label/zhCN", "/payload/label/enUS",
				"/payload/description", "/payload/description/key", "/payload/description/zhCN", "/payload/description/enUS",
				"/payload/identity", "/payload/scope",
			},
		},
		{
			name: "field", base: validFieldEnvelopeWithOptionals(t), decode: decodeFieldError, source: FieldAnnotationName,
			pointers: []string{
				"/payload/label", "/payload/label/key", "/payload/label/zhCN", "/payload/label/enUS",
				"/payload/description", "/payload/description/key", "/payload/description/zhCN", "/payload/description/enUS",
				"/payload/uiHint",
				"/payload/physicalDisplay/field",
				"/payload/visibility", "/payload/crud/read", "/payload/crud/mutation",
			},
		},
		{name: "crud", base: crudEnvelope(`{"operations":["get"]}`), decode: decodeCRUDError, source: CRUDAnnotationName, pointers: []string{"/payload/operations"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, pointer := range test.pointers {
				t.Run("missing "+pointer, func(t *testing.T) {
					data := mutateJSONPointer(t, test.base, pointer, nil, true)
					assertTypedError(t, test.decode(data), "annotation_invalid", "document_required_missing", pointer, test.source)
				})
				t.Run("null "+pointer, func(t *testing.T) {
					data := mutateJSONPointer(t, test.base, pointer, nil, false)
					assertTypedError(t, test.decode(data), "annotation_invalid", "document_required_missing", pointer, test.source)
				})
			}
		})
	}

	for _, pointer := range []string{"/payload/physicalDisplay", "/payload/crud"} {
		data := mutateJSONPointer(t, validFieldEnvelopeWithOptionals(t), pointer, nil, false)
		assertTypedError(t, decodeFieldError(data), "annotation_invalid", "document_type_invalid", pointer, FieldAnnotationName)
	}
}

func TestEveryTypedMemberRejectsWrongJSONTypeAtItsExactPointer(t *testing.T) {
	type typeCase struct {
		name        string
		base        string
		decode      func([]byte) error
		source      string
		pointer     string
		replacement any
	}
	var tests []typeCase
	for _, pointer := range []string{"/payload/label", "/payload/description"} {
		tests = append(tests, typeCase{name: "schema " + pointer, base: validSchemaEnvelope(), decode: decodeSchemaError, source: SchemaAnnotationName, pointer: pointer, replacement: []any{}})
	}
	for _, pointer := range []string{
		"/payload/label/key", "/payload/label/zhCN", "/payload/label/enUS",
		"/payload/description/key", "/payload/description/zhCN", "/payload/description/enUS",
		"/payload/identity", "/payload/scope",
	} {
		tests = append(tests, typeCase{name: "schema " + pointer, base: validSchemaEnvelope(), decode: decodeSchemaError, source: SchemaAnnotationName, pointer: pointer, replacement: float64(1)})
	}
	for _, pointer := range []string{"/payload/label", "/payload/description", "/payload/physicalDisplay", "/payload/crud"} {
		tests = append(tests, typeCase{name: "field " + pointer, base: validFieldEnvelopeWithOptionals(t), decode: decodeFieldError, source: FieldAnnotationName, pointer: pointer, replacement: []any{}})
	}
	for _, pointer := range []string{
		"/payload/label/key", "/payload/label/zhCN", "/payload/label/enUS",
		"/payload/description/key", "/payload/description/zhCN", "/payload/description/enUS",
		"/payload/uiHint",
		"/payload/physicalDisplay/field",
		"/payload/visibility", "/payload/crud/read", "/payload/crud/mutation",
	} {
		tests = append(tests, typeCase{name: "field " + pointer, base: validFieldEnvelopeWithOptionals(t), decode: decodeFieldError, source: FieldAnnotationName, pointer: pointer, replacement: float64(1)})
	}
	tests = append(tests,
		typeCase{name: "crud operations", base: crudEnvelope(`{"operations":["get"]}`), decode: decodeCRUDError, source: CRUDAnnotationName, pointer: "/payload/operations", replacement: map[string]any{}},
		typeCase{name: "crud operation member", base: crudEnvelope(`{"operations":["get"]}`), decode: decodeCRUDError, source: CRUDAnnotationName, pointer: "/payload/operations", replacement: []any{float64(1)}},
	)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := mutateJSONPointer(t, test.base, test.pointer, test.replacement, false)
			assertTypedError(t, test.decode(data), "annotation_invalid", "document_type_invalid", test.pointer, test.source)
		})
	}
}

func TestConstructorInvalidSentinelAndEquivalentNormalEnvelopeParity(t *testing.T) {
	field := validFieldMeta(t)
	tests := []struct {
		name       string
		annotation Annotation
		normal     string
		decode     func([]byte) error
		code       string
		reason     string
		pointer    string
		source     string
	}{
		{name: "schema localized", annotation: Schema(SchemaMeta{Label: LocalizedText{ZhCN: "label", EnUS: "label"}, Description: validText("description"), Identity: IdentityEntID, Scope: ScopeTenant}), normal: schemaEnvelope(`{"label":{"key":"","zhCN":"label","enUS":"label"},"description":{"key":"description","zhCN":"description zh","enUS":"description en"},"identity":"ent-id","scope":"tenant"}`), decode: decodeSchemaError, code: "annotation_invalid", reason: "localized_text_invalid", pointer: "/payload/label/key", source: SchemaAnnotationName},
		{name: "schema enum", annotation: Schema(SchemaMeta{Label: validText("label"), Description: validText("description"), Identity: IdentityStrategy("unknown"), Scope: ScopeTenant}), normal: schemaEnvelope(`{"label":{"key":"label","zhCN":"label zh","enUS":"label en"},"description":{"key":"description","zhCN":"description zh","enUS":"description en"},"identity":"unknown","scope":"tenant"}`), decode: decodeSchemaError, code: "annotation_invalid", reason: "enum_invalid", pointer: "/payload/identity", source: SchemaAnnotationName},
		{name: "field localized", annotation: Field(func() FieldMeta { value := field; value.Label.Key = " "; return value }()), normal: fieldEnvelope(strings.Replace(validFieldPayload(), `"key":"label"`, `"key":" "`, 1)), decode: decodeFieldError, code: "annotation_invalid", reason: "localized_text_invalid", pointer: "/payload/label/key", source: FieldAnnotationName},
		{name: "field ui enum", annotation: Field(func() FieldMeta { value := field; value.UIHint = UIHint("unknown"); return value }()), normal: fieldEnvelope(strings.Replace(validFieldPayload(), `"uiHint":"text"`, `"uiHint":"unknown"`, 1)), decode: decodeFieldError, code: "annotation_invalid", reason: "enum_invalid", pointer: "/payload/uiHint", source: FieldAnnotationName},
		{name: "field physical display", annotation: Field(func() FieldMeta {
			value := field
			value.PhysicalDisplay = &PhysicalDisplay{Field: " "}
			return value
		}()), normal: fieldEnvelope(fieldPayloadWith(`"physicalDisplay":{"field":" "},`)), decode: decodeFieldError, code: "annotation_invalid", reason: "reference_invalid", pointer: "/payload/physicalDisplay/field", source: FieldAnnotationName},
		{name: "field logical reference", annotation: Field(func() FieldMeta {
			value := field
			value.PhysicalDisplay = nil
			value.LogicalReference = &LogicalReference{Target: " ", Display: "name"}
			return value
		}()), normal: fieldEnvelope(fieldPayloadWith(`"logicalReference":{"target":" ","display":"name"},`)), decode: decodeFieldError, code: "annotation_invalid", reason: "reference_invalid", pointer: "/payload/logicalReference/target", source: FieldAnnotationName},
		{name: "field policy", annotation: Field(func() FieldMeta {
			value := field
			value.PhysicalDisplay = nil
			value.Visibility = VisibilitySensitive
			value.CRUD = &CRUDFieldPolicy{Read: ReadInclude, Mutation: MutationNone}
			return value
		}()), normal: fieldEnvelope(strings.NewReplacer(`"visibility":"public"`, `"visibility":"sensitive"`, `"mutation":"create-update"`, `"mutation":"none"`).Replace(validFieldPayload())), decode: decodeFieldError, code: "annotation_invalid", reason: "policy_conflict", pointer: "/payload/crud", source: FieldAnnotationName},
		{name: "crud empty", annotation: CRUD(), normal: crudEnvelope(`{"operations":[]}`), decode: decodeCRUDError, code: "annotation_invalid", reason: "crud_operations_empty", pointer: "/payload/operations", source: CRUDAnnotationName},
		{name: "crud enum", annotation: CRUD(CRUDOperation("unknown")), normal: crudEnvelope(`{"operations":["unknown"]}`), decode: decodeCRUDError, code: "annotation_invalid", reason: "enum_invalid", pointer: "/payload/operations/0", source: CRUDAnnotationName},
		{name: "crud duplicate", annotation: CRUD(CRUDGet, CRUDGet), normal: crudEnvelope(`{"operations":["get","get"]}`), decode: decodeCRUDError, code: "annotation_invalid", reason: "crud_operation_duplicate", pointer: "/payload/operations/1", source: CRUDAnnotationName},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			canonical, canonicalErr := test.annotation.CanonicalJSON()
			if canonical != nil {
				t.Fatalf("invalid CanonicalJSON() bytes = %s", canonical)
			}
			assertTypedError(t, canonicalErr, test.code, test.reason, test.pointer, test.source)

			transport, err := json.Marshal(test.annotation)
			if err != nil {
				t.Fatalf("json.Marshal(invalid annotation) error = %v", err)
			}
			assertPayloadFreeInvalidSentinel(t, transport, test.reason, test.pointer)
			assertTypedError(t, test.decode(transport), test.code, test.reason, test.pointer, test.source)
			assertTypedError(t, test.decode([]byte(test.normal)), test.code, test.reason, test.pointer, test.source)

			outer, err := json.Marshal(struct {
				Annotation Annotation `json:"annotation"`
			}{Annotation: test.annotation})
			if err != nil {
				t.Fatalf("outer json.Marshal() error = %v", err)
			}
			var projected struct {
				Annotation json.RawMessage `json:"annotation"`
			}
			if err := json.Unmarshal(outer, &projected); err != nil {
				t.Fatalf("outer json.Unmarshal() error = %v", err)
			}
			assertTypedError(t, test.decode(projected.Annotation), test.code, test.reason, test.pointer, test.source)
		})
	}
}

func TestConstructorsRejectInvalidUTF8AtEveryPublicStringMember(t *testing.T) {
	invalid := string([]byte{0xff})

	for _, object := range []string{"label", "description"} {
		for _, member := range []string{"key", "zhCN", "enUS"} {
			pointer := "/payload/" + object + "/" + member
			t.Run("schema"+pointer, func(t *testing.T) {
				meta := validSchemaMeta()
				text := &meta.Label
				if object == "description" {
					text = &meta.Description
				}
				setLocalizedMember(text, member, invalid)
				assertInvalidUnicodeAnnotation(t, Schema(meta), pointer, SchemaAnnotationName)
			})
			t.Run("field"+pointer, func(t *testing.T) {
				meta := validFieldMeta(t)
				text := &meta.Label
				if object == "description" {
					text = &meta.Description
				}
				setLocalizedMember(text, member, invalid)
				assertInvalidUnicodeAnnotation(t, Field(meta), pointer, FieldAnnotationName)
			})
		}
	}

	schemaCases := []struct {
		name    string
		pointer string
		mutate  func(*SchemaMeta)
	}{
		{name: "identity", pointer: "/payload/identity", mutate: func(meta *SchemaMeta) { meta.Identity = IdentityStrategy(invalid) }},
		{name: "scope", pointer: "/payload/scope", mutate: func(meta *SchemaMeta) { meta.Scope = RecordScope(invalid) }},
	}
	for _, test := range schemaCases {
		t.Run("schema/"+test.name, func(t *testing.T) {
			meta := validSchemaMeta()
			test.mutate(&meta)
			assertInvalidUnicodeAnnotation(t, Schema(meta), test.pointer, SchemaAnnotationName)
		})
	}

	fieldCases := []struct {
		name    string
		pointer string
		mutate  func(*FieldMeta)
	}{
		{name: "ui hint", pointer: "/payload/uiHint", mutate: func(meta *FieldMeta) { meta.UIHint = UIHint(invalid) }},
		{name: "physical display field", pointer: "/payload/physicalDisplay/field", mutate: func(meta *FieldMeta) { meta.PhysicalDisplay.Field = invalid }},
		{name: "logical target", pointer: "/payload/logicalReference/target", mutate: func(meta *FieldMeta) {
			meta.PhysicalDisplay = nil
			meta.LogicalReference = &LogicalReference{Target: invalid, Display: "name"}
		}},
		{name: "logical display", pointer: "/payload/logicalReference/display", mutate: func(meta *FieldMeta) {
			meta.PhysicalDisplay = nil
			meta.LogicalReference = &LogicalReference{Target: "Account", Display: invalid}
		}},
		{name: "visibility", pointer: "/payload/visibility", mutate: func(meta *FieldMeta) { meta.Visibility = FieldVisibility(invalid) }},
		{name: "read policy", pointer: "/payload/crud/read", mutate: func(meta *FieldMeta) { meta.CRUD.Read = ReadPolicy(invalid) }},
		{name: "mutation policy", pointer: "/payload/crud/mutation", mutate: func(meta *FieldMeta) { meta.CRUD.Mutation = MutationPolicy(invalid) }},
	}
	for _, test := range fieldCases {
		t.Run("field/"+test.name, func(t *testing.T) {
			meta := validFieldMeta(t)
			test.mutate(&meta)
			assertInvalidUnicodeAnnotation(t, Field(meta), test.pointer, FieldAnnotationName)
		})
	}

	assertInvalidUnicodeAnnotation(t, CRUD(CRUDOperation(invalid)), "/payload/operations/0", CRUDAnnotationName)
}

func TestInvalidUTF8CannotCollideWithLegalReplacementCharacter(t *testing.T) {
	for _, invalidBytes := range [][]byte{{0xff}, {0xfe}, {0xc0, 0xaf}} {
		meta := validSchemaMeta()
		meta.Label.EnUS = string(invalidBytes)
		annotation := Schema(meta)
		canonical, err := annotation.CanonicalJSON()
		if canonical != nil {
			t.Fatalf("invalid UTF-8 %x produced canonical bytes %s", invalidBytes, canonical)
		}
		assertTypedError(t, err, "annotation_invalid", "unicode_invalid", "/payload/label/enUS", SchemaAnnotationName)
	}

	meta := validSchemaMeta()
	meta.Label.EnUS = "\uFFFD"
	annotation := Schema(meta)
	canonical, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatalf("legal U+FFFD CanonicalJSON() error = %v", err)
	}
	if !bytes.Contains(canonical, []byte("\uFFFD")) {
		t.Fatalf("legal U+FFFD missing from canonical JSON: %x", canonical)
	}
	transport, err := json.Marshal(annotation)
	if err != nil {
		t.Fatalf("legal U+FFFD transport error = %v", err)
	}
	decoded, err := DecodeSchema(transport)
	if err != nil || decoded.Label.EnUS != "\uFFFD" {
		t.Fatalf("legal U+FFFD round trip = %q, %v", decoded.Label.EnUS, err)
	}
}

func TestRawJSONRejectsInvalidUTF8WithoutPrivateFieldPointers(t *testing.T) {
	data := rawJSONStringAtPointer(t, validSchemaEnvelope(), "/payload/label/key", []byte{0xff})
	assertSafeUnicodeError(t, decodeSchemaError(data), "", SchemaAnnotationName)
}

func TestRawJSONSurrogateValidityAndLegalSupplementaryCharacters(t *testing.T) {
	const pointer = "/payload/label/enUS"
	for _, escaped := range [][]byte{
		[]byte(`\uD800`),
		[]byte(`\uDC00`),
		[]byte(`\uD800\u0041`),
		[]byte(`\uDC00\uD800`),
	} {
		t.Run(string(escaped), func(t *testing.T) {
			data := rawJSONStringAtPointer(t, validSchemaEnvelope(), pointer, escaped)
			assertSafeUnicodeError(t, decodeSchemaError(data), "", SchemaAnnotationName)
		})
	}

	for _, test := range []struct {
		name    string
		escaped []byte
		want    string
	}{
		{name: "supplementary pair", escaped: []byte(`\uD83D\uDE00`), want: "😀"},
		{name: "replacement character", escaped: []byte(`\uFFFD`), want: "\uFFFD"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := rawJSONStringAtPointer(t, validSchemaEnvelope(), pointer, test.escaped)
			decoded, err := DecodeSchema(data)
			if err != nil || decoded.Label.EnUS != test.want {
				t.Fatalf("DecodeSchema() label.enUS = %q, %v; want %q", decoded.Label.EnUS, err, test.want)
			}
			canonical, err := Schema(decoded).CanonicalJSON()
			if err != nil || !bytes.Contains(canonical, []byte(test.want)) {
				t.Fatalf("legal Unicode canonical JSON = %x, %v", canonical, err)
			}
		})
	}
}

func TestEveryClosedEnumValueIsAccepted(t *testing.T) {
	for _, identity := range []IdentityStrategy{IdentityEntID} {
		meta := validSchemaMeta()
		meta.Identity = identity
		assertAnnotationValid(t, Schema(meta), decodeSchemaError)
	}
	for _, scope := range []RecordScope{ScopeGlobal, ScopeTenant} {
		meta := validSchemaMeta()
		meta.Scope = scope
		assertAnnotationValid(t, Schema(meta), decodeSchemaError)
	}
	uiHints := []UIHint{UIHintText, UIHintTextarea, UIHintNumber, UIHintSwitch, UIHintSelect, UIHintMultiSelect, UIHintDatetime, UIHintJSON, UIHintReadonly, UIHintSensitive, UIHintMember, UIHintReference, UIHintAttachment, UIHintTags, UIHintComponent, UIHintI18n, UIHintIconify, UIHintPermission, UIHintRoute, UIHintScope, UIHintHTTPMethod, UIHintHTTPPath, UIHintModule, UIHintLocale, UIHintTimezone}
	for _, hint := range uiHints {
		meta := validFieldMeta(t)
		meta.UIHint = hint
		assertAnnotationValid(t, Field(meta), decodeFieldError)
	}
	meta := validFieldMeta(t)
	meta.PhysicalDisplay = nil
	meta.LogicalReference = &LogicalReference{Target: "Account", Display: "name"}
	assertAnnotationValid(t, Field(meta), decodeFieldError)
	for _, visibility := range []FieldVisibility{VisibilityPublic, VisibilityInternal, VisibilitySensitive} {
		meta := validFieldMeta(t)
		meta.Visibility = visibility
		if visibility == VisibilityInternal {
			meta.CRUD.Mutation = MutationNone
		}
		if visibility == VisibilitySensitive {
			meta.CRUD.Read = ReadExclude
		}
		assertAnnotationValid(t, Field(meta), decodeFieldError)
	}
	for _, read := range []ReadPolicy{ReadInclude, ReadExclude} {
		meta := validFieldMeta(t)
		meta.CRUD.Read = read
		assertAnnotationValid(t, Field(meta), decodeFieldError)
	}
	for _, mutation := range []MutationPolicy{MutationNone, MutationCreate, MutationUpdate, MutationCreateUpdate} {
		meta := validFieldMeta(t)
		meta.CRUD.Mutation = mutation
		assertAnnotationValid(t, Field(meta), decodeFieldError)
	}
	for _, operation := range AllCRUDOperations() {
		assertAnnotationValid(t, CRUD(operation), decodeCRUDError)
	}
}

func TestGenericLocaleAndTimezoneUIHintsStrictCanonicalRoundTrip(t *testing.T) {
	for _, hint := range []UIHint{UIHintLocale, UIHintTimezone} {
		t.Run(string(hint), func(t *testing.T) {
			meta := validFieldMeta(t)
			meta.UIHint = hint
			canonical, err := Field(meta).CanonicalJSON()
			if err != nil {
				t.Fatalf("CanonicalJSON() error = %v", err)
			}
			wireValue := []byte(`"uiHint":"` + string(hint) + `"`)
			if !bytes.Contains(canonical, wireValue) {
				t.Fatalf("canonical annotation = %s, want exact %s", canonical, wireValue)
			}

			decoded, err := DecodeField(canonical)
			if err != nil {
				t.Fatalf("DecodeField() error = %v", err)
			}
			if decoded.UIHint != hint {
				t.Fatalf("DecodeField().UIHint = %q, want %q", decoded.UIHint, hint)
			}
			roundTrip, err := Field(decoded).CanonicalJSON()
			if err != nil || !bytes.Equal(roundTrip, canonical) {
				t.Fatalf("canonical round trip = %s, %v; want byte-identical %s", roundTrip, err, canonical)
			}
		})
	}
}

func TestUnknownAndZeroEnumValuesFailAtExactPointers(t *testing.T) {
	tests := []struct {
		name    string
		build   func(string) Annotation
		base    string
		decode  func([]byte) error
		pointer string
		source  string
	}{
		{
			name: "identity", base: validSchemaEnvelope(), decode: decodeSchemaError,
			build: func(value string) Annotation {
				meta := validSchemaMeta()
				meta.Identity = IdentityStrategy(value)
				return Schema(meta)
			},
			pointer: "/payload/identity", source: SchemaAnnotationName,
		},
		{
			name: "scope", base: validSchemaEnvelope(), decode: decodeSchemaError,
			build: func(value string) Annotation {
				meta := validSchemaMeta()
				meta.Scope = RecordScope(value)
				return Schema(meta)
			},
			pointer: "/payload/scope", source: SchemaAnnotationName,
		},
		{
			name: "ui hint", base: validFieldEnvelopeWithOptionals(t), decode: decodeFieldError,
			build: func(value string) Annotation {
				meta := validFieldMeta(t)
				meta.UIHint = UIHint(value)
				return Field(meta)
			},
			pointer: "/payload/uiHint", source: FieldAnnotationName,
		},
		{
			name: "visibility", base: validFieldEnvelopeWithOptionals(t), decode: decodeFieldError,
			build: func(value string) Annotation {
				meta := validFieldMeta(t)
				meta.Visibility = FieldVisibility(value)
				return Field(meta)
			},
			pointer: "/payload/visibility", source: FieldAnnotationName,
		},
		{
			name: "read", base: validFieldEnvelopeWithOptionals(t), decode: decodeFieldError,
			build: func(value string) Annotation {
				meta := validFieldMeta(t)
				meta.CRUD.Read = ReadPolicy(value)
				return Field(meta)
			},
			pointer: "/payload/crud/read", source: FieldAnnotationName,
		},
		{
			name: "mutation", base: validFieldEnvelopeWithOptionals(t), decode: decodeFieldError,
			build: func(value string) Annotation {
				meta := validFieldMeta(t)
				meta.CRUD.Mutation = MutationPolicy(value)
				return Field(meta)
			},
			pointer: "/payload/crud/mutation", source: FieldAnnotationName,
		},
		{
			name: "operation", base: crudEnvelope(`{"operations":["get"]}`), decode: decodeCRUDError,
			build:   func(value string) Annotation { return CRUD(CRUDOperation(value)) },
			pointer: "/payload/operations/0", source: CRUDAnnotationName,
		},
	}
	for _, test := range tests {
		for _, value := range []string{"", "unknown"} {
			name := "zero"
			if value != "" {
				name = "unknown"
			}
			t.Run(test.name+" "+name, func(t *testing.T) {
				_, err := test.build(value).CanonicalJSON()
				assertTypedError(t, err, "annotation_invalid", "enum_invalid", test.pointer, test.source)
				data := mutateJSONPointer(t, test.base, test.pointer, value, false)
				assertTypedError(t, test.decode(data), "annotation_invalid", "enum_invalid", test.pointer, test.source)
			})
		}
	}
}

func TestEveryLocalizedMemberBlankUsesExactConstructorAndDecoderPointer(t *testing.T) {
	for _, owner := range []string{"schema", "field"} {
		for _, object := range []string{"label", "description"} {
			for _, member := range []string{"key", "zhCN", "enUS"} {
				name := owner + " " + object + " " + member
				t.Run(name, func(t *testing.T) {
					pointer := "/payload/" + object + "/" + member
					if owner == "schema" {
						meta := validSchemaMeta()
						text := meta.Label
						if object == "description" {
							text = meta.Description
						}
						setLocalizedMember(&text, member, "")
						if object == "label" {
							meta.Label = text
						} else {
							meta.Description = text
						}
						_, err := Schema(meta).CanonicalJSON()
						assertTypedError(t, err, "annotation_invalid", "localized_text_invalid", pointer, SchemaAnnotationName)
						data := mutateJSONPointer(t, validSchemaEnvelope(), pointer, "", false)
						assertTypedError(t, decodeSchemaError(data), "annotation_invalid", "localized_text_invalid", pointer, SchemaAnnotationName)
						return
					}

					meta := validFieldMeta(t)
					text := meta.Label
					if object == "description" {
						text = meta.Description
					}
					setLocalizedMember(&text, member, "")
					if object == "label" {
						meta.Label = text
					} else {
						meta.Description = text
					}
					_, err := Field(meta).CanonicalJSON()
					assertTypedError(t, err, "annotation_invalid", "localized_text_invalid", pointer, FieldAnnotationName)
					data := mutateJSONPointer(t, validFieldEnvelopeWithOptionals(t), pointer, "", false)
					assertTypedError(t, decodeFieldError(data), "annotation_invalid", "localized_text_invalid", pointer, FieldAnnotationName)
				})
			}
		}
	}
}

func TestEveryReferenceMemberBlankUsesExactConstructorAndDecoderPointer(t *testing.T) {
	for _, member := range []string{"physicalDisplay/field", "logicalReference/target", "logicalReference/display"} {
		t.Run(member, func(t *testing.T) {
			pointer := "/payload/" + member
			meta := validFieldMeta(t)
			switch member {
			case "physicalDisplay/field":
				meta.PhysicalDisplay.Field = ""
			case "logicalReference/target":
				meta.PhysicalDisplay = nil
				meta.LogicalReference = &LogicalReference{Target: "", Display: "name"}
			case "logicalReference/display":
				meta.PhysicalDisplay = nil
				meta.LogicalReference = &LogicalReference{Target: "Account", Display: ""}
			}
			_, err := Field(meta).CanonicalJSON()
			assertTypedError(t, err, "annotation_invalid", "reference_invalid", pointer, FieldAnnotationName)
			base := validFieldEnvelopeWithOptionals(t)
			if strings.HasPrefix(member, "logicalReference/") {
				base = fieldEnvelope(fieldPayloadWith(`"logicalReference":{"target":"Account","display":"name"},`))
			}
			data := mutateJSONPointer(t, base, pointer, "", false)
			assertTypedError(t, decodeFieldError(data), "annotation_invalid", "reference_invalid", pointer, FieldAnnotationName)
		})
	}
}

func TestNestedSemanticValidationHasFixedMemberOrder(t *testing.T) {
	field := validFieldMeta(t)
	tests := []struct {
		name       string
		annotation Annotation
		reason     string
		pointer    string
		source     string
	}{
		{name: "schema label before description", annotation: Schema(SchemaMeta{Label: LocalizedText{}, Description: LocalizedText{}, Identity: "", Scope: ""}), reason: "localized_text_invalid", pointer: "/payload/label/key", source: SchemaAnnotationName},
		{name: "localized key before zh and en", annotation: Field(func() FieldMeta { value := field; value.Label = LocalizedText{}; return value }()), reason: "localized_text_invalid", pointer: "/payload/label/key", source: FieldAnnotationName},
		{name: "localized zh before en", annotation: Field(func() FieldMeta { value := field; value.Label = LocalizedText{Key: "label"}; return value }()), reason: "localized_text_invalid", pointer: "/payload/label/zhCN", source: FieldAnnotationName},
		{name: "physical before logical conflict", annotation: Field(func() FieldMeta {
			value := field
			value.LogicalReference = &LogicalReference{Target: "Account", Display: "name"}
			return value
		}()), reason: "reference_conflict", pointer: "/payload", source: FieldAnnotationName},
		{name: "crud read before mutation", annotation: Field(func() FieldMeta { value := field; value.CRUD = &CRUDFieldPolicy{}; return value }()), reason: "enum_invalid", pointer: "/payload/crud/read", source: FieldAnnotationName},
		{name: "field ui before reference", annotation: Field(func() FieldMeta {
			value := field
			value.UIHint = "unknown"
			value.PhysicalDisplay = &PhysicalDisplay{}
			return value
		}()), reason: "enum_invalid", pointer: "/payload/uiHint", source: FieldAnnotationName},
		{name: "crud operation input index", annotation: CRUD(CRUDGet, "unknown", CRUDGet), reason: "enum_invalid", pointer: "/payload/operations/1", source: CRUDAnnotationName},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.annotation.CanonicalJSON()
			assertTypedError(t, err, "annotation_invalid", test.reason, test.pointer, test.source)
		})
	}
}

func TestFieldPolicyConflictsAreClosed(t *testing.T) {
	for _, mutation := range []MutationPolicy{MutationCreate, MutationUpdate, MutationCreateUpdate} {
		meta := validFieldMeta(t)
		meta.Visibility = VisibilityInternal
		meta.CRUD.Mutation = mutation
		_, err := Field(meta).CanonicalJSON()
		assertTypedError(t, err, "annotation_invalid", "policy_conflict", "/payload/crud", FieldAnnotationName)
	}
	meta := validFieldMeta(t)
	meta.Visibility = VisibilitySensitive
	meta.CRUD.Read = ReadInclude
	meta.CRUD.Mutation = MutationNone
	_, err := Field(meta).CanonicalJSON()
	assertTypedError(t, err, "annotation_invalid", "policy_conflict", "/payload/crud", FieldAnnotationName)
}

func TestInvalidSentinelWhitelistAcceptsOnlyConstructorReachablePairs(t *testing.T) {
	localizedPointers := func() []string {
		var pointers []string
		for _, object := range []string{"label", "description"} {
			for _, member := range []string{"key", "zhCN", "enUS"} {
				pointers = append(pointers, "/payload/"+object+"/"+member)
			}
		}
		return pointers
	}
	type owner struct {
		name       string
		apiVersion string
		kind       string
		decode     func([]byte) error
		pairs      []invalidPair
	}
	schemaPairs := pairs("localized_text_invalid", localizedPointers()...)
	schemaPairs = append(schemaPairs, pairs("enum_invalid", "/payload/identity", "/payload/scope")...)
	schemaPairs = append(schemaPairs, pairs("unicode_invalid", append(localizedPointers(), "/payload/identity", "/payload/scope")...)...)
	fieldPairs := pairs("localized_text_invalid", localizedPointers()...)
	fieldPairs = append(fieldPairs, pairs("enum_invalid", "/payload/uiHint", "/payload/visibility", "/payload/crud/read", "/payload/crud/mutation")...)
	fieldPairs = append(fieldPairs, pairs("reference_invalid", "/payload/physicalDisplay/field", "/payload/logicalReference/target", "/payload/logicalReference/display")...)
	fieldPairs = append(fieldPairs, invalidPair{"reference_conflict", "/payload"})
	fieldPairs = append(fieldPairs, invalidPair{"policy_conflict", "/payload/crud"})
	fieldUnicodePointers := append(localizedPointers(), "/payload/uiHint", "/payload/physicalDisplay/field", "/payload/logicalReference/target", "/payload/logicalReference/display", "/payload/visibility", "/payload/crud/read", "/payload/crud/mutation")
	fieldPairs = append(fieldPairs, pairs("unicode_invalid", fieldUnicodePointers...)...)
	crudPairs := []invalidPair{{"crud_operations_empty", "/payload/operations"}, {"enum_invalid", "/payload/operations/0"}, {"enum_invalid", "/payload/operations/12"}, {"crud_operation_duplicate", "/payload/operations/1"}, {"crud_operation_duplicate", "/payload/operations/12"}}
	crudPairs = append(crudPairs, pairs("unicode_invalid", "/payload/operations/0", "/payload/operations/12")...)
	owners := []owner{
		{name: "schema", apiVersion: SchemaAnnotationName, kind: SchemaAnnotationKind, decode: decodeSchemaError, pairs: schemaPairs},
		{name: "field", apiVersion: FieldAnnotationName, kind: FieldAnnotationKind, decode: decodeFieldError, pairs: fieldPairs},
		{name: "crud", apiVersion: CRUDAnnotationName, kind: CRUDAnnotationKind, decode: decodeCRUDError, pairs: crudPairs},
	}
	for _, current := range owners {
		t.Run(current.name, func(t *testing.T) {
			for _, pair := range current.pairs {
				data := invalidEnvelope(current.apiVersion, current.kind, pair.reason, pair.pointer)
				assertTypedError(t, current.decode([]byte(data)), "annotation_invalid", pair.reason, pair.pointer, current.apiVersion)
			}
		})
	}
	rejected := []invalidPair{
		{"document_invalid", ""},
		{"document_unknown_field", "/x"},
		{"document_duplicate_key", "/x"},
		{"document_trailing_input", ""},
		{"version_unsupported", "/apiVersion"},
		{"kind_invalid", "/kind"},
		{"duplicate_annotation", "/duplicate"},
		{"crud_operations_empty", "/payload/operations/0"},
		{"enum_invalid", "/payload/operations/"},
		{"enum_invalid", "/payload/operations/-1"},
		{"enum_invalid", "/payload/operations/+1"},
		{"enum_invalid", "/payload/operations/01"},
		{"enum_invalid", "/payload/operations/a"},
		{"enum_invalid", "/payload/operations/~1"},
		{"enum_invalid", "/payload/operations/999999999999999999999999999999999999999999"},
		{"crud_operation_duplicate", "/payload/operations/0"},
	}
	for _, pair := range rejected {
		t.Run(pair.reason+pair.pointer, func(t *testing.T) {
			err := decodeCRUDError([]byte(invalidEnvelope(CRUDAnnotationName, CRUDAnnotationKind, pair.reason, pair.pointer)))
			pointer := "/invalid/pointer"
			if strings.HasPrefix(pair.reason, "document_") || pair.reason == "version_unsupported" || pair.reason == "kind_invalid" || pair.reason == "duplicate_annotation" {
				pointer = "/invalid/reason"
			}
			assertTypedError(t, err, "annotation_invalid", "invalid_sentinel_invalid", pointer, CRUDAnnotationName)
		})
	}
}

func assertAnnotationValid(t *testing.T, annotation Annotation, decode func([]byte) error) {
	t.Helper()
	canonical, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if len(canonical) == 0 {
		t.Fatal("CanonicalJSON() returned no bytes")
	}
	transport, err := json.Marshal(annotation)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := decode(transport); err != nil {
		t.Fatalf("decode error = %v", err)
	}
}

func assertPayloadFreeInvalidSentinel(t *testing.T, data []byte, reason, pointer string) {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("invalid sentinel is not JSON: %v", err)
	}
	if len(document) != 3 || document["payload"] != nil || document["duplicate"] != nil {
		t.Fatalf("invalid sentinel carries unexpected fields: %s", data)
	}
	var invalid map[string]string
	if err := json.Unmarshal(document["invalid"], &invalid); err != nil {
		t.Fatalf("invalid sentinel payload error = %v", err)
	}
	if !reflect.DeepEqual(invalid, map[string]string{"reason": reason, "pointer": pointer}) {
		t.Fatalf("invalid sentinel = %#v", invalid)
	}
}

func decodeSchemaError(data []byte) error { _, err := DecodeSchema(data); return err }
func decodeFieldError(data []byte) error  { _, err := DecodeField(data); return err }
func decodeCRUDError(data []byte) error   { _, err := DecodeCRUD(data); return err }

func schemaEnvelope(payload string) string {
	return `{"apiVersion":"` + SchemaAnnotationName + `","kind":"` + SchemaAnnotationKind + `","payload":` + payload + `}`
}

func fieldEnvelope(payload string) string {
	return `{"apiVersion":"` + FieldAnnotationName + `","kind":"` + FieldAnnotationKind + `","payload":` + payload + `}`
}

func crudEnvelope(payload string) string {
	return `{"apiVersion":"` + CRUDAnnotationName + `","kind":"` + CRUDAnnotationKind + `","payload":` + payload + `}`
}

func validFieldPayload() string {
	return `{"label":{"key":"label","zhCN":"label zh","enUS":"label en"},"description":{"key":"description","zhCN":"description zh","enUS":"description en"},"uiHint":"text","visibility":"public","crud":{"read":"include","mutation":"create-update"}}`
}

func fieldPayloadWith(prefix string) string {
	return `{"label":{"key":"label","zhCN":"label zh","enUS":"label en"},"description":{"key":"description","zhCN":"description zh","enUS":"description en"},"uiHint":"text",` + prefix + `"visibility":"public","crud":{"read":"include","mutation":"create-update"}}`
}

func validSchemaEnvelope() string {
	return schemaEnvelope(`{"label":{"key":"label","zhCN":"label zh","enUS":"label en"},"description":{"key":"description","zhCN":"description zh","enUS":"description en"},"identity":"ent-id","scope":"tenant"}`)
}

func validFieldEnvelopeWithOptionals(t *testing.T) string {
	t.Helper()
	payload := `{"label":{"key":"label","zhCN":"label zh","enUS":"label en"},"description":{"key":"description","zhCN":"description zh","enUS":"description en"},"uiHint":"text","physicalDisplay":{"field":"name"},"visibility":"public","crud":{"read":"include","mutation":"create-update"}}`
	return fieldEnvelope(payload)
}

func mutateJSONPointer(t *testing.T, source, pointer string, replacement any, remove bool) []byte {
	t.Helper()
	var document any
	if err := json.Unmarshal([]byte(source), &document); err != nil {
		t.Fatalf("decode mutation source: %v", err)
	}
	components := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	current := document
	for index, component := range components {
		component = strings.ReplaceAll(component, "~1", "/")
		component = strings.ReplaceAll(component, "~0", "~")
		last := index == len(components)-1
		switch value := current.(type) {
		case map[string]any:
			if _, exists := value[component]; !exists {
				t.Fatalf("mutation pointer %q does not exist in %s", pointer, source)
			}
			if last {
				if remove {
					delete(value, component)
				} else {
					value[component] = replacement
				}
				current = nil
				continue
			}
			current = value[component]
		case []any:
			arrayIndex, err := strconv.Atoi(component)
			if err != nil || arrayIndex < 0 || arrayIndex >= len(value) {
				t.Fatalf("mutation pointer %q has invalid array index", pointer)
			}
			if last {
				if remove {
					value = append(value[:arrayIndex], value[arrayIndex+1:]...)
				} else {
					value[arrayIndex] = replacement
				}
				current = nil
				continue
			}
			current = value[arrayIndex]
		default:
			t.Fatalf("mutation pointer %q crosses non-container %T", pointer, current)
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode mutated document: %v", err)
	}
	return encoded
}

func setLocalizedMember(text *LocalizedText, member, value string) {
	switch member {
	case "key":
		text.Key = value
	case "zhCN":
		text.ZhCN = value
	case "enUS":
		text.EnUS = value
	}
}

func invalidEnvelope(apiVersion, kind, reason, pointer string) string {
	return fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"invalid":{"reason":%q,"pointer":%q}}`, apiVersion, kind, reason, pointer)
}

type invalidPair struct {
	reason  string
	pointer string
}

func pairs(reason string, pointers ...string) []invalidPair {
	result := make([]invalidPair, len(pointers))
	for index, pointer := range pointers {
		result[index] = invalidPair{reason: reason, pointer: pointer}
	}
	return result
}

func assertInvalidUnicodeAnnotation(t *testing.T, annotation Annotation, pointer, source string) {
	t.Helper()
	canonical, err := annotation.CanonicalJSON()
	if canonical != nil {
		t.Fatalf("invalid Unicode produced canonical bytes %s", canonical)
	}
	assertSafeUnicodeError(t, err, pointer, source)

	transport, err := json.Marshal(annotation)
	if err != nil {
		t.Fatalf("json.Marshal(invalid Unicode annotation) error = %v", err)
	}
	if !utf8.Valid(transport) || bytes.Contains(transport, []byte("\uFFFD")) {
		t.Fatalf("invalid Unicode leaked or was replaced in sentinel transport: %x", transport)
	}
	assertPayloadFreeInvalidSentinel(t, transport, "unicode_invalid", pointer)
	assertSafeUnicodeError(t, decodeErrorForSource(source, transport), pointer, source)
}

func assertSafeUnicodeError(t *testing.T, err error, pointer, source string) {
	t.Helper()
	assertTypedError(t, err, "annotation_invalid", "unicode_invalid", pointer, source)
	if !utf8.ValidString(err.Error()) || strings.Contains(err.Error(), "\uFFFD") {
		t.Fatalf("Unicode diagnostic is not a safe closed string: %x", []byte(err.Error()))
	}
}

func decodeErrorForSource(source string, data []byte) error {
	switch source {
	case FieldAnnotationName:
		return decodeFieldError(data)
	case CRUDAnnotationName:
		return decodeCRUDError(data)
	default:
		return decodeSchemaError(data)
	}
}

func rawJSONStringAtPointer(t *testing.T, source, pointer string, content []byte) []byte {
	t.Helper()
	const marker = "__NEXA_RAW_STRING_CONTENT__"
	data := mutateJSONPointer(t, source, pointer, marker, false)
	if count := bytes.Count(data, []byte(marker)); count != 1 {
		t.Fatalf("raw string marker count = %d in %s", count, data)
	}
	return bytes.Replace(data, []byte(marker), content, 1)
}

func TestCanonicalResultsDoNotAliasTransportOrInput(t *testing.T) {
	annotation := CRUD(CRUDDelete, CRUDList)
	canonical, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := json.Marshal(annotation)
	if err != nil {
		t.Fatal(err)
	}
	canonical[0] = 'X'
	transport[0] = 'Y'
	again, err := annotation.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, []byte(`{"apiVersion":"nexa.dev/ent-crud/v1","kind":"EntCRUD","payload":{"operations":["list","delete"]}}`)) {
		t.Fatalf("caller mutation changed annotation: %s", again)
	}
}
