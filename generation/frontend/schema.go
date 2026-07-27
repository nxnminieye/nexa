package frontend

import (
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/nxnminieye/nexa/generation/httpapi"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

const pageSchemaURL = "https://nexa.dev/schemas/generation/frontend/frontend-page-spec-v1.schema.json"
const localeSchemaURL = "https://nexa.dev/schemas/generation/frontend/frontend-locale-v1.schema.json"
const irSchemaURL = "https://nexa.dev/schemas/generation/frontend/frontend-ir-v1.schema.json"
const renderSchemaURL = "https://nexa.dev/schemas/generation/frontend/frontend-render-request-v1.schema.json"

//go:embed frontend-page-spec-v1.schema.json
var embeddedPageSchema string

//go:embed frontend-locale-v1.schema.json
var embeddedLocaleSchema string

//go:embed frontend-ir-v1.schema.json
var embeddedIRSchema string

//go:embed frontend-render-request-v1.schema.json
var embeddedRenderSchema string

var pageSchemaOnce sync.Once
var compiledPageSchema *jsonschema.Schema
var pageSchemaError error
var localeSchemaOnce sync.Once
var compiledLocaleSchema *jsonschema.Schema
var localeSchemaError error

func PageSpecSchema() []byte      { return []byte(embeddedPageSchema) }
func LocaleSchema() []byte        { return []byte(embeddedLocaleSchema) }
func IRSchema() []byte            { return []byte(embeddedIRSchema) }
func RenderRequestSchema() []byte { return []byte(embeddedRenderSchema) }

func validateWireSchema(schemaURL string, schemaBytes []byte, document any) error {
	compiler := jsonschema.NewCompiler()
	resources := map[string][]byte{
		schemaURL:   schemaBytes,
		irSchemaURL: IRSchema(),
		"https://nexa.dev/schemas/generation/httpapi/api-ir-v1.schema.json": httpapi.Schema(),
	}
	for url, data := range resources {
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		if err := compiler.AddResource(url, value); err != nil {
			return err
		}
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return err
	}
	return compiled.Validate(normalized)
}

func validatePageSchema(document any) error {
	pageSchemaOnce.Do(func() {
		var value any
		if pageSchemaError = json.Unmarshal([]byte(embeddedPageSchema), &value); pageSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if pageSchemaError = compiler.AddResource(pageSchemaURL, value); pageSchemaError != nil {
			return
		}
		compiledPageSchema, pageSchemaError = compiler.Compile(pageSchemaURL)
	})
	if pageSchemaError != nil {
		return pageSchemaError
	}
	return compiledPageSchema.Validate(document)
}

func validateLocaleSchema(document any) error {
	localeSchemaOnce.Do(func() {
		var value any
		if localeSchemaError = json.Unmarshal([]byte(embeddedLocaleSchema), &value); localeSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if localeSchemaError = compiler.AddResource(localeSchemaURL, value); localeSchemaError != nil {
			return
		}
		compiledLocaleSchema, localeSchemaError = compiler.Compile(localeSchemaURL)
	})
	if localeSchemaError != nil {
		return localeSchemaError
	}
	return compiledLocaleSchema.Validate(document)
}

func schemaFailures(source string, err error) []*Error {
	return validationFailures(source, err, pageError)
}

func localeSchemaFailures(source string, err error) []*Error {
	return validationFailures(source, err, localeError)
}

func validationFailures(source string, err error, makeError func(string, string, string, string) *Error) []*Error {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return []*Error{makeError("document_invalid", source, "", "frontend document does not match its schema")}
	}
	var result []*Error
	for _, leaf := range validationLeaves(validation) {
		locations := [][]string{leaf.InstanceLocation}
		switch failure := leaf.ErrorKind.(type) {
		case *kind.Required:
			locations = nil
			for _, name := range failure.Missing {
				locations = append(locations, append(append([]string(nil), leaf.InstanceLocation...), name))
			}
		case *kind.AdditionalProperties:
			locations = nil
			for _, name := range failure.Properties {
				locations = append(locations, append(append([]string(nil), leaf.InstanceLocation...), name))
			}
		}
		for _, location := range locations {
			reason := "document_invalid"
			if _, ok := leaf.ErrorKind.(*kind.AdditionalProperties); ok {
				reason = "document_unknown_field"
			}
			result = append(result, makeError(reason, source, pointerOf(location), "frontend document does not match its schema"))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].pointer < result[j].pointer })
	return result
}

func validationLeaves(err *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(err.Causes) == 0 {
		return []*jsonschema.ValidationError{err}
	}
	var result []*jsonschema.ValidationError
	for _, cause := range err.Causes {
		result = append(result, validationLeaves(cause)...)
	}
	return result
}
func pointerOf(location []string) string {
	var result strings.Builder
	for _, part := range location {
		result.WriteByte('/')
		result.WriteString(strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	return result.String()
}
