package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/provenance"
)

func TestParseSnapshotPreservesPublishedAlphaLegacyErrorWire(t *testing.T) {
	data, err := os.ReadFile("testdata/http-api-ir-v1-alpha-legacy.json")
	if err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("fixtures/http-api-ir-v1-alpha-legacy.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := httpapi.ParseSnapshot(source, data)
	if err != nil {
		t.Fatalf("ParseSnapshot() rejected the alpha legacy fixture: %v", err)
	}
	readback, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(readback, data) {
		t.Fatalf("legacy snapshot readback changed bytes: %v", err)
	}
	operations := snapshot.Operations()
	if len(operations) != 1 || operations[0].ID() != "legacy.get" {
		t.Fatalf("legacy operations = %#v", operations)
	}

	t.Run("mixed casing", func(t *testing.T) {
		mutated := mutateLegacyFixture(t, data, func(root map[string]any) {
			projection := firstLegacyProjection(root)
			projection["match"] = projection["Match"]
			delete(projection, "Match")
		})
		if _, err := httpapi.ParseSnapshot(source, mutated); err == nil {
			t.Fatal("ParseSnapshot() accepted mixed current and alpha legacy casing")
		}
	})

	t.Run("unknown projection field", func(t *testing.T) {
		mutated := mutateLegacyFixture(t, data, func(root map[string]any) {
			firstLegacyProjection(root)["unknown"] = true
		})
		if _, err := httpapi.ParseSnapshot(source, mutated); err == nil {
			t.Fatal("ParseSnapshot() accepted an unknown alpha legacy field")
		}
	})

	t.Run("noncanonical", func(t *testing.T) {
		mutated := append(append([]byte(nil), data...), '\n')
		if _, err := httpapi.ParseSnapshot(source, mutated); err == nil {
			t.Fatal("ParseSnapshot() accepted noncanonical alpha legacy bytes")
		}
	})

	t.Run("source digest", func(t *testing.T) {
		mutated := mutateLegacyFixture(t, data, func(root map[string]any) {
			root["sourceDigest"] = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
		})
		if _, err := httpapi.ParseSnapshot(source, mutated); err == nil {
			t.Fatal("ParseSnapshot() accepted a mismatched alpha legacy source digest")
		}
	})

	t.Run("generated provenance", func(t *testing.T) {
		mutated := mutateLegacyFixture(t, data, func(root map[string]any) {
			operation := root["operations"].([]any)[0].(map[string]any)
			sources := operation["provenance"].(map[string]any)["sources"].([]any)
			operation["provenance"].(map[string]any)["sources"] = sources[:1]
		})
		if _, err := httpapi.ParseSnapshot(source, mutated); err == nil {
			t.Fatal("ParseSnapshot() accepted incomplete alpha legacy generated provenance")
		}
	})
}

func TestHTTPAPISchemaFreezesCurrentErrorProjectionWire(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(httpapi.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	definitions := schema["$defs"].(map[string]any)
	projection := definitions["errorProjection"].(map[string]any)
	properties := projection["properties"].(map[string]any)
	if len(properties) != 2 || properties["match"] == nil || properties["project"] == nil || properties["Match"] != nil || properties["Project"] != nil {
		t.Fatalf("error projection schema = %#v", projection)
	}
}

func TestParseSnapshotPreservesHeaderErrorReasonsAcrossCurrentAndLegacyWire(t *testing.T) {
	legacy, err := os.ReadFile("testdata/http-api-ir-v1-alpha-legacy.json")
	if err != nil {
		t.Fatal(err)
	}
	current := mutateLegacyFixture(t, legacy, func(root map[string]any) {
		projection := firstLegacyProjection(root)
		match := projection["Match"].(map[string]any)
		project := projection["Project"].(map[string]any)
		projection["match"] = map[string]any{"code": match["Code"], "domain": match["Domain"]}
		projection["project"] = map[string]any{"code": project["Code"], "domain": project["Domain"], "httpStatus": project["HTTPStatus"]}
		delete(projection, "Match")
		delete(projection, "Project")
	})
	source, err := provenance.ParseDomainSource("fixtures/http-api-ir-v1-header-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range []struct {
		name string
		data []byte
	}{{name: "current", data: current}, {name: "legacy", data: legacy}} {
		wire := wire
		t.Run(wire.name+"/version", func(t *testing.T) {
			mutated := mutateLegacyFixture(t, wire.data, func(root map[string]any) {
				root["apiVersion"] = "nexa.dev/http-api-ir/v2"
			})
			assertSnapshotError(t, source, mutated, "version_unsupported", "/apiVersion")
		})
		t.Run(wire.name+"/kind", func(t *testing.T) {
			mutated := mutateLegacyFixture(t, wire.data, func(root map[string]any) {
				root["kind"] = "Other"
			})
			assertSnapshotError(t, source, mutated, "kind_invalid", "/kind")
		})
	}
}

func mutateLegacyFixture(t *testing.T, data []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	mutate(root)
	ordinary, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func firstLegacyProjection(root map[string]any) map[string]any {
	operation := root["operations"].([]any)[0].(map[string]any)
	return operation["errorProjections"].([]any)[0].(map[string]any)
}

func assertSnapshotError(t *testing.T, source provenance.DomainSource, data []byte, reason, pointer string) {
	t.Helper()
	_, err := httpapi.ParseSnapshot(source, data)
	var typed *httpapi.Error
	if !errors.As(err, &typed) || typed.Reason() != reason || typed.Pointer() != pointer {
		t.Fatalf("ParseSnapshot() error = %#v, want reason=%q pointer=%q", err, reason, pointer)
	}
}
