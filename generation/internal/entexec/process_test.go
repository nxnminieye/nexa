package entexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestRunProcessExecutesProbeThenExactMainWithClosedEnvironment(t *testing.T) {
	spec := validProcessSpec(t)
	t.Setenv("NEXA_AMBIENT_SECRET", "must-not-leak")
	spec.Args = []string{"report", "caller-a", "caller-b"}
	spec.Stdin = []byte("bounded input")

	result, err := RunProcess(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var report processHelperReport
	if err := json.Unmarshal(result.Stdout, &report); err != nil {
		t.Fatal(err)
	}
	if result.ToolID != "ent-go" || result.Version != "policy-v1" || result.ExecutableVersion != "go-test-version" || result.ExitCode != 0 {
		t.Fatalf("result identity = %#v", result)
	}
	if got, want := report.Args, []string{"caller-a", "caller-b"}; !equalStrings(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
	if report.Home != filepath.Join(spec.StagingRoot, "home") || report.Ambient != "" || report.Stdin != "bounded input" || report.Cwd != spec.StagingRoot {
		t.Fatalf("closed process report = %#v", report)
	}
	result.Stdout[0] = 'X'
	second, err := RunProcess(context.Background(), spec)
	if err != nil || bytes.Equal(second.Stdout, result.Stdout) {
		t.Fatalf("RunProcess() did not return fresh output: %v", err)
	}
}

func TestRunProcessProbeFailurePreventsMainAndPreservesExit(t *testing.T) {
	spec := validProcessSpec(t)
	marker := filepath.Join(spec.StagingRoot, "main-ran")
	spec.Tool.Probe.Args = helperArgs("exit", "23")
	spec.Args = []string{"mark", marker}

	result, err := RunProcess(context.Background(), spec)
	assertProcessError(t, err, "tool_unavailable", "probe", "version_probe_nonzero", "/tool/probe", "ent-go", 23)
	if result.ToolID != "" || result.Version != "" || result.ExecutableVersion != "" || result.ExitCode != 0 || result.Stdout != nil {
		t.Fatalf("result = %#v, want zero", result)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("main process ran after failed probe: %v", statErr)
	}
}

func TestRunProcessRejectsProbeMismatchAndPollutedNonzeroOutput(t *testing.T) {
	t.Run("probe mismatch", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Tool.Probe.ExpectedVersion = "other-version"
		_, err := RunProcess(context.Background(), spec)
		assertProcessError(t, err, "tool_version_mismatch", "probe", "executable_version_mismatch", "/tool/probe/expectedVersion", "ent-go", 0)
	})

	t.Run("main nonzero", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Args = []string{"exit", "17"}
		result, err := RunProcess(context.Background(), spec)
		assertProcessError(t, err, "tool_failed", "exit", "nonzero_exit", "", "ent-go", 17)
		var typed *Error
		if !errors.As(err, &typed) || typed.Diagnostic() != "private-stderr" || !strings.Contains(err.Error(), "private-stderr") {
			t.Fatalf("nonzero diagnostic = %#v", err)
		}
		if len(result.Stdout) != 0 {
			t.Fatalf("untrusted stdout returned on nonzero exit: %q", result.Stdout)
		}
	})

	t.Run("diagnostic redaction", func(t *testing.T) {
		spec := validProcessSpec(t)
		secret := "diagnostic-secret-value"
		spec.Tool.Environment = append(spec.Tool.Environment, ProcessEnvironmentRule{Name: "NEXA_DIAGNOSTIC_SECRET", Source: EnvironmentFixed, FixedValue: secret})
		spec.Environment = append(spec.Environment, ProcessEnvironment{Name: "NEXA_DIAGNOSTIC_SECRET", Value: secret})
		spec.Args = []string{"exit-details", "17", spec.RepositoryRoot, spec.StagingRoot, spec.Tool.Executable, secret}
		_, err := RunProcess(context.Background(), spec)
		var typed *Error
		if !errors.As(err, &typed) || typed.ExitCode() != 17 || typed.Diagnostic() == "" || len(typed.Diagnostic()) > 4096 {
			t.Fatalf("diagnostic error = %#v", err)
		}
		for _, closed := range []string{spec.RepositoryRoot, spec.StagingRoot, spec.Tool.Executable, secret} {
			if strings.Contains(typed.Diagnostic(), closed) {
				t.Fatalf("diagnostic leaked %q: %q", closed, typed.Diagnostic())
			}
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error string leaked secret: %q", err)
		}
	})
}

