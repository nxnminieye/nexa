package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	sourceadapter "github.com/nxnminieye/nexa/plugins/nexactl/source"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestResolveBuildVersionUsesExplicitNonDevVersionFirst(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/nxnminieye/nexa", Version: "v0.2.0"}}
	if got := resolveBuildVersion("v9.1.0", info, true); got != "v9.1.0" {
		t.Fatalf("resolveBuildVersion() = %q, want explicit version", got)
	}
}

func TestResolveBuildVersionFindsNexaModuleInMainOrDependencies(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{
			name: "main module",
			info: &debug.BuildInfo{Main: debug.Module{Path: "github.com/nxnminieye/nexa", Version: "v0.2.0"}},
			want: "v0.2.0",
		},
		{
			name: "dependency module",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/consumer", Version: "v1.0.0"},
				Deps: []*debug.Module{
					{Path: "example.com/other", Version: "v1.0.0"},
					{Path: "github.com/nxnminieye/nexa", Version: "v0.3.0-alpha.2"},
				},
			},
			want: "v0.3.0-alpha.2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveBuildVersion("v0.0.0-dev", tt.info, true); got != tt.want {
				t.Fatalf("resolveBuildVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveBuildVersionKeepsDevWhenNexaModuleVersionIsUnavailable(t *testing.T) {
	for _, input := range []struct {
		info      *debug.BuildInfo
		available bool
	}{
		{info: nil, available: false},
		{info: &debug.BuildInfo{Main: debug.Module{Path: "github.com/nxnminieye/nexa", Version: "(devel)"}}, available: true},
		{info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/consumer", Version: "v1.0.0"}}, available: true},
	} {
		if got := resolveBuildVersion("v0.0.0-dev", input.info, input.available); got != "v0.0.0-dev" {
			t.Fatalf("resolveBuildVersion() = %q, want dev fallback", got)
		}
	}
}

func TestReferenceSourceCompositionDoesNotSelectGitMerge(t *testing.T) {
	result, err := newReferenceMergeDriver().Merge(context.Background(), engine.TextMergeInput{
		Old: []byte("old\n"), Local: []byte("local\n"), New: []byte("new\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Clean() || len(result.Bytes()) != 0 {
		t.Fatalf("reference merge result = clean:%v bytes:%q", result.Clean(), result.Bytes())
	}
}

func TestReferenceSourcePluginInitializesWithCoreOnly(t *testing.T) {
	candidate, cleanup, err := newReferenceSourcePluginWithCache(referenceSourceTestCache(t))
	if err != nil {
		var projected *release.Error
		if errors.As(err, &projected) {
			t.Fatalf("source init code=%s reason=%s pointer=%s stage=%s", projected.Code(), projected.Reason(), projected.Pointer(), projected.Stage())
		}
		t.Fatal(err)
	}
	defer cleanup()
	spec := candidate.Spec()
	if spec.Descriptor.ID != "source" || len(spec.Descriptor.Provides) != 1 || spec.Descriptor.Provides[0].ID != "source.bundle" {
		t.Fatalf("source descriptor = %#v", spec.Descriptor)
	}
	var paths [][]string
	for _, command := range spec.Commands {
		paths = append(paths, command.Path)
	}
	want := [][]string{{"source", "plan"}, {"source", "materialize"}, {"source", "status"}, {"source", "diff"}, {"source", "upgrade"}, {"source", "detach"}, {"source", "check"}}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("source commands = %#v", paths)
	}
}

func TestReferenceSourcePluginUsesStableUserCacheRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("NEXA_SOURCE_CACHE", "")
	cacheRoot, err := referenceSourceCacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(userCache, "nexa", "source", "releases")
	if cacheRoot != want {
		t.Fatalf("cache root=%q, want %q", cacheRoot, want)
	}

	override := filepath.Join(root, "explicit-release-cache")
	t.Setenv("NEXA_SOURCE_CACHE", override)
	cacheRoot, err = referenceSourceCacheRoot()
	if err != nil {
		t.Fatal(err)
	}
	if cacheRoot != override {
		t.Fatalf("explicit cache root=%q, want %q", cacheRoot, override)
	}
}

func TestReferenceSourceCompositionRejectsMissingRequiredDependencies(t *testing.T) {
	options, provider, cleanup, err := newReferenceSourceDependencies(referenceSourceTestCache(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	tests := []struct {
		name   string
		mutate func(sourceadapter.Options) sourceadapter.Options
	}{
		{name: "version identity", mutate: func(value sourceadapter.Options) sourceadapter.Options { value.Version = ""; return value }},
		{name: "release cache", mutate: func(value sourceadapter.Options) sourceadapter.Options { value.Cache = nil; return value }},
		{name: "tree limits", mutate: func(value sourceadapter.Options) sourceadapter.Options {
			value.TreeLimits = sourceplugin.TreeLimits{}
			return value
		}},
		{name: "merge driver", mutate: func(value sourceadapter.Options) sourceadapter.Options { value.MergeDriver = nil; return value }},
		{name: "executor", mutate: func(value sourceadapter.Options) sourceadapter.Options { value.Executor = nil; return value }},
		{name: "go toolchain", mutate: func(value sourceadapter.Options) sourceadapter.Options {
			value.GoToolchain = engine.GoToolchain{}
			return value
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := composeReferenceSourcePlugin(test.mutate(options), provider)
			if err == nil || candidate != nil {
				t.Fatalf("candidate=%#v err=%v", candidate, err)
			}

			var stdout, stderr bytes.Buffer
			exit := runWithReferenceSourcePlugin(
				[]string{"inspect", "--json"}, &stdout, &stderr,
				func() (plugin.Plugin, func(), error) {
					candidate, err := composeReferenceSourcePlugin(test.mutate(options), provider)
					return candidate, func() {}, err
				},
			)
			if exit != 70 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			}
			var envelope protocol.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.OK || envelope.OperationID != protocol.SentinelOperationID || envelope.Error == nil ||
				envelope.Error.Code != "host_initialization_failed" || envelope.Result != nil {
				t.Fatalf("envelope=%#v", envelope)
			}
			wantDiagnostic := "nexactl.bootstrap operation=" + protocol.SentinelOperationID + " failure=host_initialization_failed\n"
			if stderr.String() != wantDiagnostic {
				t.Fatalf("stderr=%q, want %q", stderr.String(), wantDiagnostic)
			}
		})
	}
}

func TestReferenceSourceCacheSurvivesCompositionCleanup(t *testing.T) {
	cacheRoot := referenceSourceTestCache(t)
	options, provider, cleanup, err := newReferenceSourceDependencies(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.Cache.Store(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	cleanup()

	reopened, _, secondCleanup, err := newReferenceSourceDependencies(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer secondCleanup()
	cacheOnly, err := release.NewExactResolver(reopened.Cache)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := cacheOnly.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Ref().Equal(ref) {
		t.Fatalf("loaded ref=%#v want=%#v", loaded.Ref(), ref)
	}
}

func referenceSourceTestCache(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "release-cache")
}

func TestBootstrapFailureWithRejectedStdoutUsesStableDiagnostic(t *testing.T) {
	previousVersion := buildVersion
	buildVersion = "secret-invalid-version"
	t.Cleanup(func() { buildVersion = previousVersion })

	tests := []struct {
		name   string
		stdout io.Writer
	}{
		{name: "write error", stdout: bootstrapRejectingWriter{}},
		{name: "short write", stdout: bootstrapShortWriter{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			exit := run([]string{"inspect", "--json"}, tt.stdout, &stderr)
			if exit != 70 {
				t.Fatalf("exit = %d", exit)
			}
			want := "nexactl.bootstrap operation=" + protocol.SentinelOperationID + " failure=stdout_write_failed\n"
			if stderr.String() != want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), want)
			}
			if strings.Contains(stderr.String(), "secret-invalid-version") || strings.Contains(stderr.String(), "secret stdout failure") {
				t.Fatalf("raw failure leaked: %q", stderr.String())
			}
		})
	}
}

type bootstrapRejectingWriter struct{}

func (bootstrapRejectingWriter) Write([]byte) (int, error) {
	return 0, errors.New("secret stdout failure")
}

type bootstrapShortWriter struct{}

func (bootstrapShortWriter) Write(payload []byte) (int, error) {
	return len(payload) - 1, nil
}

func TestSDKPythonAssetInspectionAndProductionCommands(t *testing.T) {
	inspection, exit, stderr := executeSDKPythonProductionSubprocess(t, []string{"inspect", "--json"})
	if exit != 0 || stderr != "" || !inspection.OK || !protocol.IsValidOperationID(inspection.OperationID) {
		t.Fatalf("inspect exit=%d stderr=%q envelope=%#v", exit, stderr, inspection)
	}
	var result struct {
		Plugins []struct {
			ID              string              `json:"id"`
			Version         string              `json:"version"`
			ContractVersion string              `json:"contractVersion"`
			Provides        []plugin.Capability `json:"provides"`
			Requires        []plugin.Capability `json:"requires"`
		} `json:"plugins"`
		Commands []struct {
			Path          []string          `json:"path"`
			Flags         []plugin.FlagSpec `json:"flags"`
			SideEffect    plugin.SideEffect `json:"sideEffect"`
			OwnerPluginID string            `json:"ownerPluginId"`
		} `json:"commands"`
	}
	decodeEnvelopeResult(t, inspection, &result)
	var foundPlugin bool
	for _, candidate := range result.Plugins {
		if candidate.ID != "sdk-python-assets" {
			continue
		}
		foundPlugin = true
		wantProvides := []plugin.Capability{{ID: "generation.sdk-python-assets", Version: "v1.0.0"}}
		if candidate.Version != "v0.1.0" || candidate.ContractVersion != plugin.ContractVersion ||
			!reflect.DeepEqual(candidate.Provides, wantProvides) || len(candidate.Requires) != 0 {
			t.Fatalf("sdk-python-assets plugin=%#v", candidate)
		}
	}
	if !foundPlugin {
		t.Fatalf("sdk-python-assets plugin missing: %#v", result.Plugins)
	}
	type inspectedCommand struct {
		action     string
		flags      []string
		sideEffect plugin.SideEffect
	}
	var commands []inspectedCommand
	for _, command := range result.Commands {
		if command.OwnerPluginID != "sdk-python-assets" {
			continue
		}
		flags := make([]string, len(command.Flags))
		for index, flag := range command.Flags {
			flags[index] = flag.Name
		}
		commands = append(commands, inspectedCommand{
			action: command.Path[len(command.Path)-1], flags: flags, sideEffect: command.SideEffect,
		})
	}
	wantCommands := []inspectedCommand{
		{action: "check", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryRead},
		{action: "write", flags: []string{"repo-root"}, sideEffect: plugin.SideEffectRepositoryWrite},
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("production sdk-python-assets commands=%#v want=%#v", commands, wantCommands)
	}

	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module github.com/nxnminieye/nexa\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	written, exit, stderr := executeSDKPythonProductionSubprocess(t, []string{
		"generation", "sdk-python-assets", "write", "--repo-root", repo,
		"--json",
	})
	if exit != 0 || stderr != "" || !written.OK {
		t.Fatalf("write exit=%d stderr=%q envelope=%#v", exit, stderr, written)
	}
	var writeResult struct {
		IndexDigest string `json:"indexDigest"`
	}
	decodeEnvelopeResult(t, written, &writeResult)
	if writeResult.IndexDigest == "" {
		t.Fatalf("write result=%#v", writeResult)
	}
	checked, exit, stderr := executeSDKPythonProductionSubprocess(t, []string{
		"generation", "sdk-python-assets", "check", "--repo-root", repo, "--json",
	})
	if exit != 0 || stderr != "" || !checked.OK {
		t.Fatalf("check exit=%d stderr=%q envelope=%#v", exit, stderr, checked)
	}
	var checkResult struct {
		IndexDigest string `json:"indexDigest"`
		Status      string `json:"status"`
	}
	decodeEnvelopeResult(t, checked, &checkResult)
	if checkResult.IndexDigest != writeResult.IndexDigest || checkResult.Status != "clean" {
		t.Fatalf("check result=%#v write=%#v", checkResult, writeResult)
	}

	missingBuild, exit, stderr := executeSDKPythonProductionSubprocess(t, []string{
		"generation", "sdk-python-assets", "build", "--repo-root", repo,
		"--python", "/python", "--matrix-target", "darwin-arm64", "--wheelhouse", "/wheelhouse",
		"--work-dir", "/work", "--out", "/out", "--json",
	})
	if exit != 2 || stderr != "" || missingBuild.OK || missingBuild.Error == nil || missingBuild.Error.Code != "flag_invalid" {
		if missingBuild.Error != nil {
			t.Fatalf("production build exit=%d stderr=%q code=%q domain=%q category=%q", exit, stderr, missingBuild.Error.Code, missingBuild.Error.Domain, missingBuild.Error.Category)
		}
		t.Fatalf("production build exit=%d stderr=%q envelope=%#v", exit, stderr, missingBuild)
	}
}

const sdkPythonSubprocessMode = "NEXA_SDKPYTHON_CMD_SUBPROCESS"

func TestSDKPythonAssetSubprocessHelper(t *testing.T) {
	if os.Getenv(sdkPythonSubprocessMode) != "1" {
		return
	}
	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index + 1
			break
		}
	}
	if separator == 0 {
		t.Fatal("missing subprocess argument separator")
	}
	os.Exit(run(os.Args[separator:], os.Stdout, os.Stderr))
}

func executeSDKPythonProductionSubprocess(t *testing.T, args []string) (protocol.Envelope, int, string) {
	t.Helper()
	cacheParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	commandArgs := append([]string{"-test.run=^TestSDKPythonAssetSubprocessHelper$", "--"}, args...)
	command := exec.Command(os.Args[0], commandArgs...)
	command.Env = append(os.Environ(), sdkPythonSubprocessMode+"=1", "NEXA_SOURCE_CACHE="+filepath.Join(cacheParent, "source-cache"))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	exit := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatal(err)
		}
		exit = exitError.ExitCode()
	}
	var envelope protocol.Envelope
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("args=%v decode stdout=%q: %v", args, stdout.String(), err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("args=%v multiple stdout values=%q", args, stdout.String())
	}
	return envelope, exit, stderr.String()
}

func decodeEnvelopeResult(t *testing.T, envelope protocol.Envelope, target any) {
	t.Helper()
	encoded, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
}
