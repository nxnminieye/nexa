package httpapi

import "fmt"

type Error struct {
	code, reason, source, pointer, message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	location := e.source + e.pointer
	if location == "" {
		return e.message
	}
	return fmt.Sprintf("%s: %s", location, e.message)
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
func (e *Error) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}
func (e *Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}

func invalid(reason, source, pointer, message string) *Error {
	return &Error{code: "http_api_ir_invalid", reason: reason, source: source, pointer: pointer, message: message}
}