func TestRunProcessFastVersionProbeAlwaysReportsMismatch(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })
	spec := validProcessSpec(t)
	useFastRaceHelperExit(&spec)
	spec.Tool.Probe.ExpectedVersion = "other-version"
	_, err := RunProcess(context.Background(), spec)
	assertProcessError(t, err, "tool_version_mismatch", "probe", "executable_version_mismatch", "/tool/probe/expectedVersion", "ent-go", 0)
}

func TestSafeDiagnosticRedactsBeforeFinalBoundWithoutReplacingShortBareValues(t *testing.T) {
	pathValue := "/bin/go"
	longSecret := strings.Repeat("secret", 16)
	shortSecret := "x"
	raw := "bare=x " + strings.Repeat("p", 3980) + " repo=" + pathValue +
		" exec=" + pathValue + " SHORT=\"" + shortSecret + "\" SHORT='" + shortSecret + "' LONG=" + longSecret
	diagnostic := safeDiagnostic([]byte(raw), diagnosticRedactions{
		paths: []string{pathValue},
		environment: []ProcessEnvironment{
			{Name: "SHORT", Value: shortSecret},
			{Name: "LONG", Value: longSecret},
		},
	})
	if len(diagnostic) > 4096 {
		t.Fatalf("diagnostic length = %d", len(diagnostic))
	}
	for _, leaked := range []string{pathValue, longSecret, "SHORT=\"x\"", "SHORT='x'"} {
		if strings.Contains(diagnostic, leaked) {
			t.Fatalf("diagnostic leaked %q: %q", leaked, diagnostic)
		}
	}
	if !strings.Contains(diagnostic, "bare=x") {
		t.Fatalf("short bare value was globally replaced: %q", diagnostic)
	}
}

func TestRunProcessEnforcesInputAndOutputBounds(t *testing.T) {
	t.Run("stdin", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Stdin = make([]byte, MaxStdinBytes+1)
		_, err := RunProcess(context.Background(), spec)
		assertProcessError(t, err, "tool_input_invalid", "input", "stdin_limit_exceeded", "/stdin", "ent-go", 0)
	})

	t.Run("stdout", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Args = []string{"stdout", strconv.Itoa(MaxStdoutBytes + 1)}
		_, err := RunProcess(context.Background(), spec)
		assertProcessError(t, err, "tool_output_invalid", "stream", "stdout_limit_exceeded", "/stdout", "ent-go", 0)
	})

	t.Run("stderr", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Args = []string{"stderr", strconv.Itoa(MaxStderrBytes + 1)}
		_, err := RunProcess(context.Background(), spec)
		assertProcessError(t, err, "tool_output_invalid", "stream", "stderr_limit_exceeded", "/stderr", "ent-go", 0)
		if strings.Contains(err.Error(), "private-stderr") || strings.Contains(fmt.Sprint(errors.Unwrap(err)), "private-stderr") {
			t.Fatal("stderr leaked through public error projection")
		}
	})
}

func TestPreparedProcessSupportsPerMainStdoutLimit(t *testing.T) {
	t.Run("between public and package limits", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Args = []string{"stdout", strconv.Itoa(MaxStdoutBytes + 1)}
		prepared, err := prepareProcess(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.release()
		result, err := runPreparedProcessWithStdoutLimit(context.Background(), preparedExecution{process: &prepared}, maxPackageListBytes)
		if err != nil || len(result.Stdout) != MaxStdoutBytes+1 {
			t.Fatalf("result bytes = %d, err = %v", len(result.Stdout), err)
		}
	})
	t.Run("above package limit", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Args = []string{"stdout", strconv.Itoa(maxPackageListBytes + 1)}
		prepared, err := prepareProcess(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.release()
		_, err = runPreparedProcessWithStdoutLimit(context.Background(), preparedExecution{process: &prepared}, maxPackageListBytes)
		assertProcessError(t, projectDiscoveryProcessError(err, "package"), "build_input_discovery_failed", "retain", "package_list_output_limit_exceeded", "/retainedInputs", "ent-go", 0)
	})
}

