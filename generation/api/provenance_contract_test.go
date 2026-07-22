package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	api "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
	"gopkg.in/yaml.v3"
)

func TestNodeProvenanceAndOriginRoundTrip(t *testing.T) {
	spec, sources := provenanceManifestSpec(t)
	wantSources := append([]provenance.Source(nil), sources...)
	manifest, err := api.NewManifest(spec)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	spec.Sources[0] = provenance.Source{}
	spec.Schemas[0].Provenance.Refs[0] = provenance.SourceRef{}
	spec.Schemas[0].Fields[0].Provenance.Refs[0] = provenance.SourceRef{}
	spec.Schemas[0].Fields[0].Origin.Ref = provenance.SourceRef{}
	assertProvenanceProjection(t, manifest, wantSources)

	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sourceRef") || strings.Contains(string(encoded), `"origin":{"kind"`) {
		t.Fatalf("canonical JSON contains ambiguous node source/origin kind: %s", encoded)
	}
	parsed, err := api.Parse("api-manifest.json", encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	assertProvenanceProjection(t, parsed, wantSources)
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	yamlDocument, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	parsedYAML, err := api.Parse("api-manifest.yaml", yamlDocument)
	if err != nil {
		t.Fatalf("Parse(YAML) error = %v", err)
	}
	assertProvenanceProjection(t, parsedYAML, wantSources)
}

func TestNodeProvenanceValidationGates(t *testing.T) {
	valid, _ := provenanceManifestSpec(t)
	unresolved := mustRef(t, "repo:contracts/api.api#missing")
	invalid := provenance.SourceRef{}
	existing := valid.Schemas[0].Provenance.Refs[0]
	tests := []struct {
		name    string
		mutate  func(*api.ManifestSpec)
		reason  string
		pointer string
	}{
		{name: "kind before refs", mutate: func(s *api.ManifestSpec) {
			s.Schemas[0].Provenance = &api.NodeProvenanceSpec{Kind: "other", Refs: []provenance.SourceRef{invalid, invalid}}
		}, reason: "node_provenance_kind_invalid", pointer: "/schemas/0/provenance/kind"},
		{name: "canonical shape before resolution", mutate: func(s *api.ManifestSpec) {
			s.Schemas[0].Provenance = &api.NodeProvenanceSpec{Kind: api.NodeCanonical, Refs: []provenance.SourceRef{unresolved, unresolved}}
		}, reason: "node_provenance_shape_invalid", pointer: "/schemas/0/provenance/refs"},
		{name: "derived empty", mutate: func(s *api.ManifestSpec) { s.Schemas[0].Provenance = &api.NodeProvenanceSpec{Kind: api.NodeDerived} }, reason: "node_provenance_refs_empty", pointer: "/schemas/0/provenance/refs"},
		{name: "invalid before duplicate", mutate: func(s *api.ManifestSpec) {
			s.Schemas[0].Provenance = &api.NodeProvenanceSpec{Kind: api.NodeDerived, Refs: []provenance.SourceRef{invalid, invalid}}
		}, reason: "node_provenance_ref_invalid", pointer: "/schemas/0/provenance/refs/0"},
		{name: "reversed invalid compound uses canonical coordinate", mutate: func(s *api.ManifestSpec) {
			s.Schemas[0].Provenance = &api.NodeProvenanceSpec{Kind: api.NodeDerived, Refs: []provenance.SourceRef{existing, invalid, invalid}}
		}, reason: "node_provenance_ref_invalid", pointer: "/schemas/0/provenance/refs/0"},
		{name: "duplicate before unresolved", mutate: func(s *api.ManifestSpec) {
			s.Schemas[0].Provenance = &api.NodeProvenanceSpec{Kind: api.NodeDerived, Refs: []provenance.SourceRef{unresolved, unresolved}}
		}, reason: "node_provenance_ref_duplicate", pointer: "/schemas/0/provenance/refs/1"},
		{name: "exact unresolved", mutate: func(s *api.ManifestSpec) {
			s.Schemas[0].Provenance = &api.NodeProvenanceSpec{Kind: api.NodeCanonical, Refs: []provenance.SourceRef{unresolved}}
		}, reason: "node_provenance_ref_unresolved", pointer: "/schemas/0/provenance/refs/0"},
		{name: "origin redundant before resolution", mutate: func(s *api.ManifestSpec) {
			ref := s.Schemas[0].Fields[0].Provenance.Refs[0]
			s.Schemas[0].Fields[0].Origin = &api.OriginBindingSpec{Ref: ref}
			s.Sources = s.Sources[:2]
		}, reason: "origin_ref_redundant", pointer: "/schemas/0/fields/0/origin/ref"},
		{name: "origin unresolved", mutate: func(s *api.ManifestSpec) { s.Schemas[0].Fields[0].Origin = &api.OriginBindingSpec{Ref: unresolved} }, reason: "origin_ref_unresolved", pointer: "/schemas/0/fields/0/origin/ref"},
		{name: "origin invalid", mutate: func(s *api.ManifestSpec) { s.Schemas[0].Fields[0].Origin = &api.OriginBindingSpec{} }, reason: "origin_ref_invalid", pointer: "/schemas/0/fields/0/origin/ref"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneProvenanceSpec(valid)
			test.mutate(&spec)
			_, err := api.NewManifest(spec)
			requireD030APIError(t, err, test.reason, test.pointer)
		})
	}
}

