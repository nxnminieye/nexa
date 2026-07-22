package entexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
)

const scratchModulePath = "github.com/nxnminieye/nexa/generation/enthelperexec"

func TestLocateSelectsNearestModuleAndDerivesImportPath(t *testing.T) {
	fixture := newProjectionFixture(t)
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := location.ModuleDir(); got != fixture.moduleRoot {
		t.Fatalf("ModuleDir() = %q, want %q", got, fixture.moduleRoot)
	}
	if got, _ := location.SchemaDir(); got.String() != fixture.schemaSource.String() {
		t.Fatalf("SchemaDir() = %q", got.String())
	}
	if got, _ := location.SchemaImportPath(); got != "example.com/acme/service/v2/ent/schema" {
		t.Fatalf("SchemaImportPath() = %q", got)
	}
	module, err := location.ConsumerModule()
	if err != nil || module != (buildinput.ModuleRequirement{Path: "example.com/acme/service/v2", Version: "v2.0.0"}) {
		t.Fatalf("ConsumerModule() = %#v, %v", module, err)
	}
}

func TestLocateRejectsSchemaSymlinkAndMissingModule(t *testing.T) {
	base := canonicalDirectory(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	outside := filepath.Join(base, "outside")
	mustMkdir(t, outside)
	mustMkdir(t, repository)
	if err := os.Symlink(outside, filepath.Join(repository, "schema")); err != nil {
		t.Fatal(err)
	}
	schema, err := provenance.ParseDomainSource("schema")
	if err != nil {
		t.Fatal(err)
	}
	assertEntexecError(t, locateTestError(LocateSpec{RepositoryRoot: repository, SchemaDir: schema}), "scratch_projection_invalid", "locate", "schema_dir_symlink", "/schemaDir")

	mustMkdir(t, filepath.Join(repository, "plain", "schema"))
	plain, _ := provenance.ParseDomainSource("plain/schema")
	assertEntexecError(t, locateTestError(LocateSpec{RepositoryRoot: repository, SchemaDir: plain}), "scratch_projection_invalid", "locate", "module_not_found", "/schemaDir")
}

func TestLocateUsesClosedModuleSumErrorFamily(t *testing.T) {
	fixture := newProjectionFixture(t)
	if err := os.Remove(filepath.Join(fixture.moduleRoot, "go.sum")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture.repository, "go.mod"), filepath.Join(fixture.moduleRoot, "go.sum")); err != nil {
		t.Fatal(err)
	}
	assertEntexecError(t, locateTestError(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource}), "scratch_projection_invalid", "locate", "module_sum_symlink", "/moduleSum")
}

