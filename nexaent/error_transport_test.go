package nexaent

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func TestNexaentErrorTransportProjectsRealOwnerErrorsAndRoundTrips(t *testing.T) {
	corpus := entHelperRealErrorCorpus(t)
	if len(corpus) == 0 {
		t.Fatal("real owner error corpus is empty")
	}
	for _, test := range corpus {
		t.Run(test.name, func(t *testing.T) {
			projection, ok := ProjectEntHelperError(test.err)
			if !ok {
				t.Fatalf("ProjectEntHelperError(%s/%s %s %s) rejected a real reachable error", test.err.Code(), test.err.Reason(), test.err.Pointer(), test.err.Source())
			}
			assertProjectionTuple(t, projection, test.err.Code(), test.err.Reason(), test.err.Pointer(), test.err.Source())

			parsed, validationErr := ParseEntHelperErrorProjection(projection.Code(), projection.Reason(), projection.Pointer(), projection.Source())
			if validationErr != nil {
				t.Fatalf("ParseEntHelperErrorProjection(real tuple) field = %q", validationErr.Field())
			}
			assertProjectionTuple(t, parsed, projection.Code(), projection.Reason(), projection.Pointer(), projection.Source())

			mutations := []struct {
				name    string
				code    string
				reason  string
				pointer string
				source  string
				field   EntHelperErrorField
			}{
				{name: "code", code: "annotation_unknown", reason: projection.Reason(), pointer: projection.Pointer(), source: projection.Source(), field: EntHelperErrorFieldCode},
				{name: "reason", code: projection.Code(), reason: "reason_unknown", pointer: projection.Pointer(), source: projection.Source(), field: EntHelperErrorFieldReason},
				{name: "pointer", code: projection.Code(), reason: projection.Reason(), pointer: "not-a-pointer", source: projection.Source(), field: EntHelperErrorFieldPointer},
				{name: "source", code: projection.Code(), reason: projection.Reason(), pointer: projection.Pointer(), source: "nexa.dev/other/v1", field: EntHelperErrorFieldSource},
			}
			for _, mutation := range mutations {
				t.Run("one-member mutation/"+mutation.name, func(t *testing.T) {
					assertTransportFailure(t, mutation.code, mutation.reason, mutation.pointer, mutation.source, mutation.field)
				})
			}
		})
	}
}

func TestNexaentErrorTransportRejectsNonDirectAndUnsafePointerErrors(t *testing.T) {
	direct := realDecodeError(t, DecodeCRUD, []byte(`{"apiVersion":"`+CRUDAnnotationName+`","kind":"`+CRUDAnnotationKind+`","payload":{"operations":[]}}`))
	var typedNil *Error
	tests := []struct {
		name string
		err  error
	}{
		{name: "nil"},
		{name: "typed nil", err: typedNil},
		{name: "wrapped", err: fmt.Errorf("wrapped: %w", direct)},
		{name: "foreign", err: errors.New("foreign")},
		{name: "zero owner error", err: &Error{}},
		{name: "unsafe dynamic pointer", err: invalidError(ownerSchema, "unicode_invalid", "/bad~2pointer")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection, ok := ProjectEntHelperError(test.err)
			if ok {
				t.Fatal("ProjectEntHelperError accepted a non-direct error or unsafe pointer")
			}
			assertProjectionTuple(t, projection, "", "", "", "")
		})
	}
}

