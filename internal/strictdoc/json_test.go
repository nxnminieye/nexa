package strictdoc_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/internal/strictdoc"
)

type fixture struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Count   int      `json:"count"`
	Items   []string `json:"items"`
}

type nestedFixture struct {
	Parent nestedParent `json:"parent"`
}

type nestedParent struct {
	Count int `json:"count"`
}

type exactCaseDestination struct {
	Name string `json:"name"`
}

type supportedDestination struct {
	Child    *supportedChild `json:"child,omitempty"`
	Groups   [][]int16       `json:"groups"`
	Enabled  bool            `json:"enabled"`
	Name     string          `json:"name"`
	Signed   int32           `json:"signed"`
	Unsigned uint32          `json:"unsigned"`
	Ratio    float64         `json:"ratio"`
}

type supportedChild struct {
	Values []string `json:"values"`
}

type numericDestination struct {
	Signed   int8    `json:"signed"`
	Unsigned uint8   `json:"unsigned"`
	Ratio    float32 `json:"ratio"`
}

type nestedSliceDestination struct {
	Groups [][]int8 `json:"groups"`
}

type stringOptionDestination struct {
	Count int `json:"count,string"`
}

type byteSliceDestination struct {
	Data []byte `json:"data"`
}

type rawMessageDestination struct {
	Data json.RawMessage `json:"data"`
}

type mapDestination struct {
	Values map[string]string `json:"values"`
}

type interfaceDestination struct {
	Value any `json:"value"`
}

type arrayDestination struct {
	Values [2]string `json:"values"`
}

type embeddedMember struct {
	Name string `json:"name"`
}

type embeddedDestination struct {
	embeddedMember
}

type customJSONScalar string

func (*customJSONScalar) UnmarshalJSON([]byte) error { return nil }

type customJSONDestination struct {
	Value customJSONScalar `json:"value"`
}

type customTextScalar string

func (*customTextScalar) UnmarshalText([]byte) error { return nil }

type customTextDestination struct {
	Value customTextScalar `json:"value"`
}

type duplicateNameDestination struct {
	Value  string
	Second string `json:"Value"`
}

type recursiveEmbeddedDestination struct {
	*recursiveEmbeddedDestination
	Value string `json:"value"`
}

type invalidTagDestination struct {
	Name string `json:"na\\me"`
}

type DominantEmbedded struct {
	Name string `json:"name"`
}

type dominantDestination struct {
	DominantEmbedded
	Name string `json:"name"`
}

type mapChildDestination struct {
	Children map[string]exactCaseDestination `json:"children"`
}

type rootDecodeFailureDestination struct {
	marshalCalls *int
}

func (*rootDecodeFailureDestination) UnmarshalJSON([]byte) error {
	return errors.New("root decode failure")
}

func (d *rootDecodeFailureDestination) MarshalJSON() ([]byte, error) {
	*d.marshalCalls++
	return []byte(`{}`), nil
}

func TestDecodeJSONRejectsDuplicateKeys(t *testing.T) {
	var target fixture
	err := strictdoc.DecodeJSON("fixture.json", []byte(`{
  "name": "first",
  "name": "second"
}`), &target)
	projected := assertDocumentError(t, err, "document_duplicate_key", "/name")
	if projected.Line != 3 || projected.Column != 9 {
		t.Fatalf("duplicate location = %d:%d, want existing 3:9 behavior", projected.Line, projected.Column)
	}
}

