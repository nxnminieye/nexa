package enthelper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/entipc"
	"github.com/nxnminieye/nexa/provenance"
)

func TestEntGraphV2RunnerRealHelperAndAdversarialTelemetry(t *testing.T) {
	framework := helperRepositoryRoot(t)
	repository, err := os.MkdirTemp(filepath.Dir(framework), ".enthelper-v2-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(repository) })
	module := "module example.com/runnerfixture\n\ngo 1.25.0\n\nrequire (\n entgo.io/ent v0.14.5\n github.com/nxnminieye/nexa v0.0.0\n)\n\nreplace github.com/nxnminieye/nexa => " + filepath.ToSlash(framework) + "\n"
	writeV2File(t, filepath.Join(repository, "go.mod"), module)
	writeV2File(t, filepath.Join(repository, "pkg", "internal", "schema", "account.go"), "package schema\n\nimport (\n \"entgo.io/ent\"\n \"entgo.io/ent/schema/field\"\n)\ntype Account struct{ ent.Schema }\nfunc (Account) Fields() []ent.Field { return []ent.Field{field.String(\"name\")} }\n")
	writeV2File(t, filepath.Join(repository, "tools", "tools.go"), "package tools\nimport _ \"entgo.io/ent/entc/load\"\n")
	downloadEntHelperModuleGraph(t, repository)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = repository
	tidy.Env = goCommandEnvWithModuleProxy(t)
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy: %v\n%s", err, output)
	}
	file, err := os.OpenFile(filepath.Join(repository, "go.mod"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\nrequire github.com/nxnminieye/nexa v0.0.0\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	prewarm := exec.Command("go", "list", "-mod=mod", "github.com/nxnminieye/nexa/generation/enthelperexec")
	prewarm.Dir = repository
	prewarm.Env = goCommandEnvWithModuleProxy(t)
	if output, err := prewarm.CombinedOutput(); err != nil {
		t.Fatalf("prewarm helper: %v\n%s", err, output)
	}
	request, err := entipc.NewRequestV2(entipc.RequestV2Spec{ModuleDir: ".", ModulePath: "example.com/runnerfixture", SchemaDir: "pkg/internal/schema", BuildTags: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	requestBytes, _ := entipc.CanonicalRequestV2(request)
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	versionCommand := exec.Command(goExecutable, "version")
	versionCommand.Env = goCommandEnv()
	versionBytes, err := versionCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	goCache := goEnvPathV2(t, "GOCACHE")
	moduleCache := goEnvPathV2(t, "GOMODCACHE")
	tempBase := dedicatedTempBaseV2(t, framework)
	before, err := os.ReadDir(tempBase)
	if err != nil {
		t.Fatal(err)
	}
	ambient := append(goCommandEnvWithModuleProxy(t), "HOME="+repository, "XDG_CONFIG_HOME="+repository, "TEST_TELEMETRY_DIR="+repository, "GOTMPDIR="+repository, "TMPDIR="+repository, "GOFLAGS=-modfile=evil -overlay=evil")
	processResult, err := entexec.RunEntGraphV2(context.Background(), entexec.EntGraphProcessSpec{RepositoryRoot: repository, ModuleDir: ".", ModulePath: "example.com/runnerfixture", SchemaDir: "pkg/internal/schema", GoExecutable: goExecutable, ExpectedVersion: strings.TrimSpace(string(versionBytes)), Request: requestBytes, BuildTags: []string{}, GOCACHE: goCache, GOMODCACHE: moduleCache, TempBase: tempBase, BaseEnvironment: ambient})
	if err != nil {
		t.Fatal(err)
	}
	resultSource, _ := provenance.ParseDomainSource("quality/result-v2.json")
	result, err := entipc.ParseResultV2(resultSource, request, processResult.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Projected(); !ok {
		t.Fatalf("helper did not return EntityIR: %s", processResult.Stdout)
	}
	if _, err := os.Lstat(filepath.Join(repository, ".entc")); !os.IsNotExist(err) {
		t.Fatalf("consumer .entc changed: %v", err)
	}
	if entries, err := os.ReadDir(repository); err != nil {
		t.Fatal(err)
	} else {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".nexa-ent-") || entry.Name() == "zz_nexa_ent_importer.go" {
				t.Fatalf("consumer residue: %s", entry.Name())
			}
		}
	}
	after, err := os.ReadDir(tempBase)
	if err != nil {
		t.Fatal(err)
	}
	if countInvocationRootsV2(before) != countInvocationRootsV2(after) {
		t.Fatal("invocation root leaked")
	}
}

func TestEntGraphV2ProbeMismatchCleansBeforeMain(t *testing.T) {
	framework := helperRepositoryRoot(t)
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, _ = filepath.EvalSymlinks(goExecutable)
	goCache := goEnvPathV2(t, "GOCACHE")
	moduleCache := goEnvPathV2(t, "GOMODCACHE")
	tempBase := dedicatedTempBaseV2(t, framework)
	before, _ := os.ReadDir(tempBase)
	request, _ := entipc.NewRequestV2(entipc.RequestV2Spec{ModuleDir: ".", ModulePath: "github.com/nxnminieye/nexa", SchemaDir: "generation/internal/entityload", BuildTags: []string{}})
	requestBytes, _ := entipc.CanonicalRequestV2(request)
	_, err = entexec.RunEntGraphV2(context.Background(), entexec.EntGraphProcessSpec{RepositoryRoot: framework, ModuleDir: ".", ModulePath: "github.com/nxnminieye/nexa", SchemaDir: "generation/internal/entityload", GoExecutable: goExecutable, ExpectedVersion: "go version mismatch", Request: requestBytes, GOCACHE: goCache, GOMODCACHE: moduleCache, TempBase: tempBase, BaseEnvironment: goCommandEnv()})
	if err == nil {
		t.Fatal("version mismatch succeeded")
	}
	after, _ := os.ReadDir(tempBase)
	if countInvocationRootsV2(before) != countInvocationRootsV2(after) {
		t.Fatal("probe mismatch leaked invocation root")
	}
}

func writeV2File(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
func goEnvPathV2(t *testing.T, name string) string {
	t.Helper()
	command := exec.Command("go", "env", name)
	command.Env = goCommandEnv()
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	value, err := filepath.EvalSymlinks(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func countInvocationRootsV2(entries []os.DirEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".nexa-ent-") {
			count++
		}
	}
	return count
}

func dedicatedTempBaseV2(t *testing.T, framework string) string {
	t.Helper()
	base, err := os.MkdirTemp(filepath.Dir(framework), ".entgraph-temp-base-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	return base
}
