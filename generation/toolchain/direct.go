package toolchain

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/generation/directwrite"
)

// ExecDirectRunner executes a tool directly in a canonical consumer root.
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
	prepared, err := prepareDirectRequest(ctx, request)
	if err != nil {
		return Result{}, err
	}
	version, err := runDirectProbe(ctx, prepared)
	if err != nil {
		return Result{}, err
	}
	stdout, stderr, exitCode, err := runDirectCommand(ctx, prepared.executable, prepared.args, prepared.environment, prepared.stdin, prepared.root)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, newError("tool_canceled", "wait", "context_canceled", "/context", "", prepared.toolID, 0)
		}
		return Result{}, newError("tool_failed", "wait", "process_wait_failed", "", "", prepared.toolID, exitCode)
	}
	if exitCode != 0 {
		return Result{}, newDiagnosticError("tool_failed", "exit", "nonzero_exit", "", "", prepared.toolID, exitCode, sanitizeDirectDiagnostic(stderr, request))
	}
	return Result{ToolID: prepared.toolID, Version: prepared.version, ExecutableVersion: version, Stdout: stdout}, nil
}

type preparedDirect struct {
	root, executable, toolID, version, expectedVersion string
	args, probeArgs, environment                       []string
	stdin                                              []byte
}

func prepareDirectRequest(ctx context.Context, request DirectRequest) (preparedDirect, error) {
	if ctx == nil {
		return preparedDirect{}, newError("tool_input_invalid", "input", "context_invalid", "/context", "", "", 0)
	}
	if err := ctx.Err(); err != nil {
		return preparedDirect{}, newError("tool_canceled", "wait", "context_canceled", "/context", "", request.Tool.ID, 0)
	}
	root, err := CanonicalRepositoryRoot(request.RepositoryRoot)
	if err != nil || root != request.RepositoryRoot {
		return preparedDirect{}, newError("tool_input_invalid", "input", "repository_root_invalid", "/repositoryRoot", "", request.Tool.ID, 0)
	}
	tool := request.Tool
	if !validDirectToken(tool.ID, 128) || !validDirectValue(tool.Version, 256, false) || !filepath.IsAbs(tool.Executable) || filepath.Clean(tool.Executable) != tool.Executable || !validDirectValue(tool.Probe.ExpectedVersion, 1024, false) || len(tool.Probe.Args) == 0 {
		return preparedDirect{}, newError("tool_input_invalid", "input", "tool_invalid", "/tool", "", tool.ID, 0)
	}
	for _, values := range [][]string{tool.Args, tool.Probe.Args, request.Args} {
		for _, value := range values {
			if !validDirectValue(value, 1<<20, true) {
				return preparedDirect{}, newError("tool_input_invalid", "input", "tool_args_invalid", "/args", "", tool.ID, 0)
			}
		}
	}
	for _, scopes := range [][]string{tool.InputScopes, tool.WriteScopes} {
		seen := make(map[string]struct{}, len(scopes))
		for _, scope := range scopes {
			if ValidateRepositoryPath(scope) != nil {
				return preparedDirect{}, newError("tool_input_invalid", "input", "tool_scope_invalid", "/tool", "", tool.ID, 0)
			}
			if _, duplicate := seen[scope]; duplicate {
				return preparedDirect{}, newError("tool_input_invalid", "input", "tool_scope_invalid", "/tool", "", tool.ID, 0)
			}
			seen[scope] = struct{}{}
		}
	}
	if len(request.Stdin) > MaxStdinBytes || !utf8.Valid(request.Stdin) {
		return preparedDirect{}, newError("tool_input_invalid", "input", "stdin_invalid", "/stdin", "", tool.ID, 0)
	}
	rules := make(map[string]EnvironmentRule, len(tool.Environment))
	for _, rule := range tool.Environment {
		if !validDirectEnvironmentName(rule.Name) || rule.Source == EnvironmentScratch || rule.Source != EnvironmentHost && rule.Source != EnvironmentFixed || rule.Source != EnvironmentFixed && rule.FixedValue != "" || !validDirectValue(rule.FixedValue, 1<<20, true) {
			return preparedDirect{}, newError("tool_input_invalid", "input", "environment_policy_invalid", "/tool/environment", "", tool.ID, 0)
		}
		if _, duplicate := rules[rule.Name]; duplicate {
			return preparedDirect{}, newError("tool_input_invalid", "input", "environment_policy_invalid", "/tool/environment", "", tool.ID, 0)
		}
		rules[rule.Name] = rule
	}
	values := make(map[string]string, len(request.Environment))
	for _, item := range request.Environment {
		rule, ok := rules[item.Name]
		if !ok || !validDirectEnvironmentName(item.Name) || !validDirectValue(item.Value, 1<<20, false) || rule.Source == EnvironmentFixed && item.Value != rule.FixedValue {
			return preparedDirect{}, newError("tool_input_invalid", "input", "environment_value_invalid", "/environment", "", tool.ID, 0)
		}
		if _, duplicate := values[item.Name]; duplicate {
			return preparedDirect{}, newError("tool_input_invalid", "input", "environment_duplicate", "/environment", "", tool.ID, 0)
		}
		values[item.Name] = item.Value
	}
	if len(values) != len(rules) {
		return preparedDirect{}, newError("tool_input_invalid", "input", "environment_missing", "/environment", "", tool.ID, 0)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]string, 0, len(names))
	for _, name := range names {
		environment = append(environment, name+"="+values[name])
	}
	args := append(append([]string(nil), tool.Args...), request.Args...)
	return preparedDirect{root: root, executable: tool.Executable, toolID: tool.ID, version: tool.Version, expectedVersion: tool.Probe.ExpectedVersion, args: args, probeArgs: append([]string(nil), tool.Probe.Args...), environment: environment, stdin: append([]byte(nil), request.Stdin...)}, nil
}

