package toolchain_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/directwrite"
	"github.com/nxnminieye/nexa/generation/toolchain"
)

func TestExecDirectRunnerUsesCanonicalConsumerRootAndSelectedEnvironment(t *testing.T) {
	repository := canonicalTestDirectory(t, t.TempDir())
	executable, _ := filepath.Abs(os.Args[0])
	tool := toolchain.Tool{ID: "direct-helper", Version: "v2", Executable: executable, Args: directHelperArgs(), Environment: []toolchain.EnvironmentRule{{Name: "NEXA_DIRECT_HELPER", Source: toolchain.EnvironmentFixed, FixedValue: "1"}, {Name: "HOME", Source: toolchain.EnvironmentHost}}, Probe: toolchain.ExecutableProbe{Args: directHelperArgs("version"), ExpectedVersion: "direct-v2"}}
	result, err := toolchain.NewExecDirectRunner().RunDirect(context.Background(), toolchain.DirectRequest{RepositoryRoot: repository, Tool: tool, Args: []string{"report", "exact"}, Environment: []toolchain.EnvVar{{Name: "HOME", Value: repository}, {Name: "NEXA_DIRECT_HELPER", Value: "1"}}, Stdin: []byte("canonical-stdin")})
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		CWD, Home, Stdin string
		Args             []string
	}
	if err := json.Unmarshal(result.Stdout, &report); err != nil {
		t.Fatal(err)
	}
	if report.CWD != repository || report.Home != repository || report.Stdin != "canonical-stdin" || !reflect.DeepEqual(report.Args, []string{"exact"}) {
		t.Fatalf("direct report = %#v", report)
	}
	entries, err := os.ReadDir(repository)
	if err != nil || len(entries) != 0 {
		t.Fatalf("direct runner created staging/scaffold: %#v, %v", entries, err)
	}
}

func TestDirectScopeValidationReusesDirectWriteRules(t *testing.T) {
	complete := []directwrite.OutputScope{{Path: "backend/core/internal/types", Mode: directwrite.OutputModeReplaceTree}, {Path: "backend/core/internal/handler", Mode: directwrite.OutputModeReplaceTree}}
	subset, normalized, err := toolchain.OutputScopesSubset([]directwrite.OutputScope{complete[1]}, complete)
	if err != nil || len(subset) != 1 || len(normalized) != 2 {
		t.Fatalf("scope relation = %#v, %#v, %v", subset, normalized, err)
	}
	if _, _, err := toolchain.OutputScopesSubset([]directwrite.OutputScope{{Path: "backend/core/manual", Mode: directwrite.OutputModeFileSet}}, complete); err == nil {
		t.Fatal("accepted scope outside command scopes")
	}
	if err := toolchain.ValidateRepositoryPath("backend/.GIT/config"); err == nil {
		t.Fatal("accepted .git case-fold alias")
	}
}

func TestExecDirectRunnerProjectsPostStartWriteEvidence(t *testing.T) {
	repository := canonicalTestDirectory(t, t.TempDir())
	executable, _ := filepath.Abs(os.Args[0])
	tool := toolchain.Tool{ID: "direct-helper", Version: "v2", Executable: executable, Args: directHelperArgs(), Environment: []toolchain.EnvironmentRule{{Name: "NEXA_DIRECT_HELPER", Source: toolchain.EnvironmentFixed, FixedValue: "1"}}, Probe: toolchain.ExecutableProbe{Args: directHelperArgs("version"), ExpectedVersion: "direct-v2"}}
	_, err := toolchain.NewExecDirectRunner().RunDirect(context.Background(), toolchain.DirectRequest{RepositoryRoot: repository, Tool: tool, Args: []string{"fail"}, Environment: []toolchain.EnvVar{{Name: "NEXA_DIRECT_HELPER", Value: "1"}}})
	var typed *toolchain.Error
	if !errors.As(err, &typed) || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatalf("direct error = %#v", err)
	}
}

func TestExecDirectRunnerProjectsProbeStartWriteEvidence(t *testing.T) {
	repository := canonicalTestDirectory(t, t.TempDir())
	executable, _ := filepath.Abs(os.Args[0])
	tool := toolchain.Tool{ID: "direct-helper", Version: "v2", Executable: executable, Environment: []toolchain.EnvironmentRule{{Name: "NEXA_DIRECT_HELPER", Source: toolchain.EnvironmentFixed, FixedValue: "1"}}, Probe: toolchain.ExecutableProbe{Args: directHelperArgs("fail"), ExpectedVersion: "direct-v2"}}
	_, err := toolchain.NewExecDirectRunner().RunDirect(context.Background(), toolchain.DirectRequest{RepositoryRoot: repository, Tool: tool, Environment: []toolchain.EnvVar{{Name: "NEXA_DIRECT_HELPER", Value: "1"}}})
	var typed *toolchain.Error
	if !errors.As(err, &typed) || typed.Stage() != "probe" || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatalf("direct probe error = %#v", err)
	}
}

