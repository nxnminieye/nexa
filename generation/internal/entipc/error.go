package entipc

import "errors"

var errRequest = errors.New("invalid Ent graph request")
var errResult = errors.New("invalid Ent graph result")

type Error struct {
	code, stage, reason, pointer, source string
	sentinel                             error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.sentinel.Error()
}
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}
func (e *Error) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
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

func requestError(reason, pointer, source string) *Error {
	return &Error{code: "ent_graph_request_invalid", stage: "request", reason: reason, pointer: pointer, source: source, sentinel: errRequest}
}

func resultError(reason, pointer, source string) *Error {
	return &Error{code: "ent_graph_result_invalid", stage: "result-decode", reason: reason, pointer: pointer, source: source, sentinel: errResult}
}
