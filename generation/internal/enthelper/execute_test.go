package enthelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/entipc"
	"github.com/nxnminieye/nexa/provenance"
)

func TestExecuteRejectsInvalidRequestWithoutTrustedBytes(t *testing.T) {
	stdout, err := Execute(context.Background(), []byte(`{"apiVersion":"nexa.dev/ent-graph-request/v1"}`))
	if err == nil {
		t.Fatal("invalid request succeeded")
	}
	if len(stdout) != 0 {
		t.Fatalf("invalid request returned trusted bytes: %q", stdout)
	}
}

func TestTypedFixtureHelperProducesDeterministicPlan(t *testing.T) {
	frameworkRoot := helperRepositoryRoot(t)
	repository := filepath.Dir(frameworkRoot)
	fixture := filepath.Join(frameworkRoot, "fixtures/generation/ent-consumer")
	consumer, err := os.MkdirTemp(repository, "crud-consumer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(consumer) })
	copyTree(t, filepath.Join(fixture, "schema"), filepath.Join(consumer, "schema"))
	copyTree(t, filepath.Join(fixture, "tools"), filepath.Join(consumer, "tools"))
	copyTree(t, filepath.Join(fixture, "cmd", "crudplan"), filepath.Join(consumer, "cmd", "crudplan"))
	consumerGoMod := fmt.Sprintf("module github.com/nxnminieye/nexa/generation/entconsumerfixture\n\ngo 1.25.0\n\nrequire (\n entgo.io/ent v0.14.5\n github.com/nxnminieye/nexa v0.0.0\n)\nreplace github.com/nxnminieye/nexa => %s\n", frameworkRoot)
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(consumerGoMod), 0o600); err != nil {
		t.Fatal(err)
	}
	consumerTidy := exec.Command("go", "mod", "tidy")
	consumerTidy.Dir = consumer
	consumerTidy.Env = goCommandEnvWithModuleProxy(t)
	if output, err := consumerTidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy consumer: %v\n%s", err, output)
	}
	schemaDir := filepath.ToSlash(filepath.Join(filepath.Base(consumer), "schema"))
	requestBytes := buildFixtureRequest(t, frameworkRoot, consumer, repository, schemaDir)
	requestSource, err := provenance.ParseDomainSource("quality/ent-request.json")
	if err != nil {
		t.Fatal(err)
	}
	request, err := entipc.ParseRequest(requestSource, requestBytes)
	if err != nil {
		t.Fatalf("ParseRequest() error = %#v", err)
	}
	binary := filepath.Join(t.TempDir(), "crudplan")
	command := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./cmd/crudplan")
	command.Dir = consumer
	command.Env = goCommandEnv()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, output)
	}
	runtimeRoot := t.TempDir()
	runtimeRoot, err = filepath.EvalSymlinks(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	runtimeModule := fmt.Sprintf("module %s\n\ngo 1.25.0\n\nrequire (\n github.com/nxnminieye/nexa/generation/entconsumerfixture v0.0.0\n github.com/nxnminieye/nexa v0.0.0\n)\nreplace github.com/nxnminieye/nexa/generation/entconsumerfixture => %s\nreplace github.com/nxnminieye/nexa => %s\n", entexec.ScratchModulePath, consumer, frameworkRoot)
	if err := os.WriteFile(filepath.Join(runtimeRoot, "go.mod"), []byte(runtimeModule), 0o600); err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join(fixture, "cmd", "crudplan"), filepath.Join(runtimeRoot, "cmd", "crudplan"))
	runtimeEnvironment := goCommandEnvWithModuleProxy(t)
	runtimePrewarm := exec.Command("go", "list", "-mod=mod", "-deps", "./cmd/crudplan", "github.com/nxnminieye/nexa/generation/entconsumerfixture/schema")
	runtimePrewarm.Dir = runtimeRoot
	runtimePrewarm.Env = runtimeEnvironment
	if output, err := runtimePrewarm.CombinedOutput(); err != nil {
		t.Fatalf("prewarm helper runtime: %v\n%s", err, output)
	}
	tempRoot := filepath.Join(runtimeRoot, "tmp")
	if err := os.Mkdir(tempRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func() []byte {
		cmd := exec.Command(binary)
		cmd.Dir = runtimeRoot
		cmd.Env = replaceEnv(runtimeEnvironment, "TMPDIR", tempRoot)
		cmd.Stdin = bytes.NewReader(requestBytes)
		stdout, err := cmd.Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				t.Fatalf("run helper: %v: %s", err, exit.Stderr)
			}
			t.Fatalf("run helper: %v", err)
		}
		return stdout
	}
	first, second := run(), run()
	if !bytes.Equal(first, second) {
		t.Fatal("helper result is not deterministic")
	}
	resultSource, err := provenance.ParseDomainSource("quality/ent-result.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := entipc.ParseResult(resultSource, request, first)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := result.Plan()
	if !ok || len(plan.ProtoBytes()) == 0 {
		t.Fatal("helper did not return a Proto plan")
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("helper scratch residue = %v, %v", entries, err)
	}
}

func helperRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

func buildFixtureRequest(t *testing.T, frameworkRoot, consumer, repository, schemaDir string) []byte {
	t.Helper()
	root := t.TempDir()
	module := fmt.Sprintf("module %s\n\ngo 1.25.0\n\nrequire (\n github.com/nxnminieye/nexa/generation/entconsumerfixture v0.0.0\n github.com/nxnminieye/nexa v0.0.0\n)\nreplace github.com/nxnminieye/nexa/generation/entconsumerfixture => %s\nreplace github.com/nxnminieye/nexa => %s\n", entexec.ScratchModulePath, consumer, frameworkRoot)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `package main
import("context";"encoding/json";"fmt";"os";"github.com/nxnminieye/nexa/generation/internal/entexec";_ "github.com/nxnminieye/nexa/generation/internal/entityload";"github.com/nxnminieye/nexa/provenance")
type source struct{Ref,Digest string};type facts struct{Graph,Input,Version string;ModuleSources []source}
func main(){root:=os.Args[1];schema,_:=provenance.ParseDomainSource(os.Args[2]);inspection,err:=entexec.Inspect(context.Background(),entexec.Spec{RepositoryRoot:root,SchemaDir:schema});if err!=nil{if typed,ok:=err.(*entexec.Error);ok{panic(typed.Code()+"|"+typed.Stage()+"|"+typed.Reason()+"|"+typed.Pointer()+"|"+typed.ToolID()+"|"+fmt.Sprint(typed.ExitCode())+"|"+typed.Diagnostic())};panic(err)};graph,_:=inspection.ModuleGraphDigest();input,_:=inspection.BuildInputDigest();version,_:=inspection.ExecutableVersion();moduleSources,_:=inspection.ModuleSources();sources:=make([]source,len(moduleSources));for i,value:=range moduleSources{sources[i]=source{value.Ref.String(),value.Digest.String()}};json.NewEncoder(os.Stdout).Encode(facts{graph.String(),input.String(),version,sources})}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := goCommandEnvWithModuleProxy(t)
	prewarm := exec.Command("go", "list", "-mod=mod", "-deps", ".", "github.com/nxnminieye/nexa/generation/entconsumerfixture/schema")
	prewarm.Dir = root
	prewarm.Env = environment
	if output, err := prewarm.CombinedOutput(); err != nil {
		t.Fatalf("prewarm request facts: %v\n%s", err, output)
	}
	command := exec.Command("go", "run", "-mod=readonly", ".", repository, schemaDir)
	command.Dir = root
	command.Env = environment
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("build request: %v\n%s", err, stderr.Bytes())
	}
	var facts struct {
		Graph, Input, Version string
		ModuleSources         []struct{ Ref, Digest string }
	}
	if err := json.Unmarshal(output, &facts); err != nil {
		t.Fatal(err)
	}
	graph, err := provenance.ParseDigest(facts.Graph)
	if err != nil {
		t.Fatal(err)
	}
	input, err := provenance.ParseDigest(facts.Input)
	if err != nil {
		t.Fatal(err)
	}
	moduleSources := make([]provenance.Source, len(facts.ModuleSources))
	for index, value := range facts.ModuleSources {
		ref, err := provenance.ParseSourceRef(value.Ref)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := provenance.ParseDigest(value.Digest)
		if err != nil {
			t.Fatal(err)
		}
		moduleSources[index] = provenance.Source{Ref: ref, Digest: digest}
	}
	schema, err := provenance.ParseDomainSource(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	request, err := entipc.NewRequest(entipc.RequestSpec{RepositoryRoot: repository, SchemaDir: schema, ModuleGraphDigest: graph, BuildInputDigest: input, ModuleSources: moduleSources, ServiceID: "accounts", ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1", ProtoDestination: entipc.ProtoDestination{EntryPath: "api/accounts.proto", ArtifactPath: "api/accounts.crud.generated.proto", LockPath: "api/accounts.crud-protocol.lock.json"}, Tool: entipc.ToolIdentity{ID: "go", Version: "go1.25", ExecutableVersion: facts.Version}})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := entipc.CanonicalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
}

func goCommandEnv() []string {
	result := []string{}
	for _, value := range os.Environ() {
		if len(value) >= 7 && value[:7] == "GOWORK=" || len(value) >= 6 && value[:6] == "GOENV=" || len(value) >= 12 && value[:12] == "GOTOOLCHAIN=" {
			continue
		}
		result = append(result, value)
	}
	return append(result, "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
}

func goCommandEnvWithModuleProxy(t *testing.T) []string {
	t.Helper()
	command := exec.Command("go", "env", "GOMODCACHE")
	command.Env = goCommandEnv()
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	cache := strings.TrimSpace(string(output))
	environment := replaceEnv(goCommandEnv(), "GOMODCACHE", cache)
	environment = replaceEnv(environment, "GOPROXY", "file://"+filepath.ToSlash(filepath.Join(cache, "cache", "download")))
	return replaceEnv(environment, "GOSUMDB", "off")
}

func replaceEnv(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
