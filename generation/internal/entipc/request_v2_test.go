package entipc

import (
	"encoding/json"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
)

func TestRequestV2CanonicalClosedContract(t *testing.T) {
	request, err := NewRequestV2(RequestV2Spec{ModuleDir: ".", ModulePath: "example.com/service", SchemaDir: "ent/schema", BuildTags: nil})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	tags, ok := root["buildTags"].([]any)
	if !ok || len(tags) != 0 {
		t.Fatalf("buildTags = %#v, want []", root["buildTags"])
	}
	if len(root) != 7 {
		t.Fatalf("members = %v", root)
	}
	parsed, err := ParseRequestV2(mustRequestV2Source(t), canonical)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.RequestDigest() != request.RequestDigest() || parsed.ModulePath() != "example.com/service" {
		t.Fatal("round trip changed request")
	}

	for _, removed := range []string{"repositoryRoot", "moduleGraphDigest", "buildInputDigest", "moduleSources", "tool", "protoDestination", "stagingRoot", "scratchRoot", "privateCache"} {
		mutated := cloneJSONV2(t, root)
		mutated[removed] = "forbidden"
		encoded, _ := json.Marshal(mutated)
		encoded, _ = jcs.Transform(encoded)
		if _, err := ParseRequestV2(mustRequestV2Source(t), encoded); err == nil {
			t.Fatalf("removed member %q accepted", removed)
		}
	}
}

func TestRequestV2SortsAndRejectsDuplicateTagsBeforeEncoding(t *testing.T) {
	request, err := NewRequestV2(RequestV2Spec{ModuleDir: "services/api", ModulePath: "example.com/api", SchemaDir: "services/api/internal/schema", BuildTags: []string{"z", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := request.BuildTags(); len(got) != 2 || got[0] != "a" || got[1] != "z" {
		t.Fatalf("tags = %v", got)
	}
	if _, err := NewRequestV2(RequestV2Spec{ModuleDir: ".", ModulePath: "example.com/api", SchemaDir: "schema", BuildTags: []string{"a", "a"}}); err == nil {
		t.Fatal("duplicate tags accepted")
	}
}

func TestDomainResultV2ClosedEntityloadTuples(t *testing.T) {
	result, err := NewDomainResultV2("entityload", "entity_graph_load_failed", "helper_execution_failed", "", "ent/schema")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalResultV2(result)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := NewRequestV2(RequestV2Spec{ModuleDir: ".", ModulePath: "example.com/api", SchemaDir: "ent/schema", BuildTags: []string{}})
	parsed, err := ParseResultV2(mustRequestV2Source(t), request, canonical)
	if err != nil {
		t.Fatal(err)
	}
	failure, ok := parsed.DomainFailure()
	if !ok || failure.Owner() != "entityload" || failure.Source() != "ent/schema" {
		t.Fatalf("failure = %#v, %v", failure, ok)
	}
	if _, err := NewDomainResultV2("entityload", "entity_graph_load_failed", "unknown", "", "ent/schema"); err == nil {
		t.Fatal("unknown tuple accepted")
	}
}

func cloneJSONV2(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(value)
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func mustRequestV2Source(t *testing.T) provenance.DomainSource {
	t.Helper()
	source, err := provenance.ParseDomainSource("quality/request-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	return source
}
