// Package httpconvention implements the fixed Nexa JSON HTTP convention.
package httpconvention

import (
	_ "embed"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

const (
	APIVersion      = "nexa.dev/http-convention/v1"
	JSONMediaType   = "application/json"
	DefaultPageSize = 20
)

var (
	lowerCamelPattern  = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	lowerSnakePattern  = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
	pathSegmentPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
)

//go:embed conformance-v1.schema.json
var conformanceSchema []byte

// ConformanceSchema returns an independent copy of the public fixture schema.
func ConformanceSchema() []byte { return append([]byte(nil), conformanceSchema...) }

// CanonicalName performs the source-identifier conversion used when a .api
// field omits its explicit transport tag.
func CanonicalName(source string) (string, error) {
	words, err := identifierWords(source)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	for index, word := range words {
		word = strings.ToLower(word)
		if index > 0 {
			runes := []rune(word)
			runes[0] = unicode.ToUpper(runes[0])
			word = string(runes)
		}
		result.WriteString(word)
	}
	canonical := result.String()
	if !lowerCamelPattern.MatchString(canonical) {
		return "", errors.New("identifier cannot be represented as lowerCamelCase")
	}
	return canonical, nil
}

// ValidateFieldName accepts the two field spellings already used by PDCL.
// The exact authored tag value is the external field identity; no alias or
// second logical name is produced.
func ValidateFieldName(name string) error {
	if !lowerCamelPattern.MatchString(name) && !lowerSnakePattern.MatchString(name) {
		return fmt.Errorf("field name %q must be lowerCamelCase or lower_snake_case", name)
	}
	return nil
}

func identifierWords(source string) ([]string, error) {
	if source == "" {
		return nil, errors.New("identifier is empty")
	}
	if strings.HasPrefix(source, "_") || strings.HasSuffix(source, "_") || strings.Contains(source, "__") {
		return nil, errors.New("identifier contains an empty word")
	}
	for _, r := range source {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			return nil, errors.New("identifier must contain only ASCII letters, digits, and underscore")
		}
	}
	parts := strings.FieldsFunc(source, func(r rune) bool { return r == '_' })
	if len(parts) == 0 {
		return nil, errors.New("identifier is empty")
	}
	words := make([]string, 0, len(parts)*2)
	for _, part := range parts {
		runes := []rune(part)
		start := 0
		for index := 1; index < len(runes); index++ {
			previous, current := runes[index-1], runes[index]
			nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			boundary := unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsDigit(previous)) ||
				unicode.IsUpper(previous) && unicode.IsUpper(current) && nextLower
			if boundary {
				words = append(words, string(runes[start:index]))
				start = index
			}
		}
		words = append(words, string(runes[start:]))
	}
	return words, nil
}

type Location string

const (
	LocationPath  Location = "path"
	LocationQuery Location = "query"
	LocationBody  Location = "body"
)

type RequestField struct {
	Name     string
	Location Location
}

// ClassifyRequest derives request locations from the PDCL method and route
// convention. Field tags, where present, must agree with this result.
func ClassifyRequest(method, route string, fields []string) ([]RequestField, error) {
	placeholders, err := ValidateRoute(route)
	if err != nil {
		return nil, err
	}
	var remainder Location
	switch strings.ToUpper(method) {
	case "GET", "DELETE":
		remainder = LocationQuery
	case "POST", "PUT", "PATCH":
		remainder = LocationBody
	default:
		return nil, fmt.Errorf("method %q is not supported by %s", method, APIVersion)
	}
	seen := make(map[string]bool, len(fields))
	// Route placeholders may retain lower_snake wire spelling while DTO fields
	// use the canonical lowerCamel spelling; compare one semantic identity.
	canonicalPlaceholders := make(map[string]bool, len(placeholders))
	for placeholder := range placeholders {
		canonical, canonicalErr := CanonicalName(placeholder)
		if canonicalErr != nil {
			return nil, fmt.Errorf("route placeholder %q cannot be canonicalized: %w", placeholder, canonicalErr)
		}
		canonicalPlaceholders[canonical] = true
	}
	result := make([]RequestField, len(fields))
	for index, name := range fields {
		if err := ValidateFieldName(name); err != nil {
			return nil, err
		}
		if transportContextField(name) {
			return nil, fmt.Errorf("infrastructure context %q cannot enter a business DTO", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("request field %q is duplicated", name)
		}
		seen[name] = true
		location := remainder
		canonical, canonicalErr := CanonicalName(name)
		if canonicalErr != nil {
			return nil, fmt.Errorf("request field %q cannot be canonicalized: %w", name, canonicalErr)
		}
		if placeholders[name] || canonicalPlaceholders[canonical] {
			location = LocationPath
		}
		result[index] = RequestField{Name: name, Location: location}
	}
	for placeholder := range placeholders {
		canonical, canonicalErr := CanonicalName(placeholder)
		if canonicalErr != nil {
			return nil, fmt.Errorf("route placeholder %q cannot be canonicalized: %w", placeholder, canonicalErr)
		}
		if !seen[placeholder] && !seen[canonical] {
			return nil, fmt.Errorf("path placeholder %q has no exact request field", placeholder)
		}
	}
	return result, nil
}

func transportContextField(name string) bool {
	switch name {
	case "authorization", "traceparent":
		return true
	default:
		return false
	}
}

// ValidateRoute validates an operation path relative to the consumer-configured
// API base URL. The conventional /api prefix is transport configuration, not a
// second copy of every operation path in .api.
func ValidateRoute(route string) (map[string]bool, error) {
	if route == "/" || !strings.HasPrefix(route, "/") || strings.HasSuffix(route, "/") || strings.Contains(route, "//") {
		return nil, fmt.Errorf("route %q must be a non-root absolute relative operation path", route)
	}
	segments := strings.Split(strings.TrimPrefix(route, "/"), "/")
	placeholders := map[string]bool{}
	for _, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
			if err := ValidateFieldName(name); err != nil || placeholders[name] {
				return nil, fmt.Errorf("route placeholder %q is invalid or duplicated", name)
			}
			placeholders[name] = true
			continue
		}
		if !pathSegmentPattern.MatchString(segment) {
			return nil, fmt.Errorf("route segment %q is not lower-kebab-case", segment)
		}
	}
	return placeholders, nil
}