func TestConstructorNodeProvenanceCoordinatesIgnoreInputOrder(t *testing.T) {
	schemaRef := mustRef(t, "repo:contracts/api.api#schema-request")
	missingA := mustRef(t, "repo:contracts/api.api#missing-a")
	missingB := mustRef(t, "repo:contracts/api.api#missing-b")
	source := provenance.Source{Ref: schemaRef, Digest: provenance.SHA256([]byte("schema"))}
	canonicalShape := api.NodeProvenanceSpec{Kind: api.NodeCanonical, Refs: []provenance.SourceRef{missingB, missingA}}
	invalidKind := api.NodeProvenanceSpec{Kind: "other", Refs: []provenance.SourceRef{missingA}}

	tests := []struct {
		name    string
		build   func(reverse bool) api.ManifestSpec
		reason  string
		pointer string
	}{
		{name: "schemas", reason: "node_provenance_shape_invalid", pointer: "/schemas/0/provenance/refs", build: func(reverse bool) api.ManifestSpec {
			values := []api.SchemaSpec{{ID: "zeta", Kind: api.SchemaObject, Provenance: provenancePointer(invalidKind), Fields: []api.FieldSpec{}}, {ID: "alpha", Kind: api.SchemaObject, Provenance: provenancePointer(canonicalShape), Fields: []api.FieldSpec{}}}
			if reverse {
				values[0], values[1] = values[1], values[0]
			}
			return api.ManifestSpec{Schemas: values}
		}},
		{name: "fields", reason: "node_provenance_shape_invalid", pointer: "/schemas/0/fields/0/provenance/refs", build: func(reverse bool) api.ManifestSpec {
			fields := []api.FieldSpec{{Name: "zeta", SchemaRef: "scalar.string", Provenance: invalidKind}, {Name: "alpha", SchemaRef: "scalar.string", Provenance: canonicalShape}}
			if reverse {
				fields[0], fields[1] = fields[1], fields[0]
			}
			return api.ManifestSpec{Sources: []provenance.Source{source}, Schemas: []api.SchemaSpec{{ID: "request", Kind: api.SchemaObject, Provenance: canonicalNode(schemaRef), Fields: fields}, {ID: "scalar.string", Kind: api.SchemaString}}}
		}},
		{name: "operations", reason: "node_provenance_shape_invalid", pointer: "/operations/0/provenance/refs", build: func(reverse bool) api.ManifestSpec {
			operations := []api.OperationSpec{
				{ID: "zeta", Method: api.MethodGET, Path: "/zeta", Provenance: invalidKind, RequestSchemaRef: "request", ResponseBody: api.ResponseBodyNone, Auth: api.AuthSpec{Mode: api.AuthNone}},
				{ID: "alpha", Method: api.MethodGET, Path: "/alpha", Provenance: canonicalShape, RequestSchemaRef: "request", ResponseBody: api.ResponseBodyNone, Auth: api.AuthSpec{Mode: api.AuthNone}},
			}
			if reverse {
				operations[0], operations[1] = operations[1], operations[0]
			}
			return api.ManifestSpec{Sources: []provenance.Source{source}, Schemas: []api.SchemaSpec{{ID: "request", Kind: api.SchemaObject, Provenance: canonicalNode(schemaRef), Fields: []api.FieldSpec{}}}, Operations: operations}
		}},
	}
	for _, test := range tests {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reversed=%v", test.name, reverse), func(t *testing.T) {
				_, err := api.NewManifest(test.build(reverse))
				requireD030APIError(t, err, test.reason, test.pointer)
			})
		}
	}
}

