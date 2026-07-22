package entexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
)

func TestInspectWithInjectedProfileReturnsDigestsAndCleansExecutionRoot(t *testing.T) {
	fixture, profile := newExecutionFixture(t)
	inspection, err := inspectWithProfile(context.Background(), Spec{
		RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource, BuildTags: []string{"zeta", "alpha"},
	}, profile)
	if err != nil {
		t.Fatal(err)
	}
	graphDigest, graphErr := inspection.ModuleGraphDigest()
	inputDigest, inputErr := inspection.BuildInputDigest()
	version, versionErr := inspection.ExecutableVersion()
	sources, sourcesErr := inspection.ModuleSources()
	if graphErr != nil || inputErr != nil || versionErr != nil || sourcesErr != nil || graphDigest.String() == "" || inputDigest.String() == "" || version == "" || len(sources) == 0 {
		t.Fatalf("inspection = %s %s %q, errors = %v %v %v", graphDigest.String(), inputDigest.String(), version, graphErr, inputErr, versionErr)
	}
	sources[0] = provenance.Source{}
	again, err := inspection.ModuleSources()
	if err != nil || again[0].Ref.String() == "" {
		t.Fatalf("module sources are not defensive: %#v, %v", again, err)
	}
	entries, err := os.ReadDir(profile.roots.base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("execution root residue = %v, %v", entries, err)
	}
}

func TestBeginMintsSingleUseRunReadsRetainedInputAndCleansOnlyNestedRoot(t *testing.T) {
	fixture, profile := newExecutionFixture(t)
	inspection, err := inspectWithProfile(context.Background(), Spec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource}, profile)
	if err != nil {
		t.Fatal(err)
	}
	graphDigest, _ := inspection.ModuleGraphDigest()
	inputDigest, _ := inspection.BuildInputDigest()

	outer := canonicalDirectory(t, t.TempDir())
	tmp := filepath.Join(outer, "tmp")
	mustMkdir(t, tmp)
	mustWrite(t, filepath.Join(outer, "go.mod"), []byte("module "+ScratchModulePath+"\n\ngo 1.25.0\n"))
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(outer); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	t.Setenv("TMPDIR", tmp)

	run, err := beginWithProfile(context.Background(), Spec{
		RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource,
		ExpectedModuleGraphDigest: OptionalDigest{Value: graphDigest, Present: true},
		ExpectedBuildInputDigest:  OptionalDigest{Value: inputDigest, Present: true},
	}, profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.VerifyPreLoad(); err != nil {
		t.Fatal(err)
	}
	inputs, err := run.Inputs()
	if err != nil || len(inputs) == 0 {
		t.Fatalf("inputs = %d, %v", len(inputs), err)
	}
	data, err := run.ReadRetainedInput(inputs[0])
	if err != nil || provenance.SHA256(data) != inputs[0].Digest {
		t.Fatalf("retained read = %d bytes, %v", len(data), err)
	}
	scratchRoot, _ := run.scratch.Root()
	if err := run.VerifyCoordinates(fixture.repository, scratchRoot, fixture.moduleRoot, fixture.schemaSource, nil); err != nil {
		t.Fatal(err)
	}
	if err := run.ClaimLoad(); err != nil {
		t.Fatal(err)
	}
	if err := run.ClaimLoad(); err == nil {
		t.Fatal("second ClaimLoad succeeded")
	}
	if err := run.VerifyPostLoad(); err != nil {
		t.Fatal(err)
	}
	if err := run.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outer); err != nil {
		t.Fatalf("outer cwd was removed: %v", err)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("parent TMPDIR was removed: %v", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("nested execution residue = %v, %v", entries, err)
	}
	if _, err := run.Inputs(); err == nil {
		t.Fatal("cleaned Run remained readable")
	}
}

func TestBeginRejectsInvalidHelperCWDAndExpectedDigest(t *testing.T) {
	fixture, profile := newExecutionFixture(t)
	_, err := beginWithProfile(context.Background(), Spec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource}, profile)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code() != "execution_root_invalid" {
		t.Fatalf("invalid helper cwd error = %#v", err)
	}
}

func newExecutionFixture(t *testing.T) (projectionFixture, ExecutionProfile) {
	t.Helper()
	fixture, scratch, tool, environment := newNormalizerFixture(t)
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
	hostNames := map[string]bool{"PATH": true, "GOROOT": true, "GOMODCACHE": true, "GOPROXY": true, "GOSUMDB": true}
	var host []ProcessEnvironment
	for _, value := range environment {
		if hostNames[value.Name] {
			host = append(host, value)
		}
	}
	process, err := NewProcessPolicy(ProcessPolicySpec{Tool: tool, HostEnvironment: host})
	if err != nil {
		t.Fatal(err)
	}
	rootBase := canonicalDirectory(t, t.TempDir())
	roots, err := NewExecutionRootPolicy(ExecutionRootPolicySpec{Base: rootBase})
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
	profile, err := NewExecutionProfile(ExecutionProfileSpec{
		Framework: framework,
		Helper:    HelperSource{Path: "cmd/enthelper/main.go", Bytes: helper, Digest: provenance.SHA256(helper)},
		Process:   process, Roots: roots,
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture, profile
}
