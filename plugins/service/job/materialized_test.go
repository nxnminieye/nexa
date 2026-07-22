package job_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/internal/bundletest"
	jobplugin "github.com/nxnminieye/nexa/plugins/service/job"
	"github.com/nxnminieye/nexa/sourceplugin"
)

func TestMaterializedBackendCompilesAuthoredFactsAndBehavior(t *testing.T) {
	provider, err := jobplugin.New()
	if err != nil {
		t.Fatal(err)
	}
	manifest := provider.Manifest()
	closure, err := manifest.ResolveProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, declared := range closure.Files() {
		file, ok := provider.Tree().Lookup(declared.Path())
		if !ok {
			t.Fatalf("profile file %q missing from tree", declared.Path())
		}
		destination := filepath.Join(root, filepath.FromSlash(declared.Path()))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, file.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repositoryRoot := frameworkRoot(t)
	module := fmt.Sprintf(`module example.com/materialized-job

go 1.25.0

require (
	entgo.io/ent v0.14.5
	github.com/nxnminieye/nexa v0.0.0
	github.com/robfig/cron/v3 v3.0.1
)

replace github.com/nxnminieye/nexa => %s
`, filepath.ToSlash(repositoryRoot))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bundletest.RunMaterialized(t.Context(), bundletest.MaterializedModule{
		Root: root, Packages: []string{"./backend/job/..."},
	}, bundletest.Options{Race: bundletest.RaceEnabled()}); err != nil {
		t.Fatal(err)
	}

	compiled, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "job", EntryFiles: []string{"job.proto"},
		Resolver: treeResolver{tree: provider.Tree(), base: "backend/job/desc/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, ok := compiled.Service("nexa.job.v1.JobService")
	if !ok || len(service.Methods()) != 2 {
		t.Fatalf("compiled JobService = %#v, ok=%v", service, ok)
	}
}

type treeResolver struct {
	tree sourceplugin.Tree
	base string
}

func (r treeResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, ok := r.tree.Lookup(r.base + path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(file.Bytes())), nil
}

func frameworkRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