func TestManifestRequiresExactBidirectionalSourceClosure(t *testing.T) {
	valid, _ := provenanceManifestSpec(t)
	missingFragment := mustRef(t, "repo:contracts/api.api#schema-missing")
	whole := mustRef(t, "repo:contracts/api.api")
	extra := mustRef(t, "repo:contracts/api.api#extra")
	tests := []struct {
		name    string
		mutate  func(*api.ManifestSpec)
		reason  string
		pointer string
	}{
		{name: "whole document does not cover fragment", mutate: func(s *api.ManifestSpec) { s.Sources[0].Ref = whole }, reason: "node_provenance_ref_unresolved", pointer: "/schemas/0/provenance/refs/0"},
		{name: "missing exact source", mutate: func(s *api.ManifestSpec) { s.Schemas[0].Provenance.Refs[0] = missingFragment }, reason: "node_provenance_ref_unresolved", pointer: "/schemas/0/provenance/refs/0"},
		{name: "extra fragment", mutate: func(s *api.ManifestSpec) {
			s.Sources = append(s.Sources, provenance.Source{Ref: extra, Digest: provenance.SHA256([]byte("extra"))})
		}, reason: "source_unreferenced", pointer: "/sources/0/ref"},
		{name: "extra whole document sorts first", mutate: func(s *api.ManifestSpec) {
			s.Sources = append(s.Sources, provenance.Source{Ref: whole, Digest: provenance.SHA256([]byte("whole"))})
		}, reason: "source_unreferenced", pointer: "/sources/0/ref"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneProvenanceSpec(valid)
			test.mutate(&spec)
			_, err := api.NewManifest(spec)
			requireD030APIError(t, err, test.reason, test.pointer)
		})
	}

	empty, err := api.NewManifest(api.ManifestSpec{})
	if err != nil || len(empty.Sources()) != 0 {
		t.Fatalf("empty manifest = %#v, %v", empty, err)
	}
}

func TestReverseClosureExtraSourceKindsAndInputOrder(t *testing.T) {
	valid, originalSources := provenanceManifestSpec(t)
	extras := []struct {
		name   string
		source provenance.Source
	}{
		{name: "fragment", source: provenance.Source{Ref: mustRef(t, "repo:contracts/api.api#zzzz"), Digest: provenance.SHA256([]byte("fragment"))}},
		{name: "whole document", source: provenance.Source{Ref: mustRef(t, "repo:contracts/api.api"), Digest: provenance.SHA256([]byte("whole"))}},
		{name: "same-path ancestor", source: provenance.Source{Ref: mustRef(t, "repo:contracts/api.api#field"), Digest: provenance.SHA256([]byte("ancestor"))}},
	}
	for _, extra := range extras {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("constructor/%s/reversed=%v", extra.name, reverse), func(t *testing.T) {
				spec := cloneProvenanceSpec(valid)
				spec.Sources = append(spec.Sources, extra.source)
				if reverse {
					reverseSources(spec.Sources)
				}
				_, err := api.NewManifest(spec)
				requireD030APIError(t, err, "source_unreferenced", normalizedSourcePointer(spec.Sources, extra.source.Ref))
			})
		}
	}

	baseManifest, err := api.NewManifest(valid)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := baseManifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range extras {
		for _, reverse := range []bool{false, true} {
			for _, format := range []string{"json", "yaml"} {
				t.Run(fmt.Sprintf("parse/%s/reversed=%v/%s", extra.name, reverse, format), func(t *testing.T) {
					var document map[string]any
					if err := json.Unmarshal(canonical, &document); err != nil {
						t.Fatal(err)
					}
					documentSources := append(document["sources"].([]any), map[string]any{"ref": extra.source.Ref.String(), "digest": extra.source.Digest.String()})
					if reverse {
						for left, right := 0, len(documentSources)-1; left < right; left, right = left+1, right-1 {
							documentSources[left], documentSources[right] = documentSources[right], documentSources[left]
						}
					}
					document["sources"] = documentSources
					digest, digestErr := api.ComputeSourceDigest(append(append([]provenance.Source(nil), originalSources...), extra.source))
					if digestErr != nil {
						t.Fatal(digestErr)
					}
					document["sourceDigest"] = digest.String()
					data, marshalErr := marshalManifestDocument(format, document)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					_, parseErr := api.Parse("api-manifest."+format, data)
					pointer := "/sources/4/ref"
					if reverse {
						pointer = "/sources/0/ref"
					}
					manifestErr := requireD030APIError(t, parseErr, "source_unreferenced", pointer)
					if manifestErr.Line() == 0 || manifestErr.Column() == 0 {
						t.Fatalf("source location = (%d,%d)", manifestErr.Line(), manifestErr.Column())
					}
				})
			}
		}
	}
}

