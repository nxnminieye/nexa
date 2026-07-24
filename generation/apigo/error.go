package apigo

import (
	"errors"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/toolchain"
)

type Error struct {
	code, stage, reason, pointer, source, toolID, toolVersion string
	exitCode                                                  int
	started, mayHaveWritten                                   bool
	report                                                    directwrite.WriteReport
	evidence                                                  directwrite.ChangeEvidence
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
func (e *Error) Report() directwrite.WriteReport {
	if e == nil {
		return directwrite.WriteReport{}
	}
	return cloneAPIWriteReport(e.report)
}
func (e *Error) ChangeEvidence() directwrite.ChangeEvidence {
	if e == nil {
		return ""
	}
	if e.evidence != "" {
		return e.evidence
	}
	return directwrite.ChangeEvidenceComplete
}

func failure(stage, reason string, options Options, err error) *Error {
	result := &Error{code: "api_go_invalid", stage: stage, reason: reason, toolID: options.Tool.ID, toolVersion: options.Tool.Version, cause: err}
	var toolError *toolchain.Error
	if errors.As(err, &toolError) {
		result.exitCode = toolError.ExitCode()
	}
	return result
}

func apiDirectFailure(stage, reason string, options DirectOptions, err error, postLaunch bool) *Error {
	evidence := directwrite.ChangeEvidenceComplete
	if postLaunch {
		evidence = directwrite.ChangeEvidenceHostOnly
	}
	result := &Error{code: "api_go_invalid", stage: stage, reason: reason, pointer: apiDirectErrorPointer(reason), toolID: options.Tool.ID, toolVersion: options.Tool.Version, cause: err, started: postLaunch, mayHaveWritten: postLaunch, evidence: evidence}
	var toolError *toolchain.Error
	if errors.As(err, &toolError) {
		result.exitCode = toolError.ExitCode()
		result.started = toolError.Started()
		result.mayHaveWritten = toolError.MayHaveWritten()
		if !result.started && !result.mayHaveWritten {
			result.evidence = directwrite.ChangeEvidenceComplete
		}
	}
	return result
}

func apiDirectFailureWithReport(stage, reason string, options DirectOptions, err error, postLaunch bool, report directwrite.WriteReport, evidence directwrite.ChangeEvidence) *Error {
	result := apiDirectFailure(stage, reason, options, err, postLaunch)
	result.report = cloneAPIWriteReport(report)
	toolMayHaveWritten := result.started || result.mayHaveWritten
	if len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
		result.mayHaveWritten = true
	}
	if toolMayHaveWritten {
		result.evidence = directwrite.ChangeEvidenceHostOnly
	} else {
		result.evidence = evidence
	}
	return result
}

func cloneAPIWriteReport(input directwrite.WriteReport) directwrite.WriteReport {
	return directwrite.WriteReport{
		CompletedWrites:  append([]string(nil), input.CompletedWrites...),
		CompletedDeletes: append([]string(nil), input.CompletedDeletes...),
	}
}

func apiDirectErrorPointer(reason string) string {
	switch reason {
	case "request_invalid", "request_input_limit", "module_graph_invalid", "module_path_mismatch":
		return "/request"
	case "scope_invalid", "static_scope_invalid":
		return "/outputScopes"
	case "manifest_invalid", "runtime_contract_invalid":
		return "/httpAPIIR"
	case "source_closure_invalid":
		return "/sources"
	case "static_artifact_invalid", "static_input_changed":
		return "/staticInputs"
	case "tool_scope_invalid", "tool_failed", "tool_result_invalid":
		return "/tool"
	case "result_invalid", "result_output_limit":
		return "/result"
	case "artifact_invalid", "repository_invalid", "static_write_failed":
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
