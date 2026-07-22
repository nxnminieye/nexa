package service

import (
	"fmt"
	"strconv"
)

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

func invalid(reason, pointer, message string) *Error {
	return &Error{code: "service_manifest_invalid", reason: reason, pointer: pointer, message: message}
}
func digestMismatch(reason, pointer, message string) *Error {
	return &Error{code: "service_digest_mismatch", reason: reason, pointer: pointer, message: message}
}
func sourceError(source, reason, pointer, message string) *Error {
	err := invalid(reason, pointer, message)
	err.source = source
	return err
}
func sourceInvalid(reason, pointer, message string) *Error { return invalid(reason, pointer, message) }
func withSource(err error, source string) error {
	if typed, ok := err.(*Error); ok {
		copy := *typed
		copy.source = source
		return &copy
	}
	return err
}
func jsonIndex(index int) string { return strconv.Itoa(index) }