func TestDiscoveryClassifierPreservesProbeFailure(t *testing.T) {
	probe := newProcessError("tool_unavailable", "probe", "version_probe_nonzero", "/tool/probe", "go", 23)
	if got := projectDiscoveryProcessError(probe, "module"); got != probe {
		t.Fatalf("probe error was reclassified: %#v", got)
	}
	exit := newProcessDiagnosticError("tool_failed", "exit", "nonzero_exit", "", "go", 17, "schema package failed")
	projected := projectDiscoveryProcessError(exit, "package")
	assertProcessError(t, projected, "build_input_discovery_failed", "retain", "package_list_nonzero", "/retainedInputs", "go", 17)
	var typed *Error
	if !errors.As(projected, &typed) || typed.Diagnostic() != "schema package failed" {
		t.Fatalf("projected diagnostic = %#v", projected)
	}
}

func TestRunProcessRetainsOverflowWhenStreamClosesBeforeBlockedChildExits(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process-group behavior is supported on Darwin and Linux")
	}
	tests := []struct {
		name, event, code, reason, pointer string
	}{
		{name: "stdout", event: "stdout-overflow", code: "tool_output_invalid", reason: "stdout_limit_exceeded", pointer: "/stdout"},
		{name: "stderr", event: "stderr-overflow", code: "tool_output_invalid", reason: "stderr_limit_exceeded", pointer: "/stderr"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for iteration := 0; iteration < 10; iteration++ {
				spec := validProcessSpec(t)
				useFastRaceHelperExit(&spec)
				groupFile := filepath.Join(spec.StagingRoot, "overflow-group.pid")
				releaseFile := filepath.Join(spec.StagingRoot, "close-stream")
				closedFile := filepath.Join(spec.StagingRoot, "stream-closed")
				spec.Args = []string{"overflow-close-block", test.name, groupFile, releaseFile, closedFile}
				hookResult := make(chan error, 1)
				previousProcs := 0
				var procsOnce sync.Once
				spec.processHook = func(event processEvent) {
					if event.Name != test.event {
						return
					}
					procsOnce.Do(func() { previousProcs = runtime.GOMAXPROCS(1) })
					if err := os.WriteFile(releaseFile, []byte("close"), 0o600); err != nil {
						hookResult <- err
						return
					}
					hookResult <- waitForPath(closedFile, time.Second)
				}
				done := make(chan error, 1)
				go func() {
					_, err := RunProcess(context.Background(), spec)
					done <- err
				}()
				select {
				case err := <-done:
					select {
					case hookErr := <-hookResult:
						if hookErr != nil {
							t.Fatal(hookErr)
						}
					case <-time.After(time.Second):
						t.Fatal("overflow hook did not observe the closed stream")
					}
					if previousProcs != 0 {
						runtime.GOMAXPROCS(previousProcs)
					}
					assertProcessError(t, err, test.code, "stream", test.reason, test.pointer, "ent-go", 0)
					waitForFile(t, groupFile)
					waitForProcessExit(t, readPIDFile(t, groupFile))
				case <-time.After(500 * time.Millisecond):
					waitForFile(t, groupFile)
					groupPID := readPIDFile(t, groupFile)
					killTestProcessGroup(groupPID)
					runErr := <-done
					<-hookResult
					if previousProcs != 0 {
						runtime.GOMAXPROCS(previousProcs)
					}
					waitForProcessExit(t, groupPID)
					t.Fatalf("iteration %d lost %s after stream close; eventual error = %v", iteration, test.event, runErr)
				}
			}
		})
	}
}