// SuccessStatus is intentionally uniform. PDCL's shared response writer wraps
// every successful operation in the standard envelope and writes HTTP 200.
func SuccessStatus(method, route string, _ bool) (int, error) {
	if _, err := ValidateRoute(route); err != nil {
		return 0, err
	}
	switch strings.ToUpper(method) {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return 200, nil
	default:
		return 0, errors.New("unsupported method")
	}
}

// ValidatePagination validates PDCL offset pagination values. A positive
// limit is required; offset is zero based. Consumer services own any tighter
// upper limit.
func ValidatePagination(limit, offset int) error {
	if limit < 1 {
		return errors.New("limit must be positive")
	}
	if offset < 0 {
		return errors.New("offset must not be negative")
	}
	return nil
}

// ValidateListResponse enforces the paged collection payload carried in data.
func ValidateListResponse(value map[string]any) error {
	if len(value) != 2 {
		return errors.New("list response must contain exactly items and total")
	}
	items, ok := value["items"]
	if !ok {
		return errors.New("list response items are required")
	}
	itemsValue := reflect.ValueOf(items)
	if !itemsValue.IsValid() || itemsValue.Kind() != reflect.Slice || itemsValue.IsNil() {
		return errors.New("list response items must be an array")
	}
	total, ok := value["total"]
	if !ok || !nonNegativeInteger(total) {
		return errors.New("list response total must be a non-negative integer")
	}
	return nil
}

// ValidateSuccessEnvelope validates PDCL's one successful HTTP envelope.
func ValidateSuccessEnvelope(value map[string]any) error {
	for key := range value {
		if key != "code" && key != "msg" && key != "data" {
			return fmt.Errorf("success envelope member %q is not allowed", key)
		}
	}
	if code, ok := value["code"].(int); !ok || code != 0 {
		return errors.New("success envelope code must be zero")
	}
	if message, ok := value["msg"].(string); !ok || message != "ok" {
		return errors.New("success envelope msg must be ok")
	}
	return nil
}

// ValidateErrorEnvelope validates PDCL's one error HTTP envelope. message is
// a required stable member, not a compatibility fallback for msg.
func ValidateErrorEnvelope(value map[string]any, httpStatus int) error {
	if httpStatus < 400 || httpStatus > 599 {
		return errors.New("error HTTP status must be 4xx or 5xx")
	}
	if len(value) != 3 {
		return errors.New("error envelope must contain exactly code, msg, and message")
	}
	for _, key := range []string{"code", "msg", "message"} {
		if _, ok := value[key]; !ok {
			return fmt.Errorf("error envelope member %q is required", key)
		}
	}
	if code, ok := value["code"].(int); !ok || code != httpStatus {
		return errors.New("error envelope code must equal HTTP status")
	}
	msg, msgOK := value["msg"].(string)
	message, messageOK := value["message"].(string)
	if !msgOK || !messageOK || msg == "" || message == "" || msg != message {
		return errors.New("error envelope msg and message must be equal non-empty strings")
	}
	return nil
}

func nonNegativeInteger(value any) bool {
	switch number := value.(type) {
	case int:
		return number >= 0
	case int32:
		return number >= 0
	case int64:
		return number >= 0
	case uint:
		return true
	case uint32:
		return true
	case uint64:
		return true
	case float64:
		return number >= 0 && number == float64(int64(number))
	default:
		return false
	}
}
