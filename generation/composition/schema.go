package composition

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaURL = "https://nexa.dev/schemas/generation/composition/composition-ir-v1.schema.json"

//go:embed composition-ir-v1.schema.json
var embeddedSchema []byte

var schemaOnce sync.Once
var compiledSchema *jsonschema.Schema
var schemaError error

func Schema() []byte { return append([]byte(nil), embeddedSchema...) }

func validateSnapshotSchema(value any) error {
	schemaOnce.Do(func() {
		var document any
		if schemaError = json.Unmarshal(embeddedSchema, &document); schemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if schemaError = compiler.AddResource(schemaURL, document); schemaError != nil {
			return
		}
		compiledSchema, schemaError = compiler.Compile(schemaURL)
	})
	if schemaError != nil {
		return schemaError
	}
	return compiledSchema.Validate(value)
}