func TestRunProcessReducesCompoundRuntimeSignalsInFrozenOrder(t *testing.T) {
	t.Run("cancel before stdout overflow", func(t *testing.T) {
		for iteration := 0; iteration < 10; iteration++ {
			spec := validProcessSpec(t)
			spec.Args = []string{"stdout", strconv.Itoa(MaxStdoutBytes + 1)}
			ctx, cancel := context.WithCancel(context.Background())
			spec.processHook = func(event processEvent) {
				if event.Name == "stdout-overflow" {
					cancel()
				}
			}
			_, err := RunProcess(ctx, spec)
			assertProcessError(t, err, "tool_canceled", "wait", "context_canceled", "/context", "ent-go", 0)
		}
	})

	t.Run("deadline before stdout overflow", func(t *testing.T) {
		for iteration := 0; iteration < 10; iteration++ {
			spec := validProcessSpec(t)
			spec.Args = []string{"stdout", strconv.Itoa(MaxStdoutBytes + 1)}
			ctx := newControlledDeadlineContext()
			spec.processHook = func(event processEvent) {
				if event.Name == "stdout-overflow" {
					ctx.expire()
				}
			}
			_, err := RunProcess(ctx, spec)
			assertProcessError(t, err, "tool_deadline_exceeded", "wait", "context_deadline_exceeded", "/context", "ent-go", 0)
		}
	})

	t.Run("stdout before stderr overflow", func(t *testing.T) {
		for iteration := 0; iteration < 5; iteration++ {
			spec := validProcessSpec(t)
			spec.Args = []string{"both-overflow"}
			var mu sync.Mutex
			observed := make(map[string]bool)
			both := make(chan struct{})
			var closeOnce sync.Once
			spec.processHook = func(event processEvent) {
				if event.Name != "stdout-overflow" && event.Name != "stderr-overflow" {
					return
				}
				mu.Lock()
				observed[event.Name] = true
				if len(observed) == 2 {
					closeOnce.Do(func() { close(both) })
				}
				mu.Unlock()
				<-both
			}
			_, err := RunProcess(context.Background(), spec)
			assertProcessError(t, err, "tool_output_invalid", "stream", "stdout_limit_exceeded", "/stdout", "ent-go", 0)
		}
	})
}

