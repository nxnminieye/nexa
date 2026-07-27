package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

func TestRetainedBuildInputManifestExternalReadbackRoundTrip(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	scratch := filepath.Join(root, "scratch")
	writeRetainedInputFixture(t, filepath.Join(repository, "go.mod"), "module example.com/external/consumer\n\ngo 1.25\n")
	writeRetainedInputFixture(t, filepath.Join(repository, "schema/models/schema.go"), "package models\n")
	writeRetainedInputFixture(t, filepath.Join(scratch, "go.mod"), "module example.com/external/helper\n\ngo 1.25\n")

	consumer := map[string]any{
		"Path": "example.com/external/consumer", "Version": "v0.0.0", "Dir": repository, "GoMod": filepath.Join(repository, "go.mod"),
		"Replace": map[string]any{"Path": "example.com/external/consumer", "Dir": repository, "GoMod": filepath.Join(repository, "go.mod")},
	}
	moduleList := retainedInputJSONStream(t,
		map[string]any{"Path": "example.com/external/helper", "Main": true, "Dir": scratch, "GoMod": filepath.Join(scratch, "go.mod")},
		consumer,
		map[string]any{"Path": "github.com/nxnminieye/nexa", "Version": "v0.1.0", "Sum": "h1:IiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiIiI=", "GoModSum": "h1:MzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzMzM="},
	)
	packageList := retainedInputJSONStream(t, map[string]any{
		"Dir": filepath.Join(repository, "schema/models"), "ImportPath": "example.com/external/consumer/schema/models",
		"Name": "models", "Module": consumer, "GoFiles": []string{"schema.go"},
	})
	runner := &retainedInputRunner{results: []toolchain.Result{
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: moduleList},
		{ToolID: "go", Version: "v1.0.0", ExecutableVersion: "go1.25.0", Stdout: packageList},
	}}
	schemaDir, err := provenance.ParseDomainSource("schema/models")
	if err != nil {
		t.Fatal(err)
	}
	compilation, err := toolchain.CompileBuildInputManifest(context.Background(), runner, toolchain.BuildInputCompileSpec{
		RepositoryRoot: repository, ScratchRoot: scratch, SchemaDir: schemaDir,
		SchemaImportPath: "example.com/external/consumer/schema/models", BuildTags: []string{"zeta", "alpha"},
		Tool:         toolchain.Tool{ID: "go", Version: "v1.0.0", Executable: "go"},
		ToolModule:   toolchain.ModuleRequirement{Path: "github.com/nxnminieye/nexa", Version: "v0.1.0"},
		HelperDigest: provenance.SHA256([]byte("external helper")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("Runner calls = %d, want 2", len(runner.requests))
	}
	manifest, err := compilation.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte(repository)) || bytes.Contains(canonical, []byte(scratch)) || bytes.HasSuffix(canonical, []byte("\n")) {
		t.Fatalf("manifest leaked a root or newline: %s", canonical)
	}
	digest, err := manifest.Digest()
	if err != nil || digest.String() == "" {
		t.Fatalf("manifest digest = %s, %v", digest.String(), err)
	}
	if err := toolchain.VerifyBuildInputManifest(manifest); err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("quality/external-retained-input.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := toolchain.ParseBuildInputManifestSnapshot(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	snapshotCanonical, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(snapshotCanonical, canonical) {
		t.Fatalf("snapshot canonical = %s, %v", snapshotCanonical, err)
	}
	snapshotDigest, err := snapshot.Digest()
	if err != nil || snapshotDigest != digest {
		t.Fatalf("snapshot digest = %s, %v; want %s", snapshotDigest.String(), err, digest.String())
	}
	tags, err := snapshot.BuildTags()
	if err != nil || !reflect.DeepEqual(tags, []string{"alpha", "zeta"}) {
		t.Fatalf("snapshot tags = %#v, %v", tags, err)
	}
	modules, err := snapshot.LocalModules()
	if err != nil || len(modules) != 2 {
		t.Fatalf("snapshot modules = %#v, %v", modules, err)
	}
	inputs, err := snapshot.Inputs()
	if err != nil || len(inputs) != 2 {
		t.Fatalf("snapshot inputs = %#v, %v", inputs, err)
	}
	paths := make([]string, len(inputs))
	for index, input := range inputs {
		path, pathErr := input.Path()
		module, moduleErr := input.Module()
		identity, identityErr := module.Module()
		if pathErr != nil || moduleErr != nil || identityErr != nil {
			t.Fatalf("input %d readback errors = %v/%v/%v", index, pathErr, moduleErr, identityErr)
		}
		paths[index] = identity.Path + ":" + path
	}
	if got := strings.Join(paths, ","); !strings.Contains(got, "example.com/external/consumer:go.mod") || !strings.Contains(got, "example.com/external/consumer:schema/models/schema.go") || strings.Contains(got, "example.com/external/helper:go.mod") {
		t.Fatalf("snapshot input closure = %q", got)
	}
	inputs[0] = toolchain.RetainedBuildInput{}
	again, err := snapshot.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again[0].Path(); err != nil {
		t.Fatalf("snapshot input slice was not defensive: %v", err)
	}
}

type retainedInputRunner struct {
	requests []toolchain.Request
	results  []toolchain.Result
}

func (r *retainedInputRunner) Run(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
	r.requests = append(r.requests, request)
	result := r.results[len(r.requests)-1]
	result.Stdout = append([]byte(nil), result.Stdout...)
	return result, nil
}

func retainedInputJSONStream(t *testing.T, values ...any) []byte {
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

func writeRetainedInputFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
