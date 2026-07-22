package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestResultProjectionsExposeMetadataWithoutSourceBytesOrAbsolutePaths(t *testing.T) {
	const privateBytes = "private-source-bytes\n"
	provider, ref := projectionProvider(t, privateBytes)
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := engine.New(engine.Options{
		Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: projectionMergeDriver{},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	selection, err := engine.NewSelection(engine.SelectionSpec{Release: ref, ProfileID: "default", Target: "services/sample"})
	if err != nil {
		t.Fatal(err)
	}
	request := engine.PlanRequest{RepositoryRoot: root, Selection: selection}
	plan, err := candidate.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	check, err := candidate.Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	status, err := candidate.Status(context.Background(), engine.ManagedRequest{RepositoryRoot: root, Key: mustProjectionKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := candidate.Materialize(context.Background(), engine.MaterializeRequest{PlanRequest: request})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "services/sample/value.txt")
	if err := os.WriteFile(file, []byte("consumer edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := candidate.Diff(context.Background(), engine.ManagedRequest{RepositoryRoot: root, Key: mustProjectionKey(t)})
	if err != nil {
		t.Fatal(err)
	}

	values := []struct {
		command string
		value   any
	}{{"plan", projectPlan(plan)}, {"check", projectCheck(check)}, {"status", projectStatus(status)}, {"materialize", projectResult(result)}, {"diff", projectDiff(diff)}}
	for _, item := range values {
		value := item.value
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(encoded, &document); err != nil || document["apiVersion"] != resultAPIVersion || document["kind"] == "" {
			t.Fatalf("projection=%s err=%v", encoded, err)
		}
		_, output := commandSchemas(item.command)
		var schemaDocument any
		if err := json.Unmarshal(output, &schemaDocument); err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("projection.json", schemaDocument); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile("projection.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(document); err != nil {
			t.Fatalf("%s schema rejected projection %s: %v", item.command, encoded, err)
		}
		for _, forbidden := range []string{privateBytes, "consumer edit", root} {
			if stringContains(string(encoded), forbidden) {
				t.Fatalf("projection leaked private state: %s", encoded)
			}
		}
	}
}

type projectionMergeDriver struct{}

func (projectionMergeDriver) Merge(_ context.Context, input engine.TextMergeInput) (engine.TextMergeResult, error) {
	return engine.NewTextMergeResult(input.New, true), nil
}

func projectionProvider(t *testing.T, content string) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	data := []byte(content)
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample", ModulePath: "example.test/sample", PackagePath: "example.test/sample/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{{Path: "value.txt", Mode: sourceplugin.Mode0644, Size: int64(len(data)), Digest: provenance.SHA256(data)}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{"value.txt"}, RequiresProfiles: []string{}, RequiresBundles: []sourceplugin.BundleRequirementSpec{}, Validations: []sourceplugin.ValidationRecipeSpec{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: "value.txt", Content: data}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sourceplugin.NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	return provider, ref
}

func mustProjectionKey(t *testing.T) lock.Key {
	t.Helper()
	key, err := lock.NewKey("sample", "services/sample")
	if err != nil {
		t.Fatal(err)
	}
	return key
}