func TestNexaentErrorTransportValidationOrderAndTypedField(t *testing.T) {
	valid := []string{"annotation_invalid", "document_invalid", "", SchemaAnnotationName}
	tests := []struct {
		name    string
		code    string
		reason  string
		pointer string
		source  string
		field   EntHelperErrorField
	}{
		{name: "zero tuple", field: EntHelperErrorFieldCode},
		{name: "single code", code: "bad", reason: valid[1], pointer: valid[2], source: valid[3], field: EntHelperErrorFieldCode},
		{name: "single reason", code: valid[0], reason: "bad", pointer: valid[2], source: valid[3], field: EntHelperErrorFieldReason},
		{name: "single pointer", code: valid[0], reason: valid[1], pointer: "bad", source: valid[3], field: EntHelperErrorFieldPointer},
		{name: "single source", code: valid[0], reason: valid[1], pointer: valid[2], source: "nexa.dev/other/v1", field: EntHelperErrorFieldSource},
		{name: "code wins", code: "bad", reason: "bad", pointer: "bad", source: "/bad", field: EntHelperErrorFieldCode},
		{name: "reason wins", code: valid[0], reason: "bad", pointer: "bad", source: "/bad", field: EntHelperErrorFieldReason},
		{name: "pointer wins", code: valid[0], reason: valid[1], pointer: "bad", source: "/bad", field: EntHelperErrorFieldPointer},
		{name: "source wins", code: valid[0], reason: valid[1], pointer: valid[2], source: "/bad", field: EntHelperErrorFieldSource},
		{name: "owner reason relation", code: valid[0], reason: "crud_operations_empty", pointer: "/payload/operations", source: SchemaAnnotationName, field: EntHelperErrorFieldReason},
		{name: "owner pointer relation", code: valid[0], reason: "enum_invalid", pointer: "/payload/identity", source: FieldAnnotationName, field: EntHelperErrorFieldPointer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertTransportFailure(t, test.code, test.reason, test.pointer, test.source, test.field)
		})
	}

	var nilValidation *EntHelperErrorValidationError
	if got := nilValidation.Field(); got != EntHelperErrorFieldNone {
		t.Fatalf("nil validation Field() = %q", got)
	}
	if got := (&EntHelperErrorValidationError{}).Field(); got != EntHelperErrorFieldNone {
		t.Fatalf("zero validation Field() = %q", got)
	}
	if got := (EntHelperErrorProjection{}); got.Code() != "" || got.Reason() != "" || got.Pointer() != "" || got.Source() != "" {
		t.Fatalf("zero projection accessors are non-empty: %#v", got)
	}
}

func TestNexaentErrorTransportPointerBoundaryAndCanonicalGrammar(t *testing.T) {
	validPointer := "/" + strings.Repeat("a", MaxEntHelperErrorPointerBytes-1)
	if len([]byte(validPointer)) != MaxEntHelperErrorPointerBytes {
		t.Fatalf("valid pointer fixture length = %d", len([]byte(validPointer)))
	}
	projection, validationErr := ParseEntHelperErrorProjection("annotation_invalid", "document_duplicate_key", validPointer, SchemaAnnotationName)
	if validationErr != nil || projection.Pointer() != validPointer {
		t.Fatalf("max-size canonical pointer rejected: %q, %v", projection.Pointer(), validationErr)
	}

	for _, pointer := range []string{
		"/" + strings.Repeat("a", MaxEntHelperErrorPointerBytes),
		string([]byte{'/', 0xff}),
		"/e\u0301",
		"/control\x7f",
		"relative",
		"/~",
		"/~2",
	} {
		t.Run(fmt.Sprintf("%x", pointer), func(t *testing.T) {
			assertTransportFailure(t, "annotation_invalid", "document_duplicate_key", pointer, SchemaAnnotationName, EntHelperErrorFieldPointer)
		})
	}
}

func TestNexaentErrorTransportOwnerOverlapAndNonOverlap(t *testing.T) {
	schemaLocalized := realAnnotationError(t, Schema(SchemaMeta{}))
	fieldMeta := validFieldMeta(t)
	fieldMeta.Label.Key = ""
	fieldLocalized := realAnnotationError(t, Field(fieldMeta))
	if schemaLocalized.Reason() != fieldLocalized.Reason() || schemaLocalized.Pointer() != fieldLocalized.Pointer() {
		t.Fatalf("overlap fixtures differ: %#v %#v", schemaLocalized, fieldLocalized)
	}
	assertRealTupleAcceptsSource(t, schemaLocalized, FieldAnnotationName)
	assertRealTupleAcceptsSource(t, fieldLocalized, SchemaAnnotationName)

	for _, source := range []string{SchemaAnnotationName, FieldAnnotationName, CRUDAnnotationName} {
		err := realDecodeErrorForOwner(t, source, []byte(`{`))
		assertRealTupleAcceptsSource(t, err, SchemaAnnotationName)
		assertRealTupleAcceptsSource(t, err, FieldAnnotationName)
		assertRealTupleAcceptsSource(t, err, CRUDAnnotationName)
	}

	schemaMeta := validSchemaMeta()
	schemaMeta.Identity = IdentityStrategy("unknown")
	schemaEnum := realAnnotationError(t, Schema(schemaMeta))
	assertTransportFailure(t, schemaEnum.Code(), schemaEnum.Reason(), schemaEnum.Pointer(), FieldAnnotationName, EntHelperErrorFieldPointer)

	crudEmpty := realAnnotationError(t, CRUD())
	assertTransportFailure(t, crudEmpty.Code(), crudEmpty.Reason(), crudEmpty.Pointer(), SchemaAnnotationName, EntHelperErrorFieldReason)
}

