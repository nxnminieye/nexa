package entity

import (
	"errors"
	"fmt"
)

var (
	errEntityInputInvalid    = errors.New("entity input invalid")
	errEntityGraphLoadFailed = errors.New("entity graph load failed")
	errEntityIRInvalid       = errors.New("entity IR invalid")
	errEntitySnapshotInvalid = errors.New("entity snapshot invalid")
)

type Error struct {
	code     string
	reason   string
	pointer  string
	source   string
	sentinel error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	location := e.source
	if e.pointer != "" {
		location += e.pointer
	}
	if location == "" {
		return e.reason
	}
	return fmt.Sprintf("%s: %s", location, e.reason)
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
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}

func inputError(reason, pointer string) *Error {
	return &Error{code: "entity_input_invalid", reason: reason, pointer: pointer, sentinel: errEntityInputInvalid}
}

func graphError(reason, source string) *Error {
	return &Error{code: "entity_graph_load_failed", reason: reason, source: source, sentinel: errEntityGraphLoadFailed}
}

func irError(reason, pointer, source string) *Error {
	return &Error{code: "entity_ir_invalid", reason: reason, pointer: pointer, source: source, sentinel: errEntityIRInvalid}
}

func snapshotError(reason, pointer, source string) *Error {
	return &Error{code: "entity_snapshot_invalid", reason: reason, pointer: pointer, source: source, sentinel: errEntitySnapshotInvalid}
}
