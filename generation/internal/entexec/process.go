package entexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const maxDiagnosticBytes = 4 << 10

type diagnosticRedactions struct {
	paths       []string
	environment []ProcessEnvironment
}

type preparedProcess struct {
	workDir, executable, toolID, version string
	toolArgs, probeArgs                  []string
	expectedVersion                      string
	environment                          []string
	stdin                                []byte
	processHook                          func(processEvent)
	release                              func()
	probed                               bool
	probeStarted                         bool
	executableVersion                    string
	redactions                           diagnosticRedactions
	direct                               bool
}

type processOutcome struct {
	stdout, stderr                 []byte
	exitCode                       int
	startErr, stdinErr, waitErr    error
	stdoutOverflow, stderrOverflow bool
	contextErr                     error
	started                        bool
}

type preparedExecution struct{ process *preparedProcess }

func RunProcess(ctx context.Context, spec ProcessSpec) (ProcessResult, error) {
	prepared, err := prepareProcess(ctx, spec)
	if err != nil {
		return ProcessResult{}, err
	}
	defer prepared.release()
	return runPreparedProcess(ctx, preparedExecution{process: &prepared})
}

func runPreparedProcess(ctx context.Context, execution preparedExecution) (ProcessResult, error) {
	return runPreparedProcessWithStdoutLimit(ctx, execution, MaxStdoutBytes)
}

func runPreparedProcessWithStdoutLimit(ctx context.Context, execution preparedExecution, stdoutLimit int) (ProcessResult, error) {
	if execution.process == nil {
		return ProcessResult{}, newProcessError("tool_input_invalid", "input", "scratch_state_invalid", "/scratch", "", 0)
	}
	prepared := execution.process
	if contextErr := ctx.Err(); contextErr != nil {
		err := contextProcessError(contextErr, prepared.toolID)
		if prepared.direct && prepared.probeStarted {
			err = markProcessStarted(err, true)
		}
		return ProcessResult{}, err
	}
	if !processTreeSupported() {
		return ProcessResult{}, newProcessError("tool_platform_unsupported", "platform", "process_tree_unsupported", "", prepared.toolID, 0)
	}

	executableVersion, err := probePreparedProcess(ctx, prepared)
	if err != nil {
		return ProcessResult{}, err
	}

	main := executeProcessWithLimits(ctx, prepared.executable, prepared.toolArgs, prepared.environment, prepared.stdin, prepared.workDir, stdoutLimit, MaxStderrBytes, prepared.processHook)
	failMain := func(err error) (ProcessResult, error) {
		if main.started || prepared.direct && prepared.probeStarted {
			err = markProcessStarted(err, prepared.direct)
		}
		return ProcessResult{}, err
	}
	if main.contextErr != nil {
		return failMain(contextProcessError(main.contextErr, prepared.toolID))
	}
	if main.startErr != nil {
		return failMain(newProcessError("tool_unavailable", "start", "process_start_failed", "/tool/executable", prepared.toolID, 0))
	}
	if main.stdoutOverflow {
		return failMain(newProcessError("tool_output_invalid", "stream", "stdout_limit_exceeded", "/stdout", prepared.toolID, 0))
	}
	if main.stderrOverflow {
		return failMain(newProcessError("tool_output_invalid", "stream", "stderr_limit_exceeded", "/stderr", prepared.toolID, 0))
	}
	if main.stdinErr != nil {
		return failMain(newProcessError("tool_failed", "stream", "stdin_write_failed", "/stdin", prepared.toolID, 0))
	}
	if main.waitErr != nil {
		return failMain(newProcessError("tool_failed", "wait", "process_wait_failed", "", prepared.toolID, 0))
	}
	if main.exitCode != 0 {
		return failMain(newProcessDiagnosticError("tool_failed", "exit", "nonzero_exit", "", prepared.toolID, main.exitCode, safeDiagnostic(main.stderr, prepared.redactions)))
	}
	return ProcessResult{
		ToolID: prepared.toolID, Version: prepared.version, ExecutableVersion: executableVersion,
		ExitCode: 0, Stdout: append([]byte(nil), main.stdout...),
	}, nil
}

