package buildinfo

import (
	"errors"
	"fmt"
)

var errBuildInfoInvalid = errors.New("build info invalid")

type Error struct {
	code     string
	reason   string
	pointer  string
	message  string
	sentinel error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.pointer == "" {
		return e.message
	}
	return fmt.Sprintf("%s: %s", e.pointer, e.message)
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

func invalid(reason, pointer, message string) *Error {
	return &Error{code: "build_info_invalid", reason: reason, pointer: pointer, message: message, sentinel: errBuildInfoInvalid}
}
