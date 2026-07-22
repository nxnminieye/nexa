package sdkpython

import (
	"encoding/json"

	"github.com/nxnminieye/nexa/internal/sdkpythonassets"
)

const draft202012 = "https://json-schema.org/draft/2020-12/schema"

type schemaSet struct {
	writeInput, checkInput, buildInput    []byte
	writeOutput, checkOutput, buildOutput []byte
}

func newSchemaSet(roles []sdkpythonassets.Role) (schemaSet, error) {
	roleArray := roleArraySchema(roles)
	writeInput, err := canonicalSchema(inputSchema(
		"nexa.dev/sdk-python-assets-write-input/v1",
		[]string{"repo-root"},
		map[string]any{
			"repo-root": pathInputSchema(),
		},
	))
	if err != nil {
		return schemaSet{}, err
	}
	checkInput, err := canonicalSchema(inputSchema(
		"nexa.dev/sdk-python-assets-check-input/v1",
		[]string{"repo-root"},
		map[string]any{"repo-root": pathInputSchema()},
	))
	if err != nil {
		return schemaSet{}, err
	}
	buildInput, err := canonicalSchema(inputSchema(
		"nexa.dev/sdk-python-assets-build-input/v1",
		[]string{"repo-root", "python", "matrix-target", "wheelhouse", "work-dir", "out"},
		map[string]any{
			"repo-root":     pathInputSchema(),
			"python":        pathInputSchema(),
			"matrix-target": enumStringSchema([]string{"darwin-arm64", "linux-x86_64"}, 1, 4096),
			"wheelhouse":    pathInputSchema(),
			"work-dir":      pathInputSchema(),
			"out":           pathInputSchema(),
		},
	))
	if err != nil {
		return schemaSet{}, err
	}

	writeOutput, err := canonicalSchema(objectSchema(
		"nexa.dev/sdk-python-assets-write-result/v1",
		[]string{"apiVersion", "changed", "indexDigest", "bootstrapDigest", "roles", "objectsWritten", "objectsReused"},
		map[string]any{
			"apiVersion":      constStringSchema("nexa.dev/sdk-python-assets-write-result/v1"),
			"changed":         map[string]any{"type": "boolean"},
			"indexDigest":     digestSchema(),
			"bootstrapDigest": digestSchema(),
			"roles":           roleArray,
			"objectsWritten":  objectPathArraySchema(),
			"objectsReused":   objectPathArraySchema(),
		},
	))
	if err != nil {
		return schemaSet{}, err
	}
	checkOutput, err := canonicalSchema(objectSchema(
		"nexa.dev/sdk-python-assets-check-result/v1",
		[]string{"apiVersion", "indexDigest", "bootstrapDigest", "status", "roles", "objectCount"},
		map[string]any{
			"apiVersion":      constStringSchema("nexa.dev/sdk-python-assets-check-result/v1"),
			"indexDigest":     digestSchema(),
			"bootstrapDigest": digestSchema(),
			"status":          constStringSchema("clean"),
			"roles":           roleArray,
			"objectCount":     integerSchema(0, 4096),
		},
	))
	if err != nil {
		return schemaSet{}, err
	}
	buildOutput, err := canonicalSchema(objectSchema(
		"nexa.dev/sdk-python-assets-build-result/v1",
		[]string{"apiVersion", "indexDigest", "bootstrapDigest", "matrixTarget", "pythonVersion", "pathBase", "wheelPath", "wheelDigest", "wheelSize", "recordDigest", "roles"},
		map[string]any{
			"apiVersion":      constStringSchema("nexa.dev/sdk-python-assets-build-result/v1"),
			"indexDigest":     digestSchema(),
			"bootstrapDigest": digestSchema(),
			"matrixTarget":    enumStringSchema([]string{"darwin-arm64", "linux-x86_64"}, 1, 256),
			"pythonVersion":   stringPatternSchema(`^3\.(?:9|12)\.[0-9]+$`, 5, 256),
			"pathBase":        constStringSchema("out"),
			"wheelPath":       stringPatternSchema(`^[^/\\]{1,251}\.whl$`, 5, 255),
			"wheelDigest":     digestSchema(),
			"wheelSize":       integerSchema(1, 9223372036854775807),
			"recordDigest":    digestSchema(),
			"roles":           roleArray,
		},
	))
	if err != nil {
		return schemaSet{}, err
	}
	return schemaSet{
		writeInput: writeInput, checkInput: checkInput, buildInput: buildInput,
		writeOutput: writeOutput, checkOutput: checkOutput, buildOutput: buildOutput,
	}, nil
}

func inputSchema(id string, required []string, properties map[string]any) map[string]any {
	return objectSchema(id, required, properties)
}

func objectSchema(id string, required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"$id": id, "$schema": draft202012, "additionalProperties": false,
		"properties": properties, "required": append([]string(nil), required...), "type": "object",
	}
}

func roleArraySchema(roles []sdkpythonassets.Role) map[string]any {
	prefix := make([]any, len(roles))
	for index, role := range roles {
		required := []string{"id", "apiVersion", "mediaType", "path", "digest"}
		properties := map[string]any{
			"id":         constStringSchema(role.ID),
			"apiVersion": constStringSchema(role.APIVersion),
			"mediaType":  constStringSchema(role.MediaType),
			"path":       constStringSchema(role.Path),
			"digest":     constStringSchema(role.Digest),
		}
		if role.SchemaRole != "" {
			required = append(required, "schemaRole")
			properties["schemaRole"] = constStringSchema(role.SchemaRole)
		}
		prefix[index] = map[string]any{
			"additionalProperties": false,
			"properties":           properties,
			"required":             required,
			"type":                 "object",
		}
	}
	return map[string]any{
		"items": false, "maxItems": len(roles), "minItems": len(roles),
		"prefixItems": prefix, "type": "array",
	}
}

func pathInputSchema() map[string]any {
	return map[string]any{"maxLength": 4096, "minLength": 1, "type": "string"}
}

func constStringSchema(value string) map[string]any {
	return map[string]any{"const": value, "maxLength": 256, "minLength": 1, "type": "string"}
}

func enumStringSchema(values []string, minLength, maxLength int) map[string]any {
	return map[string]any{
		"enum": append([]string(nil), values...), "maxLength": maxLength,
		"minLength": minLength, "type": "string",
	}
}

func stringPatternSchema(pattern string, minLength, maxLength int) map[string]any {
	return map[string]any{
		"maxLength": maxLength, "minLength": minLength, "pattern": pattern, "type": "string",
	}
}

func digestSchema() map[string]any {
	return stringPatternSchema(`^sha256:[0-9a-f]{64}$`, 71, 71)
}

func digestOrAbsentSchema() map[string]any {
	return stringPatternSchema(`^(?:absent|sha256:[0-9a-f]{64})$`, 6, 71)
}

func integerSchema(minimum, maximum int64) map[string]any {
	return map[string]any{
		"maximum": maximum, "minimum": minimum, "type": "integer",
	}
}

func objectPathArraySchema() map[string]any {
	return map[string]any{
		"items":    stringPatternSchema(`^sdk/python/nexa/_generated/objects/sha256/[0-9a-f]{64}\.json$`, 111, 111),
		"maxItems": 4096, "minItems": 0, "type": "array", "uniqueItems": true,
	}
}

func canonicalSchema(document map[string]any) ([]byte, error) {
	return json.Marshal(document)
}
