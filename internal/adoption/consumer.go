package adoption

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

const frameworkModulePath = "github.com/nxnminieye/nexa"

// Check is a standard Go command used to verify a local framework consumer.
type Check string

const (
	CheckTest  Check = "test"
	CheckBuild Check = "build"
	CheckVet   Check = "vet"
)

// ConsumerRequest describes a local, disposable adoption check. It carries no
// release, approval, or candidate-authority meaning.
type ConsumerRequest struct {
	FixtureRoot   string
	FrameworkRoot string
	WorkRoot      string
	Checks        []Check
}

// CommandResult is the ordinary process result for one Go command.
type CommandResult struct {
	Name     string
	Binary   string
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
}

// ConsumerResult reports the commands run against the disposable consumer.
type ConsumerResult struct {
	Commands []CommandResult
}

// InspectionRequest identifies an optional local nexactl inspection command.
// The command output is returned as-is for discovery and display only.
type InspectionRequest struct {
	Binary string
	Args   []string
}

// RunInspection runs one nexactl inspect command without interpreting its
// output as execution authority or approval.
func RunInspection(ctx context.Context, request InspectionRequest) (CommandResult, error) {
	if ctx == nil {
		return CommandResult{}, fmt.Errorf("context is required")
	}
	if request.Binary == "" || countArgument(request.Args, "inspect") != 1 {
		return CommandResult{}, fmt.Errorf("nexactl inspect command is invalid")
	}
	binary, err := exec.LookPath(request.Binary)
	if err != nil {
		return CommandResult{Name: "nexactl inspect", Binary: request.Binary, Args: append([]string(nil), request.Args...), ExitCode: -1}, err
	}
	return runCommand(ctx, "", "nexactl inspect", binary, request.Args, os.Environ())
}

// RunConsumer copies a fixture into a disposable module, points the framework
// dependency at a local checkout, runs standard Go checks, and removes the
// disposable module before returning.
func RunConsumer(ctx context.Context, request ConsumerRequest) (result ConsumerResult, returnErr error) {
	if ctx == nil {
		return result, fmt.Errorf("context is required")
	}
	paths, err := resolveConsumerPaths(request)
	if err != nil {
		return result, err
	}
	checks, err := normalizeChecks(request.Checks)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(paths.workRoot, 0o755); err != nil {
		return result, fmt.Errorf("create consumer work root: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(paths.workRoot); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove consumer work root: %w", err))
		}
	}()
	if err := copyFixture(paths.fixtureRoot, paths.workRoot); err != nil {
		return result, err
	}
	if err := writeConsumerModule(paths.workRoot, paths.frameworkRoot, paths.goVersion); err != nil {
		return result, err
	}

	commands := []struct {
		name string
		args []string
	}{{name: "go mod tidy", args: []string{"mod", "tidy"}}}
	for _, check := range checks {
		commands = append(commands, struct {
			name string
			args []string
		}{name: "go " + string(check), args: []string{string(check), "./..."}})
	}
	for _, command := range commands {
		commandResult, runErr := runGo(ctx, paths.workRoot, command.name, command.args)
		result.Commands = append(result.Commands, commandResult)
		if runErr != nil {
			return result, fmt.Errorf("%s: %w", command.name, runErr)
		}
	}
	return result, nil
}

type consumerPaths struct {
	fixtureRoot   string
	frameworkRoot string
	workRoot      string
	goVersion     string
}

func resolveConsumerPaths(request ConsumerRequest) (consumerPaths, error) {
	frameworkRoot, err := canonicalExistingDirectory("framework root", request.FrameworkRoot)
	if err != nil {
		return consumerPaths{}, err
	}
	fixtureRoot, err := canonicalExistingDirectory("fixture root", request.FixtureRoot)
	if err != nil {
		return consumerPaths{}, err
	}
	workRoot, err := canonicalAbsentPath("work root", request.WorkRoot)
	if err != nil {
		return consumerPaths{}, err
	}
	if pathsOverlap(workRoot, fixtureRoot) {
		return consumerPaths{}, fmt.Errorf("work root overlaps fixture root")
	}
	if pathsOverlap(workRoot, frameworkRoot) {
		return consumerPaths{}, fmt.Errorf("work root overlaps framework root")
	}
	goVersion, err := frameworkGoVersion(frameworkRoot)
	if err != nil {
		return consumerPaths{}, err
	}
	return consumerPaths{
		fixtureRoot: fixtureRoot, frameworkRoot: frameworkRoot, workRoot: workRoot, goVersion: goVersion,
	}, nil
}

func canonicalExistingDirectory(label, root string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("%s must be a clean absolute path", label)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	canonical = filepath.Clean(canonical)
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s must be an existing directory", label)
	}
	return canonical, nil
}

