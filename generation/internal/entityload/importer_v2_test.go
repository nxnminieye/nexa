package entityload

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entexec"
)

func TestImporterV2RealReadonlyOverlayProjectsEntityIR(t *testing.T) {
	frameworkRoot := repositoryRootV2(t)
	repository, err := os.MkdirTemp(filepath.Dir(frameworkRoot), ".entityload-v2-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(repository) })
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module example.com/entityfixture\n\ngo 1.25.0\n\nrequire entgo.io/ent v0.14.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repository, "schema"), 0o700); err != nil {
		t.Fatal(err)
	}
	schema := "package schema\n\nimport (\n\t\"entgo.io/ent\"\n\t\"entgo.io/ent/schema/field\"\n)\n\ntype Account struct{ ent.Schema }\nfunc (Account) Fields() []ent.Field { return []ent.Field{field.String(\"name\")} }\n"
	if err := os.WriteFile(filepath.Join(repository, "schema", "account.go"), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repository, "tools"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "tools", "tools.go"), []byte("package tools\n\nimport _ \"entgo.io/ent/entc/load\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = repository
	tidy.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy fixture: %v\n%s", err, output)
	}
	goCache := goEnvV2(t, "GOCACHE")
	moduleCache := goEnvV2(t, "GOMODCACHE")
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := entexec.PrepareInvocationV2(repository, goCache, moduleCache, base, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := invocation.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	spec := V2Spec{RepositoryRoot: repository, ModuleDir: ".", ModulePath: "example.com/entityfixture", SchemaDir: "schema", BuildTags: []string{}, Environment: invocation.Environment()}
	first, err := DiscoverV2(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DiscoverV2(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Source, second.Source) || strings.Join(first.TypeNames, ",") != "Account" {
		t.Fatalf("non-deterministic importer or types = %v", first.TypeNames)
	}
	before := repositorySnapshotV2(t, repository)
	stdout, err := entexec.RunImporterV2(context.Background(), repository, first.Source, nil, invocation.Environment())
	if err != nil {
		if typed, ok := err.(*entexec.Error); ok {
			t.Logf("importer diagnostic: %s", typed.Diagnostic())
		}
		t.Fatal(err)
	}
	document, err := ProjectV2(spec, first, stdout)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil || len(canonical) == 0 {
		t.Fatalf("canonical EntityIR: %v", err)
	}
	after := repositorySnapshotV2(t, repository)
	if !bytes.Equal(before, after) {
		t.Fatal("readonly importer changed repository")
	}
	if _, err := os.Lstat(filepath.Join(repository, "zz_nexa_ent_importer.go")); !os.IsNotExist(err) {
		t.Fatalf("virtual importer became visible: %v", err)
	}
}

func TestImporterV2RejectsCaseFoldVirtualCollision(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "ZZ_NEXA_ENT_IMPORTER.GO"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rejectImporterCollisionForTest(repository); err == nil {
		t.Fatal("case-fold collision accepted")
	}
}

func rejectImporterCollisionForTest(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), "zz_nexa_ent_importer.go") {
			return InputV2Error("importer_visibility_invalid", "/schemaDir")
		}
	}
	return nil
}
func repositoryRootV2(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func goEnvV2(t *testing.T, name string) string {
	t.Helper()
	cmd := exec.Command("go", "env", name)
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	value, err := filepath.EvalSymlinks(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func repositorySnapshotV2(t *testing.T, root string) []byte {
	t.Helper()
	var snapshot bytes.Buffer
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot.WriteString(filepath.ToSlash(relative))
		snapshot.WriteByte(0)
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot.Write(data)
		}
		snapshot.WriteByte(0)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot.Bytes()
}
