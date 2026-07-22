package transaction_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestPlanSchemaValidatesCanonicalPlan(t *testing.T) {
	repositoryPath := t.TempDir()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	plan, err := buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Expected:     []transaction.ArtifactInput{{ID: "account", Path: "generated/account.proto", Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain}},
		ManifestPath: ".nexa/generation/crud-proto.manifest.json",
	}, []byte("proto"))
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(transaction.PlanSchema(), &schemaDocument); err != nil {
		t.Fatalf("PlanSchema() = invalid JSON: %v", err)
	}
	const publicPlanSchemaURL = "https://nexa.dev/schemas/generation-plan-v2"
	if transaction.PlanAPIVersion != "nexa.dev/generation-plan/v2" {
		t.Fatalf("PlanAPIVersion = %q", transaction.PlanAPIVersion)
	}
	if got := schemaDocument.(map[string]any)["$id"]; got != publicPlanSchemaURL {
		t.Fatalf("plan schema $id = %q, public URL = %q", got, publicPlanSchemaURL)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(publicPlanSchemaURL, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(publicPlanSchemaURL)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(plan.CanonicalJSON(), &document); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(document); err != nil {
		t.Fatalf("canonical plan failed public schema: %v", err)
	}
	artifactDocument := document.(map[string]any)["artifacts"].([]any)[0].(map[string]any)
	artifactDocument["createManual"] = true
	artifactDocument["stalePolicy"] = "delete-if-unmodified"
	if err := compiled.Validate(document); err == nil {
		t.Fatal("public schema accepted createManual with generated deletion policy")
	}
	result, err := transaction.Check(plan, root)
	if err != nil {
		t.Fatal(err)
	}
	var resultSchemaDocument any
	if err := json.Unmarshal(transaction.ResultSchema(), &resultSchemaDocument); err != nil {
		t.Fatalf("ResultSchema() = invalid JSON: %v", err)
	}
	resultCompiler := jsonschema.NewCompiler()
	if err := resultCompiler.AddResource("https://nexa.dev/schemas/generation-result-v1", resultSchemaDocument); err != nil {
		t.Fatal(err)
	}
	compiledResult, err := resultCompiler.Compile("https://nexa.dev/schemas/generation-result-v1")
	if err != nil {
		t.Fatal(err)
	}
	var resultDocument any
	if err := json.Unmarshal(result.CanonicalJSON(), &resultDocument); err != nil {
		t.Fatal(err)
	}
	if err := compiledResult.Validate(resultDocument); err != nil {
		t.Fatalf("canonical result failed public schema: %v", err)
	}
}

func TestBuildCreatesDeterministicReadOnlyPlanAndNextManifest(t *testing.T) {
	repositoryPath := t.TempDir()
	unchangedPath := "generated/existing.proto"
	unchangedContent := []byte("existing-proto")
	if err := os.MkdirAll(filepath.Join(repositoryPath, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, unchangedPath), unchangedContent, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	schemaSource := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	moduleSource := provenance.Source{Ref: mustRepositoryRef(t, "go.mod", ""), Digest: provenance.SHA256([]byte("module"))}
	createdContent := []byte("generated-proto-secret-payload")
	inputs := []transaction.ArtifactInput{
		{
			ID: "account-crud-proto", Path: "generated/account.crud.proto", Owner: "nexa.dev/generator/crud-proto/v1",
			Sources: []provenance.SourceRef{schemaSource.Ref, moduleSource.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified,
		},
		{
			ID: "existing-proto", Path: unchangedPath, Owner: "nexa.dev/generator/crud-proto/v1",
			Sources: []provenance.SourceRef{schemaSource.Ref}, StalePolicy: artifact.StaleRetain,
		},
	}
	request := transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"},
		Sources:   []provenance.Source{schemaSource, moduleSource}, Expected: inputs,
		ManifestPath: ".nexa/generation/crud-proto.manifest.json",
	}
	plan, err := buildTransactionPlan(t, repositoryPath, request, createdContent, unchangedContent)
	if err != nil {
		t.Fatalf("Build() error = %#v", err)
	}
	changes := plan.Changes()
	if len(changes) != 1 || changes[0].Kind() != transaction.ChangeCreate || changes[0].ID() != "account-crud-proto" || changes[0].Path() != "generated/account.crud.proto" || changes[0].Digest() != provenance.SHA256(createdContent) {
		t.Fatalf("changes = %#v", changes)
	}
	if len(plan.Conflicts()) != 0 || plan.PlanDigest().String() == "" {
		t.Fatalf("plan state = conflicts:%#v digest:%q", plan.Conflicts(), plan.PlanDigest().String())
	}
	next, ok := plan.NextManifest()
	if !ok || len(next.Artifacts()) != 2 || next.Artifacts()[0].Owner() != "crud-proto" {
		t.Fatalf("next manifest = %#v, %v", next, ok)
	}
	canonical := plan.CanonicalJSON()
	if len(canonical) == 0 || bytes.Contains(canonical, createdContent) {
		t.Fatalf("canonical plan exposed artifact content: %s", canonical)
	}
	if _, err := os.Stat(filepath.Join(repositoryPath, "generated/account.crud.proto")); !os.IsNotExist(err) {
		t.Fatalf("Build wrote planned artifact: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(repositoryPath, unchangedPath)); err != nil || !bytes.Equal(got, unchangedContent) {
		t.Fatalf("existing artifact changed: %q, %v", got, err)
	}

	reversed := request
	reversed.Sources = []provenance.Source{moduleSource, schemaSource}
	reversed.Expected = []transaction.ArtifactInput{inputs[1], inputs[0]}
	second, err := buildTransactionPlan(t, repositoryPath, reversed, unchangedContent, createdContent)
	if err != nil {
		t.Fatal(err)
	}
	if second.PlanDigest() != plan.PlanDigest() || !bytes.Equal(second.CanonicalJSON(), canonical) {
		t.Fatal("plan depends on caller slice order")
	}
	ownerChanged := request
	ownerChanged.Expected = append([]transaction.ArtifactInput(nil), request.Expected...)
	ownerChanged.Expected[0].Owner = "nexa.dev/generator/crud-proto/v2"
	third, err := buildTransactionPlan(t, repositoryPath, ownerChanged, createdContent, unchangedContent)
	if err != nil {
		t.Fatal(err)
	}
	if third.PlanDigest() == plan.PlanDigest() {
		t.Fatal("versioned transaction owner is absent from plan digest")
	}

	createdContent[0] = '!'
	inputs[0].Sources[0] = provenance.SourceRef{}
	if plan.Changes()[0].Digest() != provenance.SHA256([]byte("generated-proto-secret-payload")) {
		t.Fatal("plan retained caller-owned artifact bytes")
	}
}

func TestBuildRejectsInvalidPlanInputsWithoutWriting(t *testing.T) {
	repositoryPath := t.TempDir()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	valid := func() transaction.PlanRequest {
		return transaction.PlanRequest{
			Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
			Expected:     []transaction.ArtifactInput{{ID: "account", Path: "generated/account.proto", Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain}},
			ManifestPath: ".nexa/generation/crud-proto.manifest.json",
		}
	}
	tests := []struct {
		name, reason string
		mutate       func(*transaction.PlanRequest)
	}{
		{name: "nil root", reason: "repository_invalid"},
		{name: "duplicate id", reason: "artifact_id_duplicate", mutate: func(r *transaction.PlanRequest) {
			copy := r.Expected[0]
			copy.Path = "generated/second.proto"
			r.Expected = append(r.Expected, copy)
		}},
		{name: "duplicate path", reason: "artifact_path_duplicate", mutate: func(r *transaction.PlanRequest) {
			copy := r.Expected[0]
			copy.ID = "second"
			r.Expected = append(r.Expected, copy)
		}},
		{name: "manifest alias", reason: "manifest_path_alias", mutate: func(r *transaction.PlanRequest) { r.ManifestPath = r.Expected[0].Path }},
		{name: "invalid owner", reason: "artifact_owner_invalid", mutate: func(r *transaction.PlanRequest) { r.Expected[0].Owner = "crud-proto" }},
		{name: "unresolved source", reason: "artifact_source_unresolved", mutate: func(r *transaction.PlanRequest) {
			r.Expected[0].Sources = []provenance.SourceRef{mustRepositoryRef(t, "other.go", "node")}
		}},
		{name: "invalid policy", reason: "stale_policy_invalid", mutate: func(r *transaction.PlanRequest) { r.Expected[0].StalePolicy = "delete" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid()
			if test.mutate != nil {
				test.mutate(&request)
			}
			selectedRepository := repositoryPath
			if test.name == "nil root" {
				selectedRepository = filepath.Join(repositoryPath, "missing")
			}
			_, err := buildTransactionPlan(t, selectedRepository, request, []byte("proto"))
			var owner *transaction.Error
			if !errors.As(err, &owner) || owner.Reason() != test.reason {
				t.Fatalf("Build() error = %#v, want reason %q", err, test.reason)
			}
		})
	}
	entries, err := os.ReadDir(repositoryPath)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid Build left staging entries: %#v, %v", entries, err)
	}
}
