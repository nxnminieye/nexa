package qualityapp

const (
	CodeProjectionUnavailable = "projection_unavailable"
	CodeProjectionInvalid     = "projection_invalid"
	CodeOperationCanceled     = "operation_canceled"
)

// Error is the stable, redacted Quality runtime error projection.
type Error struct {
	code string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch e.code {
	case CodeProjectionUnavailable:
		return "quality projection is unavailable"
	case CodeProjectionInvalid:
		return "quality projection is invalid"
	case CodeOperationCanceled:
		return "quality projection operation was canceled"
	default:
		return "quality runtime failed"
	}
}

func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

func runtimeError(code string) *Error { return &Error{code: code} }
