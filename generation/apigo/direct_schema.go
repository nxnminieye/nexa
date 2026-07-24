package apigo

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/nxnminieye/nexa/generation/httpapi"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	apiRequestSchemaURL = "https://nexa.dev/schemas/generation/apigo/api-go-request-v2.schema.json"
	apiResultSchemaURL  = "https://nexa.dev/schemas/generation/apigo/api-go-result-v2.schema.json"
	apiIRSchemaURL      = "https://nexa.dev/schemas/generation/httpapi/api-ir-v1.schema.json"
)

//go:embed api-go-request-v2.schema.json
var embeddedAPIGoRequestSchema []byte

//go:embed api-go-result-v2.schema.json
var embeddedAPIGoResultSchema []byte
var apiSchemasOnce sync.Once
var apiRequestSchema, apiResultSchema *jsonschema.Schema
var apiSchemaError error

func APIGoRequestSchema() []byte { return append([]byte(nil), embeddedAPIGoRequestSchema...) }
func APIGoResultSchema() []byte  { return append([]byte(nil), embeddedAPIGoResultSchema...) }
func compileAPISchemas() error {
	apiSchemasOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		for _, item := range []struct {
			url  string
			data []byte
		}{{apiRequestSchemaURL, embeddedAPIGoRequestSchema}, {apiResultSchemaURL, embeddedAPIGoResultSchema}, {apiIRSchemaURL, httpapi.Schema()}} {
			var document any
			if apiSchemaError = json.Unmarshal(item.data, &document); apiSchemaError != nil {
				return
			}
			if apiSchemaError = compiler.AddResource(item.url, document); apiSchemaError != nil {
				return
			}
		}
		if apiRequestSchema, apiSchemaError = compiler.Compile(apiRequestSchemaURL); apiSchemaError != nil {
			return
		}
		apiResultSchema, apiSchemaError = compiler.Compile(apiResultSchemaURL)
	})
	return apiSchemaError
}
func validateAPIGoRequestSchema(value any) error {
	if err := compileAPISchemas(); err != nil {
		return err
	}
	document, err := apiSchemaDocument(value)
	if err != nil {
		return err
	}
	return apiRequestSchema.Validate(document)
}
func validateAPIGoResultSchema(value any) error {
	if err := compileAPISchemas(); err != nil {
		return err
	}
	document, err := apiSchemaDocument(value)
	if err != nil {
		return err
	}
	return apiResultSchema.Validate(document)
}
func apiSchemaDocument(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document any
	err = json.Unmarshal(encoded, &document)
	return document, err
}
