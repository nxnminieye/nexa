package rpcgo

import (
	"errors"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/toolchain"
)

type Error struct {
	code, stage, reason, pointer, source, toolID, toolVersion string
	exitCode                                                  int
	started, mayHaveWritten                                   bool
	cause                                                     error
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return "RPC Go generation failed"
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
func (e *Error) ToolVersion() string {
	if e == nil {
		return ""
	}
	return e.toolVersion
}
func (e *Error) ExitCode() int {
	if e == nil {
		return 0
	}
	return e.exitCode
}
func (e *Error) Started() bool        { return e != nil && e.started }
func (e *Error) MayHaveWritten() bool { return e != nil && e.mayHaveWritten }
func (e *Error) ChangeEvidence() directwrite.ChangeEvidence {
	if e == nil {
		return ""
	}
	if e.mayHaveWritten {
		return directwrite.ChangeEvidenceHostOnly
	}
	return directwrite.ChangeEvidenceComplete
}

func failure(stage, reason string, options Options, err error) *Error {
	result := &Error{code: "rpc_go_invalid", stage: stage, reason: reason, toolID: options.Tool.ID, toolVersion: options.Tool.Version, cause: err}
	var toolError *toolchain.Error
	if errors.As(err, &toolError) {
		result.exitCode = toolError.ExitCode()
	}
	return result
}

func directFailure(stage, reason string, options DirectOptions, err error, postLaunch bool) *Error {
	result := &Error{code: "rpc_go_invalid", stage: stage, reason: reason, pointer: directErrorPointer(reason), toolID: options.Tool.ID, toolVersion: options.Tool.Version, cause: err, started: postLaunch, mayHaveWritten: postLaunch}
	var toolError *toolchain.Error
	if errors.As(err, &toolError) {
		result.exitCode = toolError.ExitCode()
		result.started = toolError.Started()
		result.mayHaveWritten = toolError.MayHaveWritten()
	}
	return result
}

func directErrorPointer(reason string) string {
	switch reason {
	case "request_invalid", "request_input_limit":
		return "/request"
	case "tool_scope_invalid", "tool_failed", "tool_result_invalid":
		return "/tool"
	case "result_invalid", "result_output_limit":
		return "/result"
	case "artifact_invalid", "repository_invalid":
		return "/repository"
	default:
		return ""
	}
}

func failureWithExit(stage, reason string, options Options, exitCode int) *Error {
	result := failure(stage, reason, options, nil)
	result.exitCode = exitCode
	return result
}
