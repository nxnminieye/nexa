package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
)

func TestNormalizeScratchModuleReturnsImmutableSemanticReadback(t *testing.T) {
	scratch, tool, environment := newPublicNormalizerFixture(t)
	result, err := NormalizeScratchModule(context.Background(), scratch, tool, environment)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := result.ModuleGraph()
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := graph.ConsumerModule()
	if err != nil || consumer.Path != "example.com/service" {
		t.Fatalf("consumer = %#v, %v", consumer, err)
	}
	modules, err := graph.Modules()
	if err != nil || len(modules) < 3 {
		t.Fatalf("modules = %d, %v", len(modules), err)
	}
	modules[0].Path = "mutated.invalid"
	again, _ := graph.Modules()
	if again[0].Path == "mutated.invalid" {
		t.Fatal("module graph readback shared mutable state")
	}
	manifest, err := result.BuildInputs()
	if err != nil {
		t.Fatal(err)
	}
	if inputs, err := manifest.Inputs(); err != nil || len(inputs) == 0 {
		t.Fatalf("inputs = %d, %v", len(inputs), err)
	}
	version, err := result.ExecutableVersion()
	if err != nil || version != tool.Probe.ExpectedVersion {
		t.Fatalf("version = %q, %v", version, err)
	}
	if err := VerifyScratchModuleDrift(scratch, result); err != nil {
		t.Fatal(err)
	}
	otherScratch, otherTool, otherEnvironment := newPublicNormalizerFixture(t)
	otherResult, err := NormalizeScratchModule(context.Background(), otherScratch, otherTool, otherEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	otherGraph, _ := otherResult.ModuleGraph()
	leftGraphDigest, leftErr := graph.Digest()
	rightGraphDigest, rightErr := otherGraph.Digest()
	if leftErr != nil || rightErr != nil || leftGraphDigest != rightGraphDigest {
		t.Fatalf("semantic graph digest depends on absolute roots: %s/%s", leftGraphDigest.String(), rightGraphDigest.String())
	}
	if err := otherScratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
	root, _ := scratch.Root()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module changed.invalid\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertToolchainProjectionError(t, VerifyScratchModuleDrift(scratch, result), "scratch_projection_invalid", "drift", "module_file_drift", "/normalized/goMod")
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizationFacadeClosesInvalidStateAndToolPolicy(t *testing.T) {
	var zero NormalizationResult
	_, err := zero.ModuleGraph()
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization")
	var scratch ScratchModule
	_, err = NormalizeScratchModule(context.Background(), &scratch, Tool{}, nil)
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch")
}

func TestEntGenerationNormalizationAcceptsConsumerEntVersionWithoutWeakeningStrictPath(t *testing.T) {
	strictScratch, strictTool, strictEnvironment := newPublicNormalizerFixtureWithEntVersion(t, "v0.15.0")
	_, err := NormalizeScratchModule(context.Background(), strictScratch, strictTool, strictEnvironment)
	assertToolchainProjectionError(t, err, "scratch_projection_invalid", "normalize", "ent_module_mismatch", "/normalized/entModule")
	if cleanupErr := strictScratch.Cleanup(); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}

	consumerScratch, consumerTool, consumerEnvironment := newPublicNormalizerFixtureWithEntVersion(t, "v0.15.0")
	result, err := NormalizeScratchModuleForEntGeneration(context.Background(), consumerScratch, consumerTool, consumerEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := result.ModuleGraph()
	if err != nil {
		t.Fatal(err)
	}
	modules, err := graph.Modules()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, module := range modules {
		if module.Path == "entgo.io/ent" {
			found = module.Version == "v0.15.0"
		}
	}
	if !found {
		t.Fatalf("consumer-resolved Ent module missing from %#v", modules)
	}
	if cleanupErr := consumerScratch.Cleanup(); cleanupErr != nil {
		t.Fatal(cleanupErr)
	}
}

func newPublicNormalizerFixture(t *testing.T) (*ScratchModule, Tool, []EnvVar) {
	return newPublicNormalizerFixtureWithEntVersion(t, "v0.14.5")
}

func newPublicNormalizerFixtureWithEntVersion(t *testing.T, entVersion string) (*ScratchModule, Tool, []EnvVar) {
	t.Helper()
	base := canonicalToolchainDir(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	moduleRoot := filepath.Join(repository, "service")
	frameworkRoot := filepath.Join(repository, "framework")
	entRoot := filepath.Join(repository, "ent")
	staging := filepath.Join(base, "staging")
	scratchParent := filepath.Join(base, "scratch")
	for _, path := range []string{filepath.Join(moduleRoot, "ent", "schema"), frameworkRoot, entRoot, staging, scratchParent} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(moduleRoot, "go.mod"), "module example.com/service\n\ngo 1.25.0\n\nrequire entgo.io/ent "+entVersion+"\n\nreplace entgo.io/ent => ../ent\n")
	write(filepath.Join(moduleRoot, "ent", "schema", "schema.go"), "package schema\n\ntype User struct{}\n")
	write(filepath.Join(frameworkRoot, "go.mod"), "module github.com/nxnminieye/nexa\n\ngo 1.25.0\n")
	write(filepath.Join(entRoot, "go.mod"), "module entgo.io/ent\n\ngo 1.25.0\n")
	schema, _ := provenance.ParseDomainSource("service/ent/schema")
	location, err := LocateModule(ModuleLocateSpec{RepositoryRoot: repository, SchemaDir: schema})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := frameworkmodule.NewIdentity(frameworkmodule.IdentitySpec{
		Module:          buildinput.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.8.0"},
		ReplacementKind: frameworkmodule.ReplacementLocal, LocalPath: frameworkRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	helper := []byte("package main\nfunc main() {}\n")
	scratch, err := ProjectScratchModule(ScratchModuleSpec{
		RepositoryRoot: repository, StagingRoot: staging, ScratchParent: scratchParent,
		Location: location, Framework: frameworkModuleIdentityFromInternal(identity),
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
	home, cache := filepath.Join(staging, "home"), filepath.Join(staging, "gocache")
	tmpdir, gopath, modcache := filepath.Join(staging, "tmp"), filepath.Join(staging, "gopath"), filepath.Join(base, "gomodcache")
	for _, path := range []string{home, cache, tmpdir, gopath, modcache} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tool := Tool{
		ID: "go", Version: "go-local", Executable: goExecutable,
		InputScopes: []string{"repository", "scratch"}, WriteScopes: []string{"scratch"},
		Environment: []EnvironmentRule{
			{Name: "PATH", Source: EnvironmentHost}, {Name: "GOROOT", Source: EnvironmentHost},
			{Name: "GOMODCACHE", Source: EnvironmentHost}, {Name: "GOPROXY", Source: EnvironmentHost},
			{Name: "GOSUMDB", Source: EnvironmentHost}, {Name: "HOME", Source: EnvironmentScratch},
			{Name: "TMPDIR", Source: EnvironmentScratch}, {Name: "GOPATH", Source: EnvironmentScratch},
			{Name: "GOCACHE", Source: EnvironmentScratch}, {Name: "GOWORK", Source: EnvironmentFixed, FixedValue: "off"},
			{Name: "GOENV", Source: EnvironmentFixed, FixedValue: "off"}, {Name: "GOTOOLCHAIN", Source: EnvironmentFixed, FixedValue: "local"},
			{Name: "GOFLAGS", Source: EnvironmentFixed, FixedValue: ""}, {Name: "CGO_ENABLED", Source: EnvironmentFixed, FixedValue: "0"},
		},
		Probe: ExecutableProbe{Args: []string{"version"}, ExpectedVersion: strings.TrimSpace(string(versionBytes))},
	}
	environment := []EnvVar{
		{Name: "PATH", Value: os.Getenv("PATH")}, {Name: "GOROOT", Value: strings.TrimSpace(string(gorootBytes))},
		{Name: "GOMODCACHE", Value: modcache}, {Name: "GOPROXY", Value: "off"}, {Name: "GOSUMDB", Value: "off"},
		{Name: "HOME", Value: home}, {Name: "TMPDIR", Value: tmpdir}, {Name: "GOPATH", Value: gopath}, {Name: "GOCACHE", Value: cache},
		{Name: "GOWORK", Value: "off"}, {Name: "GOENV", Value: "off"}, {Name: "GOTOOLCHAIN", Value: "local"},
		{Name: "GOFLAGS", Value: ""}, {Name: "CGO_ENABLED", Value: "0"},
	}
	return scratch, tool, environment
}
