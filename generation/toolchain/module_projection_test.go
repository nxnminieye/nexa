package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
)

func TestModuleProjectionFacadeReturnsDefensiveReadback(t *testing.T) {
	base := canonicalToolchainDir(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	moduleRoot := filepath.Join(repository, "service")
	frameworkRoot := filepath.Join(repository, "framework")
	staging := filepath.Join(base, "staging")
	scratchParent := filepath.Join(base, "scratch")
	for _, path := range []string{filepath.Join(moduleRoot, "ent", "schema"), frameworkRoot, staging, scratchParent} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "go.mod"), []byte("module example.com/service\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frameworkRoot, "go.mod"), []byte("module github.com/nxnminieye/nexa\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
		Location: location, BuildTags: []string{"zeta", "alpha"},
		Framework: frameworkModuleIdentityFromInternal(identity),
		Helper:    HelperSource{Path: "cmd/enthelper/main.go", Bytes: helper, Digest: provenance.SHA256(helper)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer scratch.Cleanup()
	if got, err := scratch.ModuleDir(); err != nil || got != moduleRoot {
		t.Fatalf("ModuleDir() = %q, %v", got, err)
	}
	if got, err := scratch.SchemaImportPath(); err != nil || got != "example.com/service/ent/schema" {
		t.Fatalf("SchemaImportPath() = %q, %v", got, err)
	}
	tags, err := scratch.NormalizedBuildTags()
	if err != nil || !reflect.DeepEqual(tags, []string{"alpha", "zeta"}) {
		t.Fatalf("tags = %v, %v", tags, err)
	}
	tags[0] = "mutated"
	again, _ := scratch.NormalizedBuildTags()
	if !reflect.DeepEqual(again, []string{"alpha", "zeta"}) {
		t.Fatalf("mutable readback changed state: %v", again)
	}
}

func TestModuleProjectionFacadeMapsClosedErrors(t *testing.T) {
	var location ModuleLocation
	_, err := location.ModuleDir()
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "location_state_invalid", "/location")
	var scratch ScratchModule
	_, err = scratch.Root()
	assertToolchainProjectionError(t, err, "module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch")
}

func assertToolchainProjectionError(t *testing.T, err error, code, stage, reason, pointer string) {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) || got.Code() != code || got.Stage() != stage || got.Reason() != reason || got.Pointer() != pointer {
		t.Fatalf("error = %#v", err)
	}
}

func canonicalToolchainDir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
