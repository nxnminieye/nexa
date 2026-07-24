package entexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/entipc"
)

func TestRunEntGraphV2RejectsCoordinateMismatchBeforeTempOrProcess(t *testing.T) {
	repository := canonicalDirectory(t, filepath.Clean(filepath.Join(testFileDirV2(t), "../../..")))
	request, err := entipc.NewRequestV2(entipc.RequestV2Spec{ModuleDir: ".", ModulePath: "github.com/nxnminieye/nexa", SchemaDir: "generation/internal/entityload", BuildTags: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	requestBytes, _ := entipc.CanonicalRequestV2(request)
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, _ = filepath.EvalSymlinks(goExecutable)
	goCache, moduleCache := goCachesV2(t)
	tempBase := t.TempDir()
	called := false
	_, err = RunEntGraphV2(context.Background(), EntGraphProcessSpec{RepositoryRoot: repository, ModuleDir: ".", ModulePath: "github.com/nxnminieye/nexa", SchemaDir: "generation/internal/entityload", GoExecutable: goExecutable, ExpectedVersion: "unused", Request: requestBytes, BuildTags: []string{"b"}, GOCACHE: goCache, GOMODCACHE: moduleCache, TempBase: tempBase, BaseEnvironment: os.Environ(), processHook: func(processEvent) { called = true }})
	if err == nil {
		t.Fatal("coordinate mismatch accepted")
	}
	if called {
		t.Fatal("process launched before coordinate validation")
	}
	entries, readErr := os.ReadDir(tempBase)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temp mutated: %v", entries)
	}
}

func TestRunEntGraphV2CleanupFailureOverridesSuccessWithoutRawError(t *testing.T) {
	repository := canonicalDirectory(t, filepath.Clean(filepath.Join(testFileDirV2(t), "../../..")))
	request, err := entipc.NewRequestV2(entipc.RequestV2Spec{ModuleDir: ".", ModulePath: "github.com/nxnminieye/nexa", SchemaDir: "generation/internal/entityload", BuildTags: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	requestBytes, _ := entipc.CanonicalRequestV2(request)
	goExecutable := filepath.Join(canonicalDirectory(t, t.TempDir()), "fake-go")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf 'go version fake\\n'; else printf '{}'; fi\n"
	if err := os.WriteFile(goExecutable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	goCache, moduleCache := goCachesV2(t)
	tempBase := canonicalDirectory(t, t.TempDir())
	marker := "secret-cleanup-marker"
	_, err = RunEntGraphV2(context.Background(), EntGraphProcessSpec{RepositoryRoot: repository, ModuleDir: ".", ModulePath: "github.com/nxnminieye/nexa", SchemaDir: "generation/internal/entityload", GoExecutable: goExecutable, ExpectedVersion: "go version fake", Request: requestBytes, GOCACHE: goCache, GOMODCACHE: moduleCache, TempBase: tempBase, BaseEnvironment: os.Environ(), cleanupHook: func(string) error { return errors.New(marker) }})
	if err == nil {
		t.Fatal("cleanup failure suppressed")
	}
	if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), tempBase) {
		t.Fatalf("raw cleanup error leaked: %v", err)
	}
	typed, ok := err.(*EntGraphError)
	if !ok || typed.Code() != "entity_graph_load_failed" || typed.Reason() != "helper_cleanup_failed" || typed.Source() != "generation/internal/entityload" {
		t.Fatalf("cleanup error = %T %#v", err, err)
	}
}

func TestInvocationV2ClosesAdversarialGoEnvironment(t *testing.T) {
	repository := canonicalDirectory(t, filepath.Clean(filepath.Join(testFileDirV2(t), "../../..")))
	goCache, moduleCache := goCachesV2(t)
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ambient := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + repository, "XDG_CONFIG_HOME=" + repository,
		"TEST_TELEMETRY_DIR=" + repository, "GOFLAGS=-modfile=evil.mod -overlay=evil.json -tags=evil",
		"GOTMPDIR=" + repository, "TMPDIR=" + repository, "GOMOD=evil", "GO111MODULE=off",
		"GOEXPERIMENT=evil", "GOCACHEPROG=evil", "GOTOOLDIR=evil", "GOPACKAGESDRIVER=evil",
	}
	invocation, err := PrepareInvocationV2(repository, goCache, moduleCache, base, ambient)
	if err != nil {
		t.Fatal(err)
	}
	root := invocation.Root()
	values := envMapV2(invocation.Environment())
	want := map[string]string{"HOME": filepath.Join(root, "home"), "XDG_CONFIG_HOME": filepath.Join(root, "config"), "TEST_TELEMETRY_DIR": filepath.Join(root, "telemetry"), "GOTMPDIR": filepath.Join(root, "toolchain-tmp"), "TMPDIR": filepath.Join(root, "toolchain-tmp"), "GOWORK": "off", "GOENV": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "", "GOMOD": "", "GO111MODULE": "on", "GOEXPERIMENT": "", "GOCACHEPROG": "", "GOTOOLDIR": "", "GOPACKAGESDRIVER": "off"}
	for name, expected := range want {
		if values[name] != expected {
			t.Fatalf("%s = %q, want %q", name, values[name], expected)
		}
	}
	if err := invocation.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("invocation residue: %v", err)
	}
}

func TestInvocationV2RejectsSymlinkedTempBaseBeforeCreation(t *testing.T) {
	repository := canonicalDirectory(t, filepath.Clean(filepath.Join(testFileDirV2(t), "../../..")))
	goCache, moduleCache := goCachesV2(t)
	target := t.TempDir()
	link := filepath.Join(filepath.Dir(target), filepath.Base(target)+"-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	if _, err := PrepareInvocationV2(repository, goCache, moduleCache, link, os.Environ()); err == nil {
		t.Fatal("symlinked temp base accepted")
	}
}

func goCachesV2(t *testing.T) (string, string) {
	t.Helper()
	goCache := os.Getenv("GOCACHE")
	moduleCache := os.Getenv("GOMODCACHE")
	if goCache == "" {
		goCache = filepath.Join(os.Getenv("HOME"), "Library/Caches/go-build")
	}
	if moduleCache == "" {
		moduleCache = filepath.Join(os.Getenv("HOME"), "go/pkg/mod")
	}
	var err error
	goCache, err = filepath.EvalSymlinks(goCache)
	if err != nil {
		t.Fatal(err)
	}
	moduleCache, err = filepath.EvalSymlinks(moduleCache)
	if err != nil {
		t.Fatal(err)
	}
	return goCache, moduleCache
}
func testFileDirV2(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Dir(file)
}