func probePreparedProcess(ctx context.Context, prepared *preparedProcess) (string, error) {
	if prepared.probed {
		return prepared.executableVersion, nil
	}
	probe := executeProcess(ctx, prepared.executable, prepared.probeArgs, prepared.environment, nil, prepared.workDir, prepared.processHook)
	prepared.probeStarted = probe.started
	failProbe := func(err error) (string, error) {
		if prepared.direct && probe.started {
			err = markProcessStarted(err, true)
		}
		return "", err
	}
	if probe.contextErr != nil {
		return failProbe(contextProcessError(probe.contextErr, prepared.toolID))
	}
	if probe.startErr != nil {
		reason := "version_probe_start_failed"
		pointer := "/tool/probe"
		if errors.Is(probe.startErr, exec.ErrNotFound) || errors.Is(probe.startErr, os.ErrNotExist) {
			reason, pointer = "executable_missing", "/tool/executable"
		}
		return failProbe(newProcessError("tool_unavailable", "probe", reason, pointer, prepared.toolID, 0))
	}
	if probe.exitCode != 0 {
		return failProbe(newProcessDiagnosticError("tool_unavailable", "probe", "version_probe_nonzero", "/tool/probe", prepared.toolID, probe.exitCode, safeDiagnostic(probe.stderr, prepared.redactions)))
	}
	if probe.stdoutOverflow || probe.stderrOverflow || probe.stdinErr != nil || probe.waitErr != nil || !utf8.Valid(probe.stdout) {
		return failProbe(newProcessError("tool_unavailable", "probe", "version_probe_output_invalid", "/tool/probe", prepared.toolID, 0))
	}
	executableVersion := strings.TrimSpace(string(probe.stdout))
	if executableVersion == "" {
		return failProbe(newProcessError("tool_unavailable", "probe", "version_probe_output_invalid", "/tool/probe", prepared.toolID, 0))
	}
	if executableVersion != prepared.expectedVersion {
		return failProbe(newProcessError("tool_version_mismatch", "probe", "executable_version_mismatch", "/tool/probe/expectedVersion", prepared.toolID, 0))
	}
	prepared.probed = true
	prepared.executableVersion = executableVersion
	return executableVersion, nil
}

func safeDiagnostic(stderr []byte, redactions diagnosticRedactions) string {
	if len(stderr) > MaxStderrBytes {
		return ""
	}
	value := strings.TrimSpace(strings.ToValidUTF8(string(stderr), "?"))
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return '?'
	}, value)
	value = redactDiagnosticEnvironment(value, redactions.environment)
	value = redactDiagnosticPaths(value, redactions.paths)
	fields := strings.Fields(value)
	for index, field := range fields {
		if strings.Contains(field, "file:///") || strings.HasPrefix(field, "/") {
			fields[index] = "<path>"
		}
	}
	value = strings.Join(fields, " ")
	if len(value) > maxDiagnosticBytes {
		value = strings.ToValidUTF8(value[:maxDiagnosticBytes], "?")
	}
	return value
}

func redactDiagnosticPaths(value string, paths []string) string {
	paths = append([]string(nil), paths...)
	sort.Slice(paths, func(left, right int) bool { return len(paths[left]) > len(paths[right]) })
	for _, candidate := range paths {
		if candidate != "" {
			value = strings.ReplaceAll(value, candidate, "<redacted>")
		}
	}
	return value
}

func redactDiagnosticEnvironment(value string, environment []ProcessEnvironment) string {
	bare := make([]string, 0, len(environment))
	for _, item := range environment {
		value = redactKeyedDiagnosticValue(value, item.Name, item.Value)
		if len(item.Value) >= 8 {
			bare = append(bare, item.Value)
		}
	}
	sort.Slice(bare, func(left, right int) bool { return len(bare[left]) > len(bare[right]) })
	for _, secret := range bare {
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	return value
}

func redactKeyedDiagnosticValue(value, name, secret string) string {
	patterns := []struct{ value, replacement string }{
		{name + "=\"" + secret + "\"", name + "=\"<redacted>\""},
		{name + "='" + secret + "'", name + "='<redacted>'"},
	}
	if secret != "" {
		patterns = append(patterns, struct{ value, replacement string }{name + "=" + secret, name + "=<redacted>"})
	}
	for _, pattern := range patterns {
		value = replaceDiagnosticKeyedPattern(value, pattern.value, pattern.replacement)
	}
	return value
}

func replaceDiagnosticKeyedPattern(value, pattern, replacement string) string {
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
		if index > 0 && isEnvironmentNameByte(value[index-1]) {
			result.WriteString(value[:index+len(pattern)])
			value = value[index+len(pattern):]
			continue
		}
		result.WriteString(value[:index])
		result.WriteString(replacement)
		value = value[index+len(pattern):]
	}
}

func isEnvironmentNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func prepareProcess(ctx context.Context, spec ProcessSpec) (preparedProcess, error) {
	if ctx == nil {
		return preparedProcess{}, newProcessError("tool_input_invalid", "input", "context_invalid", "/context", "", 0)
	}
	repository, err := canonicalExistingDirectory(spec.RepositoryRoot)
	if err != nil {
		return preparedProcess{}, newProcessError("tool_input_invalid", "input", "repository_root_invalid", "/repositoryRoot", "", 0)
	}
	staging := ""
	if !spec.Direct {
		staging, err = canonicalExistingDirectory(spec.StagingRoot)
		if err != nil {
			return preparedProcess{}, newProcessError("tool_input_invalid", "input", "staging_root_invalid", "/stagingRoot", "", 0)
		}
		if pathsOverlap(repository, staging) {
			return preparedProcess{}, newProcessError("tool_input_invalid", "input", "staging_root_invalid", "/stagingRoot", "", 0)
		}
	} else if spec.StagingRoot != "" || spec.WorkDir != "" || spec.Scratch != nil {
		return preparedProcess{}, newProcessError("tool_input_invalid", "input", "direct_mode_invalid", "/direct", "", 0)
	}
	workDir := ""
	release := func() {}
	if spec.Direct {
		workDir = repository
	} else if spec.Scratch == nil {
		if spec.WorkDir != staging {
			return preparedProcess{}, newProcessError("tool_input_invalid", "input", "work_dir_invalid", "/workDir", "", 0)
		}
		workDir = staging
	} else {
		if spec.WorkDir != "" {
			return preparedProcess{}, newProcessError("tool_input_invalid", "input", "work_dir_invalid", "/workDir", "", 0)
		}
		workDir, release, err = spec.Scratch.acquireProcess(repository, staging)
		if err != nil {
			return preparedProcess{}, err
		}
	}
	fail := func(inputErr error) (preparedProcess, error) {
		release()
		return preparedProcess{}, inputErr
	}

	toolID := spec.Tool.ID
	if !validClosedToken(toolID, 128) {
		return fail(newProcessError("tool_input_invalid", "input", "tool_id_invalid", "/tool/id", "", 0))
	}
	if !validClosedValue(spec.Tool.Version, 256, false) {
		return fail(newProcessError("tool_input_invalid", "input", "tool_version_invalid", "/tool/version", toolID, 0))
	}
	if !validExecutable(spec.Tool.Executable) {
		return fail(newProcessError("tool_input_invalid", "input", "tool_executable_invalid", "/tool/executable", toolID, 0))
	}
	if !validClosedValue(spec.Tool.Probe.ExpectedVersion, 1024, false) || strings.TrimSpace(spec.Tool.Probe.ExpectedVersion) != spec.Tool.Probe.ExpectedVersion {
		return fail(newProcessError("tool_input_invalid", "input", "tool_version_invalid", "/tool/probe/expectedVersion", toolID, 0))
	}
	if pointer := invalidScopePointer(spec.Tool.InputScopes, "/tool/inputScopes"); pointer != "" {
		return fail(newProcessError("tool_input_invalid", "input", "tool_scope_invalid", pointer, toolID, 0))
	}
	if pointer := invalidScopePointer(spec.Tool.WriteScopes, "/tool/writeScopes"); pointer != "" {
		return fail(newProcessError("tool_input_invalid", "input", "tool_scope_invalid", pointer, toolID, 0))
	}

	rules := make(map[string]ProcessEnvironmentRule, len(spec.Tool.Environment))
	for index, rule := range spec.Tool.Environment {
		if !validEnvironmentName(rule.Name) || (rule.Source != EnvironmentHost && rule.Source != EnvironmentScratch && rule.Source != EnvironmentFixed) ||
			(rule.Source != EnvironmentFixed && rule.FixedValue != "") || !utf8.ValidString(rule.FixedValue) || strings.ContainsRune(rule.FixedValue, '\x00') {
			return fail(newProcessError("tool_input_invalid", "input", "environment_policy_invalid", indexedPointer("/tool/environment", index), toolID, 0))
		}
		if _, exists := rules[rule.Name]; exists {
			return fail(newProcessError("tool_input_invalid", "input", "environment_policy_invalid", indexedPointer("/tool/environment", index), toolID, 0))
		}
		if spec.Direct && rule.Source == EnvironmentScratch {
			return fail(newProcessError("tool_input_invalid", "input", "environment_policy_invalid", indexedPointer("/tool/environment", index), toolID, 0))
		}
		rules[rule.Name] = rule
	}
	for index, value := range spec.Tool.Args {
		if !validArgument(value) {
			return fail(newProcessError("tool_input_invalid", "input", "tool_args_invalid", indexedPointer("/tool/args", index), toolID, 0))
		}
	}
	if len(spec.Tool.Probe.Args) == 0 {
		return fail(newProcessError("tool_input_invalid", "input", "tool_args_invalid", "/tool/probe", toolID, 0))
	}
	for index, value := range spec.Tool.Probe.Args {
		if !validArgument(value) {
			return fail(newProcessError("tool_input_invalid", "input", "tool_args_invalid", indexedPointer("/tool/probe/args", index), toolID, 0))
		}
	}
	for index, value := range spec.Args {
		if !validArgument(value) {
			return fail(newProcessError("tool_input_invalid", "input", "request_args_invalid", indexedPointer("/args", index), toolID, 0))
		}
	}
	values := make(map[string]string, len(spec.Environment))
	for index, value := range spec.Environment {
		if !validEnvironmentName(value.Name) {
			return fail(newProcessError("tool_input_invalid", "input", "environment_value_invalid", indexedPointer("/environment", index)+"/name", toolID, 0))
		}
		if _, exists := values[value.Name]; exists {
			return fail(newProcessError("tool_input_invalid", "input", "environment_duplicate", indexedPointer("/environment", index)+"/name", toolID, 0))
		}
		rule, declared := rules[value.Name]
		if !declared {
			return fail(newProcessError("tool_input_invalid", "input", "environment_undeclared", indexedPointer("/environment", index)+"/name", toolID, 0))
		}
		if !utf8.ValidString(value.Value) || strings.ContainsRune(value.Value, '\x00') || rule.Source != EnvironmentFixed && value.Value == "" || rule.Source == EnvironmentFixed && value.Value != rule.FixedValue {
			return fail(newProcessError("tool_input_invalid", "input", "environment_value_invalid", indexedPointer("/environment", index)+"/value", toolID, 0))
		}
		if rule.Source == EnvironmentScratch && !validScratchEnvironment(value.Value, staging, workDir) {
			return fail(newProcessError("tool_input_invalid", "input", "environment_value_invalid", indexedPointer("/environment", index)+"/value", toolID, 0))
		}
		values[value.Name] = value.Value
	}
	for name := range rules {
		if _, exists := values[name]; !exists {
			return fail(newProcessError("tool_input_invalid", "input", "environment_missing", "/environment", toolID, 0))
		}
	}

	if len(spec.Stdin) > MaxStdinBytes {
		return fail(newProcessError("tool_input_invalid", "input", "stdin_limit_exceeded", "/stdin", toolID, 0))
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
	toolArgs := append([]string(nil), spec.Tool.Args...)
	toolArgs = append(toolArgs, spec.Args...)
	redactionPaths := []string{repository, staging, workDir, spec.Tool.Executable}
	for _, argument := range append(append([]string(nil), toolArgs...), spec.Tool.Probe.Args...) {
		if filepath.IsAbs(argument) {
			redactionPaths = append(redactionPaths, argument)
		}
	}
	redactions := diagnosticRedactions{paths: redactionPaths, environment: make([]ProcessEnvironment, 0, len(names))}
	for _, name := range names {
		redactions.environment = append(redactions.environment, ProcessEnvironment{Name: name, Value: values[name]})
	}
	return preparedProcess{
		workDir: workDir, executable: spec.Tool.Executable, toolID: toolID, version: spec.Tool.Version,
		toolArgs: toolArgs, probeArgs: append([]string(nil), spec.Tool.Probe.Args...), expectedVersion: spec.Tool.Probe.ExpectedVersion,
		environment: environment, stdin: append([]byte(nil), spec.Stdin...), processHook: spec.processHook, release: release, redactions: redactions, direct: spec.Direct,
	}, nil
}

func executeProcess(ctx context.Context, executable string, args, environment []string, stdin []byte, workDir string, hook func(processEvent)) processOutcome {
	return executeProcessWithLimits(ctx, executable, args, environment, stdin, workDir, MaxStdoutBytes, MaxStderrBytes, hook)
}

func executeProcessWithLimits(ctx context.Context, executable string, args, environment []string, stdin []byte, workDir string, stdoutLimit, stderrLimit int, hook func(processEvent)) processOutcome {
	if hook != nil {
		hook(processEvent{Name: "start", Args: append([]string(nil), args...)})
	}
	command := exec.Command(executable, append([]string(nil), args...)...)
	command.Dir = workDir
	command.Env = append([]string(nil), environment...)
	if !configureProcessTree(command) {
		return processOutcome{startErr: errors.New("process tree unsupported")}
	}
	stdout := newBoundedProcessOutput(stdoutLimit, "stdout-overflow", hook)
	stderr := newBoundedProcessOutput(stderrLimit, "stderr-overflow", hook)
	stdinPipe, err := command.StdinPipe()
	if err != nil {
		return processOutcome{startErr: err}
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return processOutcome{startErr: err}
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return processOutcome{startErr: err}
	}
	if err := command.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return processOutcome{startErr: err, contextErr: ctx.Err()}
	}
	started := true
	stdinCloser := &singleCloseWriteCloser{WriteCloser: stdinPipe}
	stdinDone := make(chan error, 1)
	go func() {
		_, writeErr := io.Copy(stdinCloser, bytes.NewReader(stdin))
		closeErr := stdinCloser.Close()
		stdinDone <- errors.Join(writeErr, closeErr)
	}()
	stdoutDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stdout, stdoutPipe)
		closeErr := stdoutPipe.Close()
		stdoutDone <- errors.Join(copyErr, closeErr)
	}()
	stderrDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(stderr, stderrPipe)
		closeErr := stderrPipe.Close()
		stderrDone <- errors.Join(copyErr, closeErr)
	}()
	type processWaitResult struct {
		state *os.ProcessState
		err   error
	}
	waitDone := make(chan processWaitResult, 1)
	go func() {
		state, waitErr := command.Process.Wait()
		waitDone <- processWaitResult{state: state, err: waitErr}
	}()

	outcome := processOutcome{started: started}
	contextDone := ctx.Done()
	stdoutOverflow := stdout.overflowSignal
	stderrOverflow := stderr.overflowSignal
	var waitState *os.ProcessState
	terminated := false
	terminate := func() {
		if !terminated {
			terminated = true
			_ = killProcessTree(command)
		}
		_ = stdinCloser.Close()
	}
	for waitDone != nil || stdinDone != nil || stdoutDone != nil || stderrDone != nil {
		if contextDone != nil {
			select {
			case <-contextDone:
				outcome.contextErr = ctx.Err()
				contextDone = nil
				terminate()
			default:
			}
		}
		select {
		case result := <-waitDone:
			waitDone = nil
			waitState = result.state
			if result.err != nil {
				outcome.waitErr = result.err
			}
			// The runner owns every descendant in the process group, even after
			// the direct child has exited successfully.
			terminate()
		case err := <-stdinDone:
			stdinDone = nil
			if err != nil {
				outcome.stdinErr = err
				terminate()
			}
		case err := <-stdoutDone:
			stdoutDone = nil
			if stdout.Overflowed() {
				terminate()
			}
			stdoutOverflow = nil
			if err != nil && outcome.waitErr == nil {
				outcome.waitErr = err
				terminate()
			}
		case err := <-stderrDone:
			stderrDone = nil
			if stderr.Overflowed() {
				terminate()
			}
			stderrOverflow = nil
			if err != nil && outcome.waitErr == nil {
				outcome.waitErr = err
				terminate()
			}
		case <-stdoutOverflow:
			stdoutOverflow = nil
			terminate()
		case <-stderrOverflow:
			stderrOverflow = nil
			terminate()
		case <-contextDone:
			outcome.contextErr = ctx.Err()
			contextDone = nil
			terminate()
		}
	}
	if waitState != nil {
		outcome.exitCode = waitState.ExitCode()
	}
	outcome.stdout = stdout.Bytes()
	outcome.stderr = stderr.Bytes()
	outcome.stdoutOverflow = stdout.Overflowed()
	outcome.stderrOverflow = stderr.Overflowed()
	return outcome
}

