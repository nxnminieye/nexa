package entexec

import (
	"errors"
	"sync"
)

type Error struct {
	code, stage, reason, pointer, toolID string
	diagnostic                           string
	exitCode                             int
	started, mayHaveWritten              bool
	sentinel                             error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.diagnostic != "" {
		return "Ent helper module operation failed: " + e.diagnostic
	}
	return "Ent helper module operation failed"
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

// Started reports whether the main tool process was successfully started.
func (e *Error) Started() bool { return e != nil && e.started }

// MayHaveWritten reports whether the main tool may have changed its working tree.
func (e *Error) MayHaveWritten() bool { return e != nil && e.mayHaveWritten }

func markProcessStarted(err error, mayHaveWritten bool) error {
	var typed *Error
	if errors.As(err, &typed) {
		typed.started = true
		typed.mayHaveWritten = mayHaveWritten
	}
	return err
}

var entexecErrorSentinels sync.Map

func entexecStableSentinel(code, reason string) error {
	key := code + "\x00" + reason
	value, _ := entexecErrorSentinels.LoadOrStore(key, errors.New(code+": "+reason))
	return value.(error)
}

func newError(code, stage, reason, pointer string) *Error {
	return newProcessError(code, stage, reason, pointer, "", 0)
}

func newProcessError(code, stage, reason, pointer, toolID string, exitCode int) *Error {
	return newProcessDiagnosticError(code, stage, reason, pointer, toolID, exitCode, "")
}

func newProcessDiagnosticError(code, stage, reason, pointer, toolID string, exitCode int, diagnostic string) *Error {
	return &Error{
		code: code, stage: stage, reason: reason, pointer: pointer, toolID: toolID, exitCode: exitCode, diagnostic: diagnostic,
		sentinel: entexecStableSentinel(code, reason),
	}
}

func locateError(reason, pointer string) *Error {
	return newError("scratch_projection_invalid", "locate", reason, pointer)
}

func projectError(reason, pointer string) *Error {
	return newError("scratch_projection_invalid", "project", reason, pointer)
}

func normalizeError(reason, pointer string) *Error {
	return newError("scratch_projection_invalid", "normalize", reason, pointer)
}

func readbackError(reason, pointer string) *Error {
	return newError("module_graph_readback_invalid", "readback", reason, pointer)
}

func cleanupError(reason string) *Error {
	return newError("scratch_cleanup_failed", "cleanup", reason, "/scratch")
}

func executionRootError(reason, pointer string) *Error {
	return newError("execution_root_invalid", "execution-root", reason, pointer)
}

func executionCleanupError(reason string) *Error {
	return newError("execution_root_cleanup_failed", "cleanup", reason, "/executionRoot")
}
