package nexactl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
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

func TestReferenceSourcePluginReceivesHostVersion(t *testing.T) {
	previousVersion := buildVersion
	buildVersion = "v0.8.0-test"
	t.Cleanup(func() { buildVersion = previousVersion })

	var received string
	var stdout, stderr bytes.Buffer
	exit := runWithReferenceSourcePlugin(
		[]string{"inspect", "--json"}, &stdout, &stderr,
		func(version string) (plugin.Plugin, func(), error) {
			received = version
			candidate, err := plugin.NewStatic(plugin.Spec{Descriptor: plugin.Descriptor{
				ID: "source", Version: version, ContractVersion: plugin.ContractVersion,
				Provides: []plugin.Capability{{ID: sourceadapter.CapabilityID, Version: sourceadapter.CapabilityVersion}},
			}})
			return candidate, func() {}, err
		},
	)
	if exit != 0 || stderr.String() != "" || received != "v0.8.0-test" {
		t.Fatalf("exit=%d stderr=%q source version=%q", exit, stderr.String(), received)
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
	candidate, cleanup, err := newReferenceSourcePluginWithCache("v0.0.0-test", referenceSourceTestCache(t))
	if err != nil {
		var projected *release.Error
		if errors.As(err, &projected) {
			t.Fatalf("source init code=%s reason=%s pointer=%s stage=%s", projected.Code(), projected.Reason(), projected.Pointer(), projected.Stage())
		}
		t.Fatal(err)
	}
	defer cleanup()
	spec := candidate.Spec()
	if spec.Descriptor.ID != "source" || spec.Descriptor.Version != "v0.0.0-test" || len(spec.Descriptor.Provides) != 1 || spec.Descriptor.Provides[0].ID != "source.bundle" {
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
	options, provider, cleanup, err := newReferenceSourceDependencies("v0.0.0-test", referenceSourceTestCache(t))
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
				func(string) (plugin.Plugin, func(), error) {
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
	options, provider, cleanup, err := newReferenceSourceDependencies("v0.0.0-test", cacheRoot)
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

	reopened, _, secondCleanup, err := newReferenceSourceDependencies("v0.0.0-test", cacheRoot)
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