func runDirectProbe(ctx context.Context, request preparedDirect) (string, error) {
	stdout, _, exitCode, err := runDirectCommand(ctx, request.executable, request.probeArgs, request.environment, nil, request.root)
	if err != nil || exitCode != 0 {
		return "", newError("tool_unavailable", "probe", "version_probe_failed", "/tool/probe", "", request.toolID, exitCode)
	}
	version := strings.TrimSpace(string(stdout))
	if version != request.expectedVersion {
		return "", newError("tool_version_mismatch", "probe", "executable_version_mismatch", "/tool/probe/expectedVersion", "", request.toolID, 0)
	}
	return version, nil
}

func runDirectCommand(ctx context.Context, executable string, args, environment []string, stdin []byte, root string) ([]byte, []byte, int, error) {
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir, command.Env, command.Stdin = root, append([]string(nil), environment...), bytes.NewReader(stdin)
	stdout, stderr := &boundedBuffer{limit: MaxStdoutBytes}, &boundedBuffer{limit: MaxStderrBytes}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	if stdout.overflow || stderr.overflow {
		return nil, nil, 0, errors.New("tool output limit exceeded")
	}
	exitCode := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exitCode = exit.ExitCode()
		} else {
			return nil, nil, 0, err
		}
	}
	return stdout.Bytes(), stderr.Bytes(), exitCode, nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	want := len(value)
	if b.Buffer.Len()+want > b.limit {
		b.overflow = true
		value = value[:max(0, b.limit-b.Buffer.Len())]
	}
	_, _ = b.Buffer.Write(value)
	return want, nil
}

func sanitizeDirectDiagnostic(stderr []byte, request DirectRequest) string {
	value := strings.TrimSpace(strings.ToValidUTF8(string(stderr), "?"))
	for _, item := range request.Environment {
		if len(item.Value) >= 8 {
			value = strings.ReplaceAll(value, item.Value, "<redacted>")
		}
	}
	value = strings.ReplaceAll(value, request.RepositoryRoot, "<redacted>")
	if len(value) > MaxDiagnosticBytes {
		value = value[:MaxDiagnosticBytes]
	}
	return value
}

func validDirectValue(value string, limit int, empty bool) bool {
	return (empty || value != "") && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func validDirectToken(value string, limit int) bool {
	if !validDirectValue(value, limit, false) {
		return false
	}
	for index, r := range []byte(value) {
		if index == 0 && !directAlphaNum(r) || !directAlphaNum(r) && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
func directAlphaNum(r byte) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
func validDirectEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, r := range []byte(value) {
		if index == 0 && !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_') || index > 0 && !(directAlphaNum(r) || r == '_') {
			return false
		}
	}
	return true
}