func TestProjectCreatesDeterministicScratchModuleAndCleansOwnedRoot(t *testing.T) {
	fixture := newProjectionFixture(t)
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	framework, err := frameworkmodule.NewIdentity(frameworkmodule.IdentitySpec{
		Module:          buildinput.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.8.0"},
		ReplacementKind: frameworkmodule.ReplacementLocal,
		LocalPath:       fixture.frameworkRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	helperBytes := []byte("package main\n\nfunc main() {}\n")
	scratch, err := Project(ProjectSpec{
		RepositoryRoot: fixture.repository,
		StagingRoot:    fixture.staging,
		ScratchParent:  fixture.scratchParent,
		Location:       location,
		BuildTags:      []string{"tenant", "integration"},
		Framework:      framework,
		Helper:         HelperSource{Path: "cmd/enthelper/main.go", Bytes: helperBytes, Digest: provenance.SHA256(helperBytes)},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := scratch.Root()
	if err != nil {
		t.Fatal(err)
	}
	if !pathContainedBy(root, fixture.scratchParent) || root == fixture.scratchParent {
		t.Fatalf("scratch root %q is not an owned child of %q", root, fixture.scratchParent)
	}
	projectedBytes := mustRead(t, filepath.Join(root, "go.mod"))
	projected, err := modfile.Parse("go.mod", projectedBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Module == nil || projected.Module.Mod.Path != scratchModulePath {
		t.Fatalf("scratch module = %#v", projected.Module)
	}
	assertRequirement(t, projected, "example.com/acme/service/v2", "v2.0.0")
	assertRequirement(t, projected, "github.com/nxnminieye/nexa", "v0.8.0")
	assertReplacement(t, projected, "example.com/acme/service/v2", "", fixture.moduleRoot, "")
	assertReplacement(t, projected, "example.com/acme/dep", "v1.4.0", fixture.dependencyRoot, "")
	assertReplacement(t, projected, "github.com/nxnminieye/nexa", "v0.8.0", fixture.frameworkRoot, "")
	if projected.Go == nil || projected.Go.Version != "1.25.0" || projected.Toolchain == nil || projected.Toolchain.Name != "go1.25.1" {
		t.Fatalf("projected directives = go %#v toolchain %#v", projected.Go, projected.Toolchain)
	}
	if len(projected.Exclude) != 1 || projected.Exclude[0].Mod.Path != "example.com/old" || projected.Exclude[0].Mod.Version != "v1.2.3" {
		t.Fatalf("projected excludes = %#v", projected.Exclude)
	}
	if got := mustRead(t, filepath.Join(root, "go.sum")); !bytes.Equal(got, fixture.goSum) {
		t.Fatalf("projected go.sum = %q", got)
	}
	if got := mustRead(t, filepath.Join(root, "cmd", "enthelper", "main.go")); !bytes.Equal(got, helperBytes) {
		t.Fatalf("projected helper = %q", got)
	}
	if tags, err := scratch.NormalizedBuildTags(); err != nil || !reflect.DeepEqual(tags, []string{"integration", "tenant"}) {
		t.Fatalf("NormalizedBuildTags() = %v, %v", tags, err)
	}
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch root still exists: %v", err)
	}
	if _, err := os.Stat(fixture.scratchParent); err != nil {
		t.Fatalf("scratch parent was removed: %v", err)
	}
	if err := scratch.Cleanup(); err != nil {
		t.Fatalf("second cleanup = %v", err)
	}
}

func TestProjectRejectsDriftAndFrameworkReplacementOutsideRepository(t *testing.T) {
	t.Run("located module drift", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(fixture.moduleRoot, "go.mod"), []byte("module example.com/acme/service/v2\n\ngo 1.25.0\n"))
		_, err = Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
		assertEntexecError(t, err, "scratch_projection_invalid", "project", "module_file_digest_drift", "/moduleFile")
	})

	t.Run("framework outside repository", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
		if err != nil {
			t.Fatal(err)
		}
		outside := canonicalDirectory(t, filepath.Join(filepath.Dir(fixture.repository), "outside-framework"))
		_, err = Project(validProjectSpec(t, fixture, location, outside))
		assertEntexecError(t, err, "scratch_projection_invalid", "project", "framework_local_replacement_outside_repository", "/framework/module/replacement/localPath")
	})
}

func TestProjectRejectsBuildTagsOutsideClosedTokenGrammar(t *testing.T) {
	for _, tag := range []string{"-race", "a/b", "标签"} {
		t.Run(tag, func(t *testing.T) {
			fixture := newProjectionFixture(t)
			location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
			if err != nil {
				t.Fatal(err)
			}
			spec := validProjectSpec(t, fixture, location, fixture.frameworkRoot)
			spec.BuildTags = []string{tag}
			_, err = Project(spec)
			assertEntexecError(t, err, "scratch_projection_invalid", "project", "build_tag_invalid", "/buildTags/0")
		})
	}
}

func TestProjectRejectsHelperPathsThatCanReplaceProjectionControlFiles(t *testing.T) {
	for _, path := range []string{"go.mod", "go.sum", "cmd/enthelper", "cmd/enthelper/main.go/child.go"} {
		t.Run(path, func(t *testing.T) {
			fixture := newProjectionFixture(t)
			location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
			if err != nil {
				t.Fatal(err)
			}
			spec := validProjectSpec(t, fixture, location, fixture.frameworkRoot)
			spec.Helper.Path = path
			_, err = Project(spec)
			assertEntexecError(t, err, "scratch_projection_invalid", "project", "helper_path_invalid", "/helper/path")
		})
	}
}

func TestScratchCleanupRejectsRenamedAndReplacedRootWithoutDeletingEither(t *testing.T) {
	fixture := newProjectionFixture(t)
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := scratch.Root()
	moved := root + ".moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "replacement.marker"), []byte("preserve"))
	assertEntexecError(t, scratch.Cleanup(), "scratch_cleanup_failed", "cleanup", "cleanup_identity_invalid", "/scratch")
	if got := mustRead(t, filepath.Join(root, "replacement.marker")); string(got) != "preserve" {
		t.Fatalf("replacement marker = %q", got)
	}
	if _, err := os.Stat(filepath.Join(moved, "go.mod")); err != nil {
		t.Fatalf("owned moved root was unexpectedly deleted: %v", err)
	}
}

func TestRunProcessRejectsReplacedScratchRoot(t *testing.T) {
	fixture := newProjectionFixture(t)
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := scratch.Root()
	moved := root + ".moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, root)
	mustWrite(t, filepath.Join(root, "replacement.marker"), []byte("preserve"))
	spec := validProcessSpecForRoots(t, fixture.repository, fixture.staging)
	spec.Scratch, spec.WorkDir = scratch, ""
	_, err = RunProcess(context.Background(), spec)
	assertProcessError(t, err, "tool_input_invalid", "input", "scratch_state_invalid", "/scratch", "", 0)
	if got := mustRead(t, filepath.Join(root, "replacement.marker")); string(got) != "preserve" {
		t.Fatalf("replacement marker = %q", got)
	}
}

func TestProjectClosesLocalReplacementFilesystemFailures(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		real := filepath.Join(fixture.repository, "deps", "real-dep")
		if err := os.Rename(fixture.dependencyRoot, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, fixture.dependencyRoot); err != nil {
			t.Fatal(err)
		}
		assertProjectReplacementError(t, fixture, "replace_symlink")
	})

	t.Run("escape", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		outside := filepath.Join(filepath.Dir(fixture.repository), "outside-dep")
		mustMkdir(t, outside)
		mustWrite(t, filepath.Join(outside, "go.mod"), []byte("module example.com/acme/dep\n\ngo 1.25.0\n"))
		writeFixtureModule(t, fixture, "replace example.com/acme/dep v1.4.0 => ../../../outside-dep\n")
		assertProjectReplacementError(t, fixture, "replace_escape")
	})

	t.Run("module mismatch", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		mustWrite(t, filepath.Join(fixture.dependencyRoot, "go.mod"), []byte("module example.com/acme/other\n\ngo 1.25.0\n"))
		assertProjectReplacementError(t, fixture, "replace_module_mismatch")
	})

	t.Run("digest drift", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
		if err != nil {
			t.Fatal(err)
		}
		spec := validProjectSpec(t, fixture, location, fixture.frameworkRoot)
		spec.projectionHook = func(event projectionEvent) {
			if event.Name == "before-local-source-recheck" {
				mustWrite(t, filepath.Join(fixture.dependencyRoot, "go.mod"), []byte("module example.com/acme/dep\n\ngo 1.25.1\n"))
			}
		}
		_, err = Project(spec)
		assertEntexecError(t, err, "scratch_projection_invalid", "project", "replace_invalid", "/moduleFile/replace/0")
	})
}

