package nexaent

import "fmt"

// Error is a closed annotation failure projection. It exposes only stable
// machine fields and never retains parser text or invalid authored bytes.
type Error struct {
	code    string
	reason  string
	pointer string
	source  string
}

// Error returns a UTF-8-safe diagnostic assembled from Source, Pointer, and Reason.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.pointer == "" {
		return fmt.Sprintf("%s: %s", e.source, e.reason)
	}
	return fmt.Sprintf("%s%s: %s", e.source, e.pointer, e.reason)
}

// Code returns the stable top-level annotation error code.
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

// Reason returns the stable closed failure reason.
func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}

// Pointer returns the JSON pointer associated with the failure, or an empty root pointer.
func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}

// Source returns the expected annotation name for the failed owner.
func (e *Error) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}

func annotationError(owner annotationOwner, code, reason, pointer string) *Error {
	return &Error{code: code, reason: reason, pointer: pointer, source: owner.name()}
}

func invalidError(owner annotationOwner, reason, pointer string) *Error {
	return annotationError(owner, "annotation_invalid", reason, pointer)
}

func duplicateError(owner annotationOwner) *Error {
	return annotationError(owner, "annotation_duplicate", "duplicate_annotation", "/duplicate")
}
