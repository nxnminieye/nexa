package main

import "testing"

func TestFrameworkSurfaces(t *testing.T) {
	result, err := exerciseFrameworkSurfaces()
	if err != nil {
		t.Fatal(err)
	}
	if result.GenerationSchemas != 2 || result.RenderedBytes == 0 || result.SourceFiles != 1 || result.SourceProfiles != 1 || result.TreeFiles != 1 || result.CRUDLimit != 20 {
		t.Fatalf("result = %#v", result)
	}
}
