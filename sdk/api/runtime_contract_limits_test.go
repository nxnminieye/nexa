package api

import (
	"encoding/json"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRuntimeContractLimitsValueDocumentsMatchTheirSchemas(t *testing.T) {
	values := []struct {
		name   string
		value  any
		schema []byte
	}{
		{name: "runtime contract", value: RuntimeContractLimits(), schema: RuntimeContractLimitsSchema()},
		{name: "runtime", value: RuntimeLimits(), schema: RuntimeLimitsSchema()},
		{name: "remote error", value: RemoteErrorLimits(), schema: RemoteErrorLimitsSchema()},
	}
	for _, vector := range values {
		t.Run(vector.name, func(t *testing.T) {
			encoded, err := json.Marshal(vector.value)
			if err != nil || !json.Valid(encoded) || !json.Valid(vector.schema) {
				t.Fatalf("value/schema invalid: value=%s schema=%s err=%v", encoded, vector.schema, err)
			}
			var schemaDocument any
			if err := json.Unmarshal(vector.schema, &schemaDocument); err != nil {
				t.Fatal(err)
			}
			var valueDocument any
			if err := json.Unmarshal(encoded, &valueDocument); err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			resource := "https://nexa.dev/test/" + vector.name + ".schema.json"
			if err := compiler.AddResource(resource, schemaDocument); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile(resource)
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(valueDocument); err != nil {
				t.Fatalf("owner value rejected by schema: %v", err)
			}
			drifted := map[string]any{"apiVersion": "nexa.dev/drift/v1"}
			if err := compiled.Validate(drifted); err == nil {
				t.Fatal("closed value schema accepted drifted/incomplete value")
			}
		})
	}
}
