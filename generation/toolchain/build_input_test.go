package toolchain_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

func TestCompileBuildInputManifestRejectsDuplicateBuildTagBeforeRunner(t *testing.T) {
	fixture := newToolchainFixture(t)
	runner := &recordingRunner{results: []toolchain.Result{
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.moduleList},
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.packageList},
	}}
	_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, toolchain.BuildInputCompileSpec{
		RepositoryRoot: fixture.repository, ScratchRoot: fixture.scratch,
		SchemaDir: fixture.schemaDir, SchemaImportPath: "example.com/acme/consumer/schema/models",
		BuildTags:    []string{"duplicate", "duplicate"},
		Tool:         toolchain.Tool{ID: "go", Version: "v1.0.0", Executable: "go"},
		ToolModule:   toolchain.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.1.0"},
		HelperDigest: provenance.SHA256([]byte("helper")),
	})
	assertToolchainError(t, err, "build_input_invalid", "retain", "build_tag_duplicate", "/buildInputs/buildTags/1", "", 0)
	if len(runner.requests) != 0 {
		t.Fatalf("Runner calls = %d, want 0 for invalid compile spec", len(runner.requests))
	}
}

func TestCompileBuildInputManifestClosesCallerCGOPolicy(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*toolchain.BuildInputCompileSpec)
	}{
		{name: "tool rule", reason: "tool_invalid", pointer: "/buildInputs/tool", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.Environment = []toolchain.EnvironmentRule{{Name: "CGO_ENABLED", Source: toolchain.EnvironmentHost}}
		}},
		{name: "environment", reason: "environment_invalid", pointer: "/buildInputs/environment", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Environment = []toolchain.EnvVar{{Name: "CGO_ENABLED", Value: "0"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newToolchainFixture(t)
			runner := &recordingRunner{}
			spec := validCompileSpec(fixture)
			test.mutate(&spec)
			_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, spec)
			assertToolchainError(t, err, "build_input_invalid", "retain", test.reason, test.pointer, "", 0)
			if len(runner.requests) != 0 {
				t.Fatalf("Runner calls = %d, want 0", len(runner.requests))
			}
		})
	}
}

func TestCompileBuildInputManifestVerifiesProbeVersion(t *testing.T) {
	fixture := newToolchainFixture(t)
	runner := &recordingRunner{results: []toolchain.Result{
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.moduleList},
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.packageList},
	}}
	spec := validCompileSpec(fixture)
	spec.Tool.Probe = toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "go1.24.0"}
	_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, spec)
	assertToolchainError(t, err, "build_input_invalid", "retain", "tool_version_mismatch", "/buildInputs/goExecutableVersion", "go", 0)
}

func TestCompileBuildInputManifestDoesNotTrustForeignRunnerErrorIdentity(t *testing.T) {
	fixture := newToolchainFixture(t)
	raw := errors.New("foreign failure at " + fixture.repository)
	runner := runnerFunc(func(context.Context, toolchain.Request) (toolchain.Result, error) {
		return toolchain.Result{ToolID: "spoofed", ExitCode: 0}, raw
	})
	_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
	toolErr := assertToolchainError(t, err, "build_input_discovery_failed", "retain", "module_list_nonzero", "/moduleGraph", "", 0)
	if strings.Contains(toolErr.Error(), fixture.repository) || errors.Is(toolErr, raw) {
		t.Fatalf("foreign error leaked through public projection: %v", toolErr)
	}
}

func TestCompileBuildInputManifestDoesNotTrustCapturedPublicRunnerError(t *testing.T) {
	source, err := provenance.ParseDomainSource("quality/captured.json")
	if err != nil {
		t.Fatal(err)
	}
	_, captured := toolchain.ParseBuildInputManifestSnapshot(source, []byte(`null`))
	if captured == nil {
		t.Fatal("snapshot parse unexpectedly succeeded")
	}
	fixture := newToolchainFixture(t)
	runner := runnerFunc(func(context.Context, toolchain.Request) (toolchain.Result, error) {
		return toolchain.Result{ToolID: "go", ExitCode: 0}, captured
	})
	_, err = toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
	assertToolchainError(t, err, "build_input_discovery_failed", "retain", "module_list_nonzero", "/moduleGraph", "", 0)
}

