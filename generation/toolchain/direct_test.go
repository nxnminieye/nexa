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
