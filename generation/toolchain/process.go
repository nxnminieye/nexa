package toolchain

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
)

type ExecRunner struct{}

func NewExecRunner() *ExecRunner { return &ExecRunner{} }

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
