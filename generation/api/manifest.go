package api

import (
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

func NewManifest(spec ManifestSpec) (Manifest, error) {
	spec = canonicalizeManifestSpec(spec)
	if err := selectError(validateSpec("", spec), normalizedSpec(spec)); err != nil {
		return Manifest{}, err
	}
	digest, err := ComputeSourceDigest(spec.Sources)
	if err != nil {
		return Manifest{}, err
	}
	return manifestFromSpec(spec, digest), nil
}

func canonicalizeManifestSpec(input ManifestSpec) ManifestSpec {
	result := ManifestSpec{
		Sources:    append([]provenance.Source(nil), input.Sources...),
		Schemas:    make([]SchemaSpec, len(input.Schemas)),
		Operations: make([]OperationSpec, len(input.Operations)),
	}
	sort.SliceStable(result.Sources, func(left, right int) bool {
		return result.Sources[left].Ref.String() < result.Sources[right].Ref.String()
	})
	for index, schema := range input.Schemas {
		result.Schemas[index] = schema
		if schema.Provenance != nil {
			value := canonicalizeNodeProvenanceSpec(*schema.Provenance)
			result.Schemas[index].Provenance = &value
		}
		result.Schemas[index].Fields = make([]FieldSpec, len(schema.Fields))
		for fieldIndex, field := range schema.Fields {
			result.Schemas[index].Fields[fieldIndex] = field
			result.Schemas[index].Fields[fieldIndex].Provenance = canonicalizeNodeProvenanceSpec(field.Provenance)
			if field.Origin != nil {
				origin := *field.Origin
				result.Schemas[index].Fields[fieldIndex].Origin = &origin
			}
		}
		sort.SliceStable(result.Schemas[index].Fields, func(left, right int) bool {
			return result.Schemas[index].Fields[left].Name < result.Schemas[index].Fields[right].Name
		})
	}
	sort.SliceStable(result.Schemas, func(left, right int) bool {
		return result.Schemas[left].ID < result.Schemas[right].ID
	})
	for index, operation := range input.Operations {
		result.Operations[index] = operation
		result.Operations[index].Provenance = canonicalizeNodeProvenanceSpec(operation.Provenance)
		result.Operations[index].RequestBindings = append([]RequestBindingSpec(nil), operation.RequestBindings...)
		sort.SliceStable(result.Operations[index].RequestBindings, func(left, right int) bool {
			return result.Operations[index].RequestBindings[left].Field < result.Operations[index].RequestBindings[right].Field
		})
		result.Operations[index].Auth.Credentials = append([]CredentialSpec(nil), operation.Auth.Credentials...)
		sort.SliceStable(result.Operations[index].Auth.Credentials, func(left, right int) bool {
			return result.Operations[index].Auth.Credentials[left].ID < result.Operations[index].Auth.Credentials[right].ID
		})
		result.Operations[index].ErrorProjections = append([]ErrorProjectionSpec(nil), operation.ErrorProjections...)
		sort.SliceStable(result.Operations[index].ErrorProjections, func(left, right int) bool {
			leftMatch := result.Operations[index].ErrorProjections[left].Match
			rightMatch := result.Operations[index].ErrorProjections[right].Match
			return leftMatch.Domain < rightMatch.Domain || leftMatch.Domain == rightMatch.Domain && leftMatch.Code < rightMatch.Code
		})
		if operation.Capability != nil {
			capability := *operation.Capability
			result.Operations[index].Capability = &capability
		}
	}
	sort.SliceStable(result.Operations, func(left, right int) bool {
		return result.Operations[left].ID < result.Operations[right].ID
	})
	return result
}

func canonicalizeNodeProvenanceSpec(input NodeProvenanceSpec) NodeProvenanceSpec {
	input.Refs = append([]provenance.SourceRef(nil), input.Refs...)
	sort.SliceStable(input.Refs, func(left, right int) bool {
		return input.Refs[left].String() < input.Refs[right].String()
	})
	return input
}

func manifestFromSpec(spec ManifestSpec, digest provenance.Digest) Manifest {
	m := Manifest{apiVersion: APIVersion, sourceDigest: digest}
	m.sources = append([]provenance.Source(nil), spec.Sources...)
	sort.Slice(m.sources, func(i, j int) bool { return m.sources[i].Ref.String() < m.sources[j].Ref.String() })
	m.sourceIndex = make(map[string]int, len(m.sources))
	for index, source := range m.sources {
		m.sourceIndex[source.Ref.String()] = index
	}
	m.schemas = make([]Schema, len(spec.Schemas))
	for i, value := range spec.Schemas {
		schema := Schema{id: value.ID, kind: value.Kind, itemSchemaRef: value.ItemSchemaRef}
		if value.Provenance != nil {
			schema.provenance, schema.hasProvenance = nodeProvenanceFromSpec(*value.Provenance), true
		}
		schema.fields = make([]Field, len(value.Fields))
		for j, fieldSpec := range value.Fields {
			field := Field{name: fieldSpec.Name, schemaRef: fieldSpec.SchemaRef, required: fieldSpec.Required, provenance: nodeProvenanceFromSpec(fieldSpec.Provenance)}
			if fieldSpec.Origin != nil {
				field.origin, field.hasOrigin = OriginBinding{ref: fieldSpec.Origin.Ref}, true
			}
			schema.fields[j] = field
		}
		sort.Slice(schema.fields, func(i, j int) bool { return schema.fields[i].name < schema.fields[j].name })
		schema.fieldIndex = make(map[string]int, len(schema.fields))
		for j := range schema.fields {
			schema.fieldIndex[schema.fields[j].name] = j
		}
		m.schemas[i] = schema
	}
	sort.Slice(m.schemas, func(i, j int) bool { return m.schemas[i].id < m.schemas[j].id })
	m.schemaIndex = make(map[string]int, len(m.schemas))
	for i := range m.schemas {
		m.schemaIndex[m.schemas[i].id] = i
	}
	m.operations = make([]Operation, len(spec.Operations))
	for i, value := range spec.Operations {
		operation := Operation{id: value.ID, method: value.Method, path: value.Path, provenance: nodeProvenanceFromSpec(value.Provenance), requestSchemaRef: value.RequestSchemaRef, responseBody: value.ResponseBody, responseSchemaRef: value.ResponseSchemaRef, permission: value.Permission}
		operation.requestBindings = make([]RequestBinding, len(value.RequestBindings))
		for j, binding := range value.RequestBindings {
			name := binding.Name
			if binding.Location == RequestBindingHeader {
				name = strings.ToLower(name)
			}
			operation.requestBindings[j] = RequestBinding{field: binding.Field, location: binding.Location, name: name}
		}
		sort.Slice(operation.requestBindings, func(i, j int) bool { return operation.requestBindings[i].field < operation.requestBindings[j].field })
		operation.auth = Auth{mode: value.Auth.Mode, credentials: make([]Credential, len(value.Auth.Credentials))}
		for j, credential := range value.Auth.Credentials {
			name := credential.Name
			if credential.Location == CredentialLocationHeader {
				name = strings.ToLower(name)
			}
			operation.auth.credentials[j] = Credential{id: credential.ID, typeID: credential.Type, location: credential.Location, name: name}
		}
		sort.Slice(operation.auth.credentials, func(i, j int) bool { return operation.auth.credentials[i].id < operation.auth.credentials[j].id })
		if value.Capability != nil {
			operation.capability, operation.hasCapability = Capability{id: value.Capability.ID, apiVersion: value.Capability.APIVersion}, true
		}
		operation.errorProjections = make([]ErrorProjection, len(value.ErrorProjections))
		for j, projection := range value.ErrorProjections {
			operation.errorProjections[j] = ErrorProjection{match: ErrorMatch{domain: projection.Match.Domain, code: projection.Match.Code}, project: ErrorTarget{domain: projection.Project.Domain, code: projection.Project.Code, httpStatus: projection.Project.HTTPStatus}}
		}
		sort.Slice(operation.errorProjections, func(i, j int) bool {
			left, right := operation.errorProjections[i].match, operation.errorProjections[j].match
			return left.domain < right.domain || left.domain == right.domain && left.code < right.code
		})
		m.operations[i] = operation
	}
	sort.Slice(m.operations, func(i, j int) bool { return m.operations[i].id < m.operations[j].id })
	m.operationIndex, m.routeIndex = make(map[string]int, len(m.operations)), make(map[string]int, len(m.operations))
	for i := range m.operations {
		m.operationIndex[m.operations[i].id] = i
		m.routeIndex[string(m.operations[i].method)+"\x00"+m.operations[i].path] = i
	}
	return m
}

func nodeProvenanceFromSpec(spec NodeProvenanceSpec) NodeProvenance {
	refs := append([]provenance.SourceRef(nil), spec.Refs...)
	sort.Slice(refs, func(left, right int) bool { return refs[left].String() < refs[right].String() })
	return NodeProvenance{kind: spec.Kind, refs: refs}
}