func canonicalAbsentPath(label, root string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("%s must be a clean absolute path", label)
	}
	if _, err := os.Lstat(root); err == nil {
		return "", fmt.Errorf("%s already exists", label)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	parent, err := canonicalExistingDirectory(label+" parent", filepath.Dir(root))
	if err != nil {
		return "", err
	}
	canonical := filepath.Join(parent, filepath.Base(root))
	if _, err := os.Lstat(canonical); err == nil {
		return "", fmt.Errorf("%s already exists", label)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	return canonical, nil
}

func pathsOverlap(left, right string) bool {
	return pathContains(left, right) || pathContains(right, left)
}

func pathContains(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func frameworkGoVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("framework root go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != frameworkModulePath || parsed.Go == nil {
		return "", fmt.Errorf("framework root is not the %s module", frameworkModulePath)
	}
	return parsed.Go.Version, nil
}

func normalizeChecks(checks []Check) ([]Check, error) {
	if len(checks) == 0 {
		return []Check{CheckTest, CheckBuild, CheckVet}, nil
	}
	seen := make(map[Check]struct{}, len(checks))
	result := make([]Check, 0, len(checks))
	for _, check := range checks {
		if check != CheckTest && check != CheckBuild && check != CheckVet {
			return nil, fmt.Errorf("unsupported Go check %q", check)
		}
		if _, duplicate := seen[check]; duplicate {
			return nil, fmt.Errorf("duplicate Go check %q", check)
		}
		seen[check] = struct{}{}
		result = append(result, check)
	}
	return result, nil
}

func copyFixture(sourceRoot, targetRoot string) error {
	return filepath.WalkDir(sourceRoot, func(source string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk fixture: %w", walkErr)
		}
		relative, err := filepath.Rel(sourceRoot, source)
		if err != nil {
			return fmt.Errorf("locate fixture entry: %w", err)
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture symlink is not supported: %s", relative)
		}
		target := filepath.Join(targetRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("fixture entry is not a regular file: %s", relative)
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("read fixture %s: %w", relative, err)
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return fmt.Errorf("write fixture %s: %w", relative, err)
		}
		return nil
	})
}

func writeConsumerModule(workRoot, frameworkRoot, goVersion string) error {
	module := new(modfile.File)
	if err := module.AddModuleStmt("nexa.dev/adoption-consumer"); err != nil {
		return fmt.Errorf("set consumer module: %w", err)
	}
	if err := module.AddGoStmt(goVersion); err != nil {
		return fmt.Errorf("set consumer Go version: %w", err)
	}
	if err := module.AddRequire(frameworkModulePath, "v0.0.0"); err != nil {
		return fmt.Errorf("require framework module: %w", err)
	}
	if err := module.AddReplace(frameworkModulePath, "", frameworkRoot, ""); err != nil {
		return fmt.Errorf("replace framework module: %w", err)
	}
	formatted, err := module.Format()
	if err != nil {
		return fmt.Errorf("format consumer go.mod: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workRoot, "go.mod"), formatted, 0o644); err != nil {
		return fmt.Errorf("write consumer go.mod: %w", err)
	}
	return nil
}

func runGo(ctx context.Context, workRoot, name string, args []string) (CommandResult, error) {
	binary, err := exec.LookPath("go")
	if err != nil {
		return CommandResult{Name: name, Binary: "go", Args: append([]string(nil), args...), ExitCode: -1}, err
	}
	return runCommand(ctx, workRoot, name, binary, args, isolatedGoEnvironment(os.Environ()))
}

func runCommand(ctx context.Context, workRoot, name, binary string, args, environment []string) (CommandResult, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = workRoot
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result := CommandResult{
		Name: name, Binary: binary, Args: append([]string(nil), args...), ExitCode: 0,
		Stdout: stdout.String(), Stderr: stderr.String(),
	}
	if err != nil {
		result.ExitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
	}
	return result, err
}

func countArgument(args []string, want string) int {
	count := 0
	for _, arg := range args {
		if arg == want {
			count++
		}
	}
	return count
}

func isolatedGoEnvironment(environment []string) []string {
	overrides := map[string]string{"GOWORK": "off", "GOENV": "off", "GOTOOLCHAIN": "local"}
	result := make([]string, 0, len(environment)+len(overrides))
	for _, row := range environment {
		name, _, found := strings.Cut(row, "=")
		if _, overridden := overrides[name]; found && overridden {
			continue
		}
		result = append(result, row)
	}
	for _, name := range []string{"GOWORK", "GOENV", "GOTOOLCHAIN"} {
		result = append(result, name+"="+overrides[name])
	}
	return result
}
