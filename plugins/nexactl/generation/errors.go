package generation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/generation/apigo"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/crudlogic"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/rpcgo"
	servicecontract "github.com/nxnminieye/nexa/generation/service"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	sdkapi "github.com/nxnminieye/nexa/sdk/api"
)

const errorDomain = "nexactl.generation"

type errorDetails struct {
	errorCode   string
	Stage       string `json:"stage"`
	Reason      string `json:"reason"`
	Pointer     string `json:"pointer,omitempty"`
	Source      string `json:"source,omitempty"`
	ToolID      string `json:"toolId,omitempty"`
	ToolVersion string `json:"toolVersion,omitempty"`
	ExitCode    int    `json:"exitCode,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

func delegatedToolError(code, message, stage, reason, toolID, toolVersion string, exitCode int) error {
	projected, err := protocol.NewErrorWithDetails(
		code,
		errorDomain,
		protocol.CategoryInput,
		message,
		"",
		errorDetails{errorCode: code, Stage: stage, Reason: reason, ToolID: toolID, ToolVersion: toolVersion, ExitCode: exitCode},
	)
	if err != nil {
		return protocol.NewError("internal_error", errorDomain, protocol.CategoryInternal, "generation command failed", "")
	}
	return projected
}

func (d errorDetails) ErrorCode() string { return d.errorCode }

func (d errorDetails) CanonicalJSON() ([]byte, error) {
	encoded, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}

func generationError(code string, category protocol.Category, message, stage, reason, pointer, source string) error {
	return generationDiagnosticError(code, category, message, stage, reason, pointer, source, "", 0, "")
}

func generationDiagnosticError(code string, category protocol.Category, message, stage, reason, pointer, source, toolID string, exitCode int, diagnostic string) error {
	projected, err := protocol.NewErrorWithDetails(
		code,
		errorDomain,
		category,
		message,
		"",
		errorDetails{errorCode: code, Stage: stage, Reason: reason, Pointer: pointer, Source: source, ToolID: toolID, ExitCode: exitCode, Diagnostic: diagnostic},
	)
	if err != nil {
		return protocol.NewError("internal_error", errorDomain, protocol.CategoryInternal, "generation command failed", "")
	}
	return projected
}

func unavailableProviderError() error {
	return generationError(
		"capability_unavailable",
		protocol.CategoryUnavailable,
		"generation provider is unavailable",
		"provider",
		"provider_missing",
		"/provider",
		"",
	)
}

func inputError(code, stage, reason, pointer, source string) error {
	return generationError(code, protocol.CategoryInput, "generation input is invalid", stage, reason, pointer, source)
}

func driftError(code, stage, reason, pointer, source string) error {
	return generationError(code, protocol.CategoryDrift, "generation plan does not match repository state", stage, reason, pointer, source)
}

type stagedOwnerError interface {
	error
	Code() string
	Stage() string
	Reason() string
	Pointer() string
}

type sourcedOwnerError interface {
	Source() string
}

func projectOwnerError(owner error) (projected error) {
	if owner == nil {
		return nil
	}
	defer func() {
		if projected != nil {
			projected = &safeCauseError{projected: projected, cause: owner}
		}
	}()
	err := owner
	var staged stagedOwnerError
	if errors.As(err, &staged) {
		if staged.Code() == "tool_canceled" || staged.Code() == "tool_deadline_exceeded" || staged.Reason() == "cancelled" {
			source := ""
			var sourced sourcedOwnerError
			if errors.As(err, &sourced) {
				source = sourced.Source()
			}
			return canceledError(staged.Stage(), staged.Reason(), staged.Pointer(), source)
		}
		if errors.Is(err, context.Canceled) {
			return canceledError("context", "context_canceled", "/context", "")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return canceledError("context", "context_deadline_exceeded", "/context", "")
		}
	}
	var transactionError *transaction.Error
	if errors.As(err, &transactionError) {
		category := protocol.CategoryInput
		switch transactionError.Reason() {
		case "plan_digest_mismatch", "current_changed_after_plan":
			category = protocol.CategoryDrift
		case "plan_conflict":
			category = protocol.CategoryConflict
		case "cancelled":
			category = protocol.CategoryCanceled
		}
		return generationError(transactionError.Code(), category, "generation transaction failed", transactionError.Stage(), transactionError.Reason(), transactionError.Pointer(), "")
	}
	if errors.Is(err, context.Canceled) {
		return canceledError("context", "context_canceled", "/context", "")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return canceledError("context", "context_deadline_exceeded", "/context", "")
	}
	var crudError *crudproto.Error
	if errors.As(err, &crudError) {
		return generationDiagnosticError(crudError.Code(), protocol.CategoryInput, "CRUD Proto generation failed", crudError.Stage(), crudError.Reason(), crudError.Pointer(), crudError.Source(), crudError.ToolID(), crudError.ExitCode(), crudError.Diagnostic())
	}
	var logicError *crudlogic.Error
	if errors.As(err, &logicError) {
		return generationError(logicError.Code(), protocol.CategoryInput, "CRUD logic generation failed", logicError.Stage(), logicError.Reason(), logicError.Pointer(), "")
	}
	var protocolError *genprotocol.Error
	if errors.As(err, &protocolError) {
		return generationError(protocolError.Code(), protocol.CategoryInput, "Protocol generation failed", "protocol", protocolError.Reason(), protocolError.Pointer(), protocolError.Source())
	}
	var rpcError *rpcgo.Error
	if errors.As(err, &rpcError) {
		if rpcError.Reason() == "operation_canceled" {
			return canceledError(rpcError.Stage(), rpcError.Reason(), "", "")
		}
		return delegatedToolError(rpcError.Code(), "RPC Go generation failed", rpcError.Stage(), rpcError.Reason(), rpcError.ToolID(), rpcError.ToolVersion(), rpcError.ExitCode())
	}
	var apiGoError *apigo.Error
	if errors.As(err, &apiGoError) {
		if apiGoError.Reason() == "operation_canceled" {
			return canceledError(apiGoError.Stage(), apiGoError.Reason(), "", "")
		}
		return delegatedToolError(apiGoError.Code(), "API Go generation failed", apiGoError.Stage(), apiGoError.Reason(), apiGoError.ToolID(), apiGoError.ToolVersion(), apiGoError.ExitCode())
	}
	var apiError *httpapi.Error
	if errors.As(err, &apiError) {
		return generationError(apiError.Code(), protocol.CategoryInput, "HTTP API generation failed", "api", apiError.Reason(), apiError.Pointer(), apiError.Source())
	}
	var compositionError *composition.Error
	if errors.As(err, &compositionError) {
		return generationError(compositionError.Code(), protocol.CategoryInput, "API composition failed", "composition", compositionError.Reason(), compositionError.Pointer(), compositionError.Source())
	}
	var catalogError *servicecatalog.Error
	if errors.As(err, &catalogError) {
		return generationError(catalogError.Code(), protocol.CategoryInput, "service catalog is invalid", "catalog", catalogError.Reason(), catalogError.Pointer(), catalogError.Source())
	}
	var serviceError *servicecontract.Error
	if errors.As(err, &serviceError) {
		return generationError(serviceError.Code(), protocol.CategoryInput, "service manifest generation failed", "service-manifest", serviceError.Reason(), serviceError.Pointer(), serviceError.Source())
	}
	var runtimeError *sdkapi.Error
	if errors.As(err, &runtimeError) {
		details := runtimeError.Details()
		return generationError(runtimeError.Code(), runtimeError.Category(), "API runtime contract generation failed", "runtime-contract", details.Reason(), details.Pointer(), "")
	}
	var toolError *toolchain.Error
	if errors.As(err, &toolError) {
		return generationDiagnosticError(toolError.Code(), protocol.CategoryInput, "Ent generation failed", toolError.Stage(), toolError.Reason(), toolError.Pointer(), toolError.Source(), toolError.ToolID(), toolError.ExitCode(), toolError.Diagnostic())
	}
	var artifactError *artifact.Error
	if errors.As(err, &artifactError) {
		return generationError(artifactError.Code(), protocol.CategoryInput, "artifact manifest is invalid", "manifest", artifactError.Reason(), artifactError.Pointer(), artifactError.Source())
	}
	return generationError("internal_error", protocol.CategoryInternal, "generation command failed", "command", "owner_error_unrecognized", "", "")
}

type safeCauseError struct {
	projected error
	cause     error
}

func (e *safeCauseError) Error() string   { return e.projected.Error() }
func (e *safeCauseError) Unwrap() []error { return []error{e.projected, e.cause} }

func canceledError(stage, reason, pointer, source string) error {
	return generationError("operation_canceled", protocol.CategoryCanceled, "generation operation was canceled", stage, reason, pointer, source)
}
