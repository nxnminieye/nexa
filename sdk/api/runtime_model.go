package api

import (
	"encoding/json"
	"sort"
	"strings"

	generationapi "github.com/nxnminieye/nexa/generation/api"
)

type runtimeModel struct {
	trace      runtimeContractTraceDocument
	schemas    []runtimeSchema
	operations map[string]runtimeOperation
}

type runtimeSchema struct {
	kind   generationapi.SchemaKind
	items  int
	fields map[string]runtimeField
}

type runtimeField struct {
	required bool
	schema   int
}

type runtimeOperation struct {
	id               string
	method           generationapi.HTTPMethod
	pathSegments     []runtimePathSegment
	request          runtimeRequest
	response         runtimeResponse
	auth             runtimeAuth
	permission       string
	capability       *runtimeCapability
	errorProjections map[string]map[string]runtimeErrorTarget
}

type runtimePathSegment struct {
	literal string
	field   string
}

type runtimeRequest struct {
	schema   int
	bindings map[string]runtimeBinding
}

type runtimeBinding struct {
	location generationapi.RequestBindingLocation
	name     string
}

type runtimeResponse struct {
	body      generationapi.ResponseBodyMode
	schema    int
	hasSchema bool
}

type runtimeAuth struct {
	mode        generationapi.AuthMode
	credentials map[string]runtimeCredential
}

type runtimeCredential struct {
	typeID   generationapi.CredentialType
	location generationapi.CredentialLocation
	name     string
}

type runtimeCapability struct {
	id         string
	apiVersion string
}

type runtimeErrorTarget struct {
	domain     string
	code       string
	httpStatus int
}

func buildRuntimeModel(manifest generationapi.Manifest, trace runtimeContractTraceDocument) (*runtimeModel, error) {
	authoringSchemas := manifest.Schemas()
	byID := make(map[string]generationapi.Schema, len(authoringSchemas))
	for _, schema := range authoringSchemas {
		byID[schema.ID()] = schema
	}

	model := &runtimeModel{
		trace:      trace,
		schemas:    make([]runtimeSchema, 0, len(authoringSchemas)),
		operations: make(map[string]runtimeOperation, len(manifest.Operations())),
	}
	indices := make(map[string]int, len(authoringSchemas))
	semanticIndices := make(map[string]int, len(authoringSchemas))

	type visit struct {
		id       string
		expanded bool
	}
	for _, root := range authoringSchemas {
		if _, exists := indices[root.ID()]; exists {
			continue
		}
		stack := []visit{{id: root.ID()}}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, exists := indices[current.id]; exists {
				continue
			}
			schema := byID[current.id]
			if !current.expanded {
				stack = append(stack, visit{id: current.id, expanded: true})
				dependencies := runtimeSchemaDependencies(schema)
				for index := len(dependencies) - 1; index >= 0; index-- {
					if _, exists := indices[dependencies[index]]; !exists {
						stack = append(stack, visit{id: dependencies[index]})
					}
				}
				continue
			}

			row := runtimeSchema{kind: schema.Kind(), items: -1}
			switch schema.Kind() {
			case generationapi.SchemaArray:
				row.items = indices[schema.ItemSchemaRef()]
			case generationapi.SchemaObject:
				row.fields = make(map[string]runtimeField, len(schema.Fields()))
				for _, field := range schema.Fields() {
					row.fields[field.Name()] = runtimeField{required: field.Required(), schema: indices[field.SchemaRef()]}
				}
			}
			key := runtimeSchemaSemanticKey(row)
			if existing, duplicate := semanticIndices[key]; duplicate {
				indices[current.id] = existing
				continue
			}
			indices[current.id] = len(model.schemas)
			semanticIndices[key] = len(model.schemas)
			model.schemas = append(model.schemas, row)
		}
	}

	for _, operation := range manifest.Operations() {
		compiled := runtimeOperation{
			id:               operation.ID(),
			method:           operation.Method(),
			request:          runtimeRequest{schema: indices[operation.RequestSchemaRef()], bindings: make(map[string]runtimeBinding, len(operation.RequestBindings()))},
			response:         runtimeResponse{body: operation.ResponseBody()},
			auth:             runtimeAuth{mode: operation.Auth().Mode(), credentials: make(map[string]runtimeCredential, len(operation.Auth().Credentials()))},
			permission:       operation.Permission(),
			errorProjections: make(map[string]map[string]runtimeErrorTarget),
		}
		pathFields := make(map[string]string)
		for _, binding := range operation.RequestBindings() {
			name := binding.Name()
			if binding.Location() == generationapi.RequestBindingPath {
				pathFields[name] = binding.Field()
				name = binding.Field()
			}
			compiled.request.bindings[binding.Field()] = runtimeBinding{location: binding.Location(), name: name}
		}
		compiled.pathSegments = compileRuntimePath(operation.Path(), pathFields)
		if operation.ResponseBody() == generationapi.ResponseBodyJSON {
			compiled.response.schema = indices[operation.ResponseSchemaRef()]
			compiled.response.hasSchema = true
		}
		for _, credential := range operation.Auth().Credentials() {
			compiled.auth.credentials[credential.ID()] = runtimeCredential{
				typeID: credential.Type(), location: credential.Location(), name: credential.Name(),
			}
		}
		if capability, present := operation.Capability(); present {
			compiled.capability = &runtimeCapability{id: capability.ID(), apiVersion: capability.APIVersion()}
		}
		for _, projection := range operation.ErrorProjections() {
			match, target := projection.Match(), projection.Project()
			codes := compiled.errorProjections[match.Domain()]
			if codes == nil {
				codes = make(map[string]runtimeErrorTarget)
				compiled.errorProjections[match.Domain()] = codes
			}
			codes[match.Code()] = runtimeErrorTarget{domain: target.Domain(), code: target.Code(), httpStatus: target.HTTPStatus()}
		}
		model.operations[operation.ID()] = compiled
	}
	return model, nil
}

