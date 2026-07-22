package toolchain_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

func TestCompileBuildInputManifestUsesExactReadonlyRunnerProtocol(t *testing.T) {
	fixture := newToolchainFixture(t)
	runner := &recordingRunner{results: []toolchain.Result{
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", ExitCode: 0, Stdout: fixture.moduleList},
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", ExitCode: 0, Stdout: fixture.packageList},
	}}
	compilation, err := toolchain.CompileBuildInputManifest(context.Background(), runner, toolchain.BuildInputCompileSpec{
		RepositoryRoot:   fixture.repository,
		ScratchRoot:      fixture.scratch,
		SchemaDir:        fixture.schemaDir,
		SchemaImportPath: "example.com/acme/consumer/schema/models",
		BuildTags:        []string{"zeta", "alpha"},
		Tool: toolchain.Tool{
			ID: "go", Version: "v1.0.0", Executable: "go",
			InputScopes: []string{"repository", "scratch"},
			Environment: []toolchain.EnvironmentRule{{Name: "PATH", Source: toolchain.EnvironmentHost}},
			Probe:       toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "go1.25.0"},
		},
		Environment:  []toolchain.EnvVar{{Name: "PATH", Value: "/usr/bin"}},
		ToolModule:   toolchain.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.1.0"},
		HelperDigest: provenance.SHA256([]byte("helper")),
	})
	if err != nil {
		t.Fatalf("CompileBuildInputManifest() error = %v", err)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("Runner calls = %d, want 2", len(runner.requests))
	}
	wantArgs := [][]string{
		{"list", "-mod=readonly", "-m", "-json", "all"},
		{"list", "-mod=readonly", "-deps", "-json", "-tags=alpha,zeta", "example.com/acme/consumer/schema/models"},
	}
	for index, request := range runner.requests {
		if !reflect.DeepEqual(request.Args, wantArgs[index]) {
			t.Errorf("request %d Args = %#v, want %#v", index, request.Args, wantArgs[index])
		}
		if request.RepositoryRoot != fixture.repository || request.StagingRoot != fixture.scratch || request.WorkDir != fixture.scratch {
			t.Errorf("request %d roots/workdir = %#v", index, request)
		}
		if request.Tool.ID != "go" || request.Tool.Version != "v1.0.0" || request.Tool.Executable != "go" {
			t.Errorf("request %d Tool = %#v", index, request.Tool)
		}
		if !reflect.DeepEqual(request.Environment, []toolchain.EnvVar{{Name: "CGO_ENABLED", Value: "0"}, {Name: "PATH", Value: "/usr/bin"}}) {
			t.Errorf("request %d Environment = %#v", index, request.Environment)
		}
	}
	version, err := compilation.ExecutableVersion()
	if err != nil || version != "go1.25.0" {
		t.Fatalf("ExecutableVersion() = %q, %v", version, err)
	}
	graph, err := compilation.ModuleGraph()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := graph.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte(fixture.repository)) || bytes.Contains(canonical, []byte(fixture.scratch)) {
		t.Fatalf("absolute root leaked into ModuleGraph: %s", canonical)
	}
	digest, err := graph.Digest()
	if err != nil || digest != provenance.SHA256(canonical) {
		t.Fatalf("Digest() = %s, %v", digest.String(), err)
	}
	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	tags, err := manifest.BuildTags()
	if err != nil || !reflect.DeepEqual(tags, []string{"alpha", "zeta"}) {
		t.Fatalf("BuildTags() = %#v, %v", tags, err)
	}
	inputs, err := manifest.Inputs()
	if err != nil || len(inputs) != 3 {
		t.Fatalf("Inputs() count = %d, %v", len(inputs), err)
	}
}

type recordingRunner struct {
	requests []toolchain.Request
	results  []toolchain.Result
}

func (r *recordingRunner) Run(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
	copyRequest := request
	copyRequest.Args = append([]string(nil), request.Args...)
	copyRequest.Environment = append([]toolchain.EnvVar(nil), request.Environment...)
	copyRequest.Tool.Args = append([]string(nil), request.Tool.Args...)
	copyRequest.Tool.InputScopes = append([]string(nil), request.Tool.InputScopes...)
	copyRequest.Tool.WriteScopes = append([]string(nil), request.Tool.WriteScopes...)
	copyRequest.Tool.Environment = append([]toolchain.EnvironmentRule(nil), request.Tool.Environment...)
	copyRequest.Tool.Probe.Args = append([]string(nil), request.Tool.Probe.Args...)
	r.requests = append(r.requests, copyRequest)
	if len(request.Args) > 0 {
		request.Args[0] = "mutated-by-runner"
	}
	request.Environment[0].Value = "mutated-by-runner"
	if len(r.requests) > len(r.results) {
		return toolchain.Result{}, errors.New("unexpected Runner call")
	}
	result := r.results[len(r.requests)-1]
	result.Stdout = append([]byte(nil), result.Stdout...)
	return result, nil
}

type toolchainFixture struct {
	repository, scratch string
	schemaDir           provenance.DomainSource
	moduleList          []byte
	packageList         []byte
}

func newToolchainFixture(t *testing.T) toolchainFixture {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	scratch := filepath.Join(root, "scratch")
	writeToolchainFile(t, filepath.Join(repository, "go.mod"), "module example.com/acme/consumer\n\ngo 1.25\n")
	writeToolchainFile(t, filepath.Join(repository, "schema/models/schema.go"), "package models\n")
	writeToolchainFile(t, filepath.Join(scratch, "go.mod"), "module example.com/ent-helper\n\ngo 1.25\n")
	consumer := map[string]any{
		"Path": "example.com/acme/consumer", "Version": "v0.0.0", "Dir": repository, "GoMod": filepath.Join(repository, "go.mod"),
		"Replace": map[string]any{"Path": "example.com/acme/consumer", "Dir": repository, "GoMod": filepath.Join(repository, "go.mod")},
	}
	moduleList := toolchainJSONStream(t,
		map[string]any{"Path": "example.com/ent-helper", "Main": true, "Dir": scratch, "GoMod": filepath.Join(scratch, "go.mod")},
		consumer,
		map[string]any{"Path": "github.com/nxnminieye/nexa", "Version": "v0.1.0", "Sum": "h1:IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI=", "GoModSum": "h1:MzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzM="},
	)
	packageList := toolchainJSONStream(t, map[string]any{
		"Dir": filepath.Join(repository, "schema/models"), "ImportPath": "example.com/acme/consumer/schema/models", "Name": "models", "Module": consumer, "GoFiles": []string{"schema.go"},
	})
	schemaDir, err := provenance.ParseDomainSource("schema/models")
	if err != nil {
		t.Fatal(err)
	}
	return toolchainFixture{repository: repository, scratch: scratch, schemaDir: schemaDir, moduleList: moduleList, packageList: packageList}
}

func toolchainJSONStream(t *testing.T, values ...any) []byte {
	t.Helper()
	var result strings.Builder
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result.Write(encoded)
		result.WriteByte('\n')
	}
	return []byte(result.String())
}

func writeToolchainFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
