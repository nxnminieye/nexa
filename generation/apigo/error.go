package apigo

import (
	"errors"

	"github.com/nxnminieye/nexa/generation/toolchain"
)

type Error struct {
	code, stage, reason, toolID, toolVersion string
	exitCode                                 int
	cause                                    error
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
	return "API Go generation failed"
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

func failure(stage, reason string, options Options, err error) *Error {
	result := &Error{code: "api_go_invalid", stage: stage, reason: reason, toolID: options.Tool.ID, toolVersion: options.Tool.Version, cause: err}
	var toolError *toolchain.Error
	if errors.As(err, &toolError) {
		result.exitCode = toolError.ExitCode()
	}
	return result
}

func failureWithExit(stage, reason string, options Options, exitCode int) *Error {
	result := failure(stage, reason, options, nil)
	result.exitCode = exitCode
	return result
}