func runtimeSchemaDependencies(schema generationapi.Schema) []string {
	switch schema.Kind() {
	case generationapi.SchemaArray:
		return []string{schema.ItemSchemaRef()}
	case generationapi.SchemaObject:
		fields := schema.Fields()
		result := make([]string, 0, len(fields))
		seen := make(map[string]struct{}, len(fields))
		for _, field := range fields {
			if _, exists := seen[field.SchemaRef()]; exists {
				continue
			}
			seen[field.SchemaRef()] = struct{}{}
			result = append(result, field.SchemaRef())
		}
		return result
	default:
		return nil
	}
}

func runtimeSchemaSemanticKey(schema runtimeSchema) string {
	document := runtimeSchemaToDocument(schema)
	encoded, _ := json.Marshal(document)
	return string(encoded)
}

func compileRuntimePath(path string, pathFields map[string]string) []runtimePathSegment {
	segments := make([]runtimePathSegment, 0, 3)
	for len(path) > 0 {
		open := strings.IndexByte(path, '{')
		if open < 0 {
			segments = append(segments, runtimePathSegment{literal: path})
			break
		}
		if open > 0 {
			segments = append(segments, runtimePathSegment{literal: path[:open]})
		}
		close := strings.IndexByte(path[open:], '}')
		if close < 0 {
			segments = append(segments, runtimePathSegment{literal: path[open:]})
			break
		}
		close += open
		name := path[open+1 : close]
		segments = append(segments, runtimePathSegment{field: pathFields[name]})
		path = path[close+1:]
	}
	return segments
}

func (m *runtimeModel) operation(id string) (runtimeOperation, bool) {
	if m == nil {
		return runtimeOperation{}, false
	}
	operation, ok := m.operations[id]
	return operation, ok
}

func (m *runtimeModel) schema(index int) (runtimeSchema, bool) {
	if m == nil || index < 0 || index >= len(m.schemas) {
		return runtimeSchema{}, false
	}
	return m.schemas[index], true
}

func (s runtimeSchema) field(name string) (runtimeField, bool) {
	field, ok := s.fields[name]
	return field, ok
}

func (s runtimeSchema) fieldNames() []string {
	names := make([]string, 0, len(s.fields))
	for name := range s.fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func runtimeOperationIDs(operations map[string]runtimeOperation) []string {
	ids := make([]string, 0, len(operations))
	for id := range operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
