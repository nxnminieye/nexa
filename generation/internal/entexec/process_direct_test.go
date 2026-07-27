package entexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func validDirectProcessSpec(t *testing.T) ProcessSpec {
	t.Helper()
	repository := canonicalDirectory(t, t.TempDir())
	return ProcessSpec{
		RepositoryRoot: repository,
		Direct:         true,
		Tool: ProcessTool{
			ID: "ent-direct", Version: "policy-v1", Executable: os.Args[0], Args: helperArgs(),
			Environment: []ProcessEnvironmentRule{{Name: "NEXA_PROCESS_HELPER", Source: EnvironmentFixed, FixedValue: "1"}},
			Probe:       ProcessProbe{Args: helperArgs("version", "go-test-version"), ExpectedVersion: "go-test-version"},
		},
		Environment: []ProcessEnvironment{{Name: "NEXA_PROCESS_HELPER", Value: "1"}},
	}
}

func TestRunProcessDirectUsesRepositoryWithoutCreatingTree(t *testing.T) {
	spec := validDirectProcessSpec(t)
	spec.Args = []string{"report", "direct"}
	before, err := os.ReadDir(spec.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunProcess(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	var report processHelperReport
	if err := json.Unmarshal(result.Stdout, &report); err != nil {
		t.Fatal(err)
	}
	if report.Cwd != spec.RepositoryRoot {
		t.Fatalf("cwd = %q, want %q", report.Cwd, spec.RepositoryRoot)
	}
	after, err := os.ReadDir(spec.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("repository entries changed: %d -> %d", len(before), len(after))
	}
}

func TestPrepareProcessDirectRejectsScratchInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProcessSpec)
	}{
		{"staging", func(s *ProcessSpec) { s.StagingRoot = s.RepositoryRoot }},
		{"workdir", func(s *ProcessSpec) { s.WorkDir = s.RepositoryRoot }},
		{"scratch environment", func(s *ProcessSpec) { s.Tool.Environment[0].Source = EnvironmentScratch }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validDirectProcessSpec(t)
			test.mutate(&spec)
			_, err := RunProcess(context.Background(), spec)
			var typed *Error
			if !errors.As(err, &typed) || typed.Started() || typed.MayHaveWritten() {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestDirectProcessErrorStartedState(t *testing.T) {
	t.Run("probe start failure", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		spec.Tool.Executable = filepath.Join(spec.RepositoryRoot, "missing")
		_, err := RunProcess(context.Background(), spec)
		assertNotStarted(t, err)
	})
	t.Run("probe nonzero", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		spec.Tool.Probe.Args = helperArgs("exit", "23")
		_, err := RunProcess(context.Background(), spec)
		assertDirectInvoked(t, err)
	})
	t.Run("probe output invalid", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		spec.Tool.Probe.Args = helperArgs("stdout", strconv.Itoa(MaxStdoutBytes+1))
		_, err := RunProcess(context.Background(), spec)
		assertDirectInvoked(t, err)
	})
	t.Run("probe context after start", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		ready := filepath.Join(spec.RepositoryRoot, "probe-ready")
		spec.Tool.Probe.Args = helperArgs("block", ready)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { _, err := RunProcess(ctx, spec); done <- err }()
		waitForFile(t, ready)
		cancel()
		assertDirectInvoked(t, <-done)
	})
	t.Run("probe canceled before start", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		ctx, cancel := context.WithCancel(context.Background())
		spec.processHook = func(event processEvent) {
			if event.Name == "start" {
				cancel()
			}
		}
		_, err := RunProcess(ctx, spec)
		assertProcessError(t, err, "tool_canceled", "wait", "context_canceled", "/context", "ent-direct", 0)
		assertNotStarted(t, err)
	})
	t.Run("main start failure", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		prepared, err := prepareProcess(context.Background(), spec)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.release()
		if _, err := probePreparedProcess(context.Background(), &prepared); err != nil {
			t.Fatal(err)
		}
		prepared.executable = filepath.Join(spec.RepositoryRoot, "missing")
		_, err = runPreparedProcess(context.Background(), preparedExecution{process: &prepared})
		assertDirectInvoked(t, err)
	})
	t.Run("main nonzero", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		spec.Args = []string{"exit", "17"}
		_, err := RunProcess(context.Background(), spec)
		var typed *Error
		if !errors.As(err, &typed) || !typed.Started() || !typed.MayHaveWritten() {
			t.Fatalf("error = %#v", err)
		}
	})
	t.Run("main canceled at launch after probe", func(t *testing.T) {
		spec := validDirectProcessSpec(t)
		ctx, cancel := context.WithCancel(context.Background())
		marker := filepath.Join(spec.RepositoryRoot, "main-ran")
		spec.Args = []string{"mark", marker}
		starts := 0
		spec.processHook = func(event processEvent) {
			if event.Name != "start" {
				return
			}
			starts++
			if len(event.Args) > 0 && event.Args[len(event.Args)-1] == marker {
				cancel()
			}
		}
		_, err := RunProcess(ctx, spec)
		assertProcessError(t, err, "tool_canceled", "wait", "context_canceled", "/context", "ent-direct", 0)
		assertDirectInvoked(t, err)
		if starts != 2 {
			t.Fatalf("start events = %d, want probe and main", starts)
		}
		if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("main process started: %v", statErr)
		}
	})
}

