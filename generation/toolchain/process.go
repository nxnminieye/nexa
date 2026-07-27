package toolchain

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
)

type ExecRunner struct{}

func NewExecRunner() *ExecRunner { return &ExecRunner{} }

// RunDirect executes a consumer-selected generator in the consumer repository.
// It intentionally provides no staging or rollback semantics.
func (*ExecRunner) RunDirect(ctx context.Context, request Request) (Result, error) {
	if ctx == nil || request.RepositoryRoot == "" || request.WorkDir == "" || request.Tool.ID == "" || request.Tool.Version == "" || request.Tool.Executable == "" || request.Tool.Probe.ExpectedVersion == "" || !equalScopes(request.Tool.InputScopes, "repository") || !equalScopes(request.Tool.WriteScopes, "repository") {
		return Result{}, newError("tool_input_invalid", "input", "direct_request_invalid", "/tool", "", request.Tool.ID, 0)
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(request.RepositoryRoot))
	if err != nil {
		return Result{}, newError("tool_input_invalid", "input", "repository_root_invalid", "/repositoryRoot", "", request.Tool.ID, 0)
	}
	work, err := filepath.EvalSymlinks(filepath.Clean(request.WorkDir))
	if err != nil {
		return Result{}, newError("tool_input_invalid", "input", "work_dir_invalid", "/workDir", "", request.Tool.ID, 0)
	}
	relative, err := filepath.Rel(root, work)
	if err != nil || relative != "." && !filepath.IsLocal(relative) {
		return Result{}, newError("tool_input_invalid", "input", "work_dir_invalid", "/workDir", "", request.Tool.ID, 0)
	}
	environment, err := directEnvironment(request)
	if err != nil {
		return Result{}, err
	}
	probe := exec.CommandContext(ctx, request.Tool.Executable, request.Tool.Probe.Args...)
	probe.Dir, probe.Env = work, environment
	probeOutput, probeErr := probe.Output()
	if probeErr != nil {
		if ctx.Err() != nil {
			return Result{}, newError("tool_canceled", "probe", "cancelled", "/tool/probe", "", request.Tool.ID, exitStatus(probeErr))
		}
		return Result{}, newError("tool_probe_failed", "probe", "tool_version_mismatch", "/tool/probe", "", request.Tool.ID, exitStatus(probeErr))
	}
	if strings.TrimSpace(string(probeOutput)) != request.Tool.Probe.ExpectedVersion {
		return Result{}, newError("tool_probe_failed", "probe", "tool_version_mismatch", "/tool/probe", "", request.Tool.ID, exitStatus(probeErr))
	}
	command := exec.CommandContext(ctx, request.Tool.Executable, append(append([]string(nil), request.Tool.Args...), request.Args...)...)
	command.Dir, command.Env, command.Stdin = work, environment, bytes.NewReader(request.Stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	if stdout.Len() > MaxStdoutBytes || stderr.Len() > MaxStderrBytes {
		return Result{}, newError("tool_output_limit", "execute", "tool_output_limit", "/tool", "", request.Tool.ID, exitStatus(runErr))
	}
	result := Result{ToolID: request.Tool.ID, Version: request.Tool.Version, ExecutableVersion: request.Tool.Probe.ExpectedVersion, ExitCode: exitStatus(runErr), Stdout: append([]byte(nil), stdout.Bytes()...), Diagnostic: sanitizeDiagnostic(stderr.String(), request)}
	if runErr != nil {
		if ctx.Err() != nil {
			return Result{}, newError("tool_canceled", "execute", "cancelled", "/tool", "", request.Tool.ID, result.ExitCode)
		}
		return result, nil
	}
	return result, nil
}

// RunDirect uses a direct-capable runner in production and preserves injected
// Runner compatibility for consumer and unit-test adapters.
func RunDirect(ctx context.Context, runner Runner, request Request) (Result, error) {
	if direct, ok := runner.(interface {
		RunDirect(context.Context, Request) (Result, error)
	}); ok {
		return direct.RunDirect(ctx, request)
	}
	return runner.Run(ctx, request)
}

func directEnvironment(request Request) ([]string, error) {
	values := make(map[string]string, len(request.Environment))
	for _, value := range request.Environment {
		if value.Name == "" {
			return nil, newError("tool_input_invalid", "input", "environment_invalid", "/environment", "", request.Tool.ID, 0)
		}
		if _, duplicate := values[value.Name]; duplicate {
			return nil, newError("tool_input_invalid", "input", "environment_duplicate", "/environment", "", request.Tool.ID, 0)
		}
		values[value.Name] = value.Value
	}
	if len(values) != len(request.Tool.Environment) {
		return nil, newError("tool_input_invalid", "input", "environment_incomplete", "/environment", "", request.Tool.ID, 0)
	}
	result := make([]string, 0, len(request.Tool.Environment))
	for _, rule := range request.Tool.Environment {
		value, ok := values[rule.Name]
		if !ok || rule.Source == EnvironmentScratch || rule.Source == EnvironmentFixed && value != rule.FixedValue {
			return nil, newError("tool_input_invalid", "input", "environment_invalid", "/environment", "", request.Tool.ID, 0)
		}
		result = append(result, rule.Name+"="+value)
	}
	return result, nil
}

func equalScopes(actual []string, expected string) bool {
	return len(actual) == 1 && actual[0] == expected
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

func (*ExecRunner) Run(ctx context.Context, request Request) (Result, error) {
	var scratch *entexec.Scratch
	if request.Scratch != nil {
		scratch = request.Scratch.scratch
		if scratch == nil {
			scratch = &entexec.Scratch{}
		}
	}
	rules := make([]entexec.ProcessEnvironmentRule, len(request.Tool.Environment))
	for index, rule := range request.Tool.Environment {
		rules[index] = entexec.ProcessEnvironmentRule{Name: rule.Name, Source: string(rule.Source), FixedValue: rule.FixedValue}
	}
	environment := make([]entexec.ProcessEnvironment, len(request.Environment))
	for index, value := range request.Environment {
		environment[index] = entexec.ProcessEnvironment{Name: value.Name, Value: value.Value}
	}
	value, err := entexec.RunProcess(ctx, entexec.ProcessSpec{
		RepositoryRoot: request.RepositoryRoot,
		StagingRoot:    request.StagingRoot,
		WorkDir:        request.WorkDir,
		Scratch:        scratch,
		Tool: entexec.ProcessTool{
			ID: request.Tool.ID, Version: request.Tool.Version, Executable: request.Tool.Executable,
			Args: append([]string(nil), request.Tool.Args...), InputScopes: append([]string(nil), request.Tool.InputScopes...), WriteScopes: append([]string(nil), request.Tool.WriteScopes...),
			Environment: rules,
			Probe:       entexec.ProcessProbe{Args: append([]string(nil), request.Tool.Probe.Args...), ExpectedVersion: request.Tool.Probe.ExpectedVersion},
		},
		Args:        append([]string(nil), request.Args...),
		Environment: environment,
		Stdin:       append([]byte(nil), request.Stdin...),
	})
	if err != nil {
		projected := projectEntExecError(err)
		var typed *Error
		if errors.As(projected, &typed) {
			typed.diagnostic = sanitizeDiagnostic(typed.diagnostic, request)
		}
		return Result{}, projected
	}
	return Result{
		ToolID: value.ToolID, Version: value.Version, ExecutableVersion: value.ExecutableVersion,
		ExitCode: value.ExitCode, Stdout: append([]byte(nil), value.Stdout...),
	}, nil
}

func sanitizeDiagnostic(value string, request Request) string {
	if value == "" || len(value) > MaxStderrBytes {
		return ""
	}
	value = strings.TrimSpace(strings.ToValidUTF8(value, "?"))
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return '?'
	}, value)
	bare := make([]string, 0, len(request.Environment))
	for _, item := range request.Environment {
		value = redactPublicKeyedValue(value, item.Name, item.Value)
		if len(item.Value) >= 8 {
			bare = append(bare, item.Value)
		}
	}
	sort.Slice(bare, func(left, right int) bool { return len(bare[left]) > len(bare[right]) })
	for _, secret := range bare {
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	paths := []string{request.RepositoryRoot, request.StagingRoot, request.WorkDir, request.Tool.Executable}
	sort.Slice(paths, func(left, right int) bool { return len(paths[left]) > len(paths[right]) })
	for _, candidate := range paths {
		if candidate != "" && filepath.IsAbs(candidate) {
			value = strings.ReplaceAll(value, candidate, "<redacted>")
		}
	}
	fields := strings.Fields(value)
	for index, field := range fields {
		if strings.Contains(field, "file:///") || strings.HasPrefix(field, "/") {
			fields[index] = "<path>"
		}
	}
	value = strings.Join(fields, " ")
	if len(value) > MaxDiagnosticBytes {
		value = strings.ToValidUTF8(value[:MaxDiagnosticBytes], "?")
	}
	return value
}

func redactPublicKeyedValue(value, name, secret string) string {
	patterns := []struct{ value, replacement string }{
		{name + "=\"" + secret + "\"", name + "=\"<redacted>\""},
		{name + "='" + secret + "'", name + "='<redacted>'"},
	}
	if secret != "" {
		patterns = append(patterns, struct{ value, replacement string }{name + "=" + secret, name + "=<redacted>"})
	}
	for _, pattern := range patterns {
		value = replacePublicKeyedPattern(value, pattern.value, pattern.replacement)
	}
	return value
}

func replacePublicKeyedPattern(value, pattern, replacement string) string {
	if pattern == "" {
		return value
	}
	var result strings.Builder
	for {
		index := strings.Index(value, pattern)
		if index < 0 {
			result.WriteString(value)
			return result.String()
		}
		if index > 0 && isPublicEnvironmentNameByte(value[index-1]) {
			result.WriteString(value[:index+len(pattern)])
			value = value[index+len(pattern):]
			continue
		}
		result.WriteString(value[:index])
		result.WriteString(replacement)
		value = value[index+len(pattern):]
	}
}

func isPublicEnvironmentNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}
