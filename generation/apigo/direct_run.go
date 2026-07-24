package apigo

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	Sources        []provenance.Source
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
	expectedInputs := staticInputPaths(request.StaticInputs)
	sort.Strings(expectedInputs)
	actualInputs := append([]string(nil), options.Tool.InputScopes...)
	sort.Strings(actualInputs)
	if !reflect.DeepEqual(expectedInputs, actualInputs) {
		return APIGoResult{}, errors.New("API Go tool input scopes do not match request static inputs")
	}
	if err := toolchain.ValidatePathSetsDisjoint(staticInputPaths(request.StaticInputs), requestScopes); err != nil {
		return APIGoResult{}, errors.New("API Go static inputs and output scopes overlap")
	}
	request.OutputScopes = requestScopes
	stdin, err := CanonicalAPIGoRequest(request)
	if err != nil {
		return APIGoResult{}, err
	}
	manual, err := snapshotManualScopeFiles(root, requestScopes)
	if err != nil {
		return APIGoResult{}, err
	}
	staticIdentities := make(map[string]os.FileInfo, len(request.StaticInputs))
	for _, input := range request.StaticInputs {
		content, identity, readErr := readStaticInputIdentity(root, input.Path)
		if readErr != nil || provenance.SHA256(content) != input.Digest {
			return APIGoResult{}, errors.New("API Go static input does not match its declared digest")
		}
		staticIdentities[input.Path] = identity
	}
	processResult, err := options.Runner.RunDirect(ctx, toolchain.DirectRequest{RepositoryRoot: root, Tool: options.Tool, Args: []string{"generate", "--core-service", request.CoreServiceID}, Environment: append([]toolchain.EnvVar(nil), options.Environment...), Stdin: stdin})
	if err != nil {
		return APIGoResult{}, err
	}
	// The trusted tool receives static inputs by path. Re-read their identity
	// after it exits as well as before launch; a runner that mutates an input
	// must never be able to return a successful acknowledgement.
	for _, input := range request.StaticInputs {
		content, identity, readErr := readStaticInputIdentity(root, input.Path)
		if readErr != nil || provenance.SHA256(content) != input.Digest || !os.SameFile(staticIdentities[input.Path], identity) {
			cause := errors.New("API Go static input changed during tool invocation")
			return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, cause)
		}
	}
	if err := verifyManualScopeFiles(root, manual); err != nil {
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, err)
	}
	if err := rejectNewUnmarkedFiles(root, requestScopes, manual); err != nil {
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, err)
	}
	if processResult.ToolID != options.Tool.ID || processResult.Version != options.Tool.Version || processResult.ExecutableVersion != options.Tool.Probe.ExpectedVersion || processResult.ExitCode != 0 {
		cause := errors.New("API Go tool process identity is invalid")
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationProcessIdentityInvalid, cause)
	}
	result, err := ParseAPIGoResult(processResult.Stdout)
	if err != nil {
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationResultInvalid, err)
	}
	if result.CoreServiceID != request.CoreServiceID || result.InputDigest != provenance.SHA256(stdin) || !reflect.DeepEqual(result.OutputScopes, requestScopes) {
		cause := errors.New("API Go tool result does not acknowledge the exact request")
		return APIGoResult{}, toolchain.DirectPostInvocationError(options.Tool.ID, toolchain.DirectPostInvocationAcknowledgementInvalid, cause)
	}
	return result, nil
}

func readStaticInput(root, relative string) ([]byte, error) {
	content, _, err := readStaticInputIdentity(root, relative)
	return content, err
}

func readStaticInputIdentity(root, relative string) ([]byte, os.FileInfo, error) {
	if toolchain.ValidateRepositoryPath(relative) != nil {
		return nil, nil, os.ErrInvalid
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer handle.Close()
	components := strings.Split(relative, "/")
	for index := range components {
		name := filepath.Join(components[:index+1]...)
		info, err := handle.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, os.ErrInvalid
		}
	}
	info, err := handle.Lstat(filepath.FromSlash(relative))
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, os.ErrInvalid
	}
	file, err := handle.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	return content, info, err
}
func apiScopePaths(scopes []directwrite.OutputScope) []string {
	result := make([]string, len(scopes))
	for index, scope := range scopes {
		result[index] = scope.Path
	}
	return result
}