func TestStagedProcessErrorStartedStateIsUnchanged(t *testing.T) {
	t.Run("probe nonzero", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Tool.Probe.Args = helperArgs("exit", "23")
		_, err := RunProcess(context.Background(), spec)
		assertNotStarted(t, err)
	})
	t.Run("main nonzero", func(t *testing.T) {
		spec := validProcessSpec(t)
		spec.Args = []string{"exit", "17"}
		_, err := RunProcess(context.Background(), spec)
		var typed *Error
		if !errors.As(err, &typed) || !typed.Started() || typed.MayHaveWritten() {
			t.Fatalf("error = %#v", err)
		}
	})
}

func TestDirectProcessDiagnosticRedaction(t *testing.T) {
	spec := validDirectProcessSpec(t)
	spec.Tool.Environment = append(spec.Tool.Environment, ProcessEnvironmentRule{Name: "SHORT", Source: EnvironmentFixed, FixedValue: "x"})
	spec.Environment = append(spec.Environment, ProcessEnvironment{Name: "SHORT", Value: "x"})
	absoluteArgument := filepath.Join(t.TempDir(), "private-argument")
	spec.Args = []string{"exit-details", "17", "SHORT=x", spec.RepositoryRoot, spec.Tool.Executable, absoluteArgument, "line\ncontrol\tvalue"}
	_, err := RunProcess(context.Background(), spec)
	var typed *Error
	if !errors.As(err, &typed) || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatalf("error = %#v", err)
	}
	diagnostic := typed.Diagnostic()
	for _, secret := range []string{"SHORT=x", spec.RepositoryRoot, spec.Tool.Executable, absoluteArgument} {
		if strings.Contains(diagnostic, secret) {
			t.Fatalf("diagnostic leaked %q: %q", secret, diagnostic)
		}
	}
	if strings.ContainsAny(diagnostic, "\n\r\t") {
		t.Fatalf("diagnostic retained control characters: %q", diagnostic)
	}
}

func TestDirectProcessCancellationKillsDescendants(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process groups unsupported")
	}
	spec := validDirectProcessSpec(t)
	pidFile, readyFile := filepath.Join(spec.RepositoryRoot, "leaf.pid"), filepath.Join(spec.RepositoryRoot, "ready")
	spec.Args = []string{"tree", pidFile, readyFile}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := RunProcess(ctx, spec); done <- err }()
	waitForFile(t, readyFile)
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		var typed *Error
		if !errors.As(err, &typed) || !typed.Started() || !typed.MayHaveWritten() {
			t.Fatalf("error = %#v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("direct process did not terminate")
	}
	waitForProcessExit(t, pid)
}

func TestDirectProcessOverflowIsPostLaunch(t *testing.T) {
	spec := validDirectProcessSpec(t)
	spec.Args = []string{"stdout", strconv.Itoa(MaxStdoutBytes + 1)}
	_, err := RunProcess(context.Background(), spec)
	var typed *Error
	if !errors.As(err, &typed) || typed.Reason() != "stdout_limit_exceeded" || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatalf("error = %#v", err)
	}
}

func assertNotStarted(t *testing.T, err error) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Started() || typed.MayHaveWritten() {
		t.Fatalf("error = %#v", err)
	}
}

func assertDirectInvoked(t *testing.T, err error) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatalf("error = %#v", err)
	}
}