func TestCompileBuildInputManifestValidatesToolAndEnvironmentBeforeRunner(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(*toolchain.BuildInputCompileSpec)
	}{
		{name: "unsafe tool id invalid UTF-8", reason: "tool_invalid", pointer: "/buildInputs/tool", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.ID = string([]byte{'g', 'o', 0xff})
		}},
		{name: "unsafe tool id token", reason: "tool_invalid", pointer: "/buildInputs/tool", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.ID = "go\nother"
		}},
		{name: "unsafe tool id leading punctuation", reason: "tool_invalid", pointer: "/buildInputs/tool", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.ID = "-go"
		}},
		{name: "missing host materialization", reason: "environment_invalid", pointer: "/buildInputs/environment", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.Environment = []toolchain.EnvironmentRule{{Name: "PATH", Source: toolchain.EnvironmentHost}}
		}},
		{name: "missing scratch materialization", reason: "environment_invalid", pointer: "/buildInputs/environment", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.Environment = []toolchain.EnvironmentRule{{Name: "TMPDIR", Source: toolchain.EnvironmentScratch}}
		}},
		{name: "fixed override", reason: "environment_invalid", pointer: "/buildInputs/environment", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.Environment = []toolchain.EnvironmentRule{{Name: "GOWORK", Source: toolchain.EnvironmentFixed, FixedValue: "off"}}
			spec.Environment = []toolchain.EnvVar{{Name: "GOWORK", Value: "auto"}}
		}},
		{name: "undeclared materialization", reason: "environment_invalid", pointer: "/buildInputs/environment", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Environment = []toolchain.EnvVar{{Name: "GOFLAGS", Value: "-mod=mod"}}
		}},
		{name: "duplicate materialization", reason: "environment_invalid", pointer: "/buildInputs/environment", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.Environment = []toolchain.EnvironmentRule{{Name: "PATH", Source: toolchain.EnvironmentHost}}
			spec.Environment = []toolchain.EnvVar{{Name: "PATH", Value: "/bin"}, {Name: "PATH", Value: "/usr/bin"}}
		}},
		{name: "fixed value on host rule", reason: "tool_invalid", pointer: "/buildInputs/tool", mutate: func(spec *toolchain.BuildInputCompileSpec) {
			spec.Tool.Environment = []toolchain.EnvironmentRule{{Name: "PATH", Source: toolchain.EnvironmentHost, FixedValue: "/bin"}}
			spec.Environment = []toolchain.EnvVar{{Name: "PATH", Value: "/bin"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newToolchainFixture(t)
			runner := &recordingRunner{}
			spec := validCompileSpec(fixture)
			test.mutate(&spec)
			_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, spec)
			assertToolchainError(t, err, "build_input_invalid", "retain", test.reason, test.pointer, "", 0)
			if len(runner.requests) != 0 {
				t.Fatalf("Runner calls = %d, want 0", len(runner.requests))
			}
		})
	}
}

func TestCompileBuildInputManifestAcceptsExactEnvironmentMaterialization(t *testing.T) {
	fixture := newToolchainFixture(t)
	runner := &recordingRunner{results: []toolchain.Result{
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.moduleList},
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.packageList},
	}}
	spec := validCompileSpec(fixture)
	spec.Tool.Environment = []toolchain.EnvironmentRule{
		{Name: "PATH", Source: toolchain.EnvironmentHost},
		{Name: "TMPDIR", Source: toolchain.EnvironmentScratch},
		{Name: "GOWORK", Source: toolchain.EnvironmentFixed, FixedValue: "off"},
		{Name: "GOFLAGS", Source: toolchain.EnvironmentFixed, FixedValue: ""},
	}
	spec.Environment = []toolchain.EnvVar{{Name: "TMPDIR", Value: "/tmp/scratch"}, {Name: "GOFLAGS", Value: ""}, {Name: "PATH", Value: "/usr/bin"}, {Name: "GOWORK", Value: "off"}}
	if _, err := toolchain.CompileBuildInputManifest(context.Background(), runner, spec); err != nil {
		t.Fatalf("CompileBuildInputManifest() error = %v", err)
	}
	want := []toolchain.EnvVar{{Name: "CGO_ENABLED", Value: "0"}, {Name: "GOFLAGS", Value: ""}, {Name: "GOWORK", Value: "off"}, {Name: "PATH", Value: "/usr/bin"}, {Name: "TMPDIR", Value: "/tmp/scratch"}}
	if !reflect.DeepEqual(runner.requests[0].Environment, want) || !reflect.DeepEqual(runner.requests[1].Environment, want) {
		t.Fatalf("Runner environments = %#v / %#v, want %#v", runner.requests[0].Environment, runner.requests[1].Environment, want)
	}
}

