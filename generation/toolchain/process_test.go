package toolchain_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/toolchain"
)

func TestMain(m *testing.M) {
	if os.Getenv("NEXA_PUBLIC_RUNNER_HELPER") == "1" {
		os.Exit(runPublicRunnerHelper())
	}
	os.Exit(m.Run())
}

func TestExecRunnerPublishesBoundedClosedProcessContract(t *testing.T) {
	if toolchain.MaxStdinBytes != 1<<20 || toolchain.MaxStdoutBytes != 16<<20 || toolchain.MaxStderrBytes != 64<<10 {
		t.Fatalf("public process limits = (%d,%d,%d)", toolchain.MaxStdinBytes, toolchain.MaxStdoutBytes, toolchain.MaxStderrBytes)
	}
	t.Setenv("NEXA_AMBIENT_PUBLIC_SECRET", "must-not-leak")
	request := validPublicRequest(t)
	request.Args = []string{"report", "request-arg"}
	request.Stdin = []byte("public-input")
	result, err := toolchain.NewExecRunner().Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var report publicRunnerReport
	if err := json.Unmarshal(result.Stdout, &report); err != nil {
		t.Fatal(err)
	}
	if result.ToolID != "public-go" || result.Version != "policy-v1" || result.ExecutableVersion != "public-version" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(report.Args) != 1 || report.Args[0] != "request-arg" || report.Ambient != "" || report.Stdin != "public-input" || report.Cwd != request.StagingRoot {
		t.Fatalf("closed process report = %#v", report)
	}
}

func TestExecRunnerProjectsStableNonzeroErrorWithoutPollutedOutput(t *testing.T) {
	request := validPublicRequest(t)
	request.Args = []string{"exit", "19", request.RepositoryRoot}
	result, err := toolchain.NewExecRunner().Run(context.Background(), request)
	var got *toolchain.Error
	if !errors.As(err, &got) || got.Code() != "tool_failed" || got.Stage() != "exit" || got.Reason() != "nonzero_exit" || got.Pointer() != "" || got.Source() != "" || got.ToolID() != "public-go" || got.ExitCode() != 19 {
		t.Fatalf("error = %#v", err)
	}
	if got.Diagnostic() != "private-stderr <redacted>" {
		t.Fatalf("diagnostic = %q", got.Diagnostic())
	}
	if len(result.Stdout) != 0 || result.ExitCode != 0 {
		t.Fatalf("untrusted result escaped = %#v", result)
	}
	_, secondErr := toolchain.NewExecRunner().Run(context.Background(), request)
	if !errors.Is(secondErr, errors.Unwrap(err)) {
		t.Fatal("process error sentinel is not stable")
	}
	if text := err.Error(); text == "" || containsAny(text, "private-stderr", "untrusted-stdout", request.Tool.Executable, request.StagingRoot) {
		t.Fatalf("public error leaked private process data: %q", text)
	}
}

func validPublicRequest(t *testing.T) toolchain.Request {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(base, "repository")
	staging := filepath.Join(base, "staging")
	home := filepath.Join(staging, "home")
	for _, path := range []string{repository, staging, home} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return toolchain.Request{
		RepositoryRoot: repository, StagingRoot: staging, WorkDir: staging,
		Tool: toolchain.Tool{
			ID: "public-go", Version: "policy-v1", Executable: executable,
			Args: publicHelperArgs(),
			Environment: []toolchain.EnvironmentRule{
				{Name: "HOME", Source: toolchain.EnvironmentScratch},
				{Name: "NEXA_PUBLIC_RUNNER_HELPER", Source: toolchain.EnvironmentFixed, FixedValue: "1"},
			},
			Probe: toolchain.ExecutableProbe{Args: publicHelperArgs("version", "public-version"), ExpectedVersion: "public-version"},
		},
		Environment: []toolchain.EnvVar{{Name: "HOME", Value: home}, {Name: "NEXA_PUBLIC_RUNNER_HELPER", Value: "1"}},
	}
}

type publicRunnerReport struct {
	Args    []string `json:"args"`
	Ambient string   `json:"ambient"`
	Stdin   string   `json:"stdin"`
	Cwd     string   `json:"cwd"`
}

func runPublicRunnerHelper() int {
	args := publicHelperPayload(os.Args)
	if len(args) == 0 {
		return 90
	}
	switch args[0] {
	case "version":
		fmt.Fprint(os.Stdout, args[1])
	case "report":
		stdin, _ := os.ReadFile("/dev/stdin")
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(publicRunnerReport{Args: args[1:], Ambient: os.Getenv("NEXA_AMBIENT_PUBLIC_SECRET"), Stdin: string(stdin), Cwd: cwd})
	case "exit":
		fmt.Fprint(os.Stdout, "untrusted-stdout")
		fmt.Fprint(os.Stderr, "private-stderr")
		if len(args) > 2 {
			fmt.Fprint(os.Stderr, " "+args[2])
		}
		code, _ := strconv.Atoi(args[1])
		return code
	default:
		return 91
	}
	return 0
}

func publicHelperArgs(payload ...string) []string {
	result := []string{"-test.run=^$", "--"}
	return append(result, payload...)
}

func publicHelperPayload(args []string) []string {
	for index, value := range args {
		if value == "--" {
			return args[index+1:]
		}
	}
	return nil
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
