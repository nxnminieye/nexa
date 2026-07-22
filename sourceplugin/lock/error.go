package lock

type ErrorClass uint8

const (
	ErrLockInput ErrorClass = iota + 1
	ErrLockConflict
	ErrLockInternal
)

func (c ErrorClass) Error() string {
	switch c {
	case ErrLockInput:
		return "source lock input is invalid"
	case ErrLockConflict:
		return "source lock conflicts with owner state"
	case ErrLockInternal:
		return "source lock operation failed"
	default:
		return ""
	}
}

type Stage string

const (
	StageKey    Stage = "key"
	StageDerive Stage = "derive"
	StageParse  Stage = "parse"
	StageVerify Stage = "verify"
)

type Error struct {
	class   ErrorClass
	code    string
	reason  string
	pointer string
	source  string
	line    int
	column  int
	stage   Stage
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
	class, ok := target.(ErrorClass)
	return ok && e.class == class
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
func (e *Error) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}
func (e *Error) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}
func (e *Error) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}
func (e *Error) Stage() Stage {
	if e == nil {
		return ""
	}
	return e.stage
}

func lockError(class ErrorClass, code, reason, pointer string, stage Stage) *Error {
	return &Error{class: class, code: code, reason: reason, pointer: pointer, stage: stage}
}

func withLocation(err *Error, source string, line, column int) *Error {
	if err == nil {
		return nil
	}
	copyError := *err
	copyError.source, copyError.line, copyError.column = source, line, column
	return &copyError
}
