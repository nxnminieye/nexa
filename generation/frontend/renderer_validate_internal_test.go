package frontend

import "testing"

func TestValidateRendererResponseWireClosure(t *testing.T) {
	body := func(name string) *rendererAPIBinding {
		return &rendererAPIBinding{Location: "body", Name: name}
	}
	for _, tc := range []struct {
		name, reason, pointer string
		types                 map[string]rendererAPIType
	}{
		{
			name:    "missing",
			reason:  "response_wire_binding_missing",
			pointer: "/frontendIR/api/types/0/fields/0/binding",
			types:   map[string]rendererAPIType{"Response": {Name: "Response", Fields: []rendererAPIField{{Path: []string{"ID"}, ValueType: rendererAPIValue{Kind: "scalar", Name: "int64"}}}}},
		},
		{
			name:    "non-body",
			reason:  "response_wire_binding_location_invalid",
			pointer: "/frontendIR/api/types/0/fields/0/binding/in",
			types:   map[string]rendererAPIType{"Response": {Name: "Response", Fields: []rendererAPIField{{Path: []string{"ID"}, Binding: &rendererAPIBinding{Location: "query", Name: "id"}, ValueType: rendererAPIValue{Kind: "scalar", Name: "int64"}}}}},
		},
		{
			name:    "duplicate",
			reason:  "response_wire_name_duplicate",
			pointer: "/frontendIR/api/types/0/fields/1/binding/name",
			types:   map[string]rendererAPIType{"Response": {Name: "Response", Fields: []rendererAPIField{{Path: []string{"ID"}, Binding: body("value"), ValueType: rendererAPIValue{Kind: "scalar", Name: "int64"}}, {Path: []string{"Name"}, Binding: body("value"), ValueType: rendererAPIValue{Kind: "scalar", Name: "string"}}}}},
		},
		{
			name:    "nested-arrays",
			reason:  "response_wire_binding_missing",
			pointer: "/frontendIR/api/types/0/fields/0/binding",
			types: map[string]rendererAPIType{
				"Child":    {Name: "Child", Fields: []rendererAPIField{{Path: []string{"ID"}, ValueType: rendererAPIValue{Kind: "scalar", Name: "int64"}}}},
				"Response": {Name: "Response", Fields: []rendererAPIField{{Path: []string{"Items"}, Binding: body("items"), ValueType: rendererAPIValue{Kind: "array", Element: &rendererAPIValue{Kind: "array", Element: &rendererAPIValue{Kind: "ref", Name: "Child"}}}}}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRendererResponseWireClosure(tc.types, "Response")
			failure, ok := err.(*Error)
			if !ok || failure.Reason() != tc.reason || failure.Pointer() != tc.pointer {
				t.Fatalf("error=%v reason/pointer want %s %s", err, tc.reason, tc.pointer)
			}
		})
	}
}

func TestResolveRendererInlineObjectArrayByOwningPath(t *testing.T) {
	types := map[string]rendererAPIType{
		"Response": {
			Name: "Response",
			Fields: []rendererAPIField{
				{Path: []string{"Items"}, Required: true, ValueType: rendererAPIValue{Kind: "array", Element: &rendererAPIValue{Kind: "object"}}},
				{Path: []string{"Items", "ID"}, Required: true, ValueType: rendererAPIValue{Kind: "scalar", Name: "int64"}},
				{Path: []string{"Items", "Meta"}, Required: true, ValueType: rendererAPIValue{Kind: "object"}},
				{Path: []string{"Items", "Meta", "Label"}, Required: true, ValueType: rendererAPIValue{Kind: "scalar", Name: "string"}},
			},
		},
	}
	for _, path := range [][]string{{"Items", "ID"}, {"Items", "Meta", "Label"}} {
		value, required, ok := resolveRendererPathAllowedArrays(types, "Response", path, map[int]bool{0: true}, false)
		if !ok || !required || value.Kind != "scalar" {
			t.Fatalf("path=%v value=%#v required=%v ok=%v", path, value, required, ok)
		}
	}
}
