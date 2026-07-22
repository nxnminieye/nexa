package engine

import "fmt"

type ErrorClass uint8

const (
	ErrInput ErrorClass = iota + 1
	ErrNotManaged
	ErrConflict
	ErrUnavailable
	ErrExternal
	ErrInternal
	ErrCanceled
)

func (c ErrorClass) Error() string {
	switch c {
	case ErrInput:
		return "input"
	case ErrNotManaged:
		return "not-managed"
	case ErrConflict:
		return "conflict"
	case ErrUnavailable:
		return "unavailable"
	case ErrExternal:
		return "external"
	case ErrCanceled:
		return "canceled"
	default:
		return "internal"
	}
}

type Error struct {
	class   ErrorClass
	code    string
	reason  string
	pointer string
	stage   string
	cause   error
}

func newError(class ErrorClass, code, reason, pointer, stage string) *Error {
	return newErrorWithCause(class, code, reason, pointer, stage, nil)
}

func newErrorWithCause(class ErrorClass, code, reason, pointer, stage string, cause error) *Error {
	return &Error{class: class, code: code, reason: reason, pointer: pointer, stage: stage, cause: cause}
}

func (e *Error) Error() string {
	if e == nil {
		return "source engine error"
	}
	return fmt.Sprintf("source engine %s: %s", e.class.Error(), e.reason)
}

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.class == other.class && e.code == other.code && e.reason == other.reason
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Class() ErrorClass {
	if e == nil {
		return 0
	}
	return e.class
}
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}
func (e *Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}
func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}
func (e *Error) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}