func TestCompileBuildInputManifestSelectsActualNonzeroBeforeVersionAndOutput(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		results               []toolchain.Result
		exitCode              int
	}{
		{name: "module", reason: "module_list_nonzero", pointer: "/moduleGraph", exitCode: 23, results: []toolchain.Result{{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "wrong", ExitCode: 23, Stdout: bytes.Repeat([]byte{'x'}, toolchain.MaxModuleListOutputBytes+1), Diagnostic: "module graph failed"}}},
		{name: "package", reason: "package_list_nonzero", pointer: "/retainedInputs", exitCode: 29, results: []toolchain.Result{
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: newToolchainFixture(t).moduleList},
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "wrong", ExitCode: 29, Stdout: bytes.Repeat([]byte{'x'}, toolchain.MaxPackageListOutputBytes+1)},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newToolchainFixture(t)
			if test.name == "package" {
				test.results[0].Stdout = fixture.moduleList
			}
			runner := &recordingRunner{results: test.results}
			_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
			toolErr := assertToolchainError(t, err, "build_input_discovery_failed", "retain", test.reason, test.pointer, "go", test.exitCode)
			if test.name == "module" && toolErr.Diagnostic() != "module graph failed" {
				t.Fatalf("diagnostic = %q", toolErr.Diagnostic())
			}
		})
	}
}

func TestCompileBuildInputManifestRedactsOversizedRunnerDiagnosticBeforeFinalBound(t *testing.T) {
	fixture := newToolchainFixture(t)
	longSecret := strings.Repeat("secret", 16)
	diagnostic := "bare=x " + strings.Repeat("p", 3980) + " repo=" + fixture.repository +
		" exec=/bin/go SHORT=\"x\" SHORT='x' LONG=" + longSecret
	runner := &recordingRunner{results: []toolchain.Result{{
		ToolID: "go", Version: "v1.0.0", ExitCode: 23,
		Diagnostic: diagnostic,
	}}}
	spec := validCompileSpec(fixture)
	spec.Tool.Executable = "/bin/go"
	spec.Tool.Environment = []toolchain.EnvironmentRule{
		{Name: "SHORT", Source: toolchain.EnvironmentFixed, FixedValue: "x"},
		{Name: "LONG", Source: toolchain.EnvironmentFixed, FixedValue: longSecret},
	}
	spec.Environment = []toolchain.EnvVar{{Name: "SHORT", Value: "x"}, {Name: "LONG", Value: longSecret}}
	_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, spec)
	toolErr := assertToolchainError(t, err, "build_input_discovery_failed", "retain", "module_list_nonzero", "/moduleGraph", "go", 23)
	if toolErr.Diagnostic() == "" || len(toolErr.Diagnostic()) > toolchain.MaxDiagnosticBytes {
		t.Fatalf("bounded diagnostic = %q", toolErr.Diagnostic())
	}
	for _, leaked := range []string{fixture.repository, "/bin/go", longSecret, "SHORT=\"x\"", "SHORT='x'"} {
		if strings.Contains(toolErr.Diagnostic(), leaked) {
			t.Fatalf("diagnostic leaked %q: %q", leaked, toolErr.Diagnostic())
		}
	}
	if !strings.Contains(toolErr.Diagnostic(), "bare=x") {
		t.Fatalf("short bare value was globally replaced: %q", toolErr.Diagnostic())
	}
}

