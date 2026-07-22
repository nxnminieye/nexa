// Package bundletest executes authored source bundles in isolated Go modules.
package bundletest

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

type Module struct {
	Path         string
	Source       fs.FS
	Requirements map[string]string
}

type Options struct {
	Race bool
}

func Run(ctx context.Context, spec Module) error {
	return RunWithOptions(ctx, spec, Options{})
}

func RunWithOptions(ctx context.Context, spec Module, options Options) error {
	if ctx == nil {
		return fmt.Errorf("bundle module: nil context")
	}
	if err := module.CheckPath(spec.Path); err != nil {
		return fmt.Errorf("bundle module: invalid module path: %w", err)
	}
	if spec.Source == nil {
		return fmt.Errorf("bundle module: nil source")
	}

	root, err := os.MkdirTemp("", "nexa-bundle-test-")
	if err != nil {
		return fmt.Errorf("bundle module: create temporary module: %w", err)
	}
	defer os.RemoveAll(root)

	if err := os.CopyFS(root, spec.Source); err != nil {
		return fmt.Errorf("bundle module: copy source: %w", err)
	}
	data, err := moduleFile(spec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), data, 0o600); err != nil {
		return fmt.Errorf("bundle module: write go.mod: %w", err)
	}

	output, err := runModuleTests(ctx, root, []string{"./..."}, options, execModuleTestRunner{})
	if err != nil {
		return fmt.Errorf("bundle module: go test: %w\n%s", err, output)
	}
	return nil
}

type MaterializedModule struct {
	Root     string
	Packages []string
}

func RunMaterialized(ctx context.Context, spec MaterializedModule, options Options) error {
	if ctx == nil {
		return fmt.Errorf("materialized module: nil context")
	}
	if spec.Root == "" {
		return fmt.Errorf("materialized module: empty root")
	}
	if len(spec.Packages) == 0 {
		return fmt.Errorf("materialized module: no packages")
	}
	output, err := runModuleTests(ctx, spec.Root, spec.Packages, options, execModuleTestRunner{})
	if err != nil {
		return fmt.Errorf("materialized module: go test: %w\n%s", err, output)
	}
	return nil
}

type moduleTestCommand struct {
	Arguments   []string
	Directory   string
	Environment []string
}

type moduleTestRunner interface {
	Run(context.Context, moduleTestCommand) ([]byte, error)
}

type execModuleTestRunner struct{}

func (execModuleTestRunner) Run(ctx context.Context, spec moduleTestCommand) ([]byte, error) {
	command := exec.CommandContext(ctx, "go", spec.Arguments...)
	command.Dir = spec.Directory
	command.Env = spec.Environment
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.Bytes(), err
}

func runModuleTests(ctx context.Context, root string, packages []string, options Options, runner moduleTestRunner) ([]byte, error) {
	arguments := []string{"test"}
	if options.Race {
		arguments = append(arguments, "-race")
	}
	arguments = append(arguments, "-mod=mod")
	arguments = append(arguments, packages...)
	return runner.Run(ctx, moduleTestCommand{
		Arguments: arguments,
		Directory: root,
		Environment: commandEnvironment(map[string]string{
			"GOENV": "off", "GOFLAGS": "", "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
		}),
	})
}

func commandEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

func moduleFile(spec Module) ([]byte, error) {
	file := new(modfile.File)
	if err := file.AddModuleStmt(spec.Path); err != nil {
		return nil, fmt.Errorf("bundle module: module statement: %w", err)
	}
	if err := file.AddGoStmt("1.25.0"); err != nil {
		return nil, fmt.Errorf("bundle module: go statement: %w", err)
	}
	paths := make([]string, 0, len(spec.Requirements))
	for path := range spec.Requirements {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		version := spec.Requirements[path]
		if err := module.Check(path, version); err != nil {
			return nil, fmt.Errorf("bundle module: invalid requirement %s: %w", path, err)
		}
		if err := file.AddRequire(path, version); err != nil {
			return nil, fmt.Errorf("bundle module: add requirement %s: %w", path, err)
		}
	}
	data, err := file.Format()
	if err != nil {
		return nil, fmt.Errorf("bundle module: format go.mod: %w", err)
	}
	return data, nil
}
