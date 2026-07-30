package httpapi

import (
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

type ValueTypeSpec struct {
	Kind    ValueKind
	Name    string
	Element *ValueTypeSpec
	Key     *ValueTypeSpec
	Value   *ValueTypeSpec
}

type GeneratedFieldSpec struct {
	SemanticID  string
	Path        []string
	Required    bool
	ValueType   ValueTypeSpec
	Origin      *provenance.Source
	Provenance  NodeProvenance
	FirstSource sourcecomment.SourceRef
}

type GeneratedTypeSpec struct {
	SemanticID  string
	Name        string
	Shape       ValueTypeSpec
	Fields      []GeneratedFieldSpec
	Provenance  NodeProvenance
	FirstSource sourcecomment.SourceRef
}

type AuthSpec struct{ Mode api.AuthMode }

type GeneratedOperationSpec struct {
	ID           string
	Method       api.HTTPMethod
	Path         string
	RequestType  string
	ResponseType string
	Auth         AuthSpec
	Permission   string
	Provenance   NodeProvenance
	FirstSource  sourcecomment.SourceRef
}

type GeneratedDocumentSpec struct {
	Types      []GeneratedTypeSpec
	Operations []GeneratedOperationSpec
	Facts      sourcecomment.FactGraph
}

func NewGeneratedProvenance(input []provenance.Source) (NodeProvenance, error) {
	if len(input) < 2 {
		return NodeProvenance{}, invalid("generated_provenance_incomplete", "", "", "generated provenance requires at least two owner sources")
	}
	values := append([]provenance.Source(nil), input...)
	sort.Slice(values, func(i, j int) bool { return values[i].Ref.String() < values[j].Ref.String() })
	previous := ""
	for _, source := range values {
		ref, refErr := provenance.ParseSourceRef(source.Ref.String())
		digest, digestErr := provenance.ParseDigest(source.Digest.String())
		if refErr != nil || digestErr != nil || ref != source.Ref || digest != source.Digest {
			return NodeProvenance{}, invalid("generated_source_invalid", "", "", "generated owner source is invalid")
		}
		if source.Ref.String() == previous {
			return NodeProvenance{}, invalid("generated_source_duplicate", source.Ref.Path(), "", "generated owner source is duplicated")
		}
		previous = source.Ref.String()
	}
	return NodeProvenance{kind: NodeFactGenerated, sources: values}, nil
}

func NewGeneratedDocument(spec GeneratedDocumentSpec) (Document, error) {
	types := make([]*typeState, 0, len(spec.Types))
	typeNames := map[string]bool{}
	for _, input := range spec.Types {
		if input.Name == "" || input.SemanticID == "" || !input.FirstSource.Valid() || typeNames[input.Name] {
			return Document{}, invalid("generated_type_invalid", "", "", "generated type name is empty or duplicated")
		}
		if input.Provenance.kind != NodeFactGenerated {
			return Document{}, invalid("generated_provenance_required", "", "", "generated type requires generated provenance")
		}
		shape, err := valueFromSpec(input.Shape)
		if err != nil {
			return Document{}, err
		}
		if shape.kind != ValueObject {
			return Document{}, invalid("generated_type_shape_invalid", "", "", "generated top-level type must be an object")
		}
		state := &typeState{name: input.Name, semanticID: input.SemanticID, firstSource: input.FirstSource, shape: shape, provenance: cloneProvenance(input.Provenance), fieldIndex: map[string]int{}}
		for _, fieldInput := range input.Fields {
			if fieldInput.Provenance.kind != NodeFactGenerated || fieldInput.SemanticID == "" || !fieldInput.FirstSource.Valid() || len(fieldInput.Path) == 0 {
				return Document{}, invalid("generated_field_invalid", "", "", "generated field path and provenance are required")
			}
			pathValue := append([]string(nil), fieldInput.Path...)
			for _, segment := range pathValue {
				if segment == "" || strings.Contains(segment, ".") {
					return Document{}, invalid("generated_field_invalid", "", "", "generated field path segment is invalid")
				}
			}
			key := pathKey(pathValue)
			if _, duplicate := state.fieldIndex[key]; duplicate {
				return Document{}, invalid("field_collision", "", "", "generated field is duplicated")
			}
			value, err := valueFromSpec(fieldInput.ValueType)
			if err != nil {
				return Document{}, err
			}
			field := &fieldState{ownerType: input.Name, semanticID: fieldInput.SemanticID, firstSource: fieldInput.FirstSource, path: pathValue, required: fieldInput.Required, valueType: value, provenance: cloneProvenance(fieldInput.Provenance)}
			if fieldInput.Origin != nil {
				field.origin, field.hasOrigin = *fieldInput.Origin, true
			}
			state.fieldIndex[key] = len(state.fields)
			state.fields = append(state.fields, field)
		}
		sort.Slice(state.fields, func(i, j int) bool { return pathKey(state.fields[i].path) < pathKey(state.fields[j].path) })
		state.fieldIndex = map[string]int{}
		for index, field := range state.fields {
			state.fieldIndex[pathKey(field.path)] = index
		}
		types, typeNames[input.Name] = append(types, state), true
	}

	operations := make([]*operationState, 0, len(spec.Operations))
	operationIDs, routes := map[string]bool{}, map[string]bool{}
	for _, input := range spec.Operations {
		if input.Provenance.kind != NodeFactGenerated || input.ID == "" || operationIDs[input.ID] || !input.FirstSource.Valid() {
			return Document{}, invalid("generated_operation_invalid", "", "", "generated operation identity and provenance are invalid")
		}
		if input.RequestType != "" && !typeNames[input.RequestType] || input.ResponseType != "" && !typeNames[input.ResponseType] {
			return Document{}, invalid("generated_operation_type_invalid", "", "", "generated operation type relation is invalid")
		}
		key := string(input.Method) + "\x00" + input.Path
		if routes[key] {
			return Document{}, invalid("route_collision", "", "", "generated route is duplicated")
		}
		operations = append(operations, &operationState{id: input.ID, method: input.Method, path: input.Path, requestType: input.RequestType, responseType: input.ResponseType, permission: input.Permission, provenance: cloneProvenance(input.Provenance), auth: Auth{mode: input.Auth.Mode}, firstSource: input.FirstSource})
		operationIDs[input.ID], routes[key] = true, true
	}
	return newDocument(types, operations, nil, spec.Facts)
}

func valueFromSpec(input ValueTypeSpec) (ValueType, error) {
	result := ValueType{kind: input.Kind, name: input.Name}
	switch input.Kind {
	case ValueObject:
		if input.Name != "" || input.Element != nil || input.Key != nil || input.Value != nil {
			return ValueType{}, invalid("value_type_invalid", "", "", "object value type shape is invalid")
		}
	case ValueScalar, ValueRef:
		if input.Name == "" || input.Element != nil || input.Key != nil || input.Value != nil {
			return ValueType{}, invalid("value_type_invalid", "", "", "named value type shape is invalid")
		}
	case ValueArray, ValueOptional:
		if input.Name != "" || input.Element == nil || input.Key != nil || input.Value != nil {
			return ValueType{}, invalid("value_type_invalid", "", "", "element value type shape is invalid")
		}
		value, err := valueFromSpec(*input.Element)
		if err != nil {
			return ValueType{}, err
		}
		result.element = &value
	case ValueMap:
		if input.Name != "" || input.Element != nil || input.Key == nil || input.Value == nil {
			return ValueType{}, invalid("value_type_invalid", "", "", "map value type shape is invalid")
		}
		key, err := valueFromSpec(*input.Key)
		if err != nil {
			return ValueType{}, err
		}
		value, err := valueFromSpec(*input.Value)
		if err != nil {
			return ValueType{}, err
		}
		result.key, result.value = &key, &value
	default:
		return ValueType{}, invalid("value_type_invalid", "", "", "value type kind is invalid")
	}
	return result, nil
}
