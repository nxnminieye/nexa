package composition

import (
	"fmt"

	genapi "github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/httpconvention"
)

// FrontendClosure projects the canonical HTTP facts into the operation/type
// closure consumed by frontend generation. The closure is never serialized as
// the HTTP compiler snapshot.
func FrontendClosure(document httpapi.Document) (genapi.Closure, error) {
	if err := httpapi.ValidateConvention(document); err != nil {
		return genapi.Closure{}, fmt.Errorf("validate frontend HTTP Convention: %w", err)
	}
	closure := genapi.Closure{ConventionValue: httpconvention.APIVersion, FactGraphValue: document.FactGraph()}
	for _, operation := range document.Operations() {
		closure.Operations = append(closure.Operations, genapi.ClosureOperation{IDValue: operation.ID(), MethodValue: operation.Method(), PathValue: operation.Path(), AuthValue: operation.Auth().Mode(), PermissionValue: operation.Permission(), RequestTypeValue: operation.RequestType(), ResponseTypeValue: operation.ResponseType(), SourcesValue: operation.Provenance().Sources()})
	}
	selected := map[string]genapi.ClosureType{}
	visiting := map[string]bool{}
	var visitType func(string) error
	var visitValue func(httpapi.ValueType) error
	visitValue = func(value httpapi.ValueType) error {
		switch value.Kind() {
		case httpapi.ValueMap:
			return fmt.Errorf("map value cannot enter the frontend TypeScript closure")
		case httpapi.ValueRef:
			return visitType(value.Name())
		}
		if element, ok := value.Element(); ok {
			return visitValue(element)
		}
		return nil
	}
	visitType = func(name string) error {
		if name == "" {
			return nil
		}
		if _, ok := selected[name]; ok || visiting[name] {
			return nil
		}
		value, ok := document.Type(name)
		if !ok {
			return fmt.Errorf("frontend type %s is unresolved", name)
		}
		visiting[name] = true
		shape, err := frontendClosureValue(value.Shape())
		if err != nil {
			return fmt.Errorf("validate frontend type %s shape: %w", value.Name(), err)
		}
		if err := visitValue(value.Shape()); err != nil {
			return fmt.Errorf("validate frontend type %s shape: %w", value.Name(), err)
		}
		item := genapi.ClosureType{NameValue: value.Name(), ShapeValue: shape, SourcesValue: value.Provenance().Sources()}
		for _, field := range value.Fields() {
			path := field.Path()
			for _, segment := range path {
				if err := httpconvention.ValidateFieldName(segment); err != nil {
					return fmt.Errorf("validate frontend field %s.%s: %w", value.Name(), segment, err)
				}
			}
			converted, err := frontendClosureValue(field.ValueType())
			if err != nil {
				return fmt.Errorf("validate frontend field %s.%s: %w", value.Name(), path[0], err)
			}
			if err := visitValue(field.ValueType()); err != nil {
				return fmt.Errorf("validate frontend field %s.%s: %w", value.Name(), path[0], err)
			}
			item.FieldsValue = append(item.FieldsValue, genapi.ClosureField{PathValue: path, RequiredValue: field.Required(), ValueTypeValue: converted, SourcesValue: field.Provenance().Sources()})
		}
		delete(visiting, name)
		selected[name] = item
		return nil
	}
	for _, operation := range document.Operations() {
		if err := visitType(operation.RequestType()); err != nil {
			return genapi.Closure{}, err
		}
		if err := visitType(operation.ResponseType()); err != nil {
			return genapi.Closure{}, err
		}
	}
	for _, value := range document.Types() {
		if item, ok := selected[value.Name()]; ok {
			closure.Types = append(closure.Types, item)
		}
	}
	return closure, nil
}

func frontendClosureValue(value httpapi.ValueType) (genapi.ClosureValue, error) {
	if value.Kind() == httpapi.ValueMap {
		return genapi.ClosureValue{}, fmt.Errorf("map value cannot enter the frontend TypeScript closure")
	}
	result := genapi.ClosureValue{KindValue: string(value.Kind()), NameValue: value.Name()}
	if element, ok := value.Element(); ok {
		converted, err := frontendClosureValue(element)
		if err != nil {
			return genapi.ClosureValue{}, err
		}
		result.ElementValuePtr = &converted
	}
	return result, nil
}
