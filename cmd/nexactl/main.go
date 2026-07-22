package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
	"github.com/nxnminieye/nexa/plugins/nexactl/governance"
	sdkpythonplugin "github.com/nxnminieye/nexa/plugins/nexactl/sdkpython"
	sourceadapter "github.com/nxnminieye/nexa/plugins/nexactl/source"
	core "github.com/nxnminieye/nexa/plugins/service/core"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

var buildVersion = "v0.0.0-dev"

type referenceSourcePluginFactory func() (plugin.Plugin, func(), error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithReferenceSourcePlugin(args, stdout, stderr, newReferenceSourcePlugin)
}

func runWithReferenceSourcePlugin(args []string, stdout, stderr io.Writer, sourceFactory referenceSourcePluginFactory) int {
	governancePlugin, err := governance.New()
	if err != nil {
		return writeBootstrapFailure(args, stdout, stderr)
	}
	generationPlugin, err := generation.New(generation.Options{})
	if err != nil {
		return writeBootstrapFailure(args, stdout, stderr)
	}
	sdkPythonPlugin, err := sdkpythonplugin.New(sdkpythonplugin.Options{})
	if err != nil {
		return writeBootstrapFailure(args, stdout, stderr)
	}
	if sourceFactory == nil {
		return writeBootstrapFailure(args, stdout, stderr)
	}
	sourcePlugin, cleanup, err := sourceFactory()
	if err != nil {
		return writeBootstrapFailure(args, stdout, stderr)
	}
	defer cleanup()
	composed, err := host.New(host.Options{Version: buildVersion}, governancePlugin, generationPlugin, sdkPythonPlugin, sourcePlugin)
	if err != nil {
		return writeBootstrapFailure(args, stdout, stderr)
	}
	return composed.Execute(context.Background(), args, stdout, stderr)
}

func newReferenceSourcePlugin() (plugin.Plugin, func(), error) {
	cacheRoot, err := referenceSourceCacheRoot()
	if err != nil {
		return nil, func() {}, err
	}
	return newReferenceSourcePluginWithCache(cacheRoot)
}

func newReferenceSourcePluginWithCache(cacheRoot string) (plugin.Plugin, func(), error) {
	options, provider, cleanup, err := newReferenceSourceDependencies(cacheRoot)
	if err != nil {
		return nil, func() {}, err
	}
	candidate, err := composeReferenceSourcePlugin(options, provider)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return candidate, cleanup, nil
}

func newReferenceSourceDependencies(cacheRoot string) (sourceadapter.Options, sourceplugin.Provider, func(), error) {
	workingRoot, err := os.MkdirTemp("", "nexa-reference-source-")
	if err != nil {
		return sourceadapter.Options{}, nil, func() {}, err
	}
	createdRoot := workingRoot
	workingRoot, err = filepath.EvalSymlinks(createdRoot)
	if err != nil {
		_ = os.RemoveAll(createdRoot)
		return sourceadapter.Options{}, nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(workingRoot) }
	fail := func(err error) (sourceadapter.Options, sourceplugin.Provider, func(), error) {
		cleanup()
		return sourceadapter.Options{}, nil, func() {}, err
	}

	directories := map[string]string{
		"home": filepath.Join(workingRoot, "home"), "temp": filepath.Join(workingRoot, "temp"),
		"gopath": filepath.Join(workingRoot, "gopath"), "build-cache": filepath.Join(workingRoot, "build-cache"),
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fail(err)
		}
	}

	goExecutable, err := absoluteExecutable("go")
	if err != nil {
		return fail(err)
	}
	moduleCacheOutput, err := exec.Command(goExecutable, "env", "GOMODCACHE").Output()
	if err != nil {
		return fail(err)
	}
	moduleCache := strings.TrimSpace(string(moduleCacheOutput))
	if !filepath.IsAbs(moduleCache) {
		return fail(fmt.Errorf("Go module cache is not absolute"))
	}
	if err := os.MkdirAll(moduleCache, 0o755); err != nil {
		return fail(err)
	}

	cacheLimits := release.DefaultCacheLimits()
	cache, err := release.OpenDirectoryCache(cacheRoot, cacheLimits)
	if err != nil {
		return fail(err)
	}
	provider, err := core.New()
	if err != nil {
		return fail(err)
	}
	return sourceadapter.Options{
		Version: buildVersion, Cache: cache, CacheLimits: cacheLimits, TreeLimits: cacheLimits.Tree,
		LockLimits: lock.DefaultLimits(), MergeDriver: newReferenceMergeDriver(), Executor: engine.NewOSExecutor(),
		GoToolchain: engine.GoToolchain{
			Executable: goExecutable, Home: directories["home"], TempDir: directories["temp"],
			GOPATH: directories["gopath"], ModuleCache: moduleCache, BuildCache: directories["build-cache"],
		},
	}, provider, cleanup, nil
}

