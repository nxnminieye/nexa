package strictdoc_test

import (
	"errors"
	"testing"

	"github.com/nxnminieye/nexa/internal/strictdoc"
)

func TestParseDocumentsPreserveNormalizedJSONShape(t *testing.T) {
	const expected = `{"count":1.20,"dependsOn":null,"empty":[],"items":[null],"name":"sample","nested":{"value":"child"}}`
	tests := []struct {
		name  string
		parse func(string, []byte) (strictdoc.Document, error)
		data  []byte
	}{
		{
			name:  "JSON",
			parse: strictdoc.ParseJSON,
			data:  []byte(`{"nested":{"value":"child"},"name":"sample","items":[null],"empty":[],"dependsOn":null,"count":1.20}`),
		},
		{
			name:  "YAML",
			parse: strictdoc.ParseYAML,
			data:  []byte("dependsOn: null\nitems:\n  - null\nname: sample\ncount: 1.20\nempty: []\nnested:\n  value: child\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document, err := tt.parse("fixture", tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(document.JSON()); got != expected {
				t.Fatalf("normalized JSON = %s, want %s", got, expected)
			}
		})
	}
}

func TestParseDocumentsPreservePresentNullAndAbsentField(t *testing.T) {
	present, err := strictdoc.ParseJSON("present.json", []byte(`{"dependsOn":null}`))
	if err != nil {
		t.Fatal(err)
	}
	absent, err := strictdoc.ParseJSON("absent.json", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(present.JSON()); got != `{"dependsOn":null}` {
		t.Fatalf("present JSON = %s", got)
	}
	if got := string(absent.JSON()); got != `{}` {
		t.Fatalf("absent JSON = %s", got)
	}
}

func TestDocumentJSONReturnsDefensiveCopy(t *testing.T) {
	document, err := strictdoc.ParseYAML("fixture.yaml", []byte("name: sample\nitems: [null]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := string(document.JSON())
	mutated := document.JSON()
	for index := range mutated {
		mutated[index] = 'x'
	}
	if got := string(document.JSON()); got != want {
		t.Fatalf("JSON after caller mutation = %s, want %s", got, want)
	}
}

func TestDocumentLocationReportsAuthoredValueCoordinates(t *testing.T) {
	tests := []struct {
		name      string
		parse     func(string, []byte) (strictdoc.Document, error)
		data      []byte
		locations map[string][2]int
	}{
		{
			name:  "JSON",
			parse: strictdoc.ParseJSON,
			data:  []byte("{\n  \"name\": \"sample\",\n  \"nested\": {\"value\": \"child\"},\n  \"items\": [\n    \"first\",\n    {\"id\": 2}\n  ]\n}"),
			locations: map[string][2]int{
				"":              {1, 1},
				"/name":         {2, 11},
				"/nested":       {3, 13},
				"/nested/value": {3, 23},
				"/items":        {4, 12},
				"/items/0":      {5, 5},
				"/items/1":      {6, 5},
				"/items/1/id":   {6, 12},
			},
		},
		{
			name:  "YAML",
			parse: strictdoc.ParseYAML,
			data:  []byte("name: sample\nnested:\n  value: child\nitems:\n  - first\n  - id: 2\n"),
			locations: map[string][2]int{
				"":              {1, 1},
				"/name":         {1, 7},
				"/nested":       {3, 3},
				"/nested/value": {3, 10},
				"/items":        {5, 3},
				"/items/0":      {5, 5},
				"/items/1":      {6, 5},
				"/items/1/id":   {6, 9},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document, err := tt.parse("fixture", tt.data)
			if err != nil {
				t.Fatal(err)
			}
			for pointer, want := range tt.locations {
				line, column, ok := document.Location(pointer)
				if !ok || line != want[0] || column != want[1] {
					t.Errorf("Location(%q) = %d:%d,%v, want %d:%d,true", pointer, line, column, ok, want[0], want[1])
				}
			}
			for _, pointer := range []string{"missing", "/missing", "/items/00", "/items/2"} {
				if line, column, ok := document.Location(pointer); ok || line != 0 || column != 0 {
					t.Errorf("Location(%q) = %d:%d,%v, want 0:0,false", pointer, line, column, ok)
				}
			}
		})
	}
}

func TestDocumentDecodeMatchesJSONWrapperError(t *testing.T) {
	data := []byte(`{"parent":{"count":1,"extra":true}}`)
	document, err := strictdoc.ParseJSON("fixture.json", data)
	if err != nil {
		t.Fatal(err)
	}
	var fromDocument nestedFixture
	documentErr := document.Decode(&fromDocument)
	var fromWrapper nestedFixture
	wrapperErr := strictdoc.DecodeJSON("fixture.json", data, &fromWrapper)
	assertMatchingDocumentErrors(t, documentErr, wrapperErr)
}

func TestDocumentDecodeMatchesYAMLWrapperPointerAndLocation(t *testing.T) {
	data := []byte("parent:\n  count: \"not-a-number\"\n")
	document, err := strictdoc.ParseYAML("fixture.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	var fromDocument nestedFixture
	documentErr := document.Decode(&fromDocument)
	var fromWrapper nestedFixture
	wrapperErr := strictdoc.DecodeYAML("fixture.yaml", data, &fromWrapper)
	assertMatchingDocumentErrors(t, documentErr, wrapperErr)

	var projected *strictdoc.Error
	if !errors.As(documentErr, &projected) {
		t.Fatalf("error type = %T, want *strictdoc.Error", documentErr)
	}
	if projected.Pointer != "/parent/count" || projected.Line != 2 || projected.Column != 10 {
		t.Fatalf("projected error = %#v", projected)
	}
}

func TestDocumentDecodeMatchesWrapperValue(t *testing.T) {
	data := []byte("name: sample\npath: backend/sample\napiVersion: nexa.dev/x/v1\nnested:\n  value: child\nitems:\n  - first\n")
	document, err := strictdoc.ParseYAML("fixture.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	var fromDocument plainContractStrings
	if err := document.Decode(&fromDocument); err != nil {
		t.Fatal(err)
	}
	var fromWrapper plainContractStrings
	if err := strictdoc.DecodeYAML("fixture.yaml", data, &fromWrapper); err != nil {
		t.Fatal(err)
	}
	if fromDocument.Name != fromWrapper.Name || fromDocument.Path != fromWrapper.Path ||
		fromDocument.Nested.Value != fromWrapper.Nested.Value || len(fromDocument.Items) != len(fromWrapper.Items) {
		t.Fatalf("decoded values differ: %#v != %#v", fromDocument, fromWrapper)
	}
}

func assertMatchingDocumentErrors(t *testing.T, left, right error) {
	t.Helper()
	var leftDocument *strictdoc.Error
	if !errors.As(left, &leftDocument) {
		t.Fatalf("left error type = %T", left)
	}
	var rightDocument *strictdoc.Error
	if !errors.As(right, &rightDocument) {
		t.Fatalf("right error type = %T", right)
	}
	if leftDocument.Code != rightDocument.Code || leftDocument.Source != rightDocument.Source ||
		leftDocument.Pointer != rightDocument.Pointer || leftDocument.Line != rightDocument.Line ||
		leftDocument.Column != rightDocument.Column {
		t.Fatalf("errors differ: %#v != %#v", leftDocument, rightDocument)
	}
}
