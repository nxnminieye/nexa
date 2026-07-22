package readmodel

import "fmt"

// Error is the stable quality read-model failure projection.
type Error struct {
	reason  string
	source  string
	pointer string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	location := e.source + e.pointer
	if location == "" {
		return "quality read model invalid"
	}
	return fmt.Sprintf("%s: quality read model invalid", location)
}

func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return "quality_read_model_invalid"
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

func invalid(reason, source, pointer string) *Error {
	return &Error{reason: reason, source: source, pointer: pointer}
}