func TestNexaentErrorTransportRejectsUnsafeRealDecoderDynamicPointers(t *testing.T) {
	tests := []struct {
		name   string
		decode func([]byte) error
		key    string
		check  func(*testing.T, string)
	}{
		{
			name: "schema over limit", decode: decodeSchemaError, key: strings.Repeat("a", MaxEntHelperErrorPointerBytes),
			check: func(t *testing.T, pointer string) {
				if got := len([]byte(pointer)); got != MaxEntHelperErrorPointerBytes+1 {
					t.Fatalf("over-limit decoder pointer length = %d", got)
				}
			},
		},
		{
			name: "field non NFC", decode: decodeFieldError, key: "e\u0301",
			check: func(t *testing.T, pointer string) {
				if norm.NFC.IsNormalString(pointer) {
					t.Fatalf("decoder pointer is unexpectedly NFC: %q", pointer)
				}
			},
		},
		{
			name: "crud Cc", decode: decodeCRUDError, key: `\u007f`,
			check: func(t *testing.T, pointer string) {
				containsCc := false
				for _, character := range pointer {
					containsCc = containsCc || unicode.Is(unicode.Cc, character)
				}
				if !containsCc {
					t.Fatalf("decoder pointer contains no Cc: %q", pointer)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ownerErr := requireOwnerError(t, test.decode(duplicateRootKeyDocument(test.key)))
			if ownerErr.Reason() != "document_duplicate_key" {
				t.Fatalf("dynamic fixture reason = %q", ownerErr.Reason())
			}
			test.check(t, ownerErr.Pointer())
			projection, ok := ProjectEntHelperError(ownerErr)
			if ok {
				t.Fatal("unsafe real decoder pointer crossed helper transport")
			}
			assertProjectionTuple(t, projection, "", "", "", "")
		})
	}

	for _, decode := range []func([]byte) error{decodeSchemaError, decodeFieldError, decodeCRUDError} {
		ownerErr := requireOwnerError(t, decode(duplicateRootKeyDocument(strings.Repeat("a", MaxEntHelperErrorPointerBytes-1))))
		if got := len([]byte(ownerErr.Pointer())); got != MaxEntHelperErrorPointerBytes {
			t.Fatalf("safe dynamic pointer length = %d", got)
		}
		projection, ok := ProjectEntHelperError(ownerErr)
		if !ok {
			t.Fatal("safe max-size real decoder pointer was rejected")
		}
		parsed, validationErr := ParseEntHelperErrorProjection(projection.Code(), projection.Reason(), projection.Pointer(), projection.Source())
		if validationErr != nil {
			t.Fatalf("safe max-size projection parse field = %q", validationErr.Field())
		}
		assertProjectionTuple(t, parsed, projection.Code(), projection.Reason(), projection.Pointer(), projection.Source())
	}
}

type realEntHelperErrorCase struct {
	name string
	err  *Error
}

func entHelperRealErrorCorpus(t *testing.T) []realEntHelperErrorCase {
	t.Helper()
	var corpus []realEntHelperErrorCase
	add := func(name string, err *Error) { corpus = append(corpus, realEntHelperErrorCase{name: name, err: err}) }
	owners := []struct {
		name   string
		source string
		kind   string
		decode func([]byte) error
		valid  string
	}{
		{name: "schema", source: SchemaAnnotationName, kind: SchemaAnnotationKind, decode: decodeSchemaError, valid: validSchemaEnvelope()},
		{name: "field", source: FieldAnnotationName, kind: FieldAnnotationKind, decode: decodeFieldError, valid: fieldEnvelope(validFieldPayload())},
		{name: "crud", source: CRUDAnnotationName, kind: CRUDAnnotationKind, decode: decodeCRUDError, valid: crudEnvelope(`{"operations":["get"]}`)},
	}
	for _, owner := range owners {
		add(owner.name+" duplicate", realDecodeErrorRaw(t, owner.decode, []byte(`{"apiVersion":"`+owner.source+`","kind":"`+owner.kind+`","duplicate":true}`)))
		add(owner.name+" document invalid", realDecodeErrorRaw(t, owner.decode, []byte(`{`)))
		add(owner.name+" trailing", realDecodeErrorRaw(t, owner.decode, []byte(owner.valid+`{}`)))
		add(owner.name+" version", realDecodeErrorRaw(t, owner.decode, []byte(strings.Replace(owner.valid, owner.source, "nexa.dev/unsupported/v1", 1))))
		add(owner.name+" kind", realDecodeErrorRaw(t, owner.decode, []byte(strings.Replace(owner.valid, owner.kind, "Unsupported", 1))))
		add(owner.name+" invalid sentinel reason", realDecodeErrorRaw(t, owner.decode, []byte(invalidEnvelope(owner.source, owner.kind, "unsupported", ""))))
		add(owner.name+" invalid sentinel pointer", realDecodeErrorRaw(t, owner.decode, []byte(invalidEnvelope(owner.source, owner.kind, ownerSemanticReason(owner.source), "/not/reachable"))))
		add(owner.name+" duplicate key", realDecodeErrorRaw(t, owner.decode, []byte(`{"apiVersion":"`+owner.source+`","apiVersion":"`+owner.source+`","kind":"`+owner.kind+`","payload":{}}`)))
		add(owner.name+" root unicode", realDecodeErrorRaw(t, owner.decode, append([]byte(`{"`), append([]byte{0xff}, []byte(`":1}`)...)...)))
		add(owner.name+" root unknown", realDecodeErrorRaw(t, owner.decode, []byte(`{"unknown":1,"apiVersion":"`+owner.source+`","kind":"`+owner.kind+`","payload":{}}`)))
		add(owner.name+" root required", realDecodeErrorRaw(t, owner.decode, []byte(`{}`)))
		add(owner.name+" root type", realDecodeErrorRaw(t, owner.decode, []byte(`{"apiVersion":1,"kind":"`+owner.kind+`","payload":{}}`)))
	}

	add("invalid object unknown", realDecodeErrorRaw(t, decodeSchemaError, []byte(`{"apiVersion":"`+SchemaAnnotationName+`","kind":"`+SchemaAnnotationKind+`","invalid":{"extra":1}}`)))
	add("invalid object required", realDecodeErrorRaw(t, decodeSchemaError, []byte(`{"apiVersion":"`+SchemaAnnotationName+`","kind":"`+SchemaAnnotationKind+`","invalid":{}}`)))
	add("invalid object type", realDecodeErrorRaw(t, decodeSchemaError, []byte(`{"apiVersion":"`+SchemaAnnotationName+`","kind":"`+SchemaAnnotationKind+`","invalid":false}`)))

	add("schema payload unknown", realDecodeErrorRaw(t, decodeSchemaError, []byte(schemaEnvelope(`{"label":{"key":"l","zhCN":"l","enUS":"l"},"description":{"key":"d","zhCN":"d","enUS":"d"},"identity":"ent-id","scope":"tenant","extra":1}`))))
	add("schema localized unknown", realDecodeErrorRaw(t, decodeSchemaError, []byte(schemaEnvelope(`{"label":{"key":"l","zhCN":"l","enUS":"l","extra":1},"description":{"key":"d","zhCN":"d","enUS":"d"},"identity":"ent-id","scope":"tenant"}`))))
	add("schema localized required", realDecodeErrorRaw(t, decodeSchemaError, []byte(schemaEnvelope(`{"label":{"zhCN":"l","enUS":"l"},"description":{"key":"d","zhCN":"d","enUS":"d"},"identity":"ent-id","scope":"tenant"}`))))
	add("schema localized type", realDecodeErrorRaw(t, decodeSchemaError, []byte(schemaEnvelope(`{"label":[],"description":{"key":"d","zhCN":"d","enUS":"d"},"identity":"ent-id","scope":"tenant"}`))))

	add("field payload unknown", realDecodeErrorRaw(t, decodeFieldError, []byte(fieldEnvelope(fieldPayloadWith(`"extra":1,`)))))
	add("field localized unknown", realDecodeErrorRaw(t, decodeFieldError, []byte(strings.Replace(fieldEnvelope(validFieldPayload()), `"label":{"key":"label","zhCN":"label zh","enUS":"label en"}`, `"label":{"key":"label","zhCN":"label zh","enUS":"label en","extra":1}`, 1))))
	add("field physical display unknown", realDecodeErrorRaw(t, decodeFieldError, []byte(fieldEnvelope(fieldPayloadWith(`"physicalDisplay":{"field":"name","extra":1},`)))))
	add("field legacy source unknown", realDecodeErrorRaw(t, decodeFieldError, []byte(fieldEnvelope(fieldPayloadWith(`"sourceBinding":{"kind":"canonical","ref":"repo:a","digest":"sha256:`+strings.Repeat("a", 64)+`"},`)))))
	add("field crud unknown", realDecodeErrorRaw(t, decodeFieldError, []byte(strings.Replace(fieldEnvelope(validFieldPayload()), `"crud":{"read":"include","mutation":"create-update"}`, `"crud":{"read":"include","mutation":"create-update","extra":1}`, 1))))
	add("field physical display required", realDecodeErrorRaw(t, decodeFieldError, []byte(fieldEnvelope(fieldPayloadWith(`"physicalDisplay":{},`)))))
	add("field crud type", realDecodeErrorRaw(t, decodeFieldError, []byte(strings.Replace(fieldEnvelope(validFieldPayload()), `"crud":{"read":"include","mutation":"create-update"}`, `"crud":[]`, 1))))

	add("crud payload unknown", realDecodeErrorRaw(t, decodeCRUDError, []byte(crudEnvelope(`{"operations":["get"],"extra":1}`))))
	add("crud operations required", realDecodeErrorRaw(t, decodeCRUDError, []byte(crudEnvelope(`{}`))))
	add("crud operations type", realDecodeErrorRaw(t, decodeCRUDError, []byte(crudEnvelope(`{"operations":{}}`))))
	add("crud operation index zero", realDecodeErrorRaw(t, decodeCRUDError, []byte(crudEnvelope(`{"operations":[1]}`))))
	add("crud operation index one", realDecodeErrorRaw(t, decodeCRUDError, []byte(invalidEnvelope(CRUDAnnotationName, CRUDAnnotationKind, "crud_operation_duplicate", "/payload/operations/1"))))
	add("crud operation index twelve", realDecodeErrorRaw(t, decodeCRUDError, []byte(invalidEnvelope(CRUDAnnotationName, CRUDAnnotationKind, "enum_invalid", "/payload/operations/12"))))

	schemaMeta := validSchemaMeta()
	schemaMeta.Label.Key = ""
	add("schema localized semantic", realAnnotationError(t, Schema(schemaMeta)))
	schemaMeta = validSchemaMeta()
	schemaMeta.Identity = IdentityStrategy("unsupported")
	add("schema enum semantic", realAnnotationError(t, Schema(schemaMeta)))
	schemaMeta = validSchemaMeta()
	schemaMeta.Scope = RecordScope(string([]byte{0xff}))
	add("schema unicode semantic", realAnnotationError(t, Schema(schemaMeta)))

	fieldMeta := validFieldMeta(t)
	fieldMeta.Label.Key = ""
	add("field localized semantic", realAnnotationError(t, Field(fieldMeta)))
	fieldMeta = validFieldMeta(t)
	fieldMeta.UIHint = UIHint("unsupported")
	add("field enum semantic", realAnnotationError(t, Field(fieldMeta)))
	fieldMeta = validFieldMeta(t)
	fieldMeta.PhysicalDisplay.Field = string([]byte{0xff})
	add("field unicode semantic", realAnnotationError(t, Field(fieldMeta)))
	fieldMeta = validFieldMeta(t)
	fieldMeta.PhysicalDisplay.Field = ""
	add("field reference semantic", realAnnotationError(t, Field(fieldMeta)))
	fieldMeta = validFieldMeta(t)
	fieldMeta.Visibility = VisibilitySensitive
	fieldMeta.CRUD.Read = ReadInclude
	add("field policy semantic", realAnnotationError(t, Field(fieldMeta)))

	add("crud empty semantic", realAnnotationError(t, CRUD()))
	add("crud enum semantic", realAnnotationError(t, CRUD(CRUDOperation("unsupported"))))
	add("crud duplicate semantic", realAnnotationError(t, CRUD(CRUDGet, CRUDGet)))
	add("crud unicode semantic", realAnnotationError(t, CRUD(CRUDOperation(string([]byte{0xff})))))
	return corpus
}

func ownerSemanticReason(source string) string {
	switch source {
	case SchemaAnnotationName:
		return "enum_invalid"
	case FieldAnnotationName:
		return "reference_invalid"
	default:
		return "crud_operations_empty"
	}
}

func realDecodeErrorForOwner(t *testing.T, source string, data []byte) *Error {
	t.Helper()
	switch source {
	case SchemaAnnotationName:
		return realDecodeErrorRaw(t, decodeSchemaError, data)
	case FieldAnnotationName:
		return realDecodeErrorRaw(t, decodeFieldError, data)
	default:
		return realDecodeErrorRaw(t, decodeCRUDError, data)
	}
}

func realDecodeError[T any](t *testing.T, decode func([]byte) (T, error), data []byte) *Error {
	t.Helper()
	_, err := decode(data)
	return requireOwnerError(t, err)
}

func realDecodeErrorRaw(t *testing.T, decode func([]byte) error, data []byte) *Error {
	t.Helper()
	return requireOwnerError(t, decode(data))
}

func realAnnotationError(t *testing.T, annotation Annotation) *Error {
	t.Helper()
	_, err := annotation.CanonicalJSON()
	return requireOwnerError(t, err)
}

func requireOwnerError(t *testing.T, err error) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("fixture produced no owner error")
	}
	typed, ok := err.(*Error)
	if !ok || typed == nil {
		t.Fatalf("fixture error type = %T", err)
	}
	return typed
}

