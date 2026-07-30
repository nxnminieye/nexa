package httpapi

import (
	"fmt"
	"strings"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpconvention"
)

// ValidateConvention is the generation gate for authored Nexa JSON HTTP APIs.
// The document remains a compiler-local structure and is never serialized as a
// public contract.
func ValidateConvention(document Document) error {
	if document.state == nil {
		return fmt.Errorf("HTTP API document is invalid")
	}
	for _, typeValue := range document.Types() {
		if err := validateConventionValue(typeValue.Shape()); err != nil {
			return fmt.Errorf("type %s shape: %w", typeValue.Name(), err)
		}
		seen := map[string]bool{}
		for _, field := range typeValue.Fields() {
			canonical := field.Path()
			for _, segment := range canonical {
				if err := httpconvention.ValidateFieldName(segment); err != nil {
					return fmt.Errorf("type %s field %s is invalid: %w", typeValue.Name(), strings.Join(field.Path(), "."), err)
				}
			}
			key := strings.Join(canonical, "\x00")
			if seen[key] {
				return fmt.Errorf("type %s has fields that collide after canonical naming", typeValue.Name())
			}
			seen[key] = true
			if err := validateConventionValue(field.ValueType()); err != nil {
				return fmt.Errorf("type %s field %s: %w", typeValue.Name(), strings.Join(canonical, "."), err)
			}
		}
	}
	for _, operation := range document.Operations() {
		if _, err := httpconvention.ValidateRoute(operation.Path()); err != nil {
			return fmt.Errorf("operation %s: %w", operation.ID(), err)
		}
		auth := operation.Auth()
		if auth.Mode() != api.AuthNone && auth.Mode() != api.AuthRequired {
			return fmt.Errorf("operation %s auth must be none or required", operation.ID())
		}
		var request Type
		if operation.RequestType() != "" {
			var ok bool
			request, ok = document.Type(operation.RequestType())
			if !ok {
				return fmt.Errorf("operation %s request type is unresolved", operation.ID())
			}
		}
		fields := make([]string, 0, len(request.Fields()))
		values := map[string]ValueType{}
		transports := map[string]httpconvention.Location{}
		for _, field := range request.Fields() {
			path := field.Path()
			if len(path) != 1 {
				return fmt.Errorf("operation %s request fields must be direct", operation.ID())
			}
			name := path[0]
			if err := httpconvention.ValidateFieldName(name); err != nil {
				return fmt.Errorf("operation %s request field %s is invalid: %w", operation.ID(), path[0], err)
			}
			fields = append(fields, name)
			values[name] = field.ValueType()
			if field.state.hasTransport {
				transports[name] = field.state.transport
			}
		}
		classified, err := httpconvention.ClassifyRequest(string(operation.Method()), operation.Path(), fields)
		if err != nil {
			return fmt.Errorf("operation %s: %w", operation.ID(), err)
		}
		for _, field := range classified {
			if transport, declared := transports[field.Name]; declared && transport != field.Location {
				return fmt.Errorf("operation %s request field %s transport tag conflicts with %s placement", operation.ID(), field.Name, field.Location)
			}
			if field.Location == httpconvention.LocationBody {
				continue
			}
			if !conventionScalar(values[field.Name]) {
				return fmt.Errorf("operation %s path/query field %s must be scalar", operation.ID(), field.Name)
			}
		}
		if err := validateListConvention(document, operation, values); err != nil {
			return fmt.Errorf("operation %s: %w", operation.ID(), err)
		}
		hasRepresentation := operation.ResponseType() != ""
		if _, err := httpconvention.SuccessStatus(string(operation.Method()), operation.Path(), hasRepresentation); err != nil {
			return fmt.Errorf("operation %s: %w", operation.ID(), err)
		}
	}
	return nil
}

func validateListConvention(document Document, operation Operation, requestValues map[string]ValueType) error {
	if operation.ResponseType() == "" {
		return nil
	}
	response, ok := document.Type(operation.ResponseType())
	if !ok {
		return fmt.Errorf("response type is unresolved")
	}
	responseFields := map[string]Field{}
	for _, field := range response.Fields() {
		path := field.Path()
		if len(path) != 1 {
			continue
		}
		name := path[0]
		if err := httpconvention.ValidateFieldName(name); err != nil {
			return fmt.Errorf("response field %s is invalid: %w", path[0], err)
		}
		responseFields[name] = field
	}
	_, hasItems := responseFields["items"]
	_, hasTotal := responseFields["total"]
	if !hasItems && !hasTotal {
		return nil
	}
	items, total := responseFields["items"], responseFields["total"]
	if len(response.Fields()) != 2 || !hasItems || !hasTotal || !items.Required() || !total.Required() {
		return fmt.Errorf("list response must be exact required {items,total}")
	}
	itemsValue := items.ValueType()
	if itemsValue.Kind() != ValueArray {
		return fmt.Errorf("list response items must be an array")
	}
	totalValue := total.ValueType()
	if totalValue.Kind() != ValueScalar || !integerScalar(totalValue.Name()) {
		return fmt.Errorf("list response total must be an integer JSON number")
	}
	for _, name := range []string{"limit", "offset"} {
		value, exists := requestValues[name]
		for value.Kind() == ValueOptional {
			element, ok := value.Element()
			if !ok {
				return fmt.Errorf("list request field %s has no element", name)
			}
			value = element
		}
		if !exists || value.Kind() != ValueScalar || !integerScalar(value.Name()) {
			return fmt.Errorf("list request must contain canonical limit and offset numbers")
		}
	}
	return nil
}

func integerScalar(name string) bool {
	return name == "int32" || name == "uint32" || name == "int64" || name == "uint64"
}

func validateConventionValue(value ValueType) error {
	switch value.Kind() {
	case ValueScalar:
		switch value.Name() {
		case "string", "bool", "int32", "uint32", "int64", "uint64", "float32", "float64":
			return nil
		default:
			return fmt.Errorf("scalar %s has no HTTP Convention v1 wire type", value.Name())
		}
	case ValueRef, ValueObject:
		return nil
	case ValueArray, ValueOptional:
		element, ok := value.Element()
		if !ok {
			return fmt.Errorf("%s value has no element", value.Kind())
		}
		return validateConventionValue(element)
	case ValueMap:
		key, keyOK := value.Key()
		mapValue, valueOK := value.Value()
		if !keyOK || !valueOK || key.Kind() != ValueScalar {
			return fmt.Errorf("map must have a scalar key and value")
		}
		if err := validateConventionValue(key); err != nil {
			return fmt.Errorf("map key: %w", err)
		}
		if err := validateConventionValue(mapValue); err != nil {
			return fmt.Errorf("map value: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("value kind %s is not supported", value.Kind())
	}
}

func conventionScalar(value ValueType) bool {
	for value.Kind() == ValueOptional {
		element, ok := value.Element()
		if !ok {
			return false
		}
		value = element
	}
	return value.Kind() == ValueScalar
}