func TestDecodeJSONRejectsInvalidUnicodeInput(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "raw invalid UTF-8", data: []byte{'{', '"', 'n', 'a', 'm', 'e', '"', ':', '"', 0xff, '"', '}'}},
		{name: "unpaired high surrogate", data: []byte(`{"name":"\ud800"}`)},
		{name: "unpaired low surrogate", data: []byte(`{"name":"\udc00"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var target exactCaseDestination
			err := strictdoc.DecodeJSON("fixture.json", test.data, &target)
			assertDocumentError(t, err, "document_unicode_invalid", "")
		})
	}
}

func TestParseJSONLocationUsesEscapedPointerAndValueStart(t *testing.T) {
	document, err := strictdoc.ParseJSON("fixture.json", []byte("{\n  \"a/b\": {\"~key\": 1}\n}"))
	if err != nil {
		t.Fatal(err)
	}
	line, column, ok := document.Location("/a~1b/~0key")
	if !ok || line != 2 || column != 19 {
		t.Fatalf("location = %d:%d,%v, want 2:19,true", line, column, ok)
	}
}

func TestDecodeJSONRejectsTrailingValue(t *testing.T) {
	var target fixture
	err := strictdoc.DecodeJSON("fixture.json", []byte(`{"name":"first"} {"name":"second"}`), &target)
	assertDocumentError(t, err, "document_trailing_input", "")
}

func TestDecodeJSONRejectsUnknownTypedField(t *testing.T) {
	var target fixture
	err := strictdoc.DecodeJSON("fixture.json", []byte(`{"name":"first","extra":true}`), &target)
	assertDocumentError(t, err, "document_unknown_field", "/extra")
}

func TestDecodeJSONReportsNestedUnknownFieldPointer(t *testing.T) {
	var target nestedFixture
	err := strictdoc.DecodeJSON(
		"fixture.json",
		[]byte(`{"parent":{"count":1,"extra":true}}`),
		&target,
	)
	projected := assertDocumentError(t, err, "document_unknown_field", "/parent/extra")
	if projected.Line <= 0 || projected.Column <= 0 {
		t.Fatalf("typed JSON error location = %d:%d", projected.Line, projected.Column)
	}
}

func TestDecodeJSONUsesStandardCaseInsensitiveMemberMatching(t *testing.T) {
	var target exactCaseDestination
	if err := strictdoc.DecodeJSON("fixture.json", []byte(`{"Name":"accepted"}`), &target); err != nil {
		t.Fatal(err)
	}
	if target.Name != "accepted" {
		t.Fatalf("Name = %q", target.Name)
	}
}

func TestDecodeJSONExactRejectsCaseVariantMembers(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		target  any
		pointer string
	}{
		{name: "top level", data: `{"Name":"accepted"}`, target: &exactCaseDestination{}, pointer: "/Name"},
		{name: "nested", data: `{"Parent":{"count":1}}`, target: &nestedFixture{}, pointer: "/Parent"},
		{name: "nested member", data: `{"parent":{"Count":1}}`, target: &nestedFixture{}, pointer: "/parent/Count"},
		{name: "case variant double write", data: `{"name":"first","Name":"second"}`, target: &exactCaseDestination{}, pointer: "/Name"},
		{name: "top level wrong type", data: `{"Name":1}`, target: &exactCaseDestination{}, pointer: "/Name"},
		{name: "nested wrong type", data: `{"parent":{"Count":"bad"}}`, target: &nestedFixture{}, pointer: "/parent/Count"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := strictdoc.DecodeJSONExact("fixture.json", []byte(test.data), test.target)
			assertDocumentError(t, err, "document_unknown_field", test.pointer)
		})
	}
}

func TestDecodeJSONExactDelegatesFieldSelectionToEncodingJSON(t *testing.T) {
	t.Run("anonymous self recursion", func(t *testing.T) {
		var target recursiveEmbeddedDestination
		if err := strictdoc.DecodeJSONExact("fixture.json", []byte(`{"value":"accepted"}`), &target); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("invalid tag fallback", func(t *testing.T) {
		var exact invalidTagDestination
		if err := strictdoc.DecodeJSONExact("fixture.json", []byte(`{"Name":"accepted"}`), &exact); err != nil {
			t.Fatal(err)
		}
		var variant invalidTagDestination
		err := strictdoc.DecodeJSONExact("fixture.json", []byte(`{"name":"accepted"}`), &variant)
		assertDocumentError(t, err, "document_unknown_field", "/name")
	})

	t.Run("embedded dominance", func(t *testing.T) {
		var target dominantDestination
		if err := strictdoc.DecodeJSONExact("fixture.json", []byte(`{"name":"outer"}`), &target); err != nil {
			t.Fatal(err)
		}
		if target.Name != "outer" || target.DominantEmbedded.Name != "" {
			t.Fatalf("dominant decode = %#v", target)
		}
	})

	t.Run("map child case variant", func(t *testing.T) {
		var base mapChildDestination
		if err := strictdoc.DecodeJSON("fixture.json", []byte(`{"children":{"one":{"Name":"accepted"}}}`), &base); err != nil {
			t.Fatal(err)
		}
		if base.Children["one"].Name != "accepted" {
			t.Fatalf("base map child decode = %#v", base.Children)
		}
		var target mapChildDestination
		err := strictdoc.DecodeJSONExact("fixture.json", []byte(`{"children":{"one":{"Name":"accepted"}}}`), &target)
		assertDocumentError(t, err, "document_unknown_field", "/children/one/Name")
	})
}

func TestDecodeJSONExactDoesNotProjectIneligibleDecodeFailures(t *testing.T) {
	t.Run("non pointer destination", func(t *testing.T) {
		target := exactCaseDestination{}
		err := strictdoc.DecodeJSONExact("fixture.json", []byte(`{"Name":"accepted"}`), target)
		assertDocumentError(t, err, "destination_invalid", "")
	})

	t.Run("root custom unmarshal failure", func(t *testing.T) {
		marshalCalls := 0
		target := &rootDecodeFailureDestination{marshalCalls: &marshalCalls}
		err := strictdoc.DecodeJSONExact("fixture.json", []byte(`{"Name":"accepted"}`), target)
		assertDocumentError(t, err, "document_invalid", "")
		if marshalCalls != 0 {
			t.Fatalf("MarshalJSON calls = %d, want 0", marshalCalls)
		}
	})
}

func TestDecodeJSONUnicodeLexingRegression(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "escaped quote", data: `{"name":"a\"b"}`, want: `a"b`},
		{name: "escaped backslash", data: `{"name":"a\\b"}`, want: `a\b`},
		{name: "legal surrogate pair", data: `{"name":"\ud83d\ude00"}`, want: "😀"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var target fixture
			if err := strictdoc.DecodeJSON("fixture.json", []byte(test.data), &target); err != nil {
				t.Fatal(err)
			}
			if target.Name != test.want {
				t.Fatalf("Name = %q, want %q", target.Name, test.want)
			}
		})
	}
	var target fixture
	err := strictdoc.DecodeJSON("fixture.json", []byte(`{"name":"ok","count":\ud800}`), &target)
	assertDocumentError(t, err, "document_invalid", "/count")
}

