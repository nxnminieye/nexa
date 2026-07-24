package toolchain

import (
	"errors"
	"fmt"
	"sync"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
)

type Error struct {
	code, reason, stage, pointer, source, toolID, message string
	diagnostic                                            string
	exitCode                                              int
	started, mayHaveWritten                               bool
	sentinel                                              error
}

func projectEntExecError(err error) error {
	if err == nil {
		return nil
	}
	var internal *entexec.Error
	if errors.As(err, &internal) {
		projected := newDiagnosticError(internal.Code(), internal.Stage(), internal.Reason(), internal.Pointer(), "", internal.ToolID(), internal.ExitCode(), internal.Diagnostic())
		projected.started, projected.mayHaveWritten = internal.Started(), internal.MayHaveWritten()
		return projected
	}
	return newError("scratch_projection_invalid", "project", "location_state_invalid", "/location", "", "", 0)
}

func projectFrameworkIdentityError(err error) error {
	if err == nil {
		return nil
	}
	var internal *frameworkmodule.Error
	if errors.As(err, &internal) {
		return newError(internal.Code(), internal.Stage(), internal.Reason(), internal.Pointer(), "", "", 0)
	}
	return newError("framework_identity_invalid", "framework-identity", "framework_identity_state_invalid", "/framework", "", "", 0)
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

func (e *Error) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
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

// Started reports whether a delegated probe or main process was successfully started.
func (e *Error) Started() bool { return e != nil && e.started }

// MayHaveWritten reports whether a direct tool may have changed the consumer tree.
func (e *Error) MayHaveWritten() bool { return e != nil && e.mayHaveWritten }

var errorSentinels sync.Map

func stableSentinel(code, reason string) error {
	key := code + "\x00" + reason
	value, _ := errorSentinels.LoadOrStore(key, errors.New(code+": "+reason))
	return value.(error)
}

func newError(code, stage, reason, pointer, source, toolID string, exitCode int) *Error {
	return newDiagnosticError(code, stage, reason, pointer, source, toolID, exitCode, "")
}

func newDiagnosticError(code, stage, reason, pointer, source, toolID string, exitCode int, diagnostic string) *Error {
	return &Error{
		code: code, stage: stage, reason: reason, pointer: pointer, source: source,
		toolID: toolID, exitCode: exitCode, diagnostic: diagnostic, message: "build input operation failed",
		sentinel: stableSentinel(code, reason),
	}
}

// DirectPostInvocationFailure is the closed set of protocol failures that can
// occur after a direct runner has returned success.
type DirectPostInvocationFailure uint8

const (
	DirectPostInvocationProcessIdentityInvalid DirectPostInvocationFailure = iota + 1
	DirectPostInvocationResultInvalid
	DirectPostInvocationAcknowledgementInvalid
)

type directPostInvocationError struct {
	projected *Error
	cause     error
}

func (e *directPostInvocationError) Error() string {
	if e == nil {
		return ""
	}
	return e.projected.Error()
}

// Unwrap returns a fresh slice so callers cannot mutate the wrapper's relation.
func (e *directPostInvocationError) Unwrap() []error {
	if e == nil {
		return nil
	}
	if e.cause == nil {
		return []error{e.projected}
	}
	return []error{e.projected, e.cause}
}

func directPostInvocationLocation(failure DirectPostInvocationFailure) (reason, pointer string) {
	switch failure {
	case DirectPostInvocationProcessIdentityInvalid:
		return "process_identity_invalid", "/process"
	case DirectPostInvocationResultInvalid:
		return "result_invalid", "/stdout"
	case DirectPostInvocationAcknowledgementInvalid:
		return "result_acknowledgement_invalid", "/result"
	default:
		return "post_invocation_failure_invalid", "/failure"
	}
}

// DirectPostInvocationError projects a closed protocol failure after a direct
// runner has returned success. The original cause remains available to errors.Is/As.
func DirectPostInvocationError(toolID string, failure DirectPostInvocationFailure, cause error) error {
	reason, pointer := directPostInvocationLocation(failure)
	projected := newError("tool_output_invalid", "result", reason, pointer, "", toolID, 0)
	projected.started = true
	projected.mayHaveWritten = true
	return &directPostInvocationError{projected: projected, cause: cause}
}

func projectBuildInputError(err error, toolID string, exitCode int) error {
	if err == nil {
		return nil
	}
	var public *Error
	if errors.As(err, &public) {
		return public
	}
	var internal *buildinput.Error
	if errors.As(err, &internal) {
		return newError(internal.Code(), internal.Stage(), internal.Reason(), internal.Pointer(), internal.Source(), toolID, exitCode)
	}
	return newError("build_input_invalid", "retain", "compile_spec_invalid", "/buildInputs", "", toolID, exitCode)
}