func TestProjectValidatesFrameworkLocalModuleBoundary(t *testing.T) {
	fixture := newProjectionFixture(t)
	mustWrite(t, filepath.Join(fixture.frameworkRoot, "go.mod"), []byte("module example.com/not-nexa\n\ngo 1.25.0\n"))
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
	assertEntexecError(t, err, "scratch_projection_invalid", "project", "tool_module_invalid", "/toolModule/path")
}

func TestProjectRechecksEveryLocalModuleDirectoryIdentity(t *testing.T) {
	t.Run("consumer replacement directory becomes symlink", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
		if err != nil {
			t.Fatal(err)
		}
		spec := validProjectSpec(t, fixture, location, fixture.frameworkRoot)
		spec.projectionHook = func(event projectionEvent) {
			if event.Name != "before-local-source-recheck" {
				return
			}
			moved := fixture.dependencyRoot + ".moved"
			if err := os.Rename(fixture.dependencyRoot, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, fixture.dependencyRoot); err != nil {
				t.Fatal(err)
			}
		}
		_, err = Project(spec)
		assertEntexecError(t, err, "scratch_projection_invalid", "project", "replace_symlink", "/moduleFile/replace/0")
	})

	t.Run("framework directory becomes symlink", func(t *testing.T) {
		fixture := newProjectionFixture(t)
		location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
		if err != nil {
			t.Fatal(err)
		}
		spec := validProjectSpec(t, fixture, location, fixture.frameworkRoot)
		spec.projectionHook = func(event projectionEvent) {
			if event.Name != "before-local-source-recheck" {
				return
			}
			moved := fixture.frameworkRoot + ".moved"
			if err := os.Rename(fixture.frameworkRoot, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, fixture.frameworkRoot); err != nil {
				t.Fatal(err)
			}
		}
		_, err = Project(spec)
		assertEntexecError(t, err, "scratch_projection_invalid", "project", "tool_module_invalid", "/toolModule/path")
	})
}

