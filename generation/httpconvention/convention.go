// Package httpconvention implements the fixed Nexa JSON HTTP convention.
package httpconvention

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	APIVersion       = "nexa.dev/http-convention/v1"
	BasePath         = "/api"
	JSONMediaType    = "application/json"
	ProblemMediaType = "application/problem+json"
	DefaultPage      = 1
	DefaultPageSize  = 20
	MaximumPageSize  = 100
)

var (
	lowerCamelPattern    = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	pathSegmentPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	problemCodePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
	decimalPattern       = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)$`)
	unsignedPattern      = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)
	decimalNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]*[1-9])?$`)
	datePattern          = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	jsonPointerPattern   = regexp.MustCompile(`^(?:/(?:[^~/]|~[01])*)+$`)
)

//go:embed conformance-v1.schema.json
var conformanceSchema []byte

// ConformanceSchema returns an independent copy of the public fixture schema.
func ConformanceSchema() []byte { return append([]byte(nil), conformanceSchema...) }

// CanonicalName performs the one allowed source-identifier conversion.
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

// ValidateCanonicalName rejects aliases and non-canonical authored names.
func ValidateCanonicalName(name string) error {
	canonical, err := CanonicalName(name)
	if err != nil || canonical != name {
		return fmt.Errorf("field name %q is not canonical lowerCamelCase", name)
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
		if part == "" {
			return nil, errors.New("identifier contains an empty word")
		}
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

// ClassifyRequest derives every business field location from method and route.
func ClassifyRequest(method, route string, fields []string) ([]RequestField, error) {
	placeholders, err := ValidateRoute(route)
	if err != nil {
		return nil, err
	}
	method = strings.ToUpper(method)
	var remainder Location
	switch method {
	case "GET", "DELETE":
		remainder = LocationQuery
	case "POST", "PUT", "PATCH":
		remainder = LocationBody
	default:
		return nil, fmt.Errorf("method %q is not supported by %s", method, APIVersion)
	}
	seen := make(map[string]bool, len(fields))
	result := make([]RequestField, len(fields))
	for index, name := range fields {
		if err := ValidateCanonicalName(name); err != nil {
			return nil, err
		}
		if infrastructureField(name) {
			return nil, fmt.Errorf("infrastructure context %q cannot enter a business DTO", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("request field %q is duplicated", name)
		}
		seen[name] = true
		location := remainder
		if placeholders[name] {
			location = LocationPath
		}
		result[index] = RequestField{Name: name, Location: location}
	}
	for placeholder := range placeholders {
		if !seen[placeholder] {
			return nil, fmt.Errorf("path placeholder %q has no exact request field", placeholder)
		}
	}
	return result, nil
}

func infrastructureField(name string) bool {
	switch name {
	case "authorization", "tenant", "traceparent", "requestId", "traceId":
		return true
	default:
		return false
	}
}

// ValidateRoute enforces the public /api, lower-kebab and exact placeholder grammar.
func ValidateRoute(route string) (map[string]bool, error) {
	if route == BasePath || !strings.HasPrefix(route, BasePath+"/") || strings.HasSuffix(route, "/") || strings.Contains(route, "//") {
		return nil, fmt.Errorf("route %q must be a non-root path below %s", route, BasePath)
	}
	segments := strings.Split(strings.TrimPrefix(route, BasePath+"/"), "/")
	placeholders := map[string]bool{}
	for _, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
			if err := ValidateCanonicalName(name); err != nil || placeholders[name] {
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

// SuccessStatus returns the only status allowed for the operation result shape.
func SuccessStatus(method, route string, hasRepresentation bool) (int, error) {
	if _, err := ValidateRoute(route); err != nil {
		return 0, err
	}
	switch strings.ToUpper(method) {
	case "GET":
		if !hasRepresentation {
			return 0, errors.New("GET success requires a representation")
		}
		return 200, nil
	case "POST":
		if !hasRepresentation {
			return 0, errors.New("POST success requires a result representation")
		}
		if strings.Contains(route, "/actions/") {
			return 200, nil
		}
		return 201, nil
	case "PUT", "PATCH":
		if !hasRepresentation {
			return 0, errors.New("update success requires a result representation")
		}
		return 200, nil
	case "DELETE":
		if hasRepresentation {
			return 0, errors.New("DELETE success must not have a representation")
		}
		return 204, nil
	default:
		return 0, errors.New("unsupported method")
	}
}

func ValidatePage(page, pageSize int) error {
	if page < 1 {
		return errors.New("page must be at least 1")
	}
	if pageSize < 1 || pageSize > MaximumPageSize {
		return fmt.Errorf("pageSize must be between 1 and %d", MaximumPageSize)
	}
	return nil
}

// ValidateListResponse enforces the exact {items,total} collection shape.
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
		return errors.New("list response items must be a non-null array")
	}
	total, ok := value["total"]
	if !ok || !nonNegativeSafeInteger(total) {
		return errors.New("list response total must be a non-negative safe integer")
	}
	return nil
}

var problemStatus = map[string]int{
	"invalid_input":   400,
	"unauthenticated": 401, "invalid_credentials": 401, "session_expired": 401, "session_replayed": 401,
	"permission_denied": 403, "not_found": 404,
	"conflict": 409, "concurrent_write": 409,
	"failed_precondition": 422, "rate_limited": 429,
	"internal_error": 500, "unavailable": 503, "not_ready": 503,
}

func ProblemStatus(category string) (int, bool) {
	status, ok := problemStatus[category]
	return status, ok
}

func ProblemType(category string) (string, error) {
	if _, ok := problemStatus[category]; !ok || !problemCodePattern.MatchString(category) {
		return "", fmt.Errorf("problem category %q is not registered", category)
	}
	return "https://nexa.dev/problems/v1/" + strings.ReplaceAll(category, "_", "-"), nil
}

// ValidateProblem enforces the Nexa RFC 9457 extension shape and status relation.
func ValidateProblem(value map[string]any, httpStatus int) error {
	allowed := map[string]bool{"type": true, "title": true, "status": true, "code": true, "detail": true, "instance": true, "requestId": true, "traceId": true, "fieldErrors": true}
	for key := range value {
		if !allowed[key] {
			return fmt.Errorf("problem member %q is not allowed", key)
		}
	}
	for _, key := range []string{"type", "title", "status", "code"} {
		if _, ok := value[key]; !ok {
			return fmt.Errorf("problem member %q is required", key)
		}
	}
	code, ok := value["code"].(string)
	if !ok || !problemCodePattern.MatchString(code) {
		return errors.New("problem code must be stable lower_snake_case")
	}
	typeURI, ok := value["type"].(string)
	const typePrefix = "https://nexa.dev/problems/v1/"
	if !ok || !strings.HasPrefix(typeURI, typePrefix) {
		return errors.New("problem type is not a Nexa v1 category URI")
	}
	category := strings.ReplaceAll(strings.TrimPrefix(typeURI, typePrefix), "-", "_")
	wantStatus, registered := ProblemStatus(category)
	if !registered || wantStatus != httpStatus || !safeIntegerEquals(value["status"], httpStatus) {
		return errors.New("problem category, body status, and HTTP status disagree")
	}
	wantType, _ := ProblemType(category)
	if value["type"] != wantType {
		return errors.New("problem type is not canonical")
	}
	if title, ok := value["title"].(string); !ok || title == "" {
		return errors.New("problem title must be non-empty")
	}
	if detail, present := value["detail"]; present && httpStatus >= 500 {
		text, ok := detail.(string)
		if !ok || httpStatus == 500 && text != "An internal error occurred." || httpStatus == 503 && text != "Service temporarily unavailable." {
			return errors.New("5xx problem detail must use the fixed safe message")
		}
	}
	if fieldErrors, present := value["fieldErrors"]; present {
		if err := ValidateFieldErrors(fieldErrors); err != nil {
			return err
		}
	}
	return ValidateNoNull(value)
}

func ValidateDecimalString(value string) error {
	if !decimalPattern.MatchString(value) || value == "-0" {
		return errors.New("value must be a canonical decimal string")
	}
	return nil
}

func ValidateUnsignedDecimalString(value string) error {
	if !unsignedPattern.MatchString(value) {
		return errors.New("value must be a canonical unsigned decimal string")
	}
	return nil
}

func ValidateDecimalNumberString(value string) error {
	if !decimalNumberPattern.MatchString(value) || value == "-0" {
		return errors.New("value must be a canonical decimal number string")
	}
	return nil
}

func ValidateOpaqueID(value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.New("ID must be a non-empty opaque string without surrounding whitespace or controls")
	}
	return nil
}

func ValidateEnumValue(value string) error {
	if !problemCodePattern.MatchString(value) {
		return errors.New("enum value must use lower_snake_case")
	}
	return nil
}

func ValidateFieldErrors(value any) error {
	items, ok := value.([]any)
	if !ok || items == nil {
		return errors.New("fieldErrors must be a non-null array")
	}
	for index, item := range items {
		field, ok := item.(map[string]any)
		if !ok || len(field) < 2 || len(field) > 3 {
			return fmt.Errorf("fieldErrors[%d] must contain pointer, code, and optional detail", index)
		}
		for key := range field {
			if key != "pointer" && key != "code" && key != "detail" {
				return fmt.Errorf("fieldErrors[%d] contains unknown member %q", index, key)
			}
		}
		pointer, pointerOK := field["pointer"].(string)
		code, codeOK := field["code"].(string)
		if !pointerOK || !jsonPointerPattern.MatchString(pointer) || !codeOK || !problemCodePattern.MatchString(code) {
			return fmt.Errorf("fieldErrors[%d] pointer or code is invalid", index)
		}
		if detail, present := field["detail"]; present {
			if text, ok := detail.(string); !ok || text == "" {
				return fmt.Errorf("fieldErrors[%d] detail must be non-empty", index)
			}
		}
	}
	return nil
}

func ValidateTimestamp(value string) error {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || !strings.HasSuffix(value, "Z") {
		return errors.New("timestamp must be RFC3339 UTC with a Z suffix")
	}
	return nil
}

func ValidateDate(value string) error {
	if !datePattern.MatchString(value) {
		return errors.New("date must use YYYY-MM-DD")
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return errors.New("date is invalid")
	}
	return nil
}

// ValidateNoNull rejects null recursively, including typed nil slices and maps.
func ValidateNoNull(value any) error {
	if value == nil {
		return errors.New("null is not permitted")
	}
	reflected := reflect.ValueOf(value)
	if (reflected.Kind() == reflect.Map || reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface) && reflected.IsNil() {
		return errors.New("null is not permitted")
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if err := ValidateCanonicalName(key); err != nil {
				return err
			}
			if err := ValidateNoNull(item); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	case []any:
		for index, item := range typed {
			if err := ValidateNoNull(item); err != nil {
				return fmt.Errorf("item %d: %w", index, err)
			}
		}
	}
	return nil
}

func nonNegativeSafeInteger(value any) bool {
	switch number := value.(type) {
	case int:
		return number >= 0 && uint64(number) <= 1<<53-1
	case int64:
		return number >= 0 && uint64(number) <= 1<<53-1
	case uint64:
		return number <= 1<<53-1
	case float64:
		return number >= 0 && number <= 1<<53-1 && math.Trunc(number) == number
	case json.Number:
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		return err == nil && parsed >= 0 && parsed <= 1<<53-1
	default:
		return false
	}
}

func safeIntegerEquals(value any, expected int) bool {
	if !nonNegativeSafeInteger(value) {
		return false
	}
	switch number := value.(type) {
	case int:
		return number == expected
	case int64:
		return number == int64(expected)
	case uint64:
		return number == uint64(expected)
	case float64:
		return number == float64(expected)
	case json.Number:
		return string(number) == strconv.Itoa(expected)
	default:
		return false
	}
}
