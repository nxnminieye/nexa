package sdkpython

import (
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/internal/sdkpythonassets"
)

func TestWriteSchemasMatchSimplifiedContract(t *testing.T) {
	bundle, err := sdkpythonassets.NewAssetBundle()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := newSchemaSet(bundle.Roles())
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if json.Unmarshal(schemas.writeInput, &input) != nil {
		t.Fatal("input schema")
	}
	required := input["required"].([]any)
	if len(required) != 1 || required[0] != "repo-root" {
		t.Fatalf("required=%#v", required)
	}
	var output map[string]any
	if json.Unmarshal(schemas.writeOutput, &output) != nil {
		t.Fatal("output schema")
	}
	properties := output["properties"].(map[string]any)
	for _, removed := range []string{"beforeIndexDigest", "afterIndexDigest", "objectsRemoved", "recovered", "cleanupStatus"} {
		if _, ok := properties[removed]; ok {
			t.Fatalf("removed property present: %s", removed)
		}
	}
	if _, ok := properties["indexDigest"]; !ok {
		t.Fatal("indexDigest missing")
	}
}
