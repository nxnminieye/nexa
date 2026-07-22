package httpapi

import (
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/provenance"
)

type ValueTypeSpec struct {
	Kind    ValueKind
	Name    string
	Element *ValueTypeSpec
	Key     *ValueTypeSpec
	Value   *ValueTypeSpec
}

type BindingSpec struct {
	Location api.RequestBindingLocation
	Name     string
}

type GeneratedFieldSpec struct {
	Path       []string
	Required   bool
	ValueType  ValueTypeSpec
	Binding    *BindingSpec
	Origin     *provenance.Source
	Provenance NodeProvenance
}

type GeneratedTypeSpec struct {
	Name       string
	Shape      ValueTypeSpec
	Fields     []GeneratedFieldSpec
	Provenance NodeProvenance
}

type CredentialSpec struct {
	ID       string
	Type     api.CredentialType
	Location api.CredentialLocation
	Name     string
}

type AuthSpec struct {
	Mode        api.AuthMode
	Credentials []CredentialSpec
}

type CapabilitySpec struct{ ID, APIVersion string }

type GeneratedOperationSpec struct {
	ID               string
	Method           api.HTTPMethod
	Path             string
	RequestType      string
	ResponseBody     api.ResponseBodyMode
	ResponseType     string
	Auth             AuthSpec
	Permission       string
	Capability       *CapabilitySpec
	ErrorProjections []api.ErrorProjectionSpec
	Provenance       NodeProvenance
}

type GeneratedDocumentSpec struct {
	Types      []GeneratedTypeSpec
	Operations []GeneratedOperationSpec
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
		if input.Name == "" || typeNames[input.Name] {
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
		state := &typeState{name: input.Name, shape: shape, provenance: cloneProvenance(input.Provenance), fieldIndex: map[string]int{}}
		for _, fieldInput := range input.Fields {
			if fieldInput.Provenance.kind != NodeFactGenerated || len(fieldInput.Path) == 0 {
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
			field := &fieldState{ownerType: input.Name, path: pathValue, required: fieldInput.Required, valueType: value, provenance: cloneProvenance(fieldInput.Provenance)}
			if fieldInput.Binding != nil {
				field.binding, field.hasBinding = Binding{location: fieldInput.Binding.Location, name: fieldInput.Binding.Name}, true
				if field.binding.location == api.RequestBindingHeader {
					field.binding.name = strings.ToLower(field.binding.name)
				}
			}
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
		if input.Provenance.kind != NodeFactGenerated || input.ID == "" || operationIDs[input.ID] {
			return Document{}, invalid("generated_operation_invalid", "", "", "generated operation identity and provenance are invalid")
		}
		if !typeNames[input.RequestType] || input.ResponseBody == api.ResponseBodyJSON && !typeNames[input.ResponseType] || input.ResponseBody == api.ResponseBodyNone && input.ResponseType != "" {
			return Document{}, invalid("generated_operation_type_invalid", "", "", "generated operation type relation is invalid")
		}
		key := string(input.Method) + "\x00" + input.Path
		if routes[key] {
			return Document{}, invalid("route_collision", "", "", "generated route is duplicated")
		}
		state := &operationState{id: input.ID, method: input.Method, path: input.Path, requestType: input.RequestType, responseBody: input.ResponseBody, responseType: input.ResponseType, permission: input.Permission, provenance: cloneProvenance(input.Provenance), auth: Auth{mode: input.Auth.Mode, credentials: make([]Credential, len(input.Auth.Credentials))}, errorProjections: append([]api.ErrorProjectionSpec(nil), input.ErrorProjections...)}
		for index, credential := range input.Auth.Credentials {
			state.auth.credentials[index] = Credential{id: credential.ID, typeID: credential.Type, location: credential.Location, name: credential.Name}
			if credential.Location == api.CredentialLocationHeader {
				state.auth.credentials[index].name = strings.ToLower(credential.Name)
			}
		}
		sort.Slice(state.auth.credentials, func(i, j int) bool { return state.auth.credentials[i].id < state.auth.credentials[j].id })
		sort.Slice(state.errorProjections, func(i, j int) bool {
			left, right := state.errorProjections[i].Match, state.errorProjections[j].Match
			return left.Domain < right.Domain || left.Domain == right.Domain && left.Code < right.Code
		})
		if input.Capability != nil {
			state.capability, state.hasCapability = Capability{id: input.Capability.ID, apiVersion: input.Capability.APIVersion}, true
		}
		operations = append(operations, state)
		operationIDs[input.ID], routes[key] = true, true
	}
	return newDocument(types, operations, nil)
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