type singleCloseWriteCloser struct {
	io.WriteCloser
	once     sync.Once
	closeErr error
}

func (closer *singleCloseWriteCloser) Close() error {
	first := false
	closer.once.Do(func() {
		first = true
		closer.closeErr = closer.WriteCloser.Close()
	})
	if first {
		return closer.closeErr
	}
	return nil
}

type boundedProcessOutput struct {
	mu             sync.Mutex
	buffer         bytes.Buffer
	limit          int
	overflow       bool
	overflowSignal chan struct{}
	overflowOnce   sync.Once
	overflowEvent  string
	hook           func(processEvent)
}

func newBoundedProcessOutput(limit int, event string, hook func(processEvent)) *boundedProcessOutput {
	return &boundedProcessOutput{limit: limit, overflowSignal: make(chan struct{}), overflowEvent: event, hook: hook}
}

func (w *boundedProcessOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		kept := len(data)
		if kept > remaining {
			kept = remaining
		}
		_, _ = w.buffer.Write(data[:kept])
	}
	if len(data) > remaining {
		w.overflow = true
		w.overflowOnce.Do(func() {
			if w.hook != nil {
				w.hook(processEvent{Name: w.overflowEvent})
			}
			close(w.overflowSignal)
		})
	}
	return len(data), nil
}

