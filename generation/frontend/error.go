package frontend

import (
	"errors"
	"fmt"
)

var errInvalid = errors.New("frontend contract invalid")

type Error struct {
	code, reason, source, pointer, message string
	line, column                           int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	location := e.source
	if e.line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, e.line, e.column)
	}
	if e.pointer != "" {
		location += e.pointer
	}
	if location == "" {
		return e.message
	}
	return location + ": " + e.message
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return errInvalid
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

func pageError(reason, source, pointer, message string) *Error {
	return &Error{code: "frontend_page_spec_invalid", reason: reason, source: source, pointer: pointer, message: message}
}
func localeError(reason, source, pointer, message string) *Error {
	return &Error{code: "frontend_locale_invalid", reason: reason, source: source, pointer: pointer, message: message}
}
func buildError(reason, pointer, message string) *Error {
	return &Error{code: "frontend_ir_invalid", reason: reason, pointer: pointer, message: message}
}
func renderError(reason, pointer, message string) *Error {
	return &Error{code: "frontend_render_request_invalid", reason: reason, pointer: pointer, message: message}
}
