package crudproto

import (
	"errors"

	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
)

var errCRUDProtocol = errors.New("CRUD protocol operation failed")

type Error struct {
	owner, code, stage, reason, pointer, source string
	toolID, diagnostic                          string
	exitCode                                    int
	cause                                       error
}

func (e *Error) Owner() string {
	if e == nil {
		return ""
	}
	return e.owner
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return errCRUDProtocol.Error()
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
func (e *Error) ToolID() string {
	if e == nil {
		return ""
	}
	return e.toolID
}
func (e *Error) ExitCode() int {
	if e == nil {
		return 0
	}
	return e.exitCode
}
func (e *Error) Diagnostic() string {
	if e == nil {
		return ""
	}
	return e.diagnostic
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return errors.Join(errCRUDProtocol, e.cause)
}

func wrapError(err error) error {
	owner, ok := err.(*crudbuild.Error)
	if !ok {
		return err
	}
	return &Error{code: owner.Code(), stage: owner.Stage(), reason: owner.Reason(), pointer: owner.Pointer(), source: owner.Source(), cause: err}
}

func newStateError(reason, pointer string) *Error {
	return &Error{code: "crud_build_invalid", stage: "build", reason: reason, pointer: pointer}
}

func newHostError(stage, reason, pointer, source string) *Error {
	return newHostCauseError(stage, reason, pointer, source, nil)
}

func newHostCauseError(stage, reason, pointer, source string, cause error) *Error {
	return &Error{code: "crud_host_invalid", stage: stage, reason: reason, pointer: pointer, source: source, cause: cause}
}