func normalizedSourcePointer(sources []provenance.Source, target provenance.SourceRef) string {
	ordered := append([]provenance.Source(nil), sources...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Ref.String() < ordered[right].Ref.String() })
	for index, source := range ordered {
		if source.Ref == target {
			return fmt.Sprintf("/sources/%d/ref", index)
		}
	}
	return ""
}

func TestParseNodeProvenanceGatesJSONAndYAML(t *testing.T) {
	valid, sources := provenanceManifestSpec(t)
	existing := valid.Schemas[0].Provenance.Refs[0].String()
	manifest, err := api.NewManifest(valid)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	extra := provenance.Source{Ref: mustRef(t, "repo:contracts/api.api#extra"), Digest: provenance.SHA256([]byte("extra"))}
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		reason  string
		pointer string
	}{
		{name: "invalid before duplicate", mutate: func(document map[string]any) {
			provenanceDocument(document)["kind"] = "derived"
			provenanceDocument(document)["refs"] = []any{"bad", "bad"}
		}, reason: "node_provenance_ref_invalid", pointer: "/schemas/0/provenance/refs/0"},
		{name: "invalid compound authored tail", mutate: func(document map[string]any) {
			provenanceDocument(document)["kind"] = "derived"
			provenanceDocument(document)["refs"] = []any{existing, "bad", "bad"}
		}, reason: "node_provenance_ref_invalid", pointer: "/schemas/0/provenance/refs/1"},
		{name: "invalid compound reversed", mutate: func(document map[string]any) {
			provenanceDocument(document)["kind"] = "derived"
			provenanceDocument(document)["refs"] = []any{"bad", "bad", existing}
		}, reason: "node_provenance_ref_invalid", pointer: "/schemas/0/provenance/refs/0"},
		{name: "duplicate before unresolved", mutate: func(document map[string]any) {
			provenanceDocument(document)["kind"] = "derived"
			provenanceDocument(document)["refs"] = []any{"repo:contracts/api.api#missing", "repo:contracts/api.api#missing"}
		}, reason: "node_provenance_ref_duplicate", pointer: "/schemas/0/provenance/refs/1"},
		{name: "duplicate unresolved authored tail", mutate: func(document map[string]any) {
			provenanceDocument(document)["kind"] = "derived"
			provenanceDocument(document)["refs"] = []any{existing, "repo:contracts/api.api#missing", "repo:contracts/api.api#missing"}
		}, reason: "node_provenance_ref_duplicate", pointer: "/schemas/0/provenance/refs/2"},
		{name: "duplicate unresolved reversed", mutate: func(document map[string]any) {
			provenanceDocument(document)["kind"] = "derived"
			provenanceDocument(document)["refs"] = []any{"repo:contracts/api.api#missing", "repo:contracts/api.api#missing", existing}
		}, reason: "node_provenance_ref_duplicate", pointer: "/schemas/0/provenance/refs/1"},
		{name: "shape before unresolved", mutate: func(document map[string]any) {
			provenanceDocument(document)["refs"] = []any{"repo:contracts/api.api#missing", "repo:contracts/api.api#other"}
		}, reason: "node_provenance_shape_invalid", pointer: "/schemas/0/provenance/refs"},
		{name: "shape reversed before unresolved", mutate: func(document map[string]any) {
			provenanceDocument(document)["refs"] = []any{"repo:contracts/api.api#other", "repo:contracts/api.api#missing"}
		}, reason: "node_provenance_shape_invalid", pointer: "/schemas/0/provenance/refs"},
		{name: "origin redundant", mutate: func(document map[string]any) {
			field := firstFieldDocument(document)
			refs := field["provenance"].(map[string]any)["refs"].([]any)
			field["origin"] = map[string]any{"ref": refs[0]}
		}, reason: "origin_ref_redundant", pointer: "/schemas/0/fields/0/origin/ref"},
		{name: "origin invalid", mutate: func(document map[string]any) {
			firstFieldDocument(document)["origin"] = map[string]any{"ref": "bad"}
		}, reason: "origin_ref_invalid", pointer: "/schemas/0/fields/0/origin/ref"},
		{name: "authored extra source index", mutate: func(document map[string]any) {
			document["sources"] = append(document["sources"].([]any), map[string]any{"ref": extra.Ref.String(), "digest": extra.Digest.String()})
			digest, digestErr := api.ComputeSourceDigest(append(append([]provenance.Source(nil), sources...), extra))
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			document["sourceDigest"] = digest.String()
		}, reason: "source_unreferenced", pointer: "/sources/4/ref"},
	}
	for _, test := range tests {
		for _, format := range []string{"json", "yaml"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				var document map[string]any
				if err := json.Unmarshal(canonical, &document); err != nil {
					t.Fatal(err)
				}
				test.mutate(document)
				var data []byte
				var marshalErr error
				if format == "json" {
					data, marshalErr = json.Marshal(document)
				} else {
					data, marshalErr = yaml.Marshal(document)
				}
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				_, parseErr := api.Parse("api-manifest."+format, data)
				manifestErr := requireD030APIError(t, parseErr, test.reason, test.pointer)
				if manifestErr.Source() != "api-manifest."+format || manifestErr.Line() == 0 || manifestErr.Column() == 0 {
					t.Fatalf("source location = (%q,%d,%d)", manifestErr.Source(), manifestErr.Line(), manifestErr.Column())
				}
			})
		}
	}
}