func TestRunProcessValidatesClosedInputsBeforeChildStart(t *testing.T) {
	tests := []struct {
		name                        string
		mutate                      func(*ProcessSpec)
		code, reason, pointer, tool string
	}{
		{name: "repository", mutate: func(spec *ProcessSpec) { spec.RepositoryRoot = "" }, code: "tool_input_invalid", reason: "repository_root_invalid", pointer: "/repositoryRoot"},
		{name: "staging", mutate: func(spec *ProcessSpec) { spec.StagingRoot = "" }, code: "tool_input_invalid", reason: "staging_root_invalid", pointer: "/stagingRoot"},
		{name: "overlapping staging", mutate: func(spec *ProcessSpec) {
			spec.StagingRoot = filepath.Join(spec.RepositoryRoot, "staging")
			mustMkdir(t, spec.StagingRoot)
			spec.WorkDir = spec.StagingRoot
		}, code: "tool_input_invalid", reason: "staging_root_invalid", pointer: "/stagingRoot"},
		{name: "work dir", mutate: func(spec *ProcessSpec) { spec.WorkDir = spec.RepositoryRoot }, code: "tool_input_invalid", reason: "work_dir_invalid", pointer: "/workDir"},
		{name: "forged scratch", mutate: func(spec *ProcessSpec) { spec.Scratch = &Scratch{}; spec.WorkDir = "" }, code: "tool_input_invalid", reason: "scratch_state_invalid", pointer: "/scratch"},
		{name: "tool id", mutate: func(spec *ProcessSpec) { spec.Tool.ID = "bad/id" }, code: "tool_input_invalid", reason: "tool_id_invalid", pointer: "/tool/id"},
		{name: "tool version", mutate: func(spec *ProcessSpec) { spec.Tool.Version = "" }, code: "tool_input_invalid", reason: "tool_version_invalid", pointer: "/tool/version", tool: "ent-go"},
		{name: "executable", mutate: func(spec *ProcessSpec) { spec.Tool.Executable = "relative" }, code: "tool_input_invalid", reason: "tool_executable_invalid", pointer: "/tool/executable", tool: "ent-go"},
		{name: "tool args", mutate: func(spec *ProcessSpec) { spec.Tool.Args[0] = "bad\x00arg" }, code: "tool_input_invalid", reason: "tool_args_invalid", pointer: "/tool/args/0", tool: "ent-go"},
		{name: "input scope", mutate: func(spec *ProcessSpec) { spec.Tool.InputScopes = []string{"bad\x00scope"} }, code: "tool_input_invalid", reason: "tool_scope_invalid", pointer: "/tool/inputScopes/0", tool: "ent-go"},
		{name: "write scope", mutate: func(spec *ProcessSpec) { spec.Tool.WriteScopes = []string{"same", "same"} }, code: "tool_input_invalid", reason: "tool_scope_invalid", pointer: "/tool/writeScopes/1", tool: "ent-go"},
		{name: "probe expected version", mutate: func(spec *ProcessSpec) { spec.Tool.Probe.ExpectedVersion = "" }, code: "tool_input_invalid", reason: "tool_version_invalid", pointer: "/tool/probe/expectedVersion", tool: "ent-go"},
		{name: "environment policy", mutate: func(spec *ProcessSpec) { spec.Tool.Environment[0].Source = "caller" }, code: "tool_input_invalid", reason: "environment_policy_invalid", pointer: "/tool/environment/0", tool: "ent-go"},
		{name: "environment missing", mutate: func(spec *ProcessSpec) { spec.Environment = spec.Environment[1:] }, code: "tool_input_invalid", reason: "environment_missing", pointer: "/environment", tool: "ent-go"},
		{name: "environment duplicate", mutate: func(spec *ProcessSpec) { spec.Environment = append(spec.Environment, spec.Environment[0]) }, code: "tool_input_invalid", reason: "environment_duplicate", pointer: "/environment/2/name", tool: "ent-go"},
		{name: "environment undeclared", mutate: func(spec *ProcessSpec) {
			spec.Environment = append(spec.Environment, ProcessEnvironment{Name: "EXTRA", Value: "x"})
		}, code: "tool_input_invalid", reason: "environment_undeclared", pointer: "/environment/2/name", tool: "ent-go"},
		{name: "fixed override", mutate: func(spec *ProcessSpec) { spec.Environment[1].Value = "0" }, code: "tool_input_invalid", reason: "environment_value_invalid", pointer: "/environment/1/value", tool: "ent-go"},
		{name: "scratch environment escape", mutate: func(spec *ProcessSpec) { spec.Environment[0].Value = spec.RepositoryRoot }, code: "tool_input_invalid", reason: "environment_value_invalid", pointer: "/environment/0/value", tool: "ent-go"},
		{name: "request args", mutate: func(spec *ProcessSpec) { spec.Args = []string{"bad\x00arg"} }, code: "tool_input_invalid", reason: "request_args_invalid", pointer: "/args/0", tool: "ent-go"},
		{name: "stdin", mutate: func(spec *ProcessSpec) { spec.Stdin = make([]byte, MaxStdinBytes+1) }, code: "tool_input_invalid", reason: "stdin_limit_exceeded", pointer: "/stdin", tool: "ent-go"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validProcessSpec(t)
			test.mutate(&spec)
			_, err := RunProcess(context.Background(), spec)
			assertProcessError(t, err, test.code, "input", test.reason, test.pointer, test.tool, 0)
		})
	}

	spec := validProcessSpec(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunProcess(ctx, spec)
	assertProcessError(t, err, "tool_canceled", "wait", "context_canceled", "/context", "ent-go", 0)
}

