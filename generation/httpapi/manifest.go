package httpapi

import (
	"sort"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

func ManifestSpec(document Document) (api.ManifestSpec, error) {
	if document.state == nil {
		return api.ManifestSpec{}, invalid("document_invalid", "", "", "HTTP API document is invalid")
	}
	result := api.ManifestSpec{Sources: document.Sources()}
	extraSchemas := map[string]api.SchemaSpec{}
	typeIDs := map[string]string{}
	for _, item := range document.state.types {
		typeIDs[item.name] = stableSchemaID(item.name)
	}
	for _, item := range document.state.types {
		schemas, err := manifestSchemas(item, typeIDs, extraSchemas)
		if err != nil {
			return api.ManifestSpec{}, err
		}
		result.Schemas = append(result.Schemas, schemas...)
	}
	extraIDs := make([]string, 0, len(extraSchemas))
	for id := range extraSchemas {
		extraIDs = append(extraIDs, id)
	}
	sort.Strings(extraIDs)
	for _, id := range extraIDs {
		result.Schemas = append(result.Schemas, extraSchemas[id])
	}
	for _, item := range document.state.operations {
		provenanceSpec, err := manifestProvenance(item.provenance)
		if err != nil {
			return api.ManifestSpec{}, err
		}
		operation := api.OperationSpec{ID: item.id, Method: item.method, Path: item.path, Provenance: provenanceSpec, RequestSchemaRef: typeIDs[item.requestType], ResponseBody: item.responseBody, ResponseSchemaRef: typeIDs[item.responseType], Auth: api.AuthSpec{Mode: item.auth.mode, Credentials: make([]api.CredentialSpec, len(item.auth.credentials))}, Permission: item.permission, ErrorProjections: append([]api.ErrorProjectionSpec(nil), item.errorProjections...)}
		for index, credential := range item.auth.credentials {
			operation.Auth.Credentials[index] = api.CredentialSpec{ID: credential.id, Type: credential.typeID, Location: credential.location, Name: credential.name}
		}
		if item.hasCapability {
			operation.Capability = &api.CapabilitySpec{ID: item.capability.id, APIVersion: item.capability.apiVersion}
		}
		request := document.state.types[document.state.typeIndex[item.requestType]]
		for _, field := range request.fields {
			if len(field.path) == 1 && field.hasBinding {
				operation.RequestBindings = append(operation.RequestBindings, api.RequestBindingSpec{Field: field.path[0], Location: field.binding.location, Name: field.binding.name})
			}
		}
		result.Operations = append(result.Operations, operation)
	}
	return result, nil
}

func manifestSchemas(item *typeState, typeIDs map[string]string, extra map[string]api.SchemaSpec) ([]api.SchemaSpec, error) {
	rootID := typeIDs[item.name]
	parents := map[string]NodeProvenance{"": item.provenance}
	for _, field := range item.fields {
		if field.valueType.kind == ValueObject {
			parents[pathKey(field.path)] = field.provenance
		}
	}
	parentPaths := make([]string, 0, len(parents))
	for parent := range parents {
		parentPaths = append(parentPaths, parent)
	}
	sort.Strings(parentPaths)
	result := make([]api.SchemaSpec, 0, len(parents))
	for _, parent := range parentPaths {
		owner := parents[parent]
		provenanceSpec, err := manifestProvenance(owner)
		if err != nil {
			return nil, err
		}
		id := rootID
		if parent != "" {
			id += "." + stableSchemaID(parent)
		}
		schema := api.SchemaSpec{ID: id, Kind: api.SchemaObject, Provenance: &provenanceSpec, Fields: []api.FieldSpec{}}
		parentSegments := []string{}
		if parent != "" {
			parentSegments = strings.Split(parent, ".")
		}
		for _, field := range item.fields {
			if len(field.path) != len(parentSegments)+1 || !samePrefix(field.path, parentSegments) {
				continue
			}
			fieldProvenance, err := manifestProvenance(field.provenance)
			if err != nil {
				return nil, err
			}
			schemaRef, err := manifestValueRef(rootID, field.path, field.valueType, field.provenance, typeIDs, extra)
			if err != nil {
				return nil, err
			}
			fieldSpec := api.FieldSpec{Name: field.path[len(field.path)-1], SchemaRef: schemaRef, Required: field.required, Provenance: fieldProvenance}
			if field.hasOrigin {
				fieldSpec.Origin = &api.OriginBindingSpec{Ref: field.origin.Ref}
			}
			schema.Fields = append(schema.Fields, fieldSpec)
		}
		result = append(result, schema)
	}
	return result, nil
}

func manifestValueRef(rootID string, path []string, value ValueType, owner NodeProvenance, typeIDs map[string]string, extra map[string]api.SchemaSpec) (string, error) {
	for value.kind == ValueOptional && value.element != nil {
		value = *value.element
	}
	switch value.kind {
	case ValueRef:
		return typeIDs[value.name], nil
	case ValueObject:
		return rootID + "." + stableSchemaID(pathKey(path)), nil
	case ValueScalar:
		id, kind := scalarSchema(value.name)
		extra[id] = api.SchemaSpec{ID: id, Kind: kind}
		return id, nil
	case ValueArray:
		if value.element == nil {
			return "", invalid("value_type_invalid", "", "", "array element is missing")
		}
		id := rootID + "." + stableSchemaID(pathKey(path)) + ".array"
		itemRef, err := manifestValueRef(rootID, append(append([]string(nil), path...), "item"), *value.element, owner, typeIDs, extra)
		if err != nil {
			return "", err
		}
		provenanceSpec, err := manifestProvenance(owner)
		if err != nil {
			return "", err
		}
		extra[id] = api.SchemaSpec{ID: id, Kind: api.SchemaArray, Provenance: &provenanceSpec, ItemSchemaRef: itemRef}
		return id, nil
	case ValueMap:
		return "", invalid("map_schema_unrepresentable", "", "", "API Manifest v1 cannot represent a map without changing its JSON wire shape")
	default:
		id := "scalar.string"
		extra[id] = api.SchemaSpec{ID: id, Kind: api.SchemaString}
		return id, nil
	}
}

func scalarSchema(name string) (string, api.SchemaKind) {
	switch name {
	case "bool":
		return "scalar.boolean", api.SchemaBoolean
	case "float32", "float64":
		return "scalar.number", api.SchemaNumber
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "scalar.integer", api.SchemaInteger
	default:
		return "scalar.string", api.SchemaString
	}
}

func manifestProvenance(value NodeProvenance) (api.NodeProvenanceSpec, error) {
	if value.kind != NodeFactNative && value.kind != NodeFactGenerated {
		return api.NodeProvenanceSpec{}, invalid("node_provenance_invalid", "", "", "HTTP API node provenance is invalid")
	}
	refs := make([]provenance.SourceRef, len(value.sources))
	for index, source := range value.sources {
		refs[index] = source.Ref
	}
	kind := api.NodeCanonical
	if value.kind == NodeFactGenerated {
		kind = api.NodeDerived
	}
	return api.NodeProvenanceSpec{Kind: kind, Refs: refs}, nil
}

func stableSchemaID(value string) string {
	var result []rune
	for _, r := range value {
		if unicode.IsUpper(r) {
			if len(result) > 0 && result[len(result)-1] != '-' {
				result = append(result, '-')
			}
			result = append(result, unicode.ToLower(r))
			continue
		}
		if unicode.IsLower(r) || unicode.IsDigit(r) {
			result = append(result, r)
			continue
		}
		if len(result) > 0 && result[len(result)-1] != '-' {
			result = append(result, '-')
		}
	}
	return strings.Trim(string(result), "-")
}

func samePrefix(value, prefix []string) bool {
	if len(value) < len(prefix) {
		return false
	}
	for index := range prefix {
		if value[index] != prefix[index] {
			return false
		}
	}
	return true
}