func TestProjectRejectsComponentReplacementBeforeLocalModuleRootBinding(t *testing.T) {
	fixture := newProjectionFixture(t)
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	spec := validProjectSpec(t, fixture, location, fixture.frameworkRoot)
	var attacked bool
	spec.projectionHook = func(event projectionEvent) {
		if event.Name != "before-local-component-bind" || event.Root != fixture.dependencyRoot {
			return
		}
		attacked = true
		moved := event.Root + ".moved"
		if err := os.Rename(event.Root, moved); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(moved, "go.mod"), []byte("module example.com/should-not-be-read\n\ngo 1.25.0\n"))
		if err := os.Symlink(filepath.Base(moved), event.Root); err != nil {
			t.Fatal(err)
		}
	}
	scratch, err := Project(spec)
	if scratch != nil {
		_ = scratch.Cleanup()
		t.Fatal("component replacement crossed local module binding")
	}
	if !attacked {
		t.Fatal("local component binding hook was not reached")
	}
	assertEntexecError(t, err, "scratch_projection_invalid", "project", "replace_symlink", "/moduleFile/replace/0")
}

func TestProjectPreservesUnrelatedVersionQualifiedFrameworkReplacement(t *testing.T) {
	fixture := newProjectionFixture(t)
	writeFixtureModule(t, fixture, "replace github.com/nxnminieye/nexa v0.7.0 => example.com/archived/nexa v0.7.1\nreplace example.com/acme/dep v1.4.0 => ../../deps/dep\n")
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer scratch.Cleanup()
	root, _ := scratch.Root()
	projected, err := modfile.Parse("go.mod", mustRead(t, filepath.Join(root, "go.mod")), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertReplacement(t, projected, "github.com/nxnminieye/nexa", "v0.7.0", "example.com/archived/nexa", "v0.7.1")
	assertReplacement(t, projected, "github.com/nxnminieye/nexa", "v0.8.0", fixture.frameworkRoot, "")
}

func TestProjectRejectsDuplicateAndUnsupportedConsumerDirectives(t *testing.T) {
	tests := []struct {
		name, directives, reason, pointer string
	}{
		{name: "replace duplicate", directives: "replace example.com/acme/dep v1.4.0 => ../../deps/dep\nreplace example.com/acme/dep v1.4.0 => ../../deps/dep\n", reason: "replace_duplicate", pointer: "/moduleFile/replace/1"},
		{name: "exclude duplicate", directives: "exclude example.com/old v1.2.3\nexclude example.com/old v1.2.3\n", reason: "exclude_duplicate", pointer: "/moduleFile/exclude/1"},
		{name: "tool directive", directives: "tool example.com/tool\n", reason: "directive_unsupported", pointer: "/moduleFile/directive/0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProjectionFixture(t)
			writeFixtureModule(t, fixture, test.directives)
			location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
			if err != nil {
				t.Fatal(err)
			}
			_, err = Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
			assertEntexecError(t, err, "scratch_projection_invalid", "project", test.reason, test.pointer)
		})
	}
}

