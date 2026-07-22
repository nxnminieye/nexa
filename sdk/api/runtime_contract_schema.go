package api

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const runtimeContractSchemaURL = "https://nexa.dev/schemas/runtime-contract-v1.schema.json"

//go:embed runtime-contract-v1.schema.json
var embeddedRuntimeContractSchema []byte

var runtimeContractSchemaOnce sync.Once
var compiledRuntimeContractSchema *jsonschema.Schema
var runtimeContractSchemaError error

func RuntimeContractSchema() []byte {
	return append([]byte(nil), embeddedRuntimeContractSchema...)
}

func runtimeContractDocumentSchema() (*jsonschema.Schema, error) {
	runtimeContractSchemaOnce.Do(func() {
		var document any
		if runtimeContractSchemaError = json.Unmarshal(embeddedRuntimeContractSchema, &document); runtimeContractSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if runtimeContractSchemaError = compiler.AddResource(runtimeContractSchemaURL, document); runtimeContractSchemaError != nil {
			return
		}
		compiledRuntimeContractSchema, runtimeContractSchemaError = compiler.Compile(runtimeContractSchemaURL)
	})
	return compiledRuntimeContractSchema, runtimeContractSchemaError
}
