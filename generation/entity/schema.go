package entity

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const entitySchemaURL = "https://nexa.dev/schemas/generation/entity/entity-ir-v2.schema.json"

//go:embed entity-ir-v2.schema.json
var embeddedSchema []byte

func Schema() []byte { return append([]byte(nil), embeddedSchema...) }

var (
	entitySchemaOnce          sync.Once
	compiledEntitySchema      *jsonschema.Schema
	compiledEntitySchemaError error
)

func validateEntitySchema(document any) error {
	entitySchemaOnce.Do(func() {
		var schemaDocument any
		if compiledEntitySchemaError = json.Unmarshal(embeddedSchema, &schemaDocument); compiledEntitySchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if compiledEntitySchemaError = compiler.AddResource(entitySchemaURL, schemaDocument); compiledEntitySchemaError != nil {
			return
		}
		compiledEntitySchema, compiledEntitySchemaError = compiler.Compile(entitySchemaURL)
	})
	if compiledEntitySchemaError != nil {
		return compiledEntitySchemaError
	}
	return compiledEntitySchema.Validate(document)
}
