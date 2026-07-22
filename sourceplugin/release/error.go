package release

import "context"

type ErrorClass uint8

const (
	ErrReleaseInput ErrorClass = iota + 1
	ErrReleaseUnavailable
	ErrReleaseConflict
	ErrReleaseInternal
	ErrReleaseCanceled
)

func (c ErrorClass) Error() string {
	switch c {
	case ErrReleaseInput:
		return "source release input is invalid"
	case ErrReleaseUnavailable:
		return "source release is unavailable"
	case ErrReleaseConflict:
		return "source release conflicts with immutable state"
	case ErrReleaseInternal:
		return "source release operation failed"
	case ErrReleaseCanceled:
		return "source release operation was canceled"
	default:
		return ""
	}
}

type Stage string

const (
	StageRef              Stage = "ref"
	StageProviderSnapshot Stage = "provider-snapshot"
	StageResolverStatic   Stage = "resolver-static"
	StageResolverCache    Stage = "resolver-cache"
	StageCacheOpen        Stage = "cache-open"
	StageCacheLoad        Stage = "cache-load"
	StageCacheStore       Stage = "cache-store"
)

type Error struct {
	class   ErrorClass
	code    string
	reason  string
	pointer string
	stage   Stage
	context error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.class.Error()
}

func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	if class, ok := target.(ErrorClass); ok {
		return e.class == class
	}
	return e.class == ErrReleaseCanceled && e.context == target
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
func (e *Error) Stage() Stage {
	if e == nil {
		return ""
	}
	return e.stage
}

func releaseError(class ErrorClass, code, reason, pointer string, stage Stage) *Error {
	return &Error{class: class, code: code, reason: reason, pointer: pointer, stage: stage}
}

func canceledError(cause error, stage Stage) *Error {
	reason := "context_canceled"
	if cause == context.DeadlineExceeded {
		reason = "deadline_exceeded"
	}
	return &Error{class: ErrReleaseCanceled, code: "source_release_canceled", reason: reason, pointer: "/context", stage: stage, context: cause}
}
