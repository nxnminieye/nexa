package entexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
)

func TestNormalizeUsesOneScratchLeaseAndCompilesReadonlyDiscovery(t *testing.T) {
	fixture, scratch, tool, environment := newNormalizerFixture(t)
	defer scratch.Cleanup()
	authored := mustRead(t, filepath.Join(fixture.moduleRoot, "go.mod"))

	var mu sync.Mutex
	var commands [][]string
	normalization, err := Normalize(context.Background(), NormalizeSpec{
		Scratch: scratch, Tool: tool, Environment: environment,
		processHook: func(event processEvent) {
			if event.Name == "start" {
				mu.Lock()
				commands = append(commands, append([]string(nil), event.Args...))
				mu.Unlock()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(fixture.moduleRoot, "go.mod")); !reflect.DeepEqual(got, authored) {
		t.Fatal("normalization changed the authored go.mod")
	}
	root, _ := scratch.Root()
	if got := mustRead(t, filepath.Join(root, "go.mod")); len(got) == 0 {
		t.Fatal("normalized scratch go.mod is empty")
	}
	graph, err := normalization.Graph()
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := graph.ConsumerModule()
	if err != nil || consumer.Path != "example.com/acme/service/v2" {
		t.Fatalf("consumer = %#v, %v", consumer, err)
	}
	manifest, err := normalization.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := manifest.Inputs()
	if err != nil || len(inputs) == 0 {
		t.Fatalf("retained inputs = %d, %v", len(inputs), err)
	}
	for _, input := range inputs {
		if input.Path == "cmd/enthelper/main.go" {
			t.Fatal("normalization helper leaked into schema-only retained inputs")
		}
	}
	version, err := normalization.ExecutableVersion()
	if err != nil || version != tool.Probe.ExpectedVersion {
		t.Fatalf("executable version = %q, %v", version, err)
	}

	var actual [][]string
	for _, command := range commands {
		if reflect.DeepEqual(command, tool.Probe.Args) {
			continue
		}
		actual = append(actual, command)
	}
	want := [][]string{
		{"list", "-mod=mod", "-deps", "-tags=alpha,zeta", "./cmd/enthelper", "example.com/acme/service/v2/ent/schema"},
		{"mod", "verify"},
		{"list", "-mod=readonly", "-m", "-json", "all"},
		{"list", "-mod=readonly", "-deps", "-json", "-tags=alpha,zeta", "example.com/acme/service/v2/ent/schema"},
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("commands = %#v, want %#v", actual, want)
	}
	if err := VerifyDrift(scratch, normalization); err != nil {
		t.Fatal(err)
	}
	tool.Environment[0].Name = "MUTATED"
	environment[0].Value = "mutated"
	if err := VerifyDrift(scratch, normalization); err != nil {
		t.Fatalf("caller mutation changed bound process policy: %v", err)
	}
}

func TestNormalizeDriftAndReadbackAreClosed(t *testing.T) {
	_, scratch, tool, environment := newNormalizerFixture(t)
	normalization, err := Normalize(context.Background(), NormalizeSpec{Scratch: scratch, Tool: tool, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := scratch.Root()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module changed.invalid\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertEntexecError(t, VerifyDrift(scratch, normalization), "scratch_projection_invalid", "drift", "module_file_drift", "/normalized/goMod")
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
	_, err = normalization.Graph()
	if err != nil {
		t.Fatal(err)
	}
	assertEntexecError(t, VerifyDrift(scratch, normalization), "module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch")
	var zero Normalization
	_, err = zero.Graph()
	assertEntexecError(t, err, "module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization")
}

func TestNormalizeStopsAfterProbeFailureAndDetectsNormalizedFileDrift(t *testing.T) {
	_, scratch, tool, environment := newNormalizerFixture(t)
	var commands [][]string
	tool.Probe.ExpectedVersion = "go version impossible"
	_, err := Normalize(context.Background(), NormalizeSpec{
		Scratch: scratch, Tool: tool, Environment: environment,
		processHook: func(event processEvent) {
			if event.Name == "start" {
				commands = append(commands, append([]string(nil), event.Args...))
			}
		},
	})
	assertEntexecError(t, err, "tool_version_mismatch", "probe", "executable_version_mismatch", "/tool/probe/expectedVersion")
	if !reflect.DeepEqual(commands, [][]string{{"version"}}) {
		t.Fatalf("commands after probe failure = %#v", commands)
	}
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}

	_, scratch, tool, environment = newNormalizerFixture(t)
	normalization, err := Normalize(context.Background(), NormalizeSpec{Scratch: scratch, Tool: tool, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	root, _ := scratch.Root()
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("unexpected sum\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertEntexecError(t, VerifyDrift(scratch, normalization), "scratch_projection_invalid", "drift", "module_sum_drift", "/normalized/goSum")
	if err := os.Remove(filepath.Join(root, "go.sum")); err != nil {
		t.Fatal(err)
	}
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyDriftRejectsAnotherEquivalentScratchAndNormalizeErrorsUseNormalizeStage(t *testing.T) {
	_, first, tool, environment := newNormalizerFixture(t)
	firstNormalization, err := Normalize(context.Background(), NormalizeSpec{Scratch: first, Tool: tool, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	_, second, secondTool, secondEnvironment := newNormalizerFixture(t)
	if _, err := Normalize(context.Background(), NormalizeSpec{Scratch: second, Tool: secondTool, Environment: secondEnvironment}); err != nil {
		t.Fatal(err)
	}
	assertEntexecError(t, VerifyDrift(second, firstNormalization), "scratch_projection_invalid", "drift", "resolved_module_drift", "/normalized/modules")
	if err := first.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := second.Cleanup(); err != nil {
		t.Fatal(err)
	}

	fixture, scratch, tool, environment := newNormalizerFixture(t)
	root, _ := scratch.Root()
	projected := mustRead(t, filepath.Join(root, "go.mod"))
	mustWrite(t, filepath.Join(root, "go.mod"), []byte(strings.ReplaceAll(string(projected), "entgo.io/ent v0.14.5", "entgo.io/ent v0.14.4")))
	consumer := mustRead(t, filepath.Join(fixture.moduleRoot, "go.mod"))
	mustWrite(t, filepath.Join(fixture.moduleRoot, "go.mod"), []byte(strings.ReplaceAll(string(consumer), "entgo.io/ent v0.14.5", "entgo.io/ent v0.14.4")))
	_, err = Normalize(context.Background(), NormalizeSpec{Scratch: scratch, Tool: tool, Environment: environment})
	var typed *Error
	if !errors.As(err, &typed) || typed.Stage() != "normalize" {
		t.Fatalf("normalize error = %#v", err)
	}
	_ = scratch.Cleanup()
}

func newNormalizerFixture(t *testing.T) (projectionFixture, *Scratch, ProcessTool, []ProcessEnvironment) {
	t.Helper()
	fixture := newProjectionFixture(t)
	_ = os.Remove(filepath.Join(fixture.moduleRoot, "go.sum"))
	entRoot := filepath.Join(fixture.repository, "deps", "ent")
	mustMkdir(t, entRoot)
	mustWrite(t, filepath.Join(entRoot, "go.mod"), []byte("module entgo.io/ent\n\ngo 1.25.0\n"))
	mustWrite(t, filepath.Join(fixture.moduleRoot, "go.mod"), []byte("module example.com/acme/service/v2\n\ngo 1.25.0\n\nrequire entgo.io/ent v0.14.5\n\nreplace entgo.io/ent => ../../deps/ent\n"))
	mustWrite(t, filepath.Join(fixture.moduleRoot, "ent", "schema", "schema.go"), []byte("package schema\n\ntype User struct{}\n"))
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	framework, err := frameworkmodule.NewIdentity(frameworkmodule.IdentitySpec{
		Module:          buildinput.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.8.0"},
		ReplacementKind: frameworkmodule.ReplacementLocal, LocalPath: fixture.frameworkRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	helper := []byte("package main\nfunc main() {}\n")
	scratch, err := Project(ProjectSpec{
		RepositoryRoot: fixture.repository, StagingRoot: fixture.staging, ScratchParent: fixture.scratchParent,
		Location: location, Framework: framework, BuildTags: []string{"zeta", "alpha"},
		Helper: HelperSource{Path: "cmd/enthelper/main.go", Bytes: helper, Digest: provenance.SHA256(helper)},
	})
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	versionBytes, err := exec.Command(goExecutable, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	gorootBytes, err := exec.Command(goExecutable, "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	home, cache := filepath.Join(fixture.staging, "home"), filepath.Join(fixture.staging, "gocache")
	tmpdir, gopath := filepath.Join(fixture.staging, "tmp"), filepath.Join(fixture.staging, "gopath")
	modcache := filepath.Join(filepath.Dir(fixture.staging), "gomodcache")
	for _, path := range []string{home, cache, tmpdir, gopath, modcache} {
		mustMkdir(t, path)
	}
	tool := ProcessTool{
		ID: "go", Version: "go-local", Executable: goExecutable,
		InputScopes: []string{"repository", "scratch"}, WriteScopes: []string{"scratch"},
		Environment: []ProcessEnvironmentRule{
			{Name: "PATH", Source: EnvironmentHost}, {Name: "GOROOT", Source: EnvironmentHost},
			{Name: "GOMODCACHE", Source: EnvironmentHost}, {Name: "GOPROXY", Source: EnvironmentHost},
			{Name: "GOSUMDB", Source: EnvironmentHost}, {Name: "HOME", Source: EnvironmentScratch},
			{Name: "TMPDIR", Source: EnvironmentScratch}, {Name: "GOPATH", Source: EnvironmentScratch},
			{Name: "GOCACHE", Source: EnvironmentScratch}, {Name: "GOWORK", Source: EnvironmentFixed, FixedValue: "off"},
			{Name: "GOENV", Source: EnvironmentFixed, FixedValue: "off"}, {Name: "GOTOOLCHAIN", Source: EnvironmentFixed, FixedValue: "local"},
			{Name: "GOFLAGS", Source: EnvironmentFixed, FixedValue: ""}, {Name: "CGO_ENABLED", Source: EnvironmentFixed, FixedValue: "0"},
		},
		Probe: ProcessProbe{Args: []string{"version"}, ExpectedVersion: strings.TrimSpace(string(versionBytes))},
	}
	environment := []ProcessEnvironment{
		{Name: "PATH", Value: os.Getenv("PATH")}, {Name: "GOROOT", Value: strings.TrimSpace(string(gorootBytes))},
		{Name: "GOMODCACHE", Value: modcache}, {Name: "GOPROXY", Value: "off"}, {Name: "GOSUMDB", Value: "off"},
		{Name: "HOME", Value: home}, {Name: "TMPDIR", Value: tmpdir}, {Name: "GOPATH", Value: gopath}, {Name: "GOCACHE", Value: cache},
		{Name: "GOWORK", Value: "off"}, {Name: "GOENV", Value: "off"}, {Name: "GOTOOLCHAIN", Value: "local"},
		{Name: "GOFLAGS", Value: ""}, {Name: "CGO_ENABLED", Value: "0"},
	}
	return fixture, scratch, tool, environment
}
