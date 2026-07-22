package servicecatalog

import (
	"errors"
	"fmt"
	"io/fs"
)

var (
	errServiceCatalogEmpty   = errors.New("service catalog empty")
	errServiceCatalogInvalid = errors.New("service catalog invalid")
	errFactSourceReadFailed  = errors.New("fact source read failed")
)

type Error struct {
	code     string
	reason   string
	source   string
	pointer  string
	line     int
	column   int
	cycle    []string
	message  string
	sentinel error
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

func (e *Error) Cycle() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.cycle...)
}

func newError(code, reason, source, pointer, message string) *Error {
	return &Error{
		code:     code,
		reason:   reason,
		source:   source,
		pointer:  pointer,
		message:  message,
		sentinel: errorSentinel(code),
	}
}

func errorSentinel(code string) error {
	switch code {
	case "fact_source_missing":
		return fs.ErrNotExist
	case "fact_source_read_failed":
		return errFactSourceReadFailed
	case "service_catalog_empty":
		return errServiceCatalogEmpty
	default:
		return errServiceCatalogInvalid
	}
}