func TestParseSchemaFieldAndOperationProvenanceGates(t *testing.T) {
	manifest, err := api.NewManifest(validWireSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		reason  string
		pointer string
	}{
		{name: "operation kind", mutate: func(document map[string]any) {
			operation := document["operations"].([]any)[0].(map[string]any)
			operation["provenance"].(map[string]any)["kind"] = "other"
		}, reason: "node_provenance_kind_invalid", pointer: "/operations/0/provenance/kind"},
		{name: "schema shape", mutate: func(document map[string]any) {
			schema := document["schemas"].([]any)[0].(map[string]any)
			schema["provenance"].(map[string]any)["refs"] = []any{"repo:contracts/api.api#missing-a", "repo:contracts/api.api#missing-b"}
		}, reason: "node_provenance_shape_invalid", pointer: "/schemas/0/provenance/refs"},
		{name: "field duplicate before unresolved", mutate: func(document map[string]any) {
			schema := document["schemas"].([]any)[0].(map[string]any)
			field := schema["fields"].([]any)[0].(map[string]any)
			field["provenance"].(map[string]any)["kind"] = "derived"
			field["provenance"].(map[string]any)["refs"] = []any{"repo:contracts/api.api#missing", "repo:contracts/api.api#missing"}
		}, reason: "node_provenance_ref_duplicate", pointer: "/schemas/0/fields/0/provenance/refs/1"},
	}
	for _, test := range tests {
		for _, format := range []string{"json", "yaml"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				var document map[string]any
				if err := json.Unmarshal(canonical, &document); err != nil {
					t.Fatal(err)
				}
				test.mutate(document)
				data, err := marshalManifestDocument(format, document)
				if err != nil {
					t.Fatal(err)
				}
				_, parseErr := api.Parse("api-manifest."+format, data)
				requireD030APIError(t, parseErr, test.reason, test.pointer)
			})
		}
	}
}

func marshalManifestDocument(format string, document map[string]any) ([]byte, error) {
	if format == "json" {
		return json.Marshal(document)
	}
	return yaml.Marshal(document)
}

func provenanceDocument(document map[string]any) map[string]any {
	return document["schemas"].([]any)[0].(map[string]any)["provenance"].(map[string]any)
}

func firstFieldDocument(document map[string]any) map[string]any {
	return document["schemas"].([]any)[0].(map[string]any)["fields"].([]any)[0].(map[string]any)
}

