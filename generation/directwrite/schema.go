package directwrite

import (
	_ "embed"
	"encoding/json"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	resultSchemaURL = "https://nexa.dev/schemas/generation/generation-result-v2.schema.json"
	errorSchemaURL  = "https://nexa.dev/schemas/generation/generation-error-details-v2.schema.json"
)

//go:embed generation-result-v2.schema.json
var embeddedGenerationResultSchema string

//go:embed generation-error-details-v2.schema.json
var embeddedGenerationErrorDetailsSchema string

var schemaOnce sync.Once
var resultSchema, errorDetailsSchema *jsonschema.Schema
var schemaCompileError error

// GenerationResultSchema returns an independent copy of the normative schema.
func GenerationResultSchema() []byte { return []byte(embeddedGenerationResultSchema) }

// GenerationErrorDetailsSchema returns an independent copy of the normative schema.
func GenerationErrorDetailsSchema() []byte { return []byte(embeddedGenerationErrorDetailsSchema) }

func compileSchemas() error {
	schemaOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		for _, item := range []struct {
			url  string
			text string
		}{{resultSchemaURL, embeddedGenerationResultSchema}, {errorSchemaURL, embeddedGenerationErrorDetailsSchema}} {
			var document any
			if schemaCompileError = json.Unmarshal([]byte(item.text), &document); schemaCompileError != nil {
				return
			}
			if schemaCompileError = compiler.AddResource(item.url, document); schemaCompileError != nil {
				return
			}
		}
		if resultSchema, schemaCompileError = compiler.Compile(resultSchemaURL); schemaCompileError != nil {
			return
		}
		errorDetailsSchema, schemaCompileError = compiler.Compile(errorSchemaURL)
	})
	return schemaCompileError
}

func validateGenerationResultSchema(input GenerationResult) error {
	if err := compileSchemas(); err != nil {
		return err
	}
	document, err := schemaDocument(input)
	if err != nil {
		return err
	}
	return resultSchema.Validate(document)
}

func validateGenerationErrorDetailsSchema(input GenerationErrorDetails) error {
	if err := compileSchemas(); err != nil {
		return err
	}
	document, err := schemaDocument(input)
	if err != nil {
		return err
	}
	return errorDetailsSchema.Validate(document)
}

func schemaDocument(input any) (any, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var document any
	err = json.Unmarshal(encoded, &document)
	return document, err
}
