package sourceplugin

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

const schemaURL = "https://nexa.dev/schemas/source-bundle-v1.schema.json"

//go:embed source-bundle-v1.schema.json
var embeddedSchema string

var (
	compiledSchema *jsonschema.Schema
	schemaErr      error
	schemaOnce     sync.Once
)

func Schema() []byte { return []byte(embeddedSchema) }

func sourceBundleSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		var document any
		if schemaErr = json.Unmarshal([]byte(embeddedSchema), &document); schemaErr != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		if schemaErr = compiler.AddResource(schemaURL, document); schemaErr != nil {
			return
		}
		compiledSchema, schemaErr = compiler.Compile(schemaURL)
	})
	return compiledSchema, schemaErr
}

func validateSchema(document any) error {
	schema, err := sourceBundleSchema()
	if err != nil {
		return err
	}
	return schema.Validate(document)
}

type schemaFailure struct {
	authoredPointer string
	publicPointer   string
	reason          string
}

func schemaFailures(err error, normalized any) []schemaFailure {
	var validationError *jsonschema.ValidationError
	if !errors.As(err, &validationError) {
		return []schemaFailure{{reason: "document_invalid"}}
	}
	var failures []schemaFailure
	for _, leaf := range validationLeaves(validationError) {
		for _, location := range validationLocations(leaf) {
			pointer := instancePointer(location)
			reason, include := schemaFailureReason(leaf.ErrorKind, location, normalized)
			if !include {
				continue
			}
			failures = append(failures, schemaFailure{authoredPointer: pointer, publicPointer: pointer, reason: reason})
		}
	}
	return failures
}

func schemaFailureReason(failure jsonschema.ErrorKind, location []string, normalized any) (string, bool) {
	switch failure.(type) {
	case *kind.Required, *kind.Type:
		return "document_invalid", true
	case *kind.AdditionalProperties:
		return "document_unknown_field", true
	case *kind.Pattern, *kind.MinLength, *kind.MaxLength:
		value, ok := valueAtLocation(normalized, location).(string)
		if !ok {
			return "", false
		}
		reason := stableIDReasonAt(location)
		return reason, reason != "" && !validStableID(value)
	default:
		return "", false
	}
}

func stableIDReasonAt(location []string) string {
	if len(location) == 3 && location[0] == "profiles" && location[2] == "id" {
		return "profile_id_invalid"
	}
	if len(location) == 4 && location[0] == "profiles" && location[2] == "requiresProfiles" {
		return "profile_dependency_invalid"
	}
	if len(location) == 5 && location[0] == "profiles" && location[2] == "requiresBundles" && location[4] == "profileId" {
		return "requirement_profile_invalid"
	}
	if len(location) == 5 && location[0] == "profiles" && location[2] == "validations" && location[4] == "id" {
		return "validation_id_invalid"
	}
	return ""
}

func valueAtLocation(document any, location []string) any {
	value := document
	for _, component := range location {
		switch current := value.(type) {
		case map[string]any:
			value = current[component]
		case []any:
			index, err := strconv.Atoi(component)
			if err != nil || index < 0 || index >= len(current) {
				return nil
			}
			value = current[index]
		default:
			return nil
		}
	}
	return value
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
		result := make([][]string, 0, len(failure.Missing))
		for _, missing := range failure.Missing {
			result = append(result, append(append([]string(nil), base...), missing))
		}
		return result
	case *kind.AdditionalProperties:
		result := make([][]string, 0, len(failure.Properties))
		for _, property := range failure.Properties {
			result = append(result, append(append([]string(nil), base...), property))
		}
		return result
	default:
		return [][]string{base}
	}
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
