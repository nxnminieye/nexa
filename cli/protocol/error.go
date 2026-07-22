package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/gowebpki/jcs"
)

type Category string

const (
	CategoryUsage       Category = "usage"
	CategoryInput       Category = "input"
	CategoryReview      Category = "review"
	CategoryDrift       Category = "drift"
	CategoryConflict    Category = "conflict"
	CategoryUnavailable Category = "unavailable"
	CategoryExternal    Category = "external"
	CategoryCanceled    Category = "canceled"
	CategoryInternal    Category = "internal"
)

type ErrorPayload struct {
	Code              string          `json:"code"`
	Domain            string          `json:"domain"`
	Category          Category        `json:"category"`
	Message           string          `json:"message"`
	RecommendedAction string          `json:"recommendedAction,omitempty"`
	Retryable         bool            `json:"retryable"`
	Details           json.RawMessage `json:"details,omitempty"`
}

type Error struct {
	payload ErrorPayload
}

type ErrorOptions struct {
	Retryable bool
}

func NewError(code, domain string, category Category, message, recommendedAction string) *Error {
	projected, err := NewErrorWithOptions(
		code,
		domain,
		category,
		message,
		recommendedAction,
		ErrorOptions{},
	)
	if err != nil {
		return &Error{payload: internalErrorPayload()}
	}
	return projected
}

func NewErrorWithOptions(
	code, domain string,
	category Category,
	message, recommendedAction string,
	options ErrorOptions,
) (*Error, error) {
	if !declaredCategory(category) || (options.Retryable && category != CategoryUnavailable && category != CategoryExternal) {
		return nil, errors.New("error options are invalid")
	}
	return &Error{
		payload: ErrorPayload{
			Code:              code,
			Domain:            domain,
			Category:          category,
			Message:           message,
			RecommendedAction: recommendedAction,
			Retryable:         options.Retryable,
		},
	}, nil
}

// DetailDocument is a closed, canonical detail projection owned by its producer.
type DetailDocument interface {
	ErrorCode() string
	CanonicalJSON() ([]byte, error)
}

// NewErrorWithDetails constructs a projected error with immutable details.
func NewErrorWithDetails(
	code, domain string,
	category Category,
	message, recommendedAction string,
	details DetailDocument,
) (*Error, error) {
	return NewErrorWithDetailsOptions(
		code,
		domain,
		category,
		message,
		recommendedAction,
		details,
		ErrorOptions{},
	)
}

func NewErrorWithDetailsOptions(
	code, domain string,
	category Category,
	message, recommendedAction string,
	details DetailDocument,
	options ErrorOptions,
) (*Error, error) {
	projected, err := NewErrorWithOptions(code, domain, category, message, recommendedAction, options)
	if err != nil {
		return nil, err
	}
	if nilDetailDocument(details) || details.ErrorCode() != code {
		return nil, errors.New("error detail document is invalid")
	}
	encoded, encodeErr := details.CanonicalJSON()
	if encodeErr != nil || !canonicalJSONObject(encoded) {
		return nil, errors.New("error detail document is invalid")
	}
	projected.payload.Details = append(json.RawMessage(nil), encoded...)
	return projected, nil
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.payload.Message
}

func Project(err error) ErrorPayload {
	var protocolError *Error
	if errors.As(err, &protocolError) && protocolError != nil {
		payload := protocolError.payload
		payload.Details = append(json.RawMessage(nil), protocolError.payload.Details...)
		return payload
	}

	return internalErrorPayload()
}

func internalErrorPayload() ErrorPayload {
	return ErrorPayload{
		Code:     "internal_error",
		Domain:   "internal",
		Category: CategoryInternal,
		Message:  "an internal error occurred",
	}
}

func declaredCategory(category Category) bool {
	switch category {
	case CategoryUsage,
		CategoryInput,
		CategoryReview,
		CategoryDrift,
		CategoryConflict,
		CategoryUnavailable,
		CategoryExternal,
		CategoryCanceled,
		CategoryInternal:
		return true
	default:
		return false
	}
}

func nilDetailDocument(details DetailDocument) bool {
	if details == nil {
		return true
	}
	value := reflect.ValueOf(details)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func canonicalJSONObject(encoded []byte) bool {
	if len(encoded) < 2 || encoded[0] != '{' || encoded[len(encoded)-1] != '}' || !json.Valid(encoded) {
		return false
	}
	if duplicateJSONObjectKey(encoded) {
		return false
	}
	canonical, err := jcs.Transform(encoded)
	return err == nil && bytes.Equal(canonical, encoded)
}

func duplicateJSONObjectKey(encoded []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	return duplicateJSONValue(decoder)
}

func duplicateJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return true
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return false
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return true
			}
			name, ok := key.(string)
			if !ok {
				return true
			}
			if _, duplicate := seen[name]; duplicate {
				return true
			}
			seen[name] = struct{}{}
			if duplicateJSONValue(decoder) {
				return true
			}
		}
	case '[':
		for decoder.More() {
			if duplicateJSONValue(decoder) {
				return true
			}
		}
	}
	closing, err := decoder.Token()
	return err != nil || !strings.Contains("}]", string(closing.(json.Delim)))
}
