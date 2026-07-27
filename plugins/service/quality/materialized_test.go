package quality

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nxnminieye/nexa/internal/bundletest"
)

func TestMaterializedBackendExecutesInExternalModule(t *testing.T) {
	provider, err := New()
	if err != nil {
		t.Fatal(err)
	}
	closure, err := provider.Manifest().ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot := t.TempDir()
	targetRoot := filepath.Join(moduleRoot, "framework", "quality")
	for _, file := range closure.Files() {
		treeFile, ok := provider.Tree().Lookup(file.Path())
		if !ok {
			t.Fatalf("tree file %q missing", file.Path())
		}
		name := filepath.Join(targetRoot, filepath.FromSlash(file.Path()))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, treeFile.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	repositoryRoot, err := filepath.EvalSymlinks(qualityRepositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	canonicalModuleRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := filepath.Rel(canonicalModuleRoot, repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module corp.example/independent/consumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\nreplace github.com/nxnminieye/nexa => %s\n", filepath.ToSlash(replacement))
	if err := os.WriteFile(filepath.Join(moduleRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := bundletest.RunMaterialized(t.Context(), bundletest.MaterializedModule{
		Root: moduleRoot, Packages: []string{"./..."},
	}, bundletest.Options{Race: bundletest.RaceEnabled()}); err != nil {
		t.Fatal(err)
	}
}

func qualityRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(filename), "../../.."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
