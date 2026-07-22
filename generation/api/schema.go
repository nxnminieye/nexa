package api

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

const documentSchemaURL = "https://nexa.dev/schemas/generation/api-manifest-v1.schema.json"

//go:embed api-manifest-v1.schema.json
var embeddedDocumentSchema string

var documentSchemaOnce sync.Once
var compiledDocumentSchema *jsonschema.Schema
var documentSchemaError error

func DocumentSchema() []byte { return []byte(embeddedDocumentSchema) }

func apiDocumentSchema() (*jsonschema.Schema, error) {
	documentSchemaOnce.Do(func() {
		var document any
		if documentSchemaError = json.Unmarshal([]byte(embeddedDocumentSchema), &document); documentSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if documentSchemaError = compiler.AddResource(documentSchemaURL, document); documentSchemaError != nil {
			return
		}
		compiledDocumentSchema, documentSchemaError = compiler.Compile(documentSchemaURL)
	})
	return compiledDocumentSchema, documentSchemaError
}

func normalizedDocument(data []byte) (any, error) {
	var document any
	err := json.Unmarshal(data, &document)
	return document, err
}

func validateDocumentSchema(document any) error {
	schema, err := apiDocumentSchema()
	if err != nil {
		return err
	}
	return schema.Validate(document)
}

func schemaValidationErrors(source string, err error) []*Error {
	var validationError *jsonschema.ValidationError
	if !errors.As(err, &validationError) {
		return []*Error{sourceError("document_invalid", source, "", "API manifest does not match its schema")}
	}
	var failures []*Error
	for _, leaf := range validationLeaves(validationError) {
		for _, location := range validationLocations(leaf) {
			failures = append(failures, sourceError(schemaReason(leaf), source, instancePointer(location), "API manifest does not match its schema"))
		}
	}
	if len(failures) == 0 {
		failures = append(failures, sourceError("document_invalid", source, "", "API manifest does not match its schema"))
	}
	return failures
}

func validationLeaves(err *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(err.Causes) == 0 {
		return []*jsonschema.ValidationError{err}
	}
	var leaves []*jsonschema.ValidationError
	for _, cause := range err.Causes {
		leaves = append(leaves, validationLeaves(cause)...)
	}
	return leaves
}

func validationLocations(err *jsonschema.ValidationError) [][]string {
	base := append([]string(nil), err.InstanceLocation...)
	switch failure := err.ErrorKind.(type) {
	case *kind.Required:
		locations := make([][]string, 0, len(failure.Missing))
		for _, missing := range failure.Missing {
			locations = append(locations, append(append([]string(nil), base...), missing))
		}
		return locations
	case *kind.AdditionalProperties:
		locations := make([][]string, 0, len(failure.Properties))
		for _, property := range failure.Properties {
			locations = append(locations, append(append([]string(nil), base...), property))
		}
		return locations
	}
	return [][]string{base}
}

func schemaReason(err *jsonschema.ValidationError) string {
	if _, ok := err.ErrorKind.(*kind.AdditionalProperties); ok {
		return "document_unknown_field"
	}
	return "document_invalid"
}

func instancePointer(location []string) string {
	var pointer strings.Builder
	for _, component := range location {
		component = strings.ReplaceAll(component, "~", "~0")
		component = strings.ReplaceAll(component, "/", "~1")
		pointer.WriteByte('/')
		pointer.WriteString(component)
	}
	return pointer.String()
}
