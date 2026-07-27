package generation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
)

const errorDomain = "nexactl.generation"

type errorDetails struct {
	errorCode  string
	Stage      string `json:"stage"`
	Reason     string `json:"reason"`
	Pointer    string `json:"pointer,omitempty"`
	Source     string `json:"source,omitempty"`
	ToolID     string `json:"toolId,omitempty"`
	ExitCode   int    `json:"exitCode,omitempty"`
	Diagnostic string `json:"diagnostic,omitempty"`
}

func (d errorDetails) ErrorCode() string { return d.errorCode }
func (d errorDetails) CanonicalJSON() ([]byte, error) {
	encoded, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

func generationError(code string, category protocol.Category, message, stage, reason, pointer, source, toolID string, exitCode int, diagnostic string) error {
	projected, err := protocol.NewErrorWithDetails(code, errorDomain, category, message, "", errorDetails{
		errorCode: code, Stage: stage, Reason: reason, Pointer: pointer, Source: source,
		ToolID: toolID, ExitCode: exitCode, Diagnostic: diagnostic,
	})
	if err != nil {
		return protocol.NewError("internal_error", errorDomain, protocol.CategoryInternal, "generation command failed", "")
	}
	return projected
}

func inputError(code, stage, reason, pointer, source string) error {
	return generationError(code, protocol.CategoryInput, "generation input is invalid", stage, reason, pointer, source, "", 0, "")
}

func unavailableProviderError() error {
	return generationError("capability_unavailable", protocol.CategoryUnavailable, "generation provider is unavailable", "provider", "provider_missing", "/provider", "", "", 0, "")
}

func delegatedToolFailure(toolID string, exitCode int, diagnostic string) error {
	return generationError("delegated_tool_failed", protocol.CategoryInput, "delegated generation tool failed", "generate", "tool_failed", "/tool", "", toolID, exitCode, diagnostic)
}

type ownerError interface {
	error
	Code() string
	Stage() string
	Reason() string
	Pointer() string
}

func projectOwnerError(owner error) error {
	if owner == nil {
		return nil
	}
	if errors.Is(owner, context.Canceled) || errors.Is(owner, context.DeadlineExceeded) {
		return generationError("operation_canceled", protocol.CategoryCanceled, "generation operation was canceled", "context", "cancelled", "/context", "", "", 0, "")
	}
	var toolError *toolchain.Error
	if errors.As(owner, &toolError) {
		category := protocol.CategoryInput
		if toolError.Reason() == "cancelled" {
			category = protocol.CategoryCanceled
		}
		return generationError(toolError.Code(), category, "delegated generation tool failed", toolError.Stage(), toolError.Reason(), toolError.Pointer(), toolError.Source(), toolError.ToolID(), toolError.ExitCode(), toolError.Diagnostic())
	}
	var typed ownerError
	if errors.As(owner, &typed) {
		return generationError(typed.Code(), protocol.CategoryInput, "generation command failed", typed.Stage(), typed.Reason(), typed.Pointer(), "", "", 0, "")
	}
	return generationError("internal_error", protocol.CategoryInternal, "generation command failed", "command", "owner_error_unrecognized", "", "", "", 0, "")
}
