package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/internal/entexec"
)

// ExecDirectRunner executes a tool directly in a canonical consumer root.
// Process lifecycle, limits, cancellation, and diagnostics remain owned by entexec.
type ExecDirectRunner struct{}

func NewExecDirectRunner() *ExecDirectRunner { return &ExecDirectRunner{} }

// CanonicalRepositoryRoot resolves an existing directory without creating it.
func CanonicalRepositoryRoot(value string) (string, error) {
	root, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", os.ErrInvalid
	}
	return root, nil
}

// NormalizeOutputScopes applies the direct-write path, topology, and order rules.
func NormalizeOutputScopes(scopes []directwrite.OutputScope) ([]directwrite.OutputScope, error) {
	encoded, err := directwrite.CanonicalGenerationResult(directwrite.GenerationResult{
		APIVersion:   directwrite.GenerationResultAPIVersion,
		Kind:         directwrite.GenerationResultKind,
		Status:       directwrite.GenerationResultStatusGenerated,
		OutputScopes: scopes,
	})
	if err != nil {
		return nil, err
	}
	parsed, err := directwrite.ParseGenerationResult(encoded)
	if err != nil {
		return nil, err
	}
	return parsed.OutputScopes, nil
}

// ValidateRepositoryPath reuses direct-write path validation for one coordinate.
func ValidateRepositoryPath(value string) error {
	_, err := NormalizeOutputScopes([]directwrite.OutputScope{{Path: value, Mode: directwrite.OutputModeFileSet}})
	return err
}

// ValidatePathSetsDisjoint rejects exact, ancestor, or descendant relations across
// input paths and output scopes under component-wise NFC and Unicode case folding.
func ValidatePathSetsDisjoint(inputPaths []string, outputScopes []directwrite.OutputScope) error {
	combined := make([]directwrite.OutputScope, 0, len(inputPaths)+len(outputScopes))
	for _, inputPath := range inputPaths {
		combined = append(combined, directwrite.OutputScope{Path: inputPath, Mode: directwrite.OutputModeFileSet})
	}
	combined = append(combined, outputScopes...)
	_, err := NormalizeOutputScopes(combined)
	return err
}

// OutputScopesSubset reports whether subset is a duplicate-free normalized subset.
func OutputScopesSubset(subset, complete []directwrite.OutputScope) ([]directwrite.OutputScope, []directwrite.OutputScope, error) {
	subset, err := NormalizeOutputScopes(subset)
	if err != nil {
		return nil, nil, err
	}
	complete, err = NormalizeOutputScopes(complete)
	if err != nil {
		return nil, nil, err
	}
	index := make(map[directwrite.OutputScope]struct{}, len(complete))
	for _, scope := range complete {
		index[scope] = struct{}{}
	}
	for _, scope := range subset {
		if _, ok := index[scope]; !ok {
			return nil, nil, errors.New("tool output scopes are not a subset of command output scopes")
		}
	}
	return subset, complete, nil
}

func (*ExecDirectRunner) RunDirect(ctx context.Context, request DirectRequest) (Result, error) {
	for _, scopes := range [][]string{request.Tool.InputScopes, request.Tool.WriteScopes} {
		seen := make(map[string]struct{}, len(scopes))
		for _, scope := range scopes {
			if ValidateRepositoryPath(scope) != nil {
				return Result{}, newError("tool_input_invalid", "input", "tool_scope_invalid", "/tool", "", request.Tool.ID, 0)
			}
			if _, duplicate := seen[scope]; duplicate {
				return Result{}, newError("tool_input_invalid", "input", "tool_scope_invalid", "/tool", "", request.Tool.ID, 0)
			}
			seen[scope] = struct{}{}
		}
	}
	processTool := entexec.ProcessTool{
		ID: request.Tool.ID, Version: request.Tool.Version, Executable: request.Tool.Executable,
		Args: append([]string(nil), request.Tool.Args...), InputScopes: append([]string(nil), request.Tool.InputScopes...),
		WriteScopes: append([]string(nil), request.Tool.WriteScopes...),
		Probe:       entexec.ProcessProbe{Args: append([]string(nil), request.Tool.Probe.Args...), ExpectedVersion: request.Tool.Probe.ExpectedVersion},
	}
	processTool.Environment = make([]entexec.ProcessEnvironmentRule, len(request.Tool.Environment))
	for index, rule := range request.Tool.Environment {
		processTool.Environment[index] = entexec.ProcessEnvironmentRule{Name: rule.Name, Source: string(rule.Source), FixedValue: rule.FixedValue}
	}
	environment := make([]entexec.ProcessEnvironment, len(request.Environment))
	for index, value := range request.Environment {
		environment[index] = entexec.ProcessEnvironment{Name: value.Name, Value: value.Value}
	}
	result, err := entexec.RunProcess(ctx, entexec.ProcessSpec{
		RepositoryRoot: request.RepositoryRoot,
		Direct:         true,
		Tool:           processTool,
		Args:           append([]string(nil), request.Args...),
		Environment:    environment,
		Stdin:          append([]byte(nil), request.Stdin...),
	})
	if err != nil {
		return Result{}, projectEntExecError(err)
	}
	return Result{
		ToolID: result.ToolID, Version: result.Version, ExecutableVersion: result.ExecutableVersion,
		ExitCode: result.ExitCode, Stdout: append([]byte(nil), result.Stdout...),
	}, nil
}