func TestProjectClassifiesMalformedDirectivesFromStructuredModfilePosition(t *testing.T) {
	tests := []struct {
		name, directive, reason, pointer string
	}{
		{name: "go", directive: "go v1.25.0", reason: "go_directive_invalid", pointer: "/moduleFile/go"},
		{name: "toolchain", directive: "toolchain rust1.25.0", reason: "toolchain_directive_invalid", pointer: "/moduleFile/toolchain"},
		{name: "replace", directive: "replace example.com/broken =>", reason: "replace_invalid", pointer: "/moduleFile/replace/0"},
		{name: "exclude", directive: "exclude example.com/broken latest", reason: "exclude_invalid", pointer: "/moduleFile/exclude/0"},
		{name: "replace block", directive: "replace (\n\texample.com/valid v1.0.0 => example.com/fork v1.0.1\n\texample.com/broken =>\n)", reason: "replace_invalid", pointer: "/moduleFile/replace/1"},
		{name: "exclude block", directive: "exclude (\n\texample.com/valid v1.0.0\n\texample.com/broken latest\n)", reason: "exclude_invalid", pointer: "/moduleFile/exclude/1"},
		{name: "unknown fallback", directive: "future-directive value", reason: "module_file_parse_failed", pointer: "/moduleFile"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProjectionFixture(t)
			mustWrite(t, filepath.Join(fixture.moduleRoot, "go.mod"), []byte("module example.com/acme/service/v2\n\n"+test.directive+"\n"))
			location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
			if err != nil {
				t.Fatalf("Locate() rejected a module before structured projection classification: %v", err)
			}
			_, err = Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
			assertEntexecError(t, err, "scratch_projection_invalid", "project", test.reason, test.pointer)
		})
	}
}

func TestZeroReadbackStatesAreRejected(t *testing.T) {
	var location Location
	if _, err := location.ModuleDir(); err == nil {
		t.Fatal("zero Location crossed readback boundary")
	}
	var scratch Scratch
	if _, err := scratch.Root(); err == nil {
		t.Fatal("zero Scratch crossed readback boundary")
	}
}

type projectionFixture struct {
	repository, moduleRoot, dependencyRoot, frameworkRoot string
	staging, scratchParent                                string
	schemaSource                                          provenance.DomainSource
	goSum                                                 []byte
}