func TestCompileBuildInputManifestPreflightsCompleteSemanticSpecBeforeRunner(t *testing.T) {
	tests := []struct {
		name, reason, pointer string
		mutate                func(toolchainFixture, *toolchain.BuildInputCompileSpec)
	}{
		{name: "relative repository root", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.RepositoryRoot = "relative/repository"
		}},
		{name: "relative scratch root", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.ScratchRoot = "relative/scratch"
		}},
		{name: "same roots", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.ScratchRoot = spec.RepositoryRoot
		}},
		{name: "scratch nested under repository", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.ScratchRoot = filepath.Join(spec.RepositoryRoot, "scratch")
		}},
		{name: "repository nested under scratch", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(fixture toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.ScratchRoot = filepath.Dir(fixture.repository)
		}},
		{name: "zero schema source", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.SchemaDir = provenance.DomainSource{}
		}},
		{name: "zero helper digest", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.HelperDigest = provenance.Digest{}
		}},
		{name: "schema import", reason: "schema_import_path_invalid", pointer: "/buildInputs/schemaImportPath", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.SchemaImportPath = "../schema"
		}},
		{name: "tool module path", reason: "module_graph_state_invalid", pointer: "/moduleGraph", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.ToolModule.Path = "../tool"
		}},
		{name: "tool module major", reason: "module_graph_state_invalid", pointer: "/moduleGraph", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.ToolModule = toolchain.ModuleRequirement{Path: "example.com/tool/v2", Version: "v1.0.0"}
		}},
		{name: "compound invalid", reason: "compile_spec_invalid", pointer: "/buildInputs", mutate: func(_ toolchainFixture, spec *toolchain.BuildInputCompileSpec) {
			spec.RepositoryRoot = "relative"
			spec.SchemaDir = provenance.DomainSource{}
			spec.SchemaImportPath = "../schema"
			spec.ToolModule = toolchain.ModuleRequirement{}
			spec.HelperDigest = provenance.Digest{}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newToolchainFixture(t)
			spec := validCompileSpec(fixture)
			test.mutate(fixture, &spec)
			calls := 0
			runner := runnerFunc(func(context.Context, toolchain.Request) (toolchain.Result, error) {
				calls++
				return toolchain.Result{}, errors.New("Runner must not be called")
			})
			_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, spec)
			assertToolchainError(t, err, "build_input_invalid", "retain", test.reason, test.pointer, "", 0)
			if calls != 0 {
				t.Fatalf("Runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestCompileBuildInputManifestRejectsNilContextBeforeRunner(t *testing.T) {
	for _, compound := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil context", true: "compound nil context"}[compound], func(t *testing.T) {
			fixture := newToolchainFixture(t)
			spec := validCompileSpec(fixture)
			if compound {
				spec.RepositoryRoot = "relative"
				spec.SchemaDir = provenance.DomainSource{}
			}
			calls := 0
			runner := runnerFunc(func(context.Context, toolchain.Request) (toolchain.Result, error) {
				calls++
				panic("Runner called with nil context")
			})
			_, err := toolchain.CompileBuildInputManifest(nil, runner, spec)
			assertToolchainError(t, err, "build_input_invalid", "retain", "compile_spec_invalid", "/buildInputs", "", 0)
			if calls != 0 {
				t.Fatalf("Runner calls = %d, want 0", calls)
			}
		})
	}
}

