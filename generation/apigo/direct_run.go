package apigo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

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
	if len(options.Tool.Args) != 0 {
		return APIGoResult{}, errors.New("API Go tool fixed args must be empty")
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
	if err := toolchain.ValidatePathSetsDisjoint(staticInputPaths(request.StaticInputs), requestScopes); err != nil {
		return APIGoResult{}, errors.New("API Go static inputs and output scopes overlap")
	}
	request.OutputScopes = requestScopes
	stdin, err := CanonicalAPIGoRequest(request)
	if err != nil {
		return APIGoResult{}, err
	}
	for _, input := range request.StaticInputs {
		content, readErr := readStaticInput(root, input.Path)
		if readErr != nil || provenance.SHA256(content) != input.Digest {
			return APIGoResult{}, errors.New("API Go static input does not match its declared digest")
		}
	}
	processResult, err := options.Runner.RunDirect(ctx, toolchain.DirectRequest{RepositoryRoot: root, Tool: options.Tool, Args: []string{"generate", "--core-service", request.CoreServiceID}, Environment: append([]toolchain.EnvVar(nil), options.Environment...), Stdin: stdin})
	if err != nil {
		return APIGoResult{}, err
	}
	if processResult.ToolID != options.Tool.ID || processResult.Version != options.Tool.Version || processResult.ExecutableVersion != options.Tool.Probe.ExpectedVersion || processResult.ExitCode != 0 {
		cause := errors.New("API Go tool process identity is invalid")
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, "process_identity_invalid", "/process", cause)
	}
	result, err := ParseAPIGoResult(processResult.Stdout)
	if err != nil {
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, "result_invalid", "/stdout", err)
	}
	if result.CoreServiceID != request.CoreServiceID || result.InputDigest != provenance.SHA256(stdin) || !reflect.DeepEqual(result.OutputScopes, requestScopes) {
		cause := errors.New("API Go tool result does not acknowledge the exact request")
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, "result_acknowledgement_invalid", "/result", cause)
	}
	return result, nil
}

func readStaticInput(root, relative string) ([]byte, error) {
	if toolchain.ValidateRepositoryPath(relative) != nil {
		return nil, os.ErrInvalid
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	components := strings.Split(relative, "/")
	for index := range components {
		name := filepath.Join(components[:index+1]...)
		info, err := handle.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, os.ErrInvalid
		}
	}
	info, err := handle.Lstat(filepath.FromSlash(relative))
	if err != nil || !info.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	file, err := handle.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}
func apiScopePaths(scopes []directwrite.OutputScope) []string {
	result := make([]string, len(scopes))
	for index, scope := range scopes {
		result[index] = scope.Path
	}
	return result
}