func newProjectionFixture(t *testing.T) projectionFixture {
	t.Helper()
	base := canonicalDirectory(t, t.TempDir())
	fixture := projectionFixture{
		repository:    filepath.Join(base, "repository"),
		staging:       filepath.Join(base, "staging"),
		scratchParent: filepath.Join(base, "scratch"),
		goSum:         []byte("example.com/old v1.2.3 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"),
	}
	fixture.moduleRoot = filepath.Join(fixture.repository, "services", "acme")
	fixture.dependencyRoot = filepath.Join(fixture.repository, "deps", "dep")
	fixture.frameworkRoot = filepath.Join(fixture.repository, "framework")
	mustMkdir(t, filepath.Join(fixture.moduleRoot, "ent", "schema"))
	mustMkdir(t, fixture.dependencyRoot)
	mustMkdir(t, fixture.frameworkRoot)
	mustMkdir(t, fixture.staging)
	mustMkdir(t, fixture.scratchParent)
	mustWrite(t, filepath.Join(fixture.repository, "go.mod"), []byte("module example.com/root\n\ngo 1.25.0\n"))
	mustWrite(t, filepath.Join(fixture.moduleRoot, "go.mod"), []byte("module example.com/acme/service/v2\n\ngo 1.25.0\n\ntoolchain go1.25.1\n\nreplace example.com/acme/dep v1.4.0 => ../../deps/dep\n\nexclude example.com/old v1.2.3\n"))
	mustWrite(t, filepath.Join(fixture.moduleRoot, "go.sum"), fixture.goSum)
	mustWrite(t, filepath.Join(fixture.dependencyRoot, "go.mod"), []byte("module example.com/acme/dep\n\ngo 1.25.0\n"))
	mustWrite(t, filepath.Join(fixture.frameworkRoot, "go.mod"), []byte("module github.com/nxnminieye/nexa\n\ngo 1.25.0\n"))
	fixture.schemaSource, _ = provenance.ParseDomainSource("services/acme/ent/schema")
	return fixture
}

func validProjectSpec(t *testing.T, fixture projectionFixture, location Location, frameworkRoot string) ProjectSpec {
	t.Helper()
	framework, err := frameworkmodule.NewIdentity(frameworkmodule.IdentitySpec{
		Module:          buildinput.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.8.0"},
		ReplacementKind: frameworkmodule.ReplacementLocal, LocalPath: frameworkRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	helper := []byte("package main\nfunc main() {}\n")
	return ProjectSpec{
		RepositoryRoot: fixture.repository, StagingRoot: fixture.staging, ScratchParent: fixture.scratchParent,
		Location: location, Framework: framework,
		Helper: HelperSource{Path: "cmd/enthelper/main.go", Bytes: helper, Digest: provenance.SHA256(helper)},
	}
}

func writeFixtureModule(t *testing.T, fixture projectionFixture, directives string) {
	t.Helper()
	mustWrite(t, filepath.Join(fixture.moduleRoot, "go.mod"), []byte("module example.com/acme/service/v2\n\ngo 1.25.0\n\ntoolchain go1.25.1\n\n"+directives))
}

func assertProjectReplacementError(t *testing.T, fixture projectionFixture, reason string) {
	t.Helper()
	location, err := Locate(LocateSpec{RepositoryRoot: fixture.repository, SchemaDir: fixture.schemaSource})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Project(validProjectSpec(t, fixture, location, fixture.frameworkRoot))
	assertEntexecError(t, err, "scratch_projection_invalid", "project", reason, "/moduleFile/replace/0")
}

func locateTestError(spec LocateSpec) error { _, err := Locate(spec); return err }

func assertEntexecError(t *testing.T, err error, code, stage, reason, pointer string) {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) || got.Code() != code || got.Stage() != stage || got.Reason() != reason || got.Pointer() != pointer {
		t.Fatalf("error = %#v, want %s/%s/%s %s", err, code, stage, reason, pointer)
	}
}

func canonicalDirectory(t *testing.T, path string) string {
	t.Helper()
	mustMkdir(t, path)
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertRequirement(t *testing.T, file *modfile.File, path, version string) {
	t.Helper()
	for _, item := range file.Require {
		if item.Mod.Path == path && item.Mod.Version == version {
			return
		}
	}
	t.Fatalf("require %s %s not found", path, version)
}

func assertReplacement(t *testing.T, file *modfile.File, oldPath, oldVersion, newPath, newVersion string) {
	t.Helper()
	for _, item := range file.Replace {
		if item.Old.Path == oldPath && item.Old.Version == oldVersion && item.New.Path == newPath && item.New.Version == newVersion {
			return
		}
	}
	t.Fatalf("replace %s %s => %s %s not found: %#v", oldPath, oldVersion, newPath, newVersion, file.Replace)
}
