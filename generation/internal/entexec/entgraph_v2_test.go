package entexec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