func TestRunProcessUsesFrozenCompoundInputPrecedence(t *testing.T) {
	tests := []struct {
		name                    string
		mutate                  func(*ProcessSpec)
		reason, pointer, toolID string
	}{
		{
			name: "scope before tool argv",
			mutate: func(spec *ProcessSpec) {
				spec.Tool.InputScopes = []string{"bad\x00scope"}
				spec.Tool.Args[0] = "bad\x00arg"
			},
			reason: "tool_scope_invalid", pointer: "/tool/inputScopes/0", toolID: "ent-go",
		},
		{
			name: "environment policy before tool argv",
			mutate: func(spec *ProcessSpec) {
				spec.Tool.Environment[0].Source = "caller"
				spec.Tool.Args[0] = "bad\x00arg"
			},
			reason: "environment_policy_invalid", pointer: "/tool/environment/0", toolID: "ent-go",
		},
		{
			name: "tool argv before explicit environment",
			mutate: func(spec *ProcessSpec) {
				spec.Tool.Args[0] = "bad\x00arg"
				spec.Environment = append(spec.Environment, spec.Environment[0])
			},
			reason: "tool_args_invalid", pointer: "/tool/args/0", toolID: "ent-go",
		},
		{
			name: "probe argv before explicit environment",
			mutate: func(spec *ProcessSpec) {
				spec.Tool.Probe.Args[0] = "bad\x00arg"
				spec.Environment = append(spec.Environment, spec.Environment[0])
			},
			reason: "tool_args_invalid", pointer: "/tool/probe/args/0", toolID: "ent-go",
		},
		{
			name: "request argv before explicit environment",
			mutate: func(spec *ProcessSpec) {
				spec.Args = []string{"bad\x00arg"}
				spec.Environment = append(spec.Environment, spec.Environment[0])
			},
			reason: "request_args_invalid", pointer: "/args/0", toolID: "ent-go",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validProcessSpec(t)
			test.mutate(&spec)
			_, err := RunProcess(context.Background(), spec)
			assertProcessError(t, err, "tool_input_invalid", "input", test.reason, test.pointer, test.toolID, 0)
		})
	}
}

func TestRunProcessCancellationKillsAndWaitsForWholeProcessGroup(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process-group behavior is supported on Darwin and Linux")
	}
	spec := validProcessSpec(t)
	pidFile := filepath.Join(spec.StagingRoot, "leaf.pid")
	readyFile := filepath.Join(spec.StagingRoot, "ready")
	spec.Args = []string{"tree", pidFile, readyFile}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := RunProcess(ctx, spec)
		done <- err
	}()
	waitForFile(t, readyFile)
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	leafPID, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case runErr := <-done:
		assertProcessError(t, runErr, "tool_canceled", "wait", "context_canceled", "/context", "ent-go", 0)
	case <-time.After(5 * time.Second):
		t.Fatal("RunProcess did not wait for process-group termination")
	}
	waitForProcessExit(t, leafPID)
}

func TestRunProcessSettlesInheritedStdinAndReapsDescendantAfterParentExit(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process-group behavior is supported on Darwin and Linux")
	}
	spec := validProcessSpec(t)
	useFastRaceHelperExit(&spec)
	groupFile := filepath.Join(spec.StagingRoot, "group.pid")
	leafFile := filepath.Join(spec.StagingRoot, "orphan-leaf.pid")
	readyFile := filepath.Join(spec.StagingRoot, "orphan-ready")
	spec.Args = []string{"orphan-stdin", groupFile, leafFile, readyFile}
	spec.Stdin = make([]byte, MaxStdinBytes)
	done := make(chan error, 1)
	go func() {
		_, err := RunProcess(context.Background(), spec)
		done <- err
	}()
	waitForFile(t, readyFile)
	groupPID := readPIDFile(t, groupFile)
	leafPID := readPIDFile(t, leafFile)
	select {
	case err := <-done:
		assertProcessError(t, err, "tool_failed", "stream", "stdin_write_failed", "/stdin", "ent-go", 0)
		if !waitForProcessExitWithin(leafPID, 500*time.Millisecond) {
			killTestProcessGroup(groupPID)
			waitForProcessExit(t, leafPID)
			t.Fatal("RunProcess returned while an inherited-stdin descendant was still alive")
		}
	case <-time.After(time.Second):
		killTestProcessGroup(groupPID)
		runErr := <-done
		waitForProcessExit(t, leafPID)
		t.Fatalf("RunProcess remained blocked after direct child exit; eventual error = %v", runErr)
	}
}

func TestRunProcessCancellationInterruptsInheritedStdinHandshake(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process-group behavior is supported on Darwin and Linux")
	}
	spec := validProcessSpec(t)
	useFastRaceHelperExit(&spec)
	leafFile := filepath.Join(spec.StagingRoot, "stdin-leaf.pid")
	readyFile := filepath.Join(spec.StagingRoot, "stdin-ready")
	spec.Args = []string{"tree-stdin", leafFile, readyFile}
	spec.Stdin = make([]byte, MaxStdinBytes)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := RunProcess(ctx, spec)
		done <- err
	}()
	waitForFile(t, readyFile)
	leafPID := readPIDFile(t, leafFile)
	cancel()
	select {
	case err := <-done:
		assertProcessError(t, err, "tool_canceled", "wait", "context_canceled", "/context", "ent-go", 0)
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt inherited stdin handshake")
	}
	waitForProcessExit(t, leafPID)
}

