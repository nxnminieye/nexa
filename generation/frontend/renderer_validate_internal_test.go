package frontend

import "testing"

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
