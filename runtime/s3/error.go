package s3

import (
	"context"
	"errors"
)

var (
	ErrValidation    = errors.New("runtime s3 validation failed")
	ErrNotFound      = errors.New("runtime s3 object not found")
	ErrReadFailed    = errors.New("runtime s3 read failed")
	ErrWriteFailed   = errors.New("runtime s3 write failed")
	ErrBodyFailed    = errors.New("runtime s3 response body failed")
	ErrPresignFailed = errors.New("runtime s3 presign failed")
)

type errorKind uint8

const (
	errorKindNone errorKind = iota
	errorKindValidation
	errorKindNotFound
	errorKindRead
	errorKindWrite
	errorKindBody
	errorKindPresign
)

// Error is a stable, transport-neutral S3 failure projection.
type Error struct {
	kind    errorKind
	reason  string
	pointer string
	cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if sentinel := e.sentinel(); sentinel != nil {
		return sentinel.Error()
	}
	return "runtime s3 error"
}

func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	switch e.kind {
	case errorKindValidation:
		return "validation_failed"
	case errorKindNotFound:
		return "not_found"
	case errorKindRead:
		return "read_failed"
	case errorKindWrite:
		return "write_failed"
	case errorKindBody:
		return "body_failed"
	case errorKindPresign:
		return "presign_failed"
	default:
		return ""
	}
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

func (e *Error) Unwrap() error {
	if e == nil || (e.cause != context.Canceled && e.cause != context.DeadlineExceeded) {
		return nil
	}
	return e.cause
}

func (e *Error) Is(target error) bool {
	return e != nil && target != nil && target == e.sentinel()
}

func (e *Error) sentinel() error {
	if e == nil {
		return nil
	}
	switch e.kind {
	case errorKindValidation:
		return ErrValidation
	case errorKindNotFound:
		return ErrNotFound
	case errorKindRead:
		return ErrReadFailed
	case errorKindWrite:
		return ErrWriteFailed
	case errorKindBody:
		return ErrBodyFailed
	case errorKindPresign:
		return ErrPresignFailed
	default:
		return nil
	}
}

func validationError(reason, pointer string) *Error {
	return &Error{kind: errorKindValidation, reason: reason, pointer: pointer}
}

// ProjectValidation creates a stable validation error for transport adapters.
func ProjectValidation(reason, pointer string) *Error {
	return validationError(reason, pointer)
}

// ProjectNotFound creates a safe not-found error for transport adapters.
func ProjectNotFound() *Error {
	return &Error{kind: errorKindNotFound, reason: "object_not_found"}
}

// ProjectReadFailure creates a safe read error without retaining provider details.
func ProjectReadFailure(reason string, cause error) *Error {
	return &Error{kind: errorKindRead, reason: reason, cause: safeContextCause(cause)}
}

// ProjectWriteFailure creates a safe write error without retaining provider details.
func ProjectWriteFailure(reason string, cause error) *Error {
	return &Error{kind: errorKindWrite, reason: reason, cause: safeContextCause(cause)}
}

// ProjectBodyFailure creates a safe response-body error.
func ProjectBodyFailure(reason string) *Error {
	return &Error{kind: errorKindBody, reason: reason, pointer: "/body"}
}

// ProjectPresignFailure creates a safe presign error without retaining provider details.
func ProjectPresignFailure(reason string, cause error) *Error {
	return &Error{kind: errorKindPresign, reason: reason, cause: safeContextCause(cause)}
}

func safeContextCause(cause error) error {
	if errors.Is(cause, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
