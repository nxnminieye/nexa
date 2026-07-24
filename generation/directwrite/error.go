package directwrite

import (
	"errors"
	"fmt"
)

// ErrorKind identifies the stable direct-writer failure class.
type ErrorKind string

const (
	ErrorInvalidScope    ErrorKind = "invalid_scope"
	ErrorInvalidMutation ErrorKind = "invalid_mutation"
	ErrorPathDenied      ErrorKind = "path_denied"
	ErrorPartialWrite    ErrorKind = "partial_write"
	ErrorCanceled        ErrorKind = "canceled"
)

var errDirectWrite = errors.New("direct write failed")

// Error is a typed direct-writer failure. Report remains available on partial
// write and cancellation failures; Write never attempts rollback.
type Error struct {
	kind    ErrorKind
	path    string
	report  WriteReport
	cause   error
	message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.path != "" {
		return fmt.Sprintf("%s: %s", e.path, e.message)
	}
	return e.message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.cause != nil {
		return e.cause
	}
	return errDirectWrite
}

func (e *Error) Kind() ErrorKind {
	if e == nil {
		return ""
	}
	return e.kind
}

func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return "generation_direct_write_failed"
}

func (e *Error) Reason() string { return string(e.Kind()) }

func (e *Error) Path() string {
	if e == nil {
		return ""
	}
	return e.path
}

func (e *Error) Report() WriteReport {
	if e == nil {
		return WriteReport{}
	}
	return cloneReport(e.report)
}

func directError(kind ErrorKind, path, message string, report WriteReport, cause error) *Error {
	return &Error{kind: kind, path: path, message: message, report: cloneReport(report), cause: cause}
}

func cloneReport(input WriteReport) WriteReport {
	result := WriteReport{}
	if input.CompletedWrites != nil {
		result.CompletedWrites = make([]string, len(input.CompletedWrites))
		copy(result.CompletedWrites, input.CompletedWrites)
	}
	if input.CompletedDeletes != nil {
		result.CompletedDeletes = make([]string, len(input.CompletedDeletes))
		copy(result.CompletedDeletes, input.CompletedDeletes)
	}
	return result
}
