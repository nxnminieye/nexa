package api_test

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func TestScalarSchemaClosedBuiltins(t *testing.T) {
	valid := []api.SchemaSpec{
		{ID: "scalar.string", Kind: api.SchemaString},
		{ID: "scalar.integer", Kind: api.SchemaInteger},
		{ID: "scalar.number", Kind: api.SchemaNumber},
		{ID: "scalar.boolean", Kind: api.SchemaBoolean},
	}
	if _, err := api.NewManifest(api.ManifestSpec{Schemas: valid}); err != nil {
		t.Fatalf("built-in scalar schemas rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		spec api.SchemaSpec
	}{
		{name: "arbitrary scalar id", spec: api.SchemaSpec{ID: "sample.string", Kind: api.SchemaString}},
		{name: "mismatched scalar pair", spec: api.SchemaSpec{ID: "scalar.string", Kind: api.SchemaInteger}},
		{name: "scalar with source", spec: scalarWithSource(t)},
		{name: "scalar with fields", spec: api.SchemaSpec{ID: "scalar.string", Kind: api.SchemaString, Fields: []api.FieldSpec{{Name: "value", SchemaRef: "scalar.string"}}}},
		{name: "scalar with item", spec: api.SchemaSpec{ID: "scalar.string", Kind: api.SchemaString, ItemSchemaRef: "scalar.string"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := api.NewManifest(api.ManifestSpec{Schemas: []api.SchemaSpec{test.spec}})
			manifestError := requireAPIError(t, err, "scalar_schema_invalid")
			if manifestError.Pointer() != "/schemas/0/id" {
				t.Fatalf("Pointer() = %q", manifestError.Pointer())
			}
		})
	}
}

func TestInvalidSchemaSemantics(t *testing.T) {
	sourceRef := mustRef(t, "repo:desc/core.api#Request")
	source := nodeSources(sourceRef)[0]
	valid := []api.SchemaSpec{{ID: "request", Kind: api.SchemaObject, Provenance: canonicalNode(sourceRef), Fields: []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Required: true, Provenance: *canonicalNode(sourceRef)}}}, {ID: "scalar.string", Kind: api.SchemaString}}
	tests := []struct {
		name, reason, pointer string
		mutate                func([]api.SchemaSpec) []api.SchemaSpec
	}{
		{name: "invalid id", reason: "schema_id_invalid", pointer: "/schemas/0/id", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].ID = "Request"; return s }},
		{name: "duplicate id", reason: "schema_duplicate", pointer: "/schemas/1/id", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { return append(s, s[0]) }},
		{name: "unknown kind", reason: "schema_kind_invalid", pointer: "/schemas/0/kind", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].Kind = "map"; return s }},
		{name: "object provenance required", reason: "node_provenance_kind_invalid", pointer: "/schemas/0/provenance/kind", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].Provenance = nil; return s }},
		{name: "object item forbidden", reason: "schema_shape_invalid", pointer: "/schemas/0/itemSchemaRef", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].ItemSchemaRef = "scalar.string"; return s }},
		{name: "invalid field name", reason: "field_name_invalid", pointer: "/schemas/0/fields/0/name", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].Fields[0].Name = "display-name"; return s }},
		{name: "duplicate field", reason: "field_duplicate", pointer: "/schemas/0/fields/1/name", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].Fields = append(s[0].Fields, s[0].Fields[0]); return s }},
		{name: "invalid field ref", reason: "field_schema_ref_invalid", pointer: "/schemas/0/fields/0/schemaRef", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].Fields[0].SchemaRef = "Scalar String"; return s }},
		{name: "unresolved field ref", reason: "field_schema_ref_unresolved", pointer: "/schemas/0/fields/0/schemaRef", mutate: func(s []api.SchemaSpec) []api.SchemaSpec { s[0].Fields[0].SchemaRef = "missing"; return s }},
		{name: "array provenance required", reason: "node_provenance_kind_invalid", pointer: "/schemas/0/provenance/kind", mutate: func(s []api.SchemaSpec) []api.SchemaSpec {
			s[0] = api.SchemaSpec{ID: "items", Kind: api.SchemaArray, ItemSchemaRef: "scalar.string"}
			return s
		}},
		{name: "array item required", reason: "item_schema_ref_invalid", pointer: "/schemas/0/itemSchemaRef", mutate: func(s []api.SchemaSpec) []api.SchemaSpec {
			s[0] = api.SchemaSpec{ID: "items", Kind: api.SchemaArray, Provenance: canonicalNode(sourceRef)}
			return s
		}},
		{name: "array item unresolved", reason: "item_schema_ref_unresolved", pointer: "/schemas/0/itemSchemaRef", mutate: func(s []api.SchemaSpec) []api.SchemaSpec {
			s[0] = api.SchemaSpec{ID: "items", Kind: api.SchemaArray, Provenance: canonicalNode(sourceRef), ItemSchemaRef: "missing"}
			return s
		}},
		{name: "array fields forbidden", reason: "schema_shape_invalid", pointer: "/schemas/0/fields", mutate: func(s []api.SchemaSpec) []api.SchemaSpec {
			s[0] = api.SchemaSpec{ID: "items", Kind: api.SchemaArray, Provenance: canonicalNode(sourceRef), ItemSchemaRef: "scalar.string", Fields: []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Provenance: *canonicalNode(sourceRef)}}}
			return s
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schemas := cloneSchemaSpecs(valid)
			schemas = test.mutate(schemas)
			_, err := api.NewManifest(api.ManifestSpec{Sources: []provenance.Source{source}, Schemas: schemas})
			manifestError := requireAPIError(t, err, test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestSchemaCycleRejectsObjectArraySelfAndMutualCycles(t *testing.T) {
	refA := mustRef(t, "repo:desc/core.api#A")
	refB := mustRef(t, "repo:desc/core.api#B")
	tests := []struct {
		name, pointer string
		schemas       []api.SchemaSpec
	}{
		{name: "object self", pointer: "/schemas/0/fields/0/schemaRef", schemas: []api.SchemaSpec{{ID: "a", Kind: api.SchemaObject, Provenance: canonicalNode(refA), Fields: []api.FieldSpec{{Name: "self", SchemaRef: "a", Provenance: *canonicalNode(refA)}}}}},
		{name: "array self", pointer: "/schemas/0/itemSchemaRef", schemas: []api.SchemaSpec{{ID: "a", Kind: api.SchemaArray, Provenance: canonicalNode(refA), ItemSchemaRef: "a"}}},
		{name: "object mutual", pointer: "/schemas/0/fields/0/schemaRef", schemas: []api.SchemaSpec{{ID: "a", Kind: api.SchemaObject, Provenance: canonicalNode(refA), Fields: []api.FieldSpec{{Name: "b", SchemaRef: "b", Provenance: *canonicalNode(refA)}}}, {ID: "b", Kind: api.SchemaObject, Provenance: canonicalNode(refB), Fields: []api.FieldSpec{{Name: "a", SchemaRef: "a", Provenance: *canonicalNode(refB)}}}}},
		{name: "mixed object array", pointer: "/schemas/0/fields/0/schemaRef", schemas: []api.SchemaSpec{{ID: "a", Kind: api.SchemaObject, Provenance: canonicalNode(refA), Fields: []api.FieldSpec{{Name: "b", SchemaRef: "b", Provenance: *canonicalNode(refA)}}}, {ID: "b", Kind: api.SchemaArray, Provenance: canonicalNode(refB), ItemSchemaRef: "a"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := api.NewManifest(api.ManifestSpec{Sources: nodeSources(refA, refB), Schemas: test.schemas})
			manifestError := requireAPIError(t, err, "schema_cycle")
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func scalarWithSource(t *testing.T) api.SchemaSpec {
	t.Helper()
	ref := mustRef(t, "repo:desc/core.api#Scalar")
	return api.SchemaSpec{ID: "scalar.string", Kind: api.SchemaString, Provenance: canonicalNode(ref)}
}

func cloneSchemaSpecs(input []api.SchemaSpec) []api.SchemaSpec {
	result := make([]api.SchemaSpec, len(input))
	for i, schema := range input {
		result[i] = schema
		if schema.Provenance != nil {
			provenanceCopy := *schema.Provenance
			provenanceCopy.Refs = append([]provenance.SourceRef(nil), provenanceCopy.Refs...)
			result[i].Provenance = &provenanceCopy
		}
		result[i].Fields = append([]api.FieldSpec(nil), schema.Fields...)
	}
	return result
}