type referenceMergeDriver struct{}

func newReferenceMergeDriver() engine.MergeDriver { return referenceMergeDriver{} }

func (referenceMergeDriver) Merge(context.Context, engine.TextMergeInput) (engine.TextMergeResult, error) {
	return engine.NewTextMergeResult(nil, false), nil
}

func referenceSourceCacheRoot() (string, error) {
	if root, configured := os.LookupEnv("NEXA_SOURCE_CACHE"); configured && root != "" {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return "", fmt.Errorf("reference source cache root is invalid")
		}
		return root, nil
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root = filepath.Join(root, "nexa", "source", "releases")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("reference source cache root is invalid")
	}
	return root, nil
}

func composeReferenceSourcePlugin(options sourceadapter.Options, provider sourceplugin.Provider) (plugin.Plugin, error) {
	if options.Version == "" {
		return nil, fmt.Errorf("reference source version identity is required")
	}
	if options.Cache == nil {
		return nil, fmt.Errorf("reference source release cache is required")
	}
	if options.TreeLimits == (sourceplugin.TreeLimits{}) {
		return nil, fmt.Errorf("reference source tree limits are required")
	}
	if options.MergeDriver == nil {
		return nil, fmt.Errorf("reference source merge driver is required")
	}
	if options.Executor == nil {
		return nil, fmt.Errorf("reference source executor is required")
	}
	toolchain := options.GoToolchain
	if toolchain.Executable == "" || toolchain.Home == "" || toolchain.TempDir == "" ||
		toolchain.GOPATH == "" || toolchain.ModuleCache == "" || toolchain.BuildCache == "" {
		return nil, fmt.Errorf("reference source isolated Go toolchain is required")
	}
	return sourceadapter.New(options, provider)
}

func absoluteExecutable(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
	}
	return filepath.EvalSymlinks(path)
}

func writeBootstrapFailure(args []string, stdout, stderr io.Writer) int {
	operationID := protocol.SentinelOperationID
	bootstrapError := protocol.NewError(
		"host_initialization_failed",
		"nexactl.bootstrap",
		protocol.CategoryInternal,
		"nexactl host could not be initialized",
		"",
	)

	var buffer bytes.Buffer
	if err := protocol.Encode(
		&buffer,
		protocol.Failure(operationID, bootstrapError),
		protocol.CompactJSONRequested(args),
	); err != nil {
		writeBootstrapDiagnostic(stderr, operationID, "failure_envelope_encoding_failed")
		return 70
	}
	if stdout == nil {
		writeBootstrapDiagnostic(stderr, operationID, "stdout_write_failed")
		return 70
	}
	written, err := stdout.Write(buffer.Bytes())
	if err != nil || written != buffer.Len() {
		writeBootstrapDiagnostic(stderr, operationID, "stdout_write_failed")
		return 70
	}
	writeBootstrapDiagnostic(stderr, operationID, "host_initialization_failed")
	return protocol.ExitStatus(bootstrapError)
}

func writeBootstrapDiagnostic(stderr io.Writer, operationID, failureClass string) {
	if stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(
		stderr,
		"nexactl.bootstrap operation=%s failure=%s\n",
		operationID,
		failureClass,
	)
}
