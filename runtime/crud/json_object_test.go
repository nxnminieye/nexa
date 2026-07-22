package crud

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseJSONObjectUsesStandardJSONSemantics(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "normalizes whitespace and key order", input: ` { "z" : [null,true], "a" : 1e+2 } `, want: `{"a":1e+2,"z":[null,true]}`},
		{name: "duplicate key keeps last value", input: `{"name":"old","name":"new"}`, want: `{"name":"new"}`},
		{name: "uses standard string escaping", input: `{"value":"<>&\u2028"}`, want: `{"value":"\u003c\u003e\u0026\u2028"}`},
		{name: "uses standard escaped surrogate replacement", input: `{"value":"\ud800"}`, want: `{"value":"�"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			object, err := ParseJSONObject([]byte(test.input))
			if err != nil {
				t.Fatalf("ParseJSONObject() error = %v", err)
			}
			got, err := object.String()
			if err != nil || got != test.want {
				t.Fatalf("String() = (%q, %v), want %q", got, err, test.want)
			}
		})
	}
}

func TestParseJSONObjectValidatesObjectShapeAndInputBoundary(t *testing.T) {
	tests := []struct {
		name   string
		input  []byte
		reason string
	}{
		{name: "empty", input: nil, reason: "syntax_invalid"},
		{name: "null", input: []byte(`null`), reason: "null_forbidden"},
		{name: "array", input: []byte(`[]`), reason: "root_not_object"},
		{name: "scalar", input: []byte(`true`), reason: "root_not_object"},
		{name: "syntax", input: []byte(`{"bad":`), reason: "syntax_invalid"},
		{name: "raw invalid UTF-8", input: []byte{'{', '"', 0xff, '"', ':', '1', '}'}, reason: "unicode_invalid"},
		{name: "trailing", input: []byte(`{} false`), reason: "trailing_input"},
		{name: "oversize", input: make([]byte, maxJSONObjectBytes+1), reason: "size_limit_exceeded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseJSONObject(test.input)
			assertJSONObjectError(t, err, ErrJSONObjectInvalid, "json_object_invalid", test.reason)
		})
	}
}

func TestJSONObjectDefensivelyCopiesAndSupportsJSON(t *testing.T) {
	input := []byte(`{"b":2,"a":1}`)
	object, err := ParseJSONObject(input)
	if err != nil {
		t.Fatal(err)
	}
	input[2] = 'x'
	first, err := object.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	first[2] = 'x'
	second, _ := object.Bytes()
	if string(second) != `{"a":1,"b":2}` {
		t.Fatalf("Bytes() = %q", second)
	}

	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	var decoded JSONObject
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	decodedText, _ := decoded.String()
	if decodedText != string(second) {
		t.Fatalf("round trip = %q", decodedText)
	}
}

func TestNewJSONObjectUsesStandardMarshalAndRejectsNil(t *testing.T) {
	object, err := NewJSONObject(map[string]any{"b": 2, "a": "value"})
	if err != nil {
		t.Fatal(err)
	}
	text, _ := object.String()
	if text != `{"a":"value","b":2}` {
		t.Fatalf("String() = %q", text)
	}
	var nilMap map[string]any
	_, err = NewJSONObject(nilMap)
	assertJSONObjectError(t, err, ErrJSONObjectEncodeFailed, "json_object_encode_failed", "input_nil")
	_, err = NewJSONObject(func() {})
	assertJSONObjectError(t, err, ErrJSONObjectEncodeFailed, "json_object_encode_failed", "marshal_failed")
}

func TestJSONObjectSQLInterfacesAndTransactionalScan(t *testing.T) {
	var _ json.Marshaler = JSONObject{}
	var _ json.Unmarshaler = (*JSONObject)(nil)
	var _ driver.Valuer = JSONObject{}
	var _ sql.Scanner = (*JSONObject)(nil)

	object, err := ParseJSONObject([]byte(`{"keep":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := object.Scan([]byte(`{"b":2,"a":1}`)); err != nil {
		t.Fatal(err)
	}
	value, err := object.Value()
	if err != nil || value != `{"a":1,"b":2}` {
		t.Fatalf("Value() = (%v, %v)", value, err)
	}
	before, _ := object.String()
	if err := object.Scan([]byte(`{"bad":`)); err == nil {
		t.Fatal("Scan(invalid) succeeded")
	}
	after, _ := object.String()
	if after != before {
		t.Fatalf("failed Scan mutated receiver: %q != %q", after, before)
	}
	if err := object.Scan(nil); err == nil {
		t.Fatal("Scan(nil) succeeded")
	}
	if err := object.Scan(12); err == nil {
		t.Fatal("Scan(int) succeeded")
	}
}

func TestJSONObjectZeroAndNilReceiverFailures(t *testing.T) {
	var zero JSONObject
	_, err := zero.Bytes()
	assertJSONObjectError(t, err, ErrJSONObjectInvalid, "json_object_invalid", "zero_value")
	_, err = zero.Value()
	assertJSONObjectError(t, err, ErrJSONObjectInvalid, "json_object_invalid", "zero_value")
	var nilObject *JSONObject
	assertJSONObjectError(t, nilObject.UnmarshalJSON([]byte(`{}`)), ErrJSONObjectInvalid, "json_object_invalid", "receiver_nil")
	assertJSONObjectError(t, nilObject.Scan(`{}`), ErrJSONObjectInvalid, "json_object_invalid", "receiver_nil")
}

func TestJSONObjectRawSizeLimit(t *testing.T) {
	exact := []byte(`{"x":"` + strings.Repeat("a", maxJSONObjectBytes-len(`{"x":""}`)) + `"}`)
	if len(exact) != maxJSONObjectBytes {
		t.Fatalf("fixture size = %d", len(exact))
	}
	if _, err := ParseJSONObject(exact); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}
	_, err := ParseJSONObject(append(exact, ' '))
	assertJSONObjectError(t, err, ErrJSONObjectInvalid, "json_object_invalid", "size_limit_exceeded")
}

func assertJSONObjectError(t *testing.T, err, sentinel error, code, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s/%s", code, reason)
	}
	var typed *Error
	if !errors.As(err, &typed) || !errors.Is(err, sentinel) || typed.Code() != code || typed.Reason() != reason || typed.Pointer() != "" {
		t.Fatalf("error = %T %v projection=(%q,%q,%q)", err, err, typed.Code(), typed.Reason(), typed.Pointer())
	}
}

func assertCRUDerror(t *testing.T, err, sentinel error, code, reason, pointer string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s/%s at %q", code, reason, pointer)
	}
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("error = %T %v, want *crud.Error", err, err)
	}
	if !errors.Is(err, sentinel) || typed.Code() != code || typed.Reason() != reason || typed.Pointer() != pointer {
		t.Fatalf("error projection = (%q,%q,%q), want (%q,%q,%q)", typed.Code(), typed.Reason(), typed.Pointer(), code, reason, pointer)
	}
}