func assertProvenanceProjection(t *testing.T, manifest api.Manifest, sources []provenance.Source) {
	t.Helper()
	schema, ok := manifest.Schema("request")
	if !ok {
		t.Fatal("Schema(request) not found")
	}
	schemaProvenance, ok := schema.Provenance()
	if !ok || schemaProvenance.Kind() != api.NodeCanonical || !reflect.DeepEqual(schemaProvenance.Refs(), []provenance.SourceRef{sources[0].Ref}) {
		t.Fatalf("schema provenance = %#v, %v", schemaProvenance, ok)
	}
	field, ok := schema.Field("id")
	if !ok {
		t.Fatal("Field(id) not found")
	}
	fieldProvenance := field.Provenance()
	if fieldProvenance.Kind() != api.NodeDerived || !reflect.DeepEqual(fieldProvenance.Refs(), []provenance.SourceRef{sources[1].Ref, sources[2].Ref}) {
		t.Fatalf("field provenance = %#v", fieldProvenance)
	}
	origin, ok := field.Origin()
	if !ok || origin.Ref() != sources[3].Ref {
		t.Fatalf("field origin = %#v, %v", origin, ok)
	}
	for _, source := range sources {
		if got, exists := manifest.Source(source.Ref); !exists || got != source {
			t.Fatalf("Source(%s) = %#v, %v", source.Ref.String(), got, exists)
		}
	}
	refs := fieldProvenance.Refs()
	refs[0] = provenance.SourceRef{}
	if field.Provenance().Refs()[0] != sources[1].Ref {
		t.Fatal("mutating Refs() changed field provenance")
	}
	manifestSources := manifest.Sources()
	manifestSources[0] = provenance.Source{}
	if got, _ := manifest.Source(sources[0].Ref); got != sources[0] {
		t.Fatal("mutating Sources() changed exact lookup")
	}
}

func provenanceManifestSpec(t *testing.T) (api.ManifestSpec, []provenance.Source) {
	t.Helper()
	refs := []provenance.SourceRef{
		mustRef(t, "repo:contracts/api.api#schema-request"),
		mustRef(t, "repo:contracts/api.api#field-id"),
		mustRef(t, "repo:contracts/service.proto#field-id"),
		mustRef(t, "repo:contracts/relation.yaml#binding-id"),
	}
	sources := make([]provenance.Source, len(refs))
	for index, ref := range refs {
		sources[index] = provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte(ref.String()))}
	}
	return api.ManifestSpec{
		Sources: sources,
		Schemas: []api.SchemaSpec{
			{ID: "request", Kind: api.SchemaObject, Provenance: &api.NodeProvenanceSpec{Kind: api.NodeCanonical, Refs: []provenance.SourceRef{refs[0]}}, Fields: []api.FieldSpec{{Name: "id", SchemaRef: "scalar.string", Required: true, Provenance: api.NodeProvenanceSpec{Kind: api.NodeDerived, Refs: []provenance.SourceRef{refs[2], refs[1]}}, Origin: &api.OriginBindingSpec{Ref: refs[3]}}}},
			{ID: "scalar.string", Kind: api.SchemaString},
		},
	}, sources
}

func cloneProvenanceSpec(input api.ManifestSpec) api.ManifestSpec {
	result := input
	result.Sources = append([]provenance.Source(nil), input.Sources...)
	result.Schemas = append([]api.SchemaSpec(nil), input.Schemas...)
	for index := range result.Schemas {
		if input.Schemas[index].Provenance != nil {
			value := *input.Schemas[index].Provenance
			value.Refs = append([]provenance.SourceRef(nil), value.Refs...)
			result.Schemas[index].Provenance = &value
		}
		result.Schemas[index].Fields = append([]api.FieldSpec(nil), input.Schemas[index].Fields...)
		for fieldIndex := range result.Schemas[index].Fields {
			result.Schemas[index].Fields[fieldIndex].Provenance.Refs = append([]provenance.SourceRef(nil), input.Schemas[index].Fields[fieldIndex].Provenance.Refs...)
			if input.Schemas[index].Fields[fieldIndex].Origin != nil {
				origin := *input.Schemas[index].Fields[fieldIndex].Origin
				result.Schemas[index].Fields[fieldIndex].Origin = &origin
			}
		}
	}
	return result
}

func requireD030APIError(t *testing.T, err error, reason, pointer string) *api.Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var manifestErr *api.Error
	if !errors.As(err, &manifestErr) {
		t.Fatalf("error type = %T", err)
	}
	if manifestErr.Code() != "api_manifest_invalid" || manifestErr.Reason() != reason || manifestErr.Pointer() != pointer {
		t.Fatalf("error projection = (%q,%q,%q), want (%q,%q,%q)", manifestErr.Code(), manifestErr.Reason(), manifestErr.Pointer(), "api_manifest_invalid", reason, pointer)
	}
	return manifestErr
}