func TestDirectPostInvocationErrorPreservesCauseAndStableEvidence(t *testing.T) {
	cause := &json.SyntaxError{Offset: 7}
	err := toolchain.DirectPostInvocationError("api", toolchain.DirectPostInvocationResultInvalid, cause)
	var typed *toolchain.Error
	if !errors.As(err, &typed) || typed.Code() != "tool_output_invalid" || typed.Stage() != "result" || typed.Reason() != "result_invalid" || typed.Pointer() != "/stdout" || typed.Source() != "" || typed.ToolID() != "api" || typed.ExitCode() != 0 || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatalf("post-invocation error = %#v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatal("post-invocation error hid its original cause")
	}
	var found *json.SyntaxError
	if !errors.As(err, &found) || found != cause {
		t.Fatal("post-invocation error hid its original cause type")
	}
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatal("post-invocation error does not expose the immutable dual relation")
	}
	children := multi.Unwrap()
	if len(children) != 2 {
		t.Fatalf("unwrap children = %#v", children)
	}
	children[0], children[1] = nil, nil
	if !errors.Is(err, cause) || !errors.As(err, &typed) || !typed.Started() || !typed.MayHaveWritten() {
		t.Fatal("mutating an unwrap result changed post-invocation evidence")
	}
	second := toolchain.DirectPostInvocationError("api", toolchain.DirectPostInvocationResultInvalid, nil)
	var secondTyped *toolchain.Error
	if !errors.As(second, &secondTyped) || secondTyped == typed || errors.Is(second, cause) {
		t.Fatal("post-invocation errors share mutable typed state")
	}
}

func TestDirectPostInvocationFailureMappingsAreClosed(t *testing.T) {
	tests := []struct {
		failure toolchain.DirectPostInvocationFailure
		reason  string
		pointer string
	}{
		{toolchain.DirectPostInvocationProcessIdentityInvalid, "process_identity_invalid", "/process"},
		{toolchain.DirectPostInvocationResultInvalid, "result_invalid", "/stdout"},
		{toolchain.DirectPostInvocationAcknowledgementInvalid, "result_acknowledgement_invalid", "/result"},
	}
	for _, test := range tests {
		err := toolchain.DirectPostInvocationError("rpc", test.failure, nil)
		var typed *toolchain.Error
		if !errors.As(err, &typed) || typed.Code() != "tool_output_invalid" || typed.Stage() != "result" || typed.Reason() != test.reason || typed.Pointer() != test.pointer || typed.Source() != "" || typed.ToolID() != "rpc" || !typed.Started() || !typed.MayHaveWritten() {
			t.Fatalf("failure %d = %#v", test.failure, err)
		}
	}
}

func TestDirectPostInvocationInvalidFailureUsesFixedBoundedFallback(t *testing.T) {
	first := toolchain.DirectPostInvocationError("api", toolchain.DirectPostInvocationFailure(200), nil)
	second := toolchain.DirectPostInvocationError("api", toolchain.DirectPostInvocationFailure(201), nil)
	var firstTyped, secondTyped *toolchain.Error
	if !errors.As(first, &firstTyped) || !errors.As(second, &secondTyped) {
		t.Fatalf("invalid failure errors = %#v, %#v", first, second)
	}
	for _, typed := range []*toolchain.Error{firstTyped, secondTyped} {
		if typed.Code() != "tool_output_invalid" || typed.Stage() != "result" || typed.Reason() != "post_invocation_failure_invalid" || typed.Pointer() != "/failure" || typed.Source() != "" || !typed.Started() || !typed.MayHaveWritten() {
			t.Fatalf("invalid failure projection = %#v", typed)
		}
	}
	if firstTyped == secondTyped || !errors.Is(second, firstTyped.Unwrap()) {
		t.Fatal("invalid failure values did not share the fixed bounded sentinel contract")
	}
}

func canonicalTestDirectory(t *testing.T, value string) string {
	t.Helper()
	result, err := filepath.EvalSymlinks(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func directHelperArgs(payload ...string) []string {
	return append([]string{"-test.run=^TestExecDirectRunnerHelperProcess$", "--"}, payload...)
}

func TestExecDirectRunnerHelperProcess(t *testing.T) {
	if os.Getenv("NEXA_DIRECT_HELPER") != "1" {
		return
	}
	args := publicHelperPayload(os.Args)
	if len(args) == 1 && args[0] == "version" {
		fmt.Fprint(os.Stdout, "direct-v2")
		os.Exit(0)
	}
	if len(args) >= 1 && args[0] == "report" {
		cwd, _ := os.Getwd()
		stdin, _ := os.ReadFile("/dev/stdin")
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			CWD, Home, Stdin string
			Args             []string
		}{cwd, os.Getenv("HOME"), string(stdin), args[1:]})
		os.Exit(0)
	}
	os.Exit(91)
}
