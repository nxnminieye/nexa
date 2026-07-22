package strictdoc_test

import (
	"testing"

	"github.com/nxnminieye/nexa/internal/strictdoc"
)

func TestDecodeYAMLRejectsForbiddenStructure(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		code    string
		pointer string
	}{
		{
			name:    "duplicate key",
			data:    "name: \"first\"\nname: \"second\"\n",
			code:    "document_duplicate_key",
			pointer: "/name",
		},
		{
			name:    "second document",
			data:    "name: first\n---\nname: second\n",
			code:    "document_trailing_input",
			pointer: "",
		},
		{
			name:    "alias",
			data:    "base: &base \"first\"\nname: *base\n",
			code:    "document_alias_forbidden",
			pointer: "/name",
		},
		{
			name:    "merge key",
			data:    "base: &base\n  name: \"first\"\n<<: *base\n",
			code:    "document_merge_key_forbidden",
			pointer: "/<<",
		},
		{
			name:    "custom tag",
			data:    "name: !secret first\n",
			code:    "document_tag_forbidden",
			pointer: "/name",
		},
		{
			name:    "alias key",
			data:    "&key name: \"first\"\n*key: \"second\"\n",
			code:    "document_alias_forbidden",
			pointer: "/name",
		},
		{
			name:    "custom tag key",
			data:    "!secret name: \"first\"\n",
			code:    "document_tag_forbidden",
			pointer: "/name",
		},
		{
			name:    "nested duplicate key",
			data:    "nested:\n  name: \"first\"\n  name: \"second\"\n",
			code:    "document_duplicate_key",
			pointer: "/nested/name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target fixture
			err := strictdoc.DecodeYAML("fixture.yaml", []byte(tt.data), &target)
			assertDocumentError(t, err, tt.code, tt.pointer)
		})
	}
}

func TestParseYAMLLocationUsesEscapedPointerAndValueStart(t *testing.T) {
	document, err := strictdoc.ParseYAML("fixture.yaml", []byte("a/b:\n  '~key': 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	line, column, ok := document.Location("/a~1b/~0key")
	if !ok || line != 2 || column != 11 {
		t.Fatalf("location = %d:%d,%v, want 2:11,true", line, column, ok)
	}
}

func TestDecodeYAMLRejectsValuesOutsideJSONSubsetWithLocation(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		pointer string
		line    int
		column  int
	}{
		{name: "non-string key", data: "1: first\n", pointer: "/1", line: 1, column: 1},
		{name: "timestamp", data: "name: 2026-07-11\n", pointer: "/name", line: 1, column: 7},
		{name: "binary", data: "name: !!binary SGVsbG8=\n", pointer: "/name", line: 1, column: 7},
		{name: "tilde null", data: "name: ~\n", pointer: "/name", line: 1, column: 7},
		{name: "NaN", data: "count: .nan\n", pointer: "/count", line: 1, column: 8},
		{name: "positive infinity", data: "count: .inf\n", pointer: "/count", line: 1, column: 8},
		{name: "negative infinity", data: "count: -.inf\n", pointer: "/count", line: 1, column: 8},
		{name: "hexadecimal", data: "count: 0x10\n", pointer: "/count", line: 1, column: 8},
		{name: "octal", data: "count: 0o10\n", pointer: "/count", line: 1, column: 8},
		{name: "numeric underscore", data: "count: 1_000\n", pointer: "/count", line: 1, column: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target fixture
			err := strictdoc.DecodeYAML("fixture.yaml", []byte(tt.data), &target)
			documentError := assertDocumentError(t, err, "document_invalid", tt.pointer)
			if documentError.Line != tt.line || documentError.Column != tt.column {
				t.Fatalf("location = %d:%d, want %d:%d", documentError.Line, documentError.Column, tt.line, tt.column)
			}
		})
	}
}

type plainContractStrings struct {
	Name       string             `json:"name"`
	Path       string             `json:"path"`
	APIVersion string             `json:"apiVersion"`
	Nested     plainNestedStrings `json:"nested"`
	Items      []string           `json:"items"`
}

type plainNestedStrings struct {
	Value string `json:"value"`
}

func TestDecodeYAMLAcceptsPlainContractStrings(t *testing.T) {
	document := []byte("name: sample\npath: backend/sample\napiVersion: nexa.dev/x/v1\nnested:\n  value: child\nitems:\n  - first\n  - backend/second\n")
	var target plainContractStrings
	if err := strictdoc.DecodeYAML("fixture.yaml", document, &target); err != nil {
		t.Fatal(err)
	}
	if target.Name != "sample" || target.Path != "backend/sample" || target.APIVersion != "nexa.dev/x/v1" {
		t.Fatalf("top-level strings = %#v", target)
	}
}

func TestDecodeYAMLAcceptsNestedAndListPlainStrings(t *testing.T) {
	document := []byte("name: sample\npath: backend/sample\napiVersion: nexa.dev/x/v1\nnested:\n  value: child\nitems:\n  - first\n  - backend/second\n")
	var target plainContractStrings
	if err := strictdoc.DecodeYAML("fixture.yaml", document, &target); err != nil {
		t.Fatal(err)
	}
	if target.Nested.Value != "child" || len(target.Items) != 2 ||
		target.Items[0] != "first" || target.Items[1] != "backend/second" {
		t.Fatalf("nested/list strings = %#v", target)
	}
}

