package apigo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func RunDirectAPIGo(ctx context.Context, request APIGoRequest, options DirectOptions) (APIGoResult, error) {
	if ctx == nil || options.Runner == nil {
		return APIGoResult{}, errors.New("API Go direct invocation is invalid")
	}
	root, err := toolchain.CanonicalRepositoryRoot(options.RepositoryRoot)
	if err != nil {
		return APIGoResult{}, err
	}
	requestScopes, _, err := toolchain.OutputScopesSubset(request.OutputScopes, options.OutputScopes)
	if err != nil {
		return APIGoResult{}, err
	}
	if !reflect.DeepEqual(apiScopePaths(requestScopes), options.Tool.WriteScopes) {
		return APIGoResult{}, errors.New("API Go tool write scopes do not match request scopes")
	}
	request.OutputScopes = requestScopes
	stdin, err := CanonicalAPIGoRequest(request)
	if err != nil {
		return APIGoResult{}, err
	}
	for _, input := range request.StaticInputs {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(input.Path)))
		if readErr != nil || provenance.SHA256(content) != input.Digest {
			return APIGoResult{}, errors.New("API Go static input does not match its declared digest")
		}
	}
	processResult, err := options.Runner.RunDirect(ctx, toolchain.DirectRequest{RepositoryRoot: root, Tool: options.Tool, Args: []string{"generate", "--core-service", request.CoreServiceID}, Environment: append([]toolchain.EnvVar(nil), options.Environment...), Stdin: stdin})
	if err != nil {
		return APIGoResult{}, err
	}
	if processResult.ToolID != options.Tool.ID || processResult.Version != options.Tool.Version || processResult.ExecutableVersion != options.Tool.Probe.ExpectedVersion || processResult.ExitCode != 0 {
		return APIGoResult{}, errors.New("API Go tool process identity is invalid")
	}
	result, err := ParseAPIGoResult(processResult.Stdout)
	if err != nil {
		return APIGoResult{}, err
	}
	if result.CoreServiceID != request.CoreServiceID || result.InputDigest != provenance.SHA256(stdin) || !reflect.DeepEqual(result.OutputScopes, requestScopes) {
		return APIGoResult{}, errors.New("API Go tool result does not acknowledge the exact request")
	}
	return result, nil
}
func apiScopePaths(scopes []directwrite.OutputScope) []string {
	result := make([]string, len(scopes))
	for index, scope := range scopes {
		result[index] = scope.Path
	}
	return result
}
