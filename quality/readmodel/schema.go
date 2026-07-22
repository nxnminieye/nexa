package readmodel

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaURL = "https://nexa.dev/schemas/quality/quality-read-model-v1.schema.json"

//go:embed quality-read-model-v1.schema.json
var embeddedSchema []byte

var schemaState struct {
	sync.Once
	value *jsonschema.Schema
	err   error
}

func Schema() []byte { return append([]byte(nil), embeddedSchema...) }

func compiledSchema() (*jsonschema.Schema, error) {
	schemaState.Do(func() {
		var document any
		if schemaState.err = json.Unmarshal(embeddedSchema, &document); schemaState.err != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if schemaState.err = compiler.AddResource(schemaURL, document); schemaState.err != nil {
			return
		}
		schemaState.value, schemaState.err = compiler.Compile(schemaURL)
	})
	return schemaState.value, schemaState.err
}