func TestRunProcessRejectsConcurrentScratchReuseBeforeStartingSecondChild(t *testing.T) {
	fixture := newProjectionFixture(t)
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scratch.Cleanup(); err != nil {
			t.Error(err)
		}
	}()

	spec := validProcessSpecForRoots(t, fixture.repository, fixture.staging)
	spec.Scratch = scratch
	spec.WorkDir = ""
	readyFile := filepath.Join(fixture.staging, "scratch-ready")
	spec.Args = []string{"block", readyFile}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := RunProcess(ctx, spec)
		done <- runErr
	}()
	waitForFile(t, readyFile)

	second := spec
	second.Args = []string{"mark", filepath.Join(fixture.staging, "second-ran")}
	_, secondErr := RunProcess(context.Background(), second)
	assertProcessError(t, secondErr, "tool_input_invalid", "input", "scratch_state_invalid", "/scratch", "", 0)
	cancel()
	assertProcessError(t, <-done, "tool_canceled", "wait", "context_canceled", "/context", "ent-go", 0)
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("NEXA_PROCESS_HELPER") != "1" {
		return
	}
	args := helperPayload(os.Args)
	if len(args) == 0 {
		os.Exit(90)
	}
	switch args[0] {
	case "version":
		fmt.Fprint(os.Stdout, args[1])
	case "report":
		stdin, _ := os.ReadFile("/dev/stdin")
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(processHelperReport{
			Args: args[1:], Home: os.Getenv("HOME"), Ambient: os.Getenv("NEXA_AMBIENT_SECRET"), Stdin: string(stdin), Cwd: cwd,
		})
	case "mark":
		_ = os.WriteFile(args[1], []byte("ran"), 0o600)
	case "exit":
		fmt.Fprint(os.Stdout, "untrusted-stdout")
		fmt.Fprint(os.Stderr, "private-stderr")
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "exit-details":
		fmt.Fprint(os.Stderr, strings.Join(args[2:], " "))
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "stdout":
		size, _ := strconv.Atoi(args[1])
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), size))
	case "stderr":
		size, _ := strconv.Atoi(args[1])
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("private-stderr"), size/14+1)[:size])
		fmt.Fprint(os.Stdout, "ok")
	case "both-overflow":
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), MaxStdoutBytes+1))
		}()
		go func() {
			defer wait.Done()
			_, _ = os.Stderr.Write(bytes.Repeat([]byte("y"), MaxStderrBytes+1))
		}()
		wait.Wait()
	case "overflow-close-block":
		stream, groupFile, releaseFile, closedFile := args[1], args[2], args[3], args[4]
		_ = os.WriteFile(groupFile, []byte(strconv.Itoa(os.Getpid())), 0o600)
		go func() {
			waitForHelperFile(releaseFile)
			if stream == "stdout" {
				_ = os.Stdout.Close()
			} else {
				_ = os.Stderr.Close()
			}
			_ = os.WriteFile(closedFile, []byte("closed"), 0o600)
		}()
		if stream == "stdout" {
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), MaxStdoutBytes+1))
		} else {
			_, _ = os.Stderr.Write(bytes.Repeat([]byte("y"), MaxStderrBytes+1))
		}
		for {
			time.Sleep(time.Hour)
		}
	case "block":
		_ = os.WriteFile(args[1], []byte("ready"), 0o600)
		for {
			time.Sleep(time.Hour)
		}
	case "tree":
		leaf := exec.Command(os.Args[0], helperArgs("leaf", args[1], args[2])...)
		leaf.Env = os.Environ()
		if err := leaf.Start(); err != nil {
			os.Exit(91)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "orphan-stdin":
		leaf := exec.Command(os.Args[0], helperArgs("leaf", args[2], args[3])...)
		leaf.Env = os.Environ()
		leaf.Stdin = os.Stdin
		if err := leaf.Start(); err != nil {
			os.Exit(91)
		}
		_ = os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0o600)
		waitForHelperFile(args[3])
	case "tree-stdin":
		leaf := exec.Command(os.Args[0], helperArgs("leaf", args[1], args[2])...)
		leaf.Env = os.Environ()
		leaf.Stdin = os.Stdin
		if err := leaf.Start(); err != nil {
			os.Exit(91)
		}
		waitForHelperFile(args[2])
		for {
			time.Sleep(time.Hour)
		}
	case "leaf":
		_ = os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0o600)
		_ = os.WriteFile(args[2], []byte("ready"), 0o600)
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(92)
	}
	os.Exit(0)
}

