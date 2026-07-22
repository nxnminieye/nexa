package crud

import "errors"

var (
	ErrJSONObjectInvalid      = errors.New("runtime crud JSON object invalid")
	ErrJSONObjectEncodeFailed = errors.New("runtime crud JSON object encode failed")
	ErrJSONObjectScanFailed   = errors.New("runtime crud JSON object scan failed")
	ErrWindowPolicyInvalid    = errors.New("runtime crud window policy invalid")
	ErrWindowInvalid          = errors.New("runtime crud window invalid")
)

// Error is the stable, transport-neutral failure projection for this package.
type Error struct {
	code     string
	reason   string
	pointer  string
	sentinel error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.sentinel == nil {
		return "runtime crud error"
	}
	return e.sentinel.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
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

func jsonObjectInvalid(reason, pointer string) *Error {
	return &Error{
		code:     "json_object_invalid",
		reason:   reason,
		pointer:  pointer,
		sentinel: ErrJSONObjectInvalid,
	}
}

func jsonObjectEncodeFailed(reason string) *Error {
	return &Error{
		code:     "json_object_encode_failed",
		reason:   reason,
		sentinel: ErrJSONObjectEncodeFailed,
	}
}

func jsonObjectScanFailed(reason string) *Error {
	return &Error{
		code:     "json_object_scan_failed",
		reason:   reason,
		sentinel: ErrJSONObjectScanFailed,
	}
}

func windowPolicyInvalid(reason, pointer string) *Error {
	return &Error{
		code:     "window_policy_invalid",
		reason:   reason,
		pointer:  pointer,
		sentinel: ErrWindowPolicyInvalid,
	}
}

func windowInvalid(reason, pointer string) *Error {
	return &Error{
		code:     "window_invalid",
		reason:   reason,
		pointer:  pointer,
		sentinel: ErrWindowInvalid,
	}
}