func assertProjectionTuple(t *testing.T, projection EntHelperErrorProjection, code, reason, pointer, source string) {
	t.Helper()
	if got := []string{projection.Code(), projection.Reason(), projection.Pointer(), projection.Source()}; !reflect.DeepEqual(got, []string{code, reason, pointer, source}) {
		t.Fatalf("projection = %#v, want %#v", got, []string{code, reason, pointer, source})
	}
}

func assertTransportFailure(t *testing.T, code, reason, pointer, source string, want EntHelperErrorField) {
	t.Helper()
	projection, validationErr := ParseEntHelperErrorProjection(code, reason, pointer, source)
	assertProjectionTuple(t, projection, "", "", "", "")
	if validationErr == nil {
		t.Fatalf("ParseEntHelperErrorProjection(%q, %q, %q, %q) succeeded", code, reason, pointer, source)
	}
	if got := validationErr.Field(); got != want {
		t.Fatalf("validation field = %q, want %q", got, want)
	}
}

func assertRealTupleAcceptsSource(t *testing.T, real *Error, source string) {
	t.Helper()
	projection, validationErr := ParseEntHelperErrorProjection(real.Code(), real.Reason(), real.Pointer(), source)
	if validationErr != nil {
		t.Fatalf("owner overlap rejected with field %q", validationErr.Field())
	}
	assertProjectionTuple(t, projection, real.Code(), real.Reason(), real.Pointer(), source)
}

func assertRealDecoderErrorTransportable(t *testing.T, err error, reason, pointer string) {
	t.Helper()
	ownerErr := requireOwnerError(t, err)
	if ownerErr.Reason() != reason || ownerErr.Pointer() != pointer {
		t.Fatalf("decoder error = %q %q, want %q %q", ownerErr.Reason(), ownerErr.Pointer(), reason, pointer)
	}
	projection, ok := ProjectEntHelperError(ownerErr)
	if !ok {
		t.Fatalf("ProjectEntHelperError rejected decoder error %q %q", reason, pointer)
	}
	parsed, validationErr := ParseEntHelperErrorProjection(projection.Code(), projection.Reason(), projection.Pointer(), projection.Source())
	if validationErr != nil {
		t.Fatalf("ParseEntHelperErrorProjection rejected decoder error field %q", validationErr.Field())
	}
	assertProjectionTuple(t, parsed, projection.Code(), reason, pointer, projection.Source())
}

func duplicateRootKeyDocument(encodedKey string) []byte {
	return []byte(`{"` + encodedKey + `":1,"` + encodedKey + `":2}`)
}
