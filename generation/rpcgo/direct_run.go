package rpcgo

import (
	"context"
	"errors"
	"reflect"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

type DirectOptions struct {
	RepositoryRoot string
	Tool           toolchain.Tool
	Runner         toolchain.DirectRunner
	Environment    []toolchain.EnvVar
	OutputScopes   []directwrite.OutputScope
}

// RunDirectRPCGo invokes the v2 RPC tool only after the complete scope relation is valid.
func RunDirectRPCGo(ctx context.Context, request RPCGoRequest, options DirectOptions) (RPCGoResult, error) {
	if ctx == nil || options.Runner == nil {
		return RPCGoResult{}, directFailure("validate-input", "request_invalid", options, errors.New("RPC Go direct invocation is invalid"), false)
	}
	if len(options.Tool.Args) != 0 {
		return RPCGoResult{}, directFailure("validate-input", "tool_scope_invalid", options, errors.New("RPC Go tool fixed args must be empty"), false)
	}
	root, err := toolchain.CanonicalRepositoryRoot(options.RepositoryRoot)
	if err != nil {
		return RPCGoResult{}, directFailure("validate-input", "repository_invalid", options, err, false)
	}
	requestScopes, _, err := toolchain.OutputScopesSubset(request.OutputScopes, options.OutputScopes)
	if err != nil {
		return RPCGoResult{}, directFailure("validate-input", "tool_scope_invalid", options, err, false)
	}
	if !reflect.DeepEqual(scopePaths(requestScopes), options.Tool.WriteScopes) {
		return RPCGoResult{}, directFailure("validate-input", "tool_scope_invalid", options, errors.New("RPC Go tool write scopes do not match request scopes"), false)
	}
	if len(options.Tool.InputScopes) != 0 {
		return RPCGoResult{}, directFailure("validate-input", "tool_scope_invalid", options, errors.New("RPC Go tool input scopes must be empty"), false)
	}
	request.OutputScopes = requestScopes
	stdin, err := CanonicalRPCGoRequest(request)
	if err != nil {
		return RPCGoResult{}, directFailure("validate-input", "request_invalid", options, err, false)
	}
	manual, err := snapshotManualScopeFiles(root, requestScopes, request.ServiceID)
	if err != nil {
		return RPCGoResult{}, directFailure("validate-input", "repository_invalid", options, err, false)
	}
	processResult, err := options.Runner.RunDirect(ctx, toolchain.DirectRequest{RepositoryRoot: root, Tool: options.Tool, Args: []string{"generate", "--service", request.ServiceID}, Environment: append([]toolchain.EnvVar(nil), options.Environment...), Stdin: stdin})
	if err != nil {
		return RPCGoResult{}, err
	}
	if err := verifyManualScopeFiles(root, manual); err != nil {
		return RPCGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, err)
	}
	if err := rejectNewUnmarkedFiles(root, requestScopes, manual, request.ServiceID); err != nil {
		return RPCGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, err)
	}
	if processResult.ToolID != options.Tool.ID || processResult.Version != options.Tool.Version || processResult.ExecutableVersion != options.Tool.Probe.ExpectedVersion || processResult.ExitCode != 0 {
		cause := errors.New("RPC Go tool process identity is invalid")
		return RPCGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationProcessIdentityInvalid, cause)
	}
	result, err := ParseRPCGoResult(processResult.Stdout)
	if err != nil {
		return RPCGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationResultInvalid, err)
	}
	if result.ServiceID != request.ServiceID || result.InputDigest != provenance.SHA256(stdin) || !reflect.DeepEqual(result.OutputScopes, requestScopes) {
		cause := errors.New("RPC Go tool result does not acknowledge the exact request")
		return RPCGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, cause)
	}
	if err := validateDirectRPCGoOutput(root, request); err != nil {
		return RPCGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, err)
	}
	return result, nil
}

func scopePaths(scopes []directwrite.OutputScope) []string {
	result := make([]string, len(scopes))
	for index, scope := range scopes {
		result[index] = scope.Path
	}
	return result
}
