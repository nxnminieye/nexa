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
		return RPCGoResult{}, errors.New("RPC Go direct invocation is invalid")
	}
	root, err := toolchain.CanonicalRepositoryRoot(options.RepositoryRoot)
	if err != nil {
		return RPCGoResult{}, err
	}
	requestScopes, _, err := toolchain.OutputScopesSubset(request.OutputScopes, options.OutputScopes)
	if err != nil {
		return RPCGoResult{}, err
	}
	if !reflect.DeepEqual(scopePaths(requestScopes), options.Tool.WriteScopes) {
		return RPCGoResult{}, errors.New("RPC Go tool write scopes do not match request scopes")
	}
	request.OutputScopes = requestScopes
	stdin, err := CanonicalRPCGoRequest(request)
	if err != nil {
		return RPCGoResult{}, err
	}
	processResult, err := options.Runner.RunDirect(ctx, toolchain.DirectRequest{RepositoryRoot: root, Tool: options.Tool, Args: []string{"generate", "--service", request.ServiceID}, Environment: append([]toolchain.EnvVar(nil), options.Environment...), Stdin: stdin})
	if err != nil {
		return RPCGoResult{}, err
	}
	if processResult.ToolID != options.Tool.ID || processResult.Version != options.Tool.Version || processResult.ExecutableVersion != options.Tool.Probe.ExpectedVersion || processResult.ExitCode != 0 {
		return RPCGoResult{}, errors.New("RPC Go tool process identity is invalid")
	}
	result, err := ParseRPCGoResult(processResult.Stdout)
	if err != nil {
		return RPCGoResult{}, err
	}
	if result.ServiceID != request.ServiceID || result.InputDigest != provenance.SHA256(stdin) || !reflect.DeepEqual(result.OutputScopes, requestScopes) {
		return RPCGoResult{}, errors.New("RPC Go tool result does not acknowledge the exact request")
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
