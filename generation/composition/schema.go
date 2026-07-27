package composition

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaURLV1 = "https://nexa.dev/schemas/generation/composition/composition-ir-v1.schema.json"
const schemaURLV2 = "https://nexa.dev/schemas/generation/composition/composition-ir-v2.schema.json"

//go:embed composition-ir-v1.schema.json
var embeddedSchemaV1 []byte

//go:embed composition-ir-v2.schema.json
var embeddedSchemaV2 []byte

var schemaOnceV1, schemaOnceV2 sync.Once
var compiledSchemaV1, compiledSchemaV2 *jsonschema.Schema
var schemaErrorV1, schemaErrorV2 error

func Schema() []byte   { return SchemaV2() }
func SchemaV1() []byte { return append([]byte(nil), embeddedSchemaV1...) }
func SchemaV2() []byte { return append([]byte(nil), embeddedSchemaV2...) }

func validateSnapshotSchema(version string, value any) error {
	embedded, schemaURL, once, compiled, schemaErr := embeddedSchemaV1, schemaURLV1, &schemaOnceV1, &compiledSchemaV1, &schemaErrorV1
	if version == APIVersionV2 {
		embedded, schemaURL, once, compiled, schemaErr = embeddedSchemaV2, schemaURLV2, &schemaOnceV2, &compiledSchemaV2, &schemaErrorV2
	}
	once.Do(func() {
		var document any
		if *schemaErr = json.Unmarshal(embedded, &document); *schemaErr != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if *schemaErr = compiler.AddResource(schemaURL, document); *schemaErr != nil {
			return
		}
		*compiled, *schemaErr = compiler.Compile(schemaURL)
	})
	if *schemaErr != nil {
		return *schemaErr
	}
	return (*compiled).Validate(value)
}
