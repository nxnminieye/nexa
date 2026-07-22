package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/sourceplugin"
)

type Execution struct {
	Executable  string
	Arguments   []string
	Directory   string
	Environment []string
}

type ExecutionResult struct {
	ExitCode int
}

type Executor interface {
	Execute(context.Context, Execution) (ExecutionResult, error)
}

type GoToolchain struct {
	Executable  string
	Home        string
	TempDir     string
	GOPATH      string
	ModuleCache string
	BuildCache  string
}

type osExecutor struct{}

func NewOSExecutor() Executor { return osExecutor{} }

func (osExecutor) Execute(ctx context.Context, execution Execution) (ExecutionResult, error) {
	if ctx == nil {
		return ExecutionResult{}, validationError(ErrInput, "source_validation_invalid", "context_required", "/context")
	}
	if ctx.Err() != nil {
		return ExecutionResult{}, validationError(ErrCanceled, "operation_canceled", "context_canceled", "/context")
	}
	if err := validateExecution(execution); err != nil {
		return ExecutionResult{}, err
	}

	command := exec.CommandContext(ctx, execution.Executable, execution.Arguments...)
	command.Dir = execution.Directory
	command.Env = append(make([]string, 0, len(execution.Environment)), execution.Environment...)
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	err := command.Run()
	if err == nil {
		return ExecutionResult{ExitCode: 0}, nil
	}
	if ctx.Err() != nil {
		return ExecutionResult{}, validationError(ErrCanceled, "operation_canceled", "context_canceled", "/context")
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return ExecutionResult{ExitCode: exitError.ExitCode()}, validationError(ErrExternal, "source_validation_failed", "validation_failed", "")
	}
	return ExecutionResult{}, validationError(ErrUnavailable, "source_validation_unavailable", "executable_unavailable", "/executable")
}

func validatePreview(
	ctx context.Context,
	executor Executor,
	toolchain GoToolchain,
	previewRoot string,
	recipes []sourceplugin.ValidationRecipe,
) error {
	if ctx == nil {
		return validationError(ErrInput, "source_validation_invalid", "context_required", "/context")
	}
	if ctx.Err() != nil {
		return validationError(ErrCanceled, "operation_canceled", "context_canceled", "/context")
	}
	if executor == nil {
		return validationError(ErrInput, "source_validation_invalid", "executor_required", "/executor")
	}
	if err := validateGoToolchain(toolchain); err != nil {
		return err
	}
	if !validAbsoluteDirectory(previewRoot) {
		return validationError(ErrInput, "source_validation_invalid", "preview_root_invalid", "/previewRoot")
	}
	environment := validationEnvironment(toolchain)
	for index, recipe := range recipes {
		command := ""
		switch recipe.Kind() {
		case sourceplugin.ValidationGoTest:
			command = "test"
		case sourceplugin.ValidationGoBuild:
			command = "build"
		default:
			return validationError(ErrInput, "source_validation_invalid", "recipe_kind_invalid", validationPointer(index, "/kind"))
		}
		workingDirectory := previewRoot
		if recipe.WorkingDirectory() != "." {
			workingDirectory = filepath.Join(previewRoot, filepath.FromSlash(recipe.WorkingDirectory()))
		}
		if !withinDirectory(previewRoot, workingDirectory) || !validAbsoluteDirectory(workingDirectory) {
			return validationError(ErrInput, "source_validation_invalid", "working_directory_invalid", validationPointer(index, "/workingDirectory"))
		}
		packages := recipe.Packages()
		if len(packages) == 0 {
			return validationError(ErrInput, "source_validation_invalid", "packages_empty", validationPointer(index, "/packages"))
		}
		arguments := make([]string, 2, len(packages)+2)
		arguments[0] = command
		arguments[1] = "-mod=mod"
		arguments = append(arguments, packages...)
		result, err := executor.Execute(ctx, Execution{
			Executable: toolchain.Executable, Arguments: arguments, Directory: workingDirectory,
			Environment: append([]string(nil), environment...),
		})
		if err != nil {
			var projected *Error
			if errors.As(err, &projected) {
				return projected
			}
			return validationError(ErrInternal, "source_validation_internal", "executor_contract_invalid", "")
		}
		if result.ExitCode != 0 {
			return validationError(ErrExternal, "source_validation_failed", "validation_failed", "")
		}
	}
	return nil
}

func validateExecution(execution Execution) error {
	if !validAbsoluteExecutable(execution.Executable) {
		return validationError(ErrUnavailable, "source_validation_unavailable", "executable_unavailable", "/executable")
	}
	if !validAbsoluteDirectory(execution.Directory) {
		return validationError(ErrInput, "source_validation_invalid", "directory_invalid", "/directory")
	}
	for index, argument := range execution.Arguments {
		if strings.ContainsRune(argument, 0) {
			return validationError(ErrInput, "source_validation_invalid", "argument_invalid", validationPointer(index, "/arguments"))
		}
	}
	seen := make(map[string]struct{}, len(execution.Environment))
	for index, value := range execution.Environment {
		name, _, ok := strings.Cut(value, "=")
		if !ok || name == "" || strings.ContainsAny(name, "\x00=") || strings.ContainsRune(value, 0) {
			return validationError(ErrInput, "source_validation_invalid", "environment_invalid", validationPointer(index, "/environment"))
		}
		if _, duplicate := seen[name]; duplicate {
			return validationError(ErrInput, "source_validation_invalid", "environment_duplicate", validationPointer(index, "/environment"))
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateGoToolchain(toolchain GoToolchain) error {
	if !validAbsoluteExecutable(toolchain.Executable) {
		return validationError(ErrUnavailable, "source_validation_unavailable", "executable_unavailable", "/goToolchain/executable")
	}
	for _, field := range []struct{ pointer, value string }{
		{pointer: "/goToolchain/home", value: toolchain.Home},
		{pointer: "/goToolchain/tempDir", value: toolchain.TempDir},
		{pointer: "/goToolchain/goPath", value: toolchain.GOPATH},
		{pointer: "/goToolchain/moduleCache", value: toolchain.ModuleCache},
		{pointer: "/goToolchain/buildCache", value: toolchain.BuildCache},
	} {
		if !validAbsoluteDirectory(field.value) {
			return validationError(ErrInput, "source_validation_invalid", "toolchain_directory_invalid", field.pointer)
		}
	}
	return nil
}

func validAbsoluteExecutable(value string) bool {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	info, err := os.Lstat(value)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func validAbsoluteDirectory(value string) bool {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	info, err := os.Lstat(value)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func validationEnvironment(toolchain GoToolchain) []string {
	return []string{
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
}

func withinDirectory(root, candidate string) bool {
	return candidate == root || strings.HasPrefix(candidate, root+string(os.PathSeparator))
}

func validationPointer(index int, suffix string) string {
	return "/validations/" + strconv.Itoa(index) + suffix
}

func validationError(class ErrorClass, code, reason, pointer string) *Error {
	return newError(class, code, reason, pointer, "validation")
}