func TestCompileBuildInputManifestMapsRunnerFailureMatrix(t *testing.T) {
	t.Run("module run error with actual exit", func(t *testing.T) {
		fixture := newToolchainFixture(t)
		runner := runnerFunc(func(context.Context, toolchain.Request) (toolchain.Result, error) {
			return toolchain.Result{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "untrusted", ExitCode: 31, Stdout: []byte(`untrusted`)}, errors.New("raw failure at " + fixture.repository)
		})
		_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
		toolErr := assertToolchainError(t, err, "build_input_discovery_failed", "retain", "module_list_nonzero", "/moduleGraph", "go", 31)
		if strings.Contains(toolErr.Error(), fixture.repository) {
			t.Fatalf("raw Runner diagnostic leaked: %v", toolErr)
		}
	})

	t.Run("module output limit", func(t *testing.T) {
		fixture := newToolchainFixture(t)
		runner := &recordingRunner{results: []toolchain.Result{{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: bytes.Repeat([]byte{'x'}, toolchain.MaxModuleListOutputBytes+1)}}}
		_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
		assertToolchainError(t, err, "build_input_discovery_failed", "retain", "module_list_output_limit_exceeded", "/moduleGraph", "go", 0)
	})

	t.Run("package output limit", func(t *testing.T) {
		fixture := newToolchainFixture(t)
		runner := &recordingRunner{results: []toolchain.Result{
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.moduleList},
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: bytes.Repeat([]byte{'x'}, toolchain.MaxPackageListOutputBytes+1)},
		}}
		_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
		assertToolchainError(t, err, "build_input_discovery_failed", "retain", "package_list_output_limit_exceeded", "/retainedInputs", "go", 0)
	})

	t.Run("module JSON after exit zero", func(t *testing.T) {
		fixture := newToolchainFixture(t)
		runner := &recordingRunner{results: []toolchain.Result{
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: []byte(`{"Path":`)},
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.packageList},
		}}
		_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
		assertToolchainError(t, err, "build_input_invalid", "retain", "module_list_output_invalid", "/moduleGraph", "go", 0)
	})

	t.Run("package JSON after exit zero", func(t *testing.T) {
		fixture := newToolchainFixture(t)
		runner := &recordingRunner{results: []toolchain.Result{
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.moduleList},
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: []byte(`{"Dir":`)},
		}}
		_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
		assertToolchainError(t, err, "build_input_invalid", "retain", "package_list_output_invalid", "/retainedInputs", "go", 0)
	})

	t.Run("semantic failure after exit zero", func(t *testing.T) {
		fixture := newToolchainFixture(t)
		var pkg map[string]any
		if err := json.Unmarshal(fixture.packageList, &pkg); err != nil {
			t.Fatal(err)
		}
		pkg["CgoFiles"] = []string{"native.c"}
		packageList := toolchainJSONStream(t, pkg)
		runner := &recordingRunner{results: []toolchain.Result{
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.moduleList},
			{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: packageList},
		}}
		_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
		assertToolchainError(t, err, "build_input_unsupported", "retain", "native_input_unsupported", "/retainedInputs/packages/0/CgoFiles/0", "go", 0)
	})

	t.Run("empty executable version after exit zero", func(t *testing.T) {
		fixture := newToolchainFixture(t)
		runner := &recordingRunner{results: []toolchain.Result{{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "", Stdout: fixture.moduleList}}}
		_, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
		assertToolchainError(t, err, "build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "go", 0)
	})
}

