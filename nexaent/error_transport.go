package nexaent

import (
	"unicode"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/text/unicode/norm"
)

// MaxEntHelperErrorPointerBytes is the maximum encoded size of a helper error
// JSON pointer at the process boundary.
const MaxEntHelperErrorPointerBytes = 512

// EntHelperErrorField identifies the first invalid transport tuple member.
type EntHelperErrorField string

const (
	// EntHelperErrorFieldNone identifies no classified field.
	EntHelperErrorFieldNone EntHelperErrorField = ""
	// EntHelperErrorFieldCode identifies the code member.
	EntHelperErrorFieldCode EntHelperErrorField = "code"
	// EntHelperErrorFieldReason identifies the reason member.
	EntHelperErrorFieldReason EntHelperErrorField = "reason"
	// EntHelperErrorFieldPointer identifies the pointer member.
	EntHelperErrorFieldPointer EntHelperErrorField = "pointer"
	// EntHelperErrorFieldSource identifies the source member.
	EntHelperErrorFieldSource EntHelperErrorField = "source"
)

// EntHelperErrorProjection is an immutable helper error tuple with closed
// code, reason, and source fields. Its pointer is only a bounded syntactic
// diagnostic; the tuple does not authenticate its producer or prove that the
// pointer is reachable in an annotation document or schema.
type EntHelperErrorProjection struct {
	code    string
	reason  string
	pointer string
	source  string
}

// Code returns the validated error code.
func (p EntHelperErrorProjection) Code() string { return p.code }

// Reason returns the validated error reason.
func (p EntHelperErrorProjection) Reason() string { return p.reason }

// Pointer returns the bounded, syntactically valid diagnostic JSON pointer.
// It does not prove document or schema reachability.
func (p EntHelperErrorProjection) Pointer() string { return p.pointer }

// Source returns the validated annotation owner name.
func (p EntHelperErrorProjection) Source() string { return p.source }

// EntHelperErrorValidationError reports the first invalid tuple member.
type EntHelperErrorValidationError struct {
	field EntHelperErrorField
}

// Error returns a fixed diagnostic that does not retain the rejected tuple.
func (e *EntHelperErrorValidationError) Error() string {
	if e == nil {
		return ""
	}
	return "invalid Ent helper error projection"
}

// Field returns the first invalid tuple member. Nil and zero values return
// EntHelperErrorFieldNone.
func (e *EntHelperErrorValidationError) Field() EntHelperErrorField {
	if e == nil {
		return EntHelperErrorFieldNone
	}
	return e.field
}

// ProjectEntHelperError validates and projects a direct, non-nil owner Error.
func ProjectEntHelperError(err error) (EntHelperErrorProjection, bool) {
	ownerErr, ok := err.(*Error)
	if !ok || ownerErr == nil {
		return EntHelperErrorProjection{}, false
	}
	projection, validationErr := ParseEntHelperErrorProjection(ownerErr.code, ownerErr.reason, ownerErr.pointer, ownerErr.source)
	if validationErr != nil {
		return EntHelperErrorProjection{}, false
	}
	return projection, true
}

// ParseEntHelperErrorProjection validates an untrusted helper error tuple.
func ParseEntHelperErrorProjection(code, reason, pointer, source string) (EntHelperErrorProjection, *EntHelperErrorValidationError) {
	if !knownEntHelperCode(code) {
		return invalidEntHelperProjection(EntHelperErrorFieldCode)
	}
	if !knownEntHelperReason(reason) {
		return invalidEntHelperProjection(EntHelperErrorFieldReason)
	}
	if !validEntHelperPointer(pointer) {
		return invalidEntHelperProjection(EntHelperErrorFieldPointer)
	}
	parsedSource, err := provenance.ParseDomainSource(source)
	if err != nil {
		return invalidEntHelperProjection(EntHelperErrorFieldSource)
	}
	owner, ok := annotationOwnerForTransport(parsedSource.String())
	if !ok {
		return invalidEntHelperProjection(EntHelperErrorFieldSource)
	}
	if !ownerSupportsEntHelperCode(owner, code) {
		return invalidEntHelperProjection(EntHelperErrorFieldCode)
	}
	if !ownerSupportsEntHelperReason(owner, code, reason) {
		return invalidEntHelperProjection(EntHelperErrorFieldReason)
	}
	if !ownerSupportsEntHelperPointer(owner, code, reason, pointer) {
		return invalidEntHelperProjection(EntHelperErrorFieldPointer)
	}
	return EntHelperErrorProjection{code: code, reason: reason, pointer: pointer, source: source}, nil
}