func TestDecodeJSONSupportsClosedDestinationSubset(t *testing.T) {
	document := []byte(`{
  "child": {"values": ["one", "two"]},
  "groups": [[1, 2], [3]],
  "enabled": true,
  "name": "sample",
  "signed": -42,
  "unsigned": 42,
  "ratio": 1.25
}`)
	var target supportedDestination
	if err := strictdoc.DecodeJSON("fixture.json", document, &target); err != nil {
		t.Fatal(err)
	}
	if target.Child == nil || len(target.Child.Values) != 2 || target.Groups[1][0] != 3 ||
		!target.Enabled || target.Name != "sample" || target.Signed != -42 ||
		target.Unsigned != 42 || target.Ratio != 1.25 {
		t.Fatalf("decoded target = %#v", target)
	}
}

func TestDecodeJSONUsesStandardCustomUnmarshaler(t *testing.T) {
	var target customJSONDestination
	if err := strictdoc.DecodeJSON("fixture.json", []byte(`{"value":"accepted"}`), &target); err != nil {
		t.Fatalf("DecodeJSON() rejected a standard json.Unmarshaler: %v", err)
	}
}

func TestDecodeJSONReportsNumericRangePointers(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		pointer string
	}{
		{name: "signed high", data: `{"signed":128}`, pointer: "/signed"},
		{name: "signed low", data: `{"signed":-129}`, pointer: "/signed"},
		{name: "unsigned negative", data: `{"unsigned":-1}`, pointer: "/unsigned"},
		{name: "unsigned high", data: `{"unsigned":256}`, pointer: "/unsigned"},
		{name: "float overflow", data: `{"ratio":1e100}`, pointer: "/ratio"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target numericDestination
			err := strictdoc.DecodeJSON("fixture.json", []byte(tt.data), &target)
			assertDocumentError(t, err, "document_invalid", tt.pointer)
		})
	}
}

func TestDecodeJSONReportsNestedSliceElementPointer(t *testing.T) {
	var target nestedSliceDestination
	err := strictdoc.DecodeJSON("fixture.json", []byte(`{"groups":[[1,128]]}`), &target)
	assertDocumentError(t, err, "document_invalid", "/groups")
}

func TestDecodeJSONReportsPointersForEverySupportedShape(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		pointer string
	}{
		{name: "bool", data: `{"enabled":"true"}`, pointer: "/enabled"},
		{name: "string", data: `{"name":true}`, pointer: "/name"},
		{name: "pointer to struct", data: `{"child":"bad"}`, pointer: "/child"},
		{name: "slice", data: `{"groups":"bad"}`, pointer: "/groups"},
		{name: "nested slice scalar", data: `{"child":{"values":[true]}}`, pointer: "/child/values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target supportedDestination
			err := strictdoc.DecodeJSON("fixture.json", []byte(tt.data), &target)
			assertDocumentError(t, err, "document_invalid", tt.pointer)
		})
	}
}

func TestDecodeJSONRejectsEmptyInput(t *testing.T) {
	var target fixture
	err := strictdoc.DecodeJSON("fixture.json", []byte(" \n\t"), &target)
	assertDocumentError(t, err, "document_invalid", "")
}

func TestDecodeJSONAndYAMLProduceEquivalentTypedValues(t *testing.T) {
	jsonDocument := []byte(`{
  "name": "sample",
  "enabled": true,
  "count": 2,
  "items": ["one", "two"]
}`)
	yamlDocument := []byte("name: \"sample\"\nenabled: true\ncount: 2\nitems:\n  - \"one\"\n  - \"two\"\n")

	var fromJSON fixture
	if err := strictdoc.DecodeJSON("fixture.json", jsonDocument, &fromJSON); err != nil {
		t.Fatal(err)
	}
	var fromYAML fixture
	if err := strictdoc.DecodeYAML("fixture.yaml", yamlDocument, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Fatalf("decoded values differ: %#v != %#v", fromJSON, fromYAML)
	}
}

func assertDocumentError(t *testing.T, err error, code, pointer string) *strictdoc.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("decode succeeded, want %s", code)
	}
	var documentError *strictdoc.Error
	if !errors.As(err, &documentError) {
		t.Fatalf("error type = %T, want *strictdoc.Error", err)
	}
	if documentError.Code != code || documentError.Pointer != pointer {
		t.Fatalf("error = %#v, want code %q pointer %q", documentError, code, pointer)
	}
	if documentError.Source == "" {
		t.Fatal("error source is empty")
	}
	return documentError
}