func TestCompileBuildInputManifestBuildTagsSelectExactPureGoSet(t *testing.T) {
	fixture := newToolchainFixture(t)
	writeToolchainFile(t, filepath.Join(fixture.repository, "schema/models/alpha.go"), "package models\n")
	writeToolchainFile(t, filepath.Join(fixture.repository, "schema/models/beta.go"), "package models\n")
	var packageTemplate map[string]any
	if err := json.Unmarshal(fixture.packageList, &packageTemplate); err != nil {
		t.Fatal(err)
	}
	compile := func(tag, selected string) toolchain.BuildInputManifest {
		t.Helper()
		runner := runnerFunc(func(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
			stdout := fixture.moduleList
			if !reflect.DeepEqual(request.Args, []string{"list", "-mod=readonly", "-m", "-json", "all"}) {
				if !reflect.DeepEqual(request.Args, []string{"list", "-mod=readonly", "-deps", "-json", "-tags=" + tag, "example.com/acme/consumer/schema/models"}) {
					t.Fatalf("package request Args = %#v", request.Args)
				}
				pkg := make(map[string]any, len(packageTemplate))
				for key, value := range packageTemplate {
					pkg[key] = value
				}
				pkg["GoFiles"] = []string{selected}
				stdout = toolchainJSONStream(t, pkg)
			}
			return toolchain.Result{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: stdout}, nil
		})
		spec := validCompileSpec(fixture)
		spec.BuildTags = []string{tag}
		compilation, err := toolchain.CompileBuildInputManifest(context.Background(), runner, spec)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := compilation.Manifest()
		if err != nil {
			t.Fatal(err)
		}
		return manifest
	}
	alpha := compile("alpha", "alpha.go")
	beta := compile("beta", "beta.go")
	paths := func(manifest toolchain.BuildInputManifest) []string {
		t.Helper()
		inputs, err := manifest.Inputs()
		if err != nil {
			t.Fatal(err)
		}
		result := make([]string, len(inputs))
		for index, input := range inputs {
			result[index], err = input.Path()
			if err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	alphaPaths, betaPaths := paths(alpha), paths(beta)
	if !containsString(alphaPaths, "schema/models/alpha.go") || containsString(alphaPaths, "schema/models/beta.go") ||
		!containsString(betaPaths, "schema/models/beta.go") || containsString(betaPaths, "schema/models/alpha.go") {
		t.Fatalf("tag-selected inputs = alpha %#v, beta %#v", alphaPaths, betaPaths)
	}
	alphaDigest, alphaErr := alpha.Digest()
	betaDigest, betaErr := beta.Digest()
	if alphaErr != nil || betaErr != nil || alphaDigest == betaDigest {
		t.Fatalf("tag-selected manifest digests = %s/%s, %v/%v", alphaDigest.String(), betaDigest.String(), alphaErr, betaErr)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestBuildInputManifestAndSnapshotExposeDefensiveReadback(t *testing.T) {
	fixture := newToolchainFixture(t)
	runner := &recordingRunner{results: []toolchain.Result{
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.moduleList},
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: fixture.packageList},
	}}
	compilation, err := toolchain.CompileBuildInputManifest(context.Background(), runner, validCompileSpec(fixture))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := manifest.Digest()
	if err != nil || digest.String() == "" {
		t.Fatalf("Digest() = %s, %v", digest.String(), err)
	}
	if err := toolchain.VerifyBuildInputManifest(manifest); err != nil {
		t.Fatalf("VerifyBuildInputManifest() error = %v", err)
	}
	for _, data := range [][]byte{canonical, {0xff, '{'}} {
		_, zeroSourceErr := toolchain.ParseBuildInputManifestSnapshot(provenance.DomainSource{}, data)
		assertToolchainError(t, zeroSourceErr, "build_input_snapshot_invalid", "decode", "document_invalid", "", "", 0)
	}
	source, err := provenance.ParseDomainSource("quality/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := toolchain.ParseBuildInputManifestSnapshot(source, canonical)
	if err != nil {
		var toolErr *toolchain.Error
		if errors.As(err, &toolErr) {
			t.Fatalf("ParseBuildInputManifestSnapshot() error = (%q,%q,%q,%q)", toolErr.Code(), toolErr.Stage(), toolErr.Reason(), toolErr.Pointer())
		}
		t.Fatal(err)
	}
	snapshotCanonical, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(snapshotCanonical, canonical) {
		t.Fatalf("snapshot CanonicalJSON() = %s, %v", snapshotCanonical, err)
	}
	snapshotDigest, err := snapshot.Digest()
	if err != nil || snapshotDigest != digest {
		t.Fatalf("snapshot Digest() = %s, %v; want %s", snapshotDigest.String(), err, digest.String())
	}
	inputs, err := snapshot.Inputs()
	if err != nil || len(inputs) == 0 {
		t.Fatalf("snapshot Inputs() = %#v, %v", inputs, err)
	}
	firstPath, _ := inputs[0].Path()
	inputs[0] = toolchain.RetainedBuildInput{}
	again, err := snapshot.Inputs()
	againPath, pathErr := again[0].Path()
	if err != nil || pathErr != nil || againPath != firstPath {
		t.Fatalf("snapshot Inputs() after mutation = %q, %v/%v", againPath, err, pathErr)
	}

	schemaA, schemaB := toolchain.BuildInputManifestSchema(), toolchain.BuildInputManifestSchema()
	if len(schemaA) == 0 || !bytes.Equal(schemaA, schemaB) {
		t.Fatal("BuildInputManifestSchema() is empty or unstable")
	}
	schemaA[0] ^= 0xff
	if bytes.Equal(schemaA, toolchain.BuildInputManifestSchema()) {
		t.Fatal("BuildInputManifestSchema() is not defensive")
	}

	_, err = (toolchain.BuildInputManifest{}).CanonicalJSON()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "manifest_state_invalid", "/buildInputs", "", 0)
	_, err = (toolchain.BuildInputManifestSnapshot{}).CanonicalJSON()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "manifest_snapshot_state_invalid", "/buildInputs", "", 0)
}

func TestBuildInputReadbackZeroValuesReturnClosedErrors(t *testing.T) {
	compilation := toolchain.BuildInputCompilation{}
	_, err := compilation.ModuleGraph()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "compilation_state_invalid", "/buildInputCompilation", "", 0)
	_, err = compilation.Manifest()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "compilation_state_invalid", "/buildInputCompilation", "", 0)
	_, err = compilation.ExecutableVersion()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "compilation_state_invalid", "/buildInputCompilation", "", 0)

	local := toolchain.RetainedLocalModule{}
	_, err = local.Role()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_module_state_invalid", "/retainedModules/0", "", 0)
	_, err = local.Module()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_module_state_invalid", "/retainedModules/0", "", 0)
	_, _, err = local.RepositoryPath()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_module_state_invalid", "/retainedModules/0", "", 0)

	input := toolchain.RetainedBuildInput{}
	_, err = input.Module()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", 0)
	_, err = input.Path()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", 0)
	_, err = input.Kind()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", 0)
	_, err = input.Size()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", 0)
	_, err = input.Digest()
	assertToolchainError(t, err, "build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", 0)
}

