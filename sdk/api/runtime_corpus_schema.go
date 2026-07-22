package api

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const runtimeCorpusSchemaURL = "https://nexa.dev/schemas/runtime-corpus-v1.schema.json"

//go:embed runtime-corpus-v1.schema.json
var embeddedRuntimeCorpusSchema []byte

var runtimeCorpusSchemaOnce sync.Once
var compiledRuntimeCorpusSchema *jsonschema.Schema
var runtimeCorpusSchemaError error

// RuntimeCorpusSchema returns independent schema bytes.
func RuntimeCorpusSchema() []byte { return append([]byte(nil), embeddedRuntimeCorpusSchema...) }

func runtimeCorpusDocumentSchema() (*jsonschema.Schema, error) {
	runtimeCorpusSchemaOnce.Do(func() {
		var document any
		if runtimeCorpusSchemaError = json.Unmarshal(embeddedRuntimeCorpusSchema, &document); runtimeCorpusSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if runtimeCorpusSchemaError = compiler.AddResource(runtimeCorpusSchemaURL, document); runtimeCorpusSchemaError != nil {
			return
		}
		compiledRuntimeCorpusSchema, runtimeCorpusSchemaError = compiler.Compile(runtimeCorpusSchemaURL)
	})
	return compiledRuntimeCorpusSchema, runtimeCorpusSchemaError
}
