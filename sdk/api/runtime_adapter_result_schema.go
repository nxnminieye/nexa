package api

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const runtimeAdapterResultSchemaURL = "https://nexa.dev/schemas/runtime-adapter-result-v1.schema.json"

//go:embed runtime-adapter-result-v1.schema.json
var embeddedRuntimeAdapterResultSchema []byte

var runtimeAdapterResultSchemaOnce sync.Once
var compiledRuntimeAdapterResultSchema *jsonschema.Schema
var runtimeAdapterResultSchemaError error

// RuntimeAdapterResultSchema returns independent schema bytes.
func RuntimeAdapterResultSchema() []byte {
	return append([]byte(nil), embeddedRuntimeAdapterResultSchema...)
}

func runtimeAdapterResultDocumentSchema() (*jsonschema.Schema, error) {
	runtimeAdapterResultSchemaOnce.Do(func() {
		var document any
		if runtimeAdapterResultSchemaError = json.Unmarshal(embeddedRuntimeAdapterResultSchema, &document); runtimeAdapterResultSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if runtimeAdapterResultSchemaError = compiler.AddResource(runtimeAdapterResultSchemaURL, document); runtimeAdapterResultSchemaError != nil {
			return
		}
		compiledRuntimeAdapterResultSchema, runtimeAdapterResultSchemaError = compiler.Compile(runtimeAdapterResultSchemaURL)
	})
	return compiledRuntimeAdapterResultSchema, runtimeAdapterResultSchemaError
}
