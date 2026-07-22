package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/sourceplugin"
)

func TestValidatePreviewBuildsClosedGoExecutions(t *testing.T) {
	toolchain := validationToolchain(t)
	preview := t.TempDir()
	if err := os.Mkdir(filepath.Join(preview, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingValidationExecutor{}
	t.Setenv("PRIVATE_PARENT_VALUE", "must-not-be-inherited")

	err := validatePreview(context.Background(), recorder, toolchain, preview, validationRecipes(t))
	if err != nil {
		t.Fatal(err)
	}
	wantEnvironment := []string{
		"HOME=" + toolchain.Home,
		"TMPDIR=" + toolchain.TempDir,
		"GOPATH=" + toolchain.GOPATH,
		"GOMODCACHE=" + toolchain.ModuleCache,
		"GOCACHE=" + toolchain.BuildCache,
		"GOENV=off",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOTELEMETRY=off",
		"GOWORK=off",
	}
	want := []Execution{
		{Executable: toolchain.Executable, Arguments: []string{"test", "-mod=mod", "."}, Directory: filepath.Join(preview, "backend"), Environment: wantEnvironment},
		{Executable: toolchain.Executable, Arguments: []string{"build", "-mod=mod", "."}, Directory: preview, Environment: wantEnvironment},
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("executions = %#v, want %#v", recorder.calls, want)
	}
}

func TestValidatePreviewRunsRealGoAndCleansIsolatedHome(t *testing.T) {
	root := t.TempDir()
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	preview := filepath.Join(root, "preview")
	toolchain := GoToolchain{
		Executable: executable, Home: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"),
		GOPATH: filepath.Join(root, "gopath"), ModuleCache: filepath.Join(root, "gomodcache"), BuildCache: filepath.Join(root, "gocache"),
	}
	for _, directory := range []string{preview, toolchain.Home, toolchain.TempDir, toolchain.GOPATH, toolchain.ModuleCache, toolchain.BuildCache} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(preview, "go.mod"), []byte("module example.test/telemetry\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(preview, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "telemetry", ModulePath: "example.test/telemetry", PackagePath: "example.test/telemetry/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{}, Profiles: []sourceplugin.ProfileSpec{{
			ID: "default", Files: []string{}, Validations: []sourceplugin.ValidationRecipeSpec{{ID: "build", Kind: sourceplugin.ValidationGoBuild, WorkingDirectory: ".", Packages: []string{"."}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	closure, err := manifest.ResolveProfile("default")
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePreview(context.Background(), NewOSExecutor(), toolchain, preview, closure.Validations()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove isolated Go home after validation: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("isolated Go home reappeared after cleanup: %v", err)
	}
}

func TestOSExecutorUsesExactExecutionWithoutInheritedEnvironment(t *testing.T) {
	directory := t.TempDir()
	home := t.TempDir()
	physicalDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	script := validationScript(t, `
[ "$#" -eq 2 ] || exit 31
[ "$1" = "test" ] || exit 32
[ "$2" = "./..." ] || exit 33
[ "$(pwd -P)" = "`+physicalDirectory+`" ] || exit 34
[ "$HOME" = "`+home+`" ] || exit 35
[ -z "${PRIVATE_PARENT_VALUE+x}" ] || exit 36
exit 0
`)
	t.Setenv("PRIVATE_PARENT_VALUE", "private-parent-secret")

	result, err := NewOSExecutor().Execute(context.Background(), Execution{
		Executable: script,
		Arguments:  []string{"test", "./..."},
		Directory:  directory,
		Environment: []string{
			"HOME=" + home,
		},
	})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestOSExecutorProjectsUnavailableNonzeroAndCanceledSafely(t *testing.T) {
	directory := t.TempDir()
	executor := NewOSExecutor()

	t.Run("unavailable", func(t *testing.T) {
		_, err := executor.Execute(context.Background(), Execution{
			Executable: filepath.Join(t.TempDir(), "missing-go"), Directory: directory, Environment: []string{"HOME=" + t.TempDir()},
		})
		assertValidationError(t, err, ErrUnavailable, "source_validation_unavailable", "executable_unavailable")
	})

	t.Run("nonzero", func(t *testing.T) {
		script := validationScript(t, `
echo private-stdout
echo private-stderr >&2
exit 7
`)
		result, err := executor.Execute(context.Background(), Execution{Executable: script, Directory: directory, Environment: []string{"HOME=" + t.TempDir()}})
		assertValidationError(t, err, ErrExternal, "source_validation_failed", "validation_failed")
		if result.ExitCode != 7 {
			t.Fatalf("exit code = %d, want 7", result.ExitCode)
		}
		for _, secret := range []string{"private-stdout", "private-stderr", script, directory} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("raw process detail escaped: %q", err)
			}
		}
	})

	t.Run("canceled", func(t *testing.T) {
		script := validationScript(t, "\nexec sleep 60\n")
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(20*time.Millisecond, cancel)
		defer timer.Stop()
		_, err := executor.Execute(ctx, Execution{Executable: script, Directory: directory, Environment: []string{"HOME=" + t.TempDir()}})
		assertValidationError(t, err, ErrCanceled, "operation_canceled", "context_canceled")
	})
}

func TestValidatePreviewProjectsExecutorContractViolations(t *testing.T) {
	toolchain := validationToolchain(t)
	preview := t.TempDir()
	if err := os.Mkdir(filepath.Join(preview, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	recipes := validationRecipes(t)[:1]

	_, err := NewOSExecutor().Execute(nil, Execution{Executable: toolchain.Executable, Directory: preview})
	assertValidationError(t, err, ErrInput, "source_validation_invalid", "context_required")

	unknown := &recordingValidationExecutor{err: errors.New("private-executor-error")}
	err = validatePreview(context.Background(), unknown, toolchain, preview, recipes)
	assertValidationError(t, err, ErrInternal, "source_validation_internal", "executor_contract_invalid")
	if strings.Contains(err.Error(), "private-executor-error") {
		t.Fatalf("executor error escaped: %v", err)
	}

	nonzero := &recordingValidationExecutor{result: ExecutionResult{ExitCode: 9}}
	err = validatePreview(context.Background(), nonzero, toolchain, preview, recipes)
	assertValidationError(t, err, ErrExternal, "source_validation_failed", "validation_failed")
	if len(nonzero.calls) != 1 || !reflect.DeepEqual(nonzero.calls[0].Arguments, []string{"test", "-mod=mod", "."}) {
		t.Fatalf("validation failure calls = %#v", nonzero.calls)
	}
}

type recordingValidationExecutor struct {
	calls   []Execution
	result  ExecutionResult
	results []ExecutionResult
	err     error
}

func (executor *recordingValidationExecutor) Execute(_ context.Context, execution Execution) (ExecutionResult, error) {
	execution.Arguments = append([]string(nil), execution.Arguments...)
	execution.Environment = append([]string(nil), execution.Environment...)
	callIndex := len(executor.calls)
	executor.calls = append(executor.calls, execution)
	if callIndex < len(executor.results) {
		return executor.results[callIndex], executor.err
	}
	return executor.result, executor.err
}

func validationRecipes(t *testing.T) []sourceplugin.ValidationRecipe {
	t.Helper()
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "validation.sample", ModulePath: "example.test/validation", PackagePath: "example.test/validation/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{},
		Profiles: []sourceplugin.ProfileSpec{{
			ID: "default", Files: []string{}, RequiresProfiles: []string{}, RequiresBundles: []sourceplugin.BundleRequirementSpec{},
			Validations: []sourceplugin.ValidationRecipeSpec{
				{ID: "backend-test", Kind: sourceplugin.ValidationGoTest, WorkingDirectory: "backend", Packages: []string{"."}},
				{ID: "root-build", Kind: sourceplugin.ValidationGoBuild, WorkingDirectory: ".", Packages: []string{"."}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	closure, err := manifest.ResolveProfile("default")
	if err != nil {
		t.Fatal(err)
	}
	return closure.Validations()
}

func validationToolchain(t *testing.T) GoToolchain {
	t.Helper()
	return GoToolchain{
		Executable:  validationScript(t, "\nexit 0\n"),
		Home:        t.TempDir(),
		TempDir:     t.TempDir(),
		GOPATH:      t.TempDir(),
		ModuleCache: t.TempDir(),
		BuildCache:  t.TempDir(),
	}
}

func validationScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertValidationError(t *testing.T, err error, class ErrorClass, code, reason string) {
	t.Helper()
	var projected *Error
	if !errors.As(err, &projected) {
		t.Fatalf("error = %#v, want *engine.Error", err)
	}
	if projected.Class() != class || projected.Code() != code || projected.Reason() != reason || projected.Stage() != "validation" {
		t.Fatalf("error = class=%v code=%q reason=%q stage=%q", projected.Class(), projected.Code(), projected.Reason(), projected.Stage())
	}
}
