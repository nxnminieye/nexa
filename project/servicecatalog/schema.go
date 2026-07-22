package servicecatalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

const schemaURL = "https://nexa.dev/schemas/project/service-catalog-v1.schema.json"

//go:embed service-catalog-v1.schema.json
var embeddedSchema string

var (
	schemaOnce     sync.Once
	compiledSchema *jsonschema.Schema
	schemaError    error
)

func Schema() []byte {
	return []byte(embeddedSchema)
}

func serviceCatalogSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		var document any
		if schemaError = json.Unmarshal([]byte(embeddedSchema), &document); schemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if schemaError = compiler.AddResource(schemaURL, document); schemaError != nil {
			return
		}
		compiledSchema, schemaError = compiler.Compile(schemaURL)
	})
	return compiledSchema, schemaError
}

func normalizedDocument(documentJSON []byte) (any, error) {
	var document any
	err := json.Unmarshal(documentJSON, &document)
	return document, err
}

func validateDocumentSchema(document any) error {
	schema, err := serviceCatalogSchema()
	if err != nil {
		return err
	}
	return schema.Validate(document)
}

func schemaValidationErrors(source string, err error) []*Error {
	var validationError *jsonschema.ValidationError
	if !errors.As(err, &validationError) {
		return []*Error{newError(
			"service_catalog_invalid", "document_invalid", source, "",
			"service catalog does not match its schema",
		)}
	}
	leaves := validationLeaves(validationError)
	var failures []*Error
	for _, leaf := range leaves {
		for _, location := range validationLocations(leaf) {
			failures = append(failures, newError(
				"service_catalog_invalid", schemaReason(leaf), source, instancePointer(location),
				"service catalog does not match its schema",
			))
		}
	}
	if len(failures) == 0 {
		failures = append(failures, newError(
			"service_catalog_invalid", "document_invalid", source, "",
			"service catalog does not match its schema",
		))
	}
	return failures
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

func compareLocations(left, right []string, normalized any) int {
	limit := min(len(left), len(right))
	parent := normalized
	for index := 0; index < limit; index++ {
		if _, isArray := parent.([]any); isArray {
			leftNumber, leftErr := strconv.Atoi(left[index])
			rightNumber, rightErr := strconv.Atoi(right[index])
			if leftErr != nil || rightErr != nil {
				return strings.Compare(left[index], right[index])
			}
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
		} else {
			if comparison := strings.Compare(left[index], right[index]); comparison != 0 {
				return comparison
			}
		}
		parent = childAt(parent, left[index])
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func childAt(parent any, component string) any {
	switch value := parent.(type) {
	case map[string]any:
		return value[component]
	case []any:
		index, err := strconv.Atoi(component)
		if err == nil && index >= 0 && index < len(value) {
			return value[index]
		}
	}
	return nil
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
