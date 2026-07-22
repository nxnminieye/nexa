package api

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	generationapi "github.com/nxnminieye/nexa/generation/api"
)

func validateSelectedRequest(model *runtimeModel, operation runtimeOperation, request Request) (map[string]requestValue, error) {
	if request.json == nil || request.root == nil {
		return nil, newRequestBuildError("request_required", "/request", operation.id)
	}
	schema, _ := model.schema(operation.request.schema)
	normalized, err := validateRequestObject(model, schema, request.root, "", operation.id)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]requestValue, len(normalized))
	for _, member := range normalized {
		fields[member.name] = member.value
	}
	return fields, nil
}

func validateRequestObject(
	model *runtimeModel,
	schema runtimeSchema,
	value requestObject,
	pointer string,
	apiOperationID string,
) (requestObject, error) {
	for _, member := range value {
		if _, exists := schema.field(member.name); !exists {
			return nil, newRequestBuildError("field_unknown", pointer+"/"+escapeJSONPointer(member.name), apiOperationID)
		}
	}
	for _, fieldName := range schema.fieldNames() {
		field, _ := schema.field(fieldName)
		if !field.required {
			continue
		}
		if _, exists := requestObjectMember(value, fieldName); !exists {
			return nil, newRequestBuildError("field_required", pointer+"/"+escapeJSONPointer(fieldName), apiOperationID)
		}
	}
	normalized := make(requestObject, len(value))
	for index, member := range value {
		field, _ := schema.field(member.name)
		fieldPointer := pointer + "/" + escapeJSONPointer(member.name)
		fieldSchema, _ := model.schema(field.schema)
		fieldValue, err := validateRequestValue(model, fieldSchema, member.value, fieldPointer, apiOperationID)
		if err != nil {
			return nil, err
		}
		normalized[index] = requestMember{name: member.name, value: fieldValue, start: member.start, end: member.end}
	}
	return normalized, nil
}

func validateRequestValue(
	model *runtimeModel,
	schema runtimeSchema,
	value requestValue,
	pointer string,
	apiOperationID string,
) (requestValue, error) {
	switch schema.kind {
	case generationapi.SchemaString:
		stringValue, ok := value.(requestString)
		if !ok || !utf8.ValidString(string(stringValue)) {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		return stringValue, nil
	case generationapi.SchemaInteger:
		number, ok := value.(requestNumber)
		if !ok || strings.ContainsAny(string(number), ".eE") {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		integer, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		return requestNumber(strconv.FormatInt(integer, 10)), nil
	case generationapi.SchemaNumber:
		number, ok := value.(requestNumber)
		if !ok {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		binary64, err := strconv.ParseFloat(string(number), 64)
		if math.IsInf(binary64, 0) || math.IsNaN(binary64) || !finiteRangeResult(err) {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		canonical, err := jcs.NumberToJSON(binary64)
		if err != nil {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		return requestNumber(canonical), nil
	case generationapi.SchemaBoolean:
		boolean, ok := value.(requestBool)
		if !ok {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		return boolean, nil
	case generationapi.SchemaObject:
		object, ok := value.(requestObject)
		if !ok {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		return validateRequestObject(model, schema, object, pointer, apiOperationID)
	case generationapi.SchemaArray:
		array, ok := value.(requestArray)
		if !ok {
			return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
		}
		itemSchema, _ := model.schema(schema.items)
		normalized := make(requestArray, len(array))
		for index, item := range array {
			itemValue, err := validateRequestValue(model, itemSchema, item, pointer+"/"+strconv.Itoa(index), apiOperationID)
			if err != nil {
				return nil, err
			}
			normalized[index] = itemValue
		}
		return normalized, nil
	default:
		return nil, newRequestBuildError("value_invalid", pointer, apiOperationID)
	}
}

func requestObjectMember(object requestObject, name string) (requestMember, bool) {
	for _, member := range object {
		if member.name == name {
			return member, true
		}
	}
	return requestMember{}, false
}

func finiteRangeResult(err error) bool {
	if err == nil {
		return true
	}
	var numberError *strconv.NumError
	return errors.As(err, &numberError) && numberError.Err == strconv.ErrRange
}

func scalarLexical(value requestValue) (string, bool) {
	switch value := value.(type) {
	case requestString:
		return string(value), true
	case requestNumber:
		return string(value), true
	case requestBool:
		if value {
			return "true", true
		}
		return "false", true
	default:
		return "", false
	}
}

func validLogicalHeaderValue(value string) bool {
	if value != "" && (value[0] == ' ' || value[0] == '\t' || value[len(value)-1] == ' ' || value[len(value)-1] == '\t') {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\t' || character >= 0x20 && character <= 0x7e {
			continue
		}
		return false
	}
	return true
}

func validCookieValue(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == 0x21 || character >= 0x23 && character <= 0x2b || character >= 0x2d && character <= 0x3a ||
			character >= 0x3c && character <= 0x5b || character >= 0x5d && character <= 0x7e {
			continue
		}
		return false
	}
	return true
}

func newRequestBuildError(reason, pointer, apiOperationID string) *Error {
	err := newSDKError(
		codeRequestInvalid,
		sdkErrorDomain,
		protocol.CategoryInput,
		"API request is invalid",
		ErrorDetails{reason: reason, pointer: pointer},
	)
	err.apiOperationID = apiOperationID
	return err
}

func newOperationLookupError() *Error {
	return newSDKError(
		codeOperationNotFound,
		sdkErrorDomain,
		protocol.CategoryInput,
		"API operation was not found",
		ErrorDetails{reason: "operation_unknown", pointer: "/apiOperationId"},
	)
}

func contextFailure(ctx context.Context, apiOperationID string) *Error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	reason := "context_canceled"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		reason = "context_deadline"
	}
	err := newSDKError(
		codeOperationCanceled,
		sdkErrorDomain,
		protocol.CategoryCanceled,
		"API operation was canceled",
		ErrorDetails{reason: reason},
	)
	err.apiOperationID = apiOperationID
	return err
}