type runnerFunc func(context.Context, toolchain.Request) (toolchain.Result, error)

func (f runnerFunc) Run(ctx context.Context, request toolchain.Request) (toolchain.Result, error) {
	return f(ctx, request)
}

func validCompileSpec(fixture toolchainFixture) toolchain.BuildInputCompileSpec {
	return toolchain.BuildInputCompileSpec{
		RepositoryRoot: fixture.repository, ScratchRoot: fixture.scratch,
		SchemaDir: fixture.schemaDir, SchemaImportPath: "example.com/acme/consumer/schema/models",
		Tool:         toolchain.Tool{ID: "go", Version: "v1.0.0", Executable: "go"},
		ToolModule:   toolchain.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.1.0"},
		HelperDigest: provenance.SHA256([]byte("helper")),
	}
}

func assertToolchainError(t *testing.T, err error, code, stage, reason, pointer, toolID string, exitCode int) *toolchain.Error {
	t.Helper()
	if err == nil {
		t.Fatal("operation succeeded, want *toolchain.Error")
	}
	var toolErr *toolchain.Error
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *toolchain.Error", err)
	}
	if toolErr.Code() != code || toolErr.Stage() != stage || toolErr.Reason() != reason || toolErr.Pointer() != pointer || toolErr.ToolID() != toolID || toolErr.ExitCode() != exitCode {
		t.Fatalf("error tuple = (%q,%q,%q,%q,%q,%d), want (%q,%q,%q,%q,%q,%d)", toolErr.Code(), toolErr.Stage(), toolErr.Reason(), toolErr.Pointer(), toolErr.ToolID(), toolErr.ExitCode(), code, stage, reason, pointer, toolID, exitCode)
	}
	if toolErr.Source() != "" || toolErr.Unwrap() == nil {
		t.Fatalf("Source/Unwrap = %q/%v", toolErr.Source(), toolErr.Unwrap())
	}
	return toolErr
}
