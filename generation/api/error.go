package api

import (
	"errors"
	"fmt"
)

var errManifestInvalid = errors.New("API manifest invalid")

type Error struct {
	code, reason, source, pointer, message string
	line, column                           int
	sentinel                               error
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

func invalidError(reason, pointer, message string) *Error {
	return &Error{code: "api_manifest_invalid", reason: reason, pointer: pointer, message: message, sentinel: errManifestInvalid}
}
func sourceError(reason, source, pointer, message string) *Error {
	err := invalidError(reason, pointer, message)
	err.source = source
	return err
}
