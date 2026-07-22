package integration_test

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

func TestEntityIRExternalConsumerLoadsTypedEntGraph(t *testing.T) {
	temporary := t.TempDir()
	repository := filepath.Dir(repositoryRoot(t))
	consumer, err := os.MkdirTemp(repository, "entity-consumer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(consumer) })
	helper := filepath.Join(temporary, "helper")
	fixture := filepath.Join(repositoryRoot(t), "fixtures", "generation", "ent-consumer")
	copyFixtureTree(t, filepath.Join(fixture, "schema"), filepath.Join(consumer, "schema"))
	copyFixtureTree(t, filepath.Join(fixture, "tools"), filepath.Join(consumer, "tools"))
	copyFixtureTree(t, filepath.Join(fixture, "cmd", "entityir", "cmd"), filepath.Join(helper, "cmd"))
	consumer, err = filepath.EvalSymlinks(consumer)
	if err != nil {
		t.Fatal(err)
	}
	helper, err = filepath.EvalSymlinks(helper)
	if err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, filepath.Join(consumer, "go.mod"), fmt.Sprintf(`module github.com/nxnminieye/nexa/generation/entconsumerfixture

go 1.25.0

require (
	entgo.io/ent v0.14.5
	github.com/nxnminieye/nexa v0.0.0
)

replace github.com/nxnminieye/nexa => %s
`, filepath.ToSlash(repositoryRoot(t))))
	writeConsumerFile(t, filepath.Join(helper, "go.mod"), fmt.Sprintf(`module github.com/nxnminieye/nexa/generation/enthelperexec

go 1.25.0

require (
	github.com/nxnminieye/nexa/generation/entconsumerfixture v0.0.0
	github.com/nxnminieye/nexa v0.0.0
)

replace github.com/nxnminieye/nexa/generation/entconsumerfixture => %s
replace github.com/nxnminieye/nexa => %s
`, filepath.ToSlash(consumer), filepath.ToSlash(repositoryRoot(t))))
	if err := os.MkdirAll(filepath.Join(consumer, ".entc"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, filepath.Join(consumer, ".entc", "sentinel"), "consumer-owned\n")
	if err := os.MkdirAll(filepath.Join(helper, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}

	environment := isolatedExternalGoEnvironment(t, temporary)
	environment = replaceEnvironment(environment, "TMPDIR", filepath.Join(helper, "tmp"))
	moduleCache := rootModuleCache(t)
	environment = replaceEnvironment(environment, "GOMODCACHE", moduleCache)
	environment = replaceEnvironment(environment, "GOPROXY", "file://"+filepath.ToSlash(filepath.Join(moduleCache, "cache", "download")))
	runEntityIRGo(t, consumer, environment, "mod", "tidy")
	runEntityIRGo(t, consumer, environment, "mod", "download", "all")
	runEntityIRGo(t, helper, environment, "list", "-mod=mod", "-deps", "./cmd/entityir", "github.com/nxnminieye/nexa/generation/entconsumerfixture/schema")
	runEntityIRGo(t, helper, environment, "mod", "download", "all")
	before := entityIRFixtureTree(t, consumer)
	schemaDir, err := filepath.Rel(repository, filepath.Join(consumer, "schema"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", "-mod=readonly", "./cmd/entityir", repository, filepath.ToSlash(schemaDir))
	command.Dir = helper
	command.Env = environment
	canonical, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run EntityIR helper: %v\n%s", err, canonical)
	}
	if after := entityIRFixtureTree(t, consumer); !reflect.DeepEqual(before, after) {
		t.Fatalf("consumer tree changed\nbefore=%v\nafter=%v", before, after)
	}
	if _, err := os.Stat(filepath.Join(helper, ".entc")); !os.IsNotExist(err) {
		t.Fatalf("helper retained .entc: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(helper, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("helper retained execution roots: %v, %v", entries, err)
	}

	source, err := provenance.ParseDomainSource("external/entity-ir.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := entity.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatalf("parse EntityIR: %v\n%s", err, canonical)
	}
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || string(roundTrip) != string(canonical) {
		t.Fatalf("EntityIR round trip failed: %v", err)
	}
	account, ok := snapshot.Entity("schema:Account")
	if !ok {
		t.Fatal("Account missing")
	}
	crud, ok := account.CRUD()
	if !ok || !sameCRUD(crud.Operations(), []nexaent.CRUDOperation{nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDCreate}) {
		t.Fatalf("Account CRUD = %#v, %v", crud.Operations(), ok)
	}
	audit, ok := snapshot.Entity("schema:AuditEntry")
	if !ok {
		t.Fatal("AuditEntry missing")
	}
	if _, ok := audit.CRUD(); ok {
		t.Fatal("AuditEntry absence became CRUD opt-in")
	}
	if _, ok := account.Field("schema:Account/field:source"); !ok {
		t.Fatal("shared mixin field missing from Account")
	}
	if _, ok := audit.Field("schema:AuditEntry/field:source"); !ok {
		t.Fatal("shared mixin field missing from AuditEntry")
	}
	if len(snapshot.ProjectedSources()) != 7 {
		t.Fatalf("source closure = %#v", snapshot.ProjectedSources())
	}
}

func copyFixtureTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func entityIRFixtureTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func replaceEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

func runEntityIRGo(t *testing.T, directory string, environment []string, arguments ...string) {
	t.Helper()
	command := exec.Command("go", arguments...)
	command.Dir = directory
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go %v: %v\n%s", arguments, err, output)
	}
}

func sameCRUD(left, right []nexaent.CRUDOperation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