func TestDecodeYAMLRejectsUnknownTypedField(t *testing.T) {
	var target fixture
	err := strictdoc.DecodeYAML("fixture.yaml", []byte("name: \"first\"\nextra: true\n"), &target)
	assertDocumentError(t, err, "document_unknown_field", "/extra")
}

func TestDecodeYAMLReportsNestedUnknownFieldPointer(t *testing.T) {
	var target nestedFixture
	err := strictdoc.DecodeYAML(
		"fixture.yaml",
		[]byte("parent:\n  count: 1\n  extra: true\n"),
		&target,
	)
	documentError := assertDocumentError(t, err, "document_unknown_field", "/parent/extra")
	if documentError.Line != 3 || documentError.Column <= 0 {
		t.Fatalf("location = %d:%d, want line 3", documentError.Line, documentError.Column)
	}
}

func TestDecodeYAMLReportsTypeMismatchAtOriginalLocation(t *testing.T) {
	var target nestedFixture
	err := strictdoc.DecodeYAML(
		"fixture.yaml",
		[]byte("parent:\n  count: \"not-a-number\"\n"),
		&target,
	)
	documentError := assertDocumentError(t, err, "document_invalid", "/parent/count")
	if documentError.Line != 2 || documentError.Column != 10 {
		t.Fatalf("location = %d:%d, want 2:10", documentError.Line, documentError.Column)
	}
}

func TestDecodeYAMLUsesStandardCaseInsensitiveMemberMatching(t *testing.T) {
	var target exactCaseDestination
	if err := strictdoc.DecodeYAML("fixture.yaml", []byte("Name: \"accepted\"\n"), &target); err != nil {
		t.Fatal(err)
	}
	if target.Name != "accepted" {
		t.Fatalf("Name = %q", target.Name)
	}
}

func TestDecodeYAMLSupportsClosedDestinationSubset(t *testing.T) {
	document := []byte("child:\n  values: [\"one\", \"two\"]\ngroups: [[1, 2], [3]]\nenabled: true\nname: \"sample\"\nsigned: -42\nunsigned: 42\nratio: 1.25\n")
	var target supportedDestination
	if err := strictdoc.DecodeYAML("fixture.yaml", document, &target); err != nil {
		t.Fatal(err)
	}
	if target.Child == nil || len(target.Child.Values) != 2 || target.Groups[1][0] != 3 ||
		!target.Enabled || target.Name != "sample" || target.Signed != -42 ||
		target.Unsigned != 42 || target.Ratio != 1.25 {
		t.Fatalf("decoded target = %#v", target)
	}
}

func TestDecodeYAMLReportsNumericRangePointersAndLocations(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		pointer string
		column  int
	}{
		{name: "signed high", data: "signed: 128\n", pointer: "/signed", column: 9},
		{name: "signed low", data: "signed: -129\n", pointer: "/signed", column: 9},
		{name: "unsigned negative", data: "unsigned: -1\n", pointer: "/unsigned", column: 11},
		{name: "unsigned high", data: "unsigned: 256\n", pointer: "/unsigned", column: 11},
		{name: "float overflow", data: "ratio: 1e100\n", pointer: "/ratio", column: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target numericDestination
			err := strictdoc.DecodeYAML("fixture.yaml", []byte(tt.data), &target)
			documentError := assertDocumentError(t, err, "document_invalid", tt.pointer)
			if documentError.Line != 1 || documentError.Column != tt.column {
				t.Fatalf("location = %d:%d, want 1:%d", documentError.Line, documentError.Column, tt.column)
			}
		})
	}
}

func TestDecodeYAMLReportsNestedSliceElementPointerAndLocation(t *testing.T) {
	document := []byte("groups:\n  -\n    - 1\n    - 128\n")
	var target nestedSliceDestination
	err := strictdoc.DecodeYAML("fixture.yaml", document, &target)
	documentError := assertDocumentError(t, err, "document_invalid", "/groups")
	if documentError.Line <= 0 || documentError.Column <= 0 {
		t.Fatalf("location = %d:%d", documentError.Line, documentError.Column)
	}
}

func TestDecodeYAMLReportsPointersAndLocationsForEverySupportedShape(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		pointer string
		line    int
		column  int
	}{
		{name: "bool", data: "enabled: \"true\"\n", pointer: "/enabled", line: 1, column: 10},
		{name: "string", data: "name: true\n", pointer: "/name", line: 1, column: 7},
		{name: "pointer to struct", data: "child: \"bad\"\n", pointer: "/child", line: 1, column: 8},
		{name: "slice", data: "groups: \"bad\"\n", pointer: "/groups", line: 1, column: 9},
		{
			name:    "nested slice scalar",
			data:    "child:\n  values:\n    - true\n",
			pointer: "/child/values",
			line:    3,
			column:  7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target supportedDestination
			err := strictdoc.DecodeYAML("fixture.yaml", []byte(tt.data), &target)
			documentError := assertDocumentError(t, err, "document_invalid", tt.pointer)
			if documentError.Line <= 0 || documentError.Column <= 0 {
				t.Fatalf("location = %d:%d", documentError.Line, documentError.Column)
			}
		})
	}
}