func (w *boundedProcessOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

func (w *boundedProcessOutput) Overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflow
}

func contextProcessError(err error, toolID string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return newProcessError("tool_deadline_exceeded", "wait", "context_deadline_exceeded", "/context", toolID, 0)
	}
	return newProcessError("tool_canceled", "wait", "context_canceled", "/context", toolID, 0)
}

func validClosedToken(value string, max int) bool {
	if !validClosedValue(value, max, false) {
		return false
	}
	for index, character := range []byte(value) {
		if index == 0 && !isASCIIAlphaNumeric(character) {
			return false
		}
		if !isASCIIAlphaNumeric(character) && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validClosedValue(value string, max int, allowEmpty bool) bool {
	return (allowEmpty || value != "") && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validArgument(value string) bool { return validClosedValue(value, 1<<20, true) }

func validExecutable(value string) bool {
	return validClosedValue(value, 4096, false) && filepath.IsAbs(value) && filepath.Clean(value) == value
}

func invalidScopePointer(values []string, prefix string) string {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !validClosedValue(value, 4096, false) {
			return indexedPointer(prefix, index)
		}
		if _, duplicate := seen[value]; duplicate {
			return indexedPointer(prefix, index)
		}
		seen[value] = struct{}{}
	}
	return ""
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range []byte(value) {
		if index == 0 && !(isASCIIAlpha(character) || character == '_') {
			return false
		}
		if !isASCIIAlphaNumeric(character) && character != '_' {
			return false
		}
	}
	return true
}

func validScratchEnvironment(value, staging, workDir string) bool {
	canonical, err := canonicalExistingDirectory(value)
	if err != nil || canonical != value {
		return false
	}
	return pathContainedBy(canonical, staging) || pathContainedBy(canonical, workDir)
}

func environmentContains(environment []string, name string) bool {
	prefix := name + "="
	for _, value := range environment {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func indexedPointer(prefix string, index int) string {
	return prefix + "/" + strconv.Itoa(index)
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isASCIIAlphaNumeric(value byte) bool {
	return isASCIIAlpha(value) || value >= '0' && value <= '9'
}