type processHelperReport struct {
	Args    []string `json:"args"`
	Home    string   `json:"home"`
	Ambient string   `json:"ambient"`
	Stdin   string   `json:"stdin"`
	Cwd     string   `json:"cwd"`
}

func validProcessSpec(t *testing.T) ProcessSpec {
	t.Helper()
	base := canonicalDirectory(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	staging := filepath.Join(base, "staging")
	mustMkdir(t, repository)
	mustMkdir(t, staging)
	return validProcessSpecForRoots(t, repository, staging)
}

func validProcessSpecForRoots(t *testing.T, repository, staging string) ProcessSpec {
	t.Helper()
	home := filepath.Join(staging, "home")
	mustMkdir(t, home)
	return ProcessSpec{
		RepositoryRoot: repository,
		StagingRoot:    staging,
		WorkDir:        staging,
		Tool: ProcessTool{
			ID: "ent-go", Version: "policy-v1", Executable: os.Args[0],
			Args: helperArgs(),
			Environment: []ProcessEnvironmentRule{
				{Name: "HOME", Source: EnvironmentScratch},
				{Name: "NEXA_PROCESS_HELPER", Source: EnvironmentFixed, FixedValue: "1"},
			},
			Probe: ProcessProbe{Args: helperArgs("version", "go-test-version"), ExpectedVersion: "go-test-version"},
		},
		Environment: []ProcessEnvironment{{Name: "HOME", Value: home}, {Name: "NEXA_PROCESS_HELPER", Value: "1"}},
	}
}

func useFastRaceHelperExit(spec *ProcessSpec) {
	spec.Tool.Environment = append(spec.Tool.Environment, ProcessEnvironmentRule{Name: "GORACE", Source: EnvironmentFixed, FixedValue: "atexit_sleep_ms=0"})
	spec.Environment = append(spec.Environment, ProcessEnvironment{Name: "GORACE", Value: "atexit_sleep_ms=0"})
}

func helperArgs(payload ...string) []string {
	result := []string{"-test.run=^TestProcessHelper$", "--"}
	return append(result, payload...)
}

func helperPayload(args []string) []string {
	for index, value := range args {
		if value == "--" {
			return args[index+1:]
		}
	}
	return nil
}

func assertProcessError(t *testing.T, err error, code, stage, reason, pointer, toolID string, exitCode int) {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) || got.Code() != code || got.Stage() != stage || got.Reason() != reason || got.Pointer() != pointer || got.ToolID() != toolID || got.ExitCode() != exitCode {
		t.Fatalf("error = %#v, tuple = (%q,%q,%q,%q,%q,%d), want (%q,%q,%q,%q,%q,%d)", err, got.Code(), got.Stage(), got.Reason(), got.Pointer(), got.ToolID(), got.ExitCode(), code, stage, reason, pointer, toolID, exitCode)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", filepath.Base(path))
}

func waitForHelperFile(path string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(93)
}

func waitForPath(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("timed out waiting for helper path")
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

type controlledDeadlineContext struct {
	done chan struct{}
	once sync.Once
}

func newControlledDeadlineContext() *controlledDeadlineContext {
	return &controlledDeadlineContext{done: make(chan struct{})}
}

func (*controlledDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *controlledDeadlineContext) Done() <-chan struct{}     { return c.done }
func (c *controlledDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}
func (*controlledDeadlineContext) Value(any) any { return nil }
func (c *controlledDeadlineContext) expire()     { c.once.Do(func() { close(c.done) }) }
