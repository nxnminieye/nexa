package protocol

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const protocolSchemaURL = "https://nexa.dev/schemas/generation/protocol/protocol-ir-v3.schema.json"

//go:embed protocol-ir-v3.schema.json
var embeddedProtocolSchema []byte

var protocolSchemaOnce sync.Once
var compiledProtocolSchema *jsonschema.Schema
var protocolSchemaError error

func Schema() []byte { return append([]byte(nil), embeddedProtocolSchema...) }

func validateSnapshotSchema(value any) error {
	protocolSchemaOnce.Do(func() {
		var document any
		if protocolSchemaError = json.Unmarshal(embeddedProtocolSchema, &document); protocolSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if protocolSchemaError = compiler.AddResource(protocolSchemaURL, document); protocolSchemaError != nil {
			return
		}
		compiledProtocolSchema, protocolSchemaError = compiler.Compile(protocolSchemaURL)
	})
	if protocolSchemaError != nil {
		return protocolSchemaError
	}
	return compiledProtocolSchema.Validate(value)
}