func invalidEntHelperProjection(field EntHelperErrorField) (EntHelperErrorProjection, *EntHelperErrorValidationError) {
	return EntHelperErrorProjection{}, &EntHelperErrorValidationError{field: field}
}

func knownEntHelperCode(code string) bool {
	return code == "annotation_duplicate" || code == "annotation_invalid"
}

func knownEntHelperReason(reason string) bool {
	if reason == "duplicate_annotation" || commonAnnotationInvalidReason(reason) {
		return true
	}
	for owner := ownerSchema; owner <= ownerCRUD; owner++ {
		known, _ := allowedInvalidPair(owner, reason, "")
		if known {
			return true
		}
	}
	return false
}

func commonAnnotationInvalidReason(reason string) bool {
	switch reason {
	case "document_invalid", "document_trailing_input", "version_unsupported", "kind_invalid",
		"invalid_sentinel_invalid", "document_duplicate_key", "unicode_invalid",
		"document_unknown_field", "document_required_missing", "document_type_invalid":
		return true
	default:
		return false
	}
}

func validEntHelperPointer(pointer string) bool {
	if !utf8.ValidString(pointer) || len(pointer) > MaxEntHelperErrorPointerBytes || !norm.NFC.IsNormalString(pointer) {
		return false
	}
	for _, character := range pointer {
		if unicode.Is(unicode.Cc, character) {
			return false
		}
	}
	if pointer == "" {
		return true
	}
	if pointer[0] != '/' {
		return false
	}
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) || pointer[index+1] != '0' && pointer[index+1] != '1' {
			return false
		}
		index++
	}
	return true
}

func annotationOwnerForTransport(source string) (annotationOwner, bool) {
	switch source {
	case SchemaAnnotationName:
		return ownerSchema, true
	case FieldAnnotationName:
		return ownerField, true
	case CRUDAnnotationName:
		return ownerCRUD, true
	default:
		return 0, false
	}
}

func ownerSupportsEntHelperCode(owner annotationOwner, code string) bool {
	return owner >= ownerSchema && owner <= ownerCRUD && knownEntHelperCode(code)
}

func ownerSupportsEntHelperReason(owner annotationOwner, code, reason string) bool {
	if code == "annotation_duplicate" {
		return reason == "duplicate_annotation"
	}
	if code != "annotation_invalid" || reason == "duplicate_annotation" {
		return false
	}
	if commonAnnotationInvalidReason(reason) {
		return true
	}
	known, _ := allowedInvalidPair(owner, reason, "")
	return known
}

func ownerSupportsEntHelperPointer(owner annotationOwner, code, reason, pointer string) bool {
	if code == "annotation_duplicate" {
		return reason == "duplicate_annotation" && pointer == "/duplicate"
	}
	switch reason {
	case "document_invalid", "document_trailing_input":
		return pointer == ""
	case "version_unsupported":
		return pointer == "/apiVersion"
	case "kind_invalid":
		return pointer == "/kind"
	case "invalid_sentinel_invalid":
		return pointer == "/invalid/reason" || pointer == "/invalid/pointer"
	case "document_duplicate_key":
		return pointer != ""
	case "unicode_invalid":
		return true
	case "document_unknown_field":
		return pointer != ""
	case "document_required_missing":
		return pointer != ""
	case "document_type_invalid":
		return pointer != ""
	default:
		_, valid := allowedInvalidPair(owner, reason, pointer)
		return valid
	}
}
