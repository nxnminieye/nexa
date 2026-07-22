package jobapp

import "errors"

type ErrorCode string

const (
	CodeInvalidInput      ErrorCode = "invalid_input"
	CodeTaskDuplicate     ErrorCode = "task_duplicate"
	CodeTaskUnknown       ErrorCode = "task_unknown"
	CodeTaskFailed        ErrorCode = "task_failed"
	CodeConcurrencyLimit  ErrorCode = "concurrency_limit"
	CodeLifecycleConflict ErrorCode = "lifecycle_conflict"
	CodeScheduleInvalid   ErrorCode = "schedule_invalid"
	CodeRunConflict       ErrorCode = "run_conflict"
	CodeStoreFailure      ErrorCode = "store_failure"
	CodeCanceled          ErrorCode = "canceled"
)

type Error struct {
	Operation string
	Code      ErrorCode
}

func (e *Error) Error() string {
	if e.Operation == "" {
		return string(e.Code)
	}
	return e.Operation + ": " + string(e.Code)
}

func CodeOf(err error) ErrorCode {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func jobError(operation string, code ErrorCode) error {
	return &Error{Operation: operation, Code: code}
}
