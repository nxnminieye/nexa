package transaction_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

func TestCreateManualIsCreateOnceAndExcludedFromArtifactManifest(t *testing.T) {
	repositoryPath := t.TempDir()
	source := provenance.Source{Ref: mustRepositoryRef(t, "api/account.api", "operation:account.get"), Digest: provenance.SHA256([]byte("api"))}
	manualPath := "internal/logic/account/getlogic.go"
	initial := []byte("package account\n")
	request := transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "api-go", Version: "v1.0.0"},
		Sources:   []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{
			ID: "account-get-logic", Path: manualPath, Owner: "nexa.dev/generator/apigo-manual/v1",
			Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain,
			CreateManual: true,
		}},
		ManifestPath: ".nexa/generation/api-go.manifest.json",
	}

	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildTransactionPlan(t, repositoryPath, request, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	changes := plan.Changes()
	if len(changes) != 1 || changes[0].Kind() != transaction.ChangeCreateManual || changes[0].Path() != manualPath {
		t.Fatalf("manual changes = %#v", changes)
	}
	next, ok := plan.NextManifest()
	if !ok || len(next.Artifacts()) != 0 {
		t.Fatalf("manual file entered next artifact manifest: %#v, %v", next.Artifacts(), ok)
	}
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(repositoryPath, manualPath)); err != nil || !bytes.Equal(got, initial) {
		t.Fatalf("created manual file = %q, %v", got, err)
	}

	modified := []byte("package account\n\nfunc customized() {}\n")
	if err := os.WriteFile(filepath.Join(repositoryPath, manualPath), modified, 0o644); err != nil {
		t.Fatal(err)
	}
	request.Previous = &next
	root, err = os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildTransactionPlan(t, repositoryPath, request, initial)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := transaction.Check(second, root)
	closeErr := root.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("Check() = %#v, %v, close=%v", checked, err, closeErr)
	}
	if len(second.Changes()) != 0 || len(second.Conflicts()) != 0 || !checked.Clean() {
		t.Fatalf("second plan = changes:%#v conflicts:%#v check:%#v", second.Changes(), second.Conflicts(), checked)
	}
	if got, err := os.ReadFile(filepath.Join(repositoryPath, manualPath)); err != nil || !bytes.Equal(got, modified) {
		t.Fatalf("manual customization changed = %q, %v", got, err)
	}
}

func TestCreateManualRejectsGeneratedDeletionPolicy(t *testing.T) {
	repositoryPath := t.TempDir()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "api/account.api", "operation:account.get"), Digest: provenance.SHA256([]byte("api"))}
	_, err = buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "api-go", Version: "v1.0.0"},
		Sources:   []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{
			ID: "account-get-logic", Path: "internal/logic/account/getlogic.go", Owner: "nexa.dev/generator/apigo-manual/v1",
			Sources:     []provenance.SourceRef{source.Ref},
			StalePolicy: artifact.StaleDeleteIfUnmodified, CreateManual: true,
		}},
		ManifestPath: ".nexa/generation/api-go.manifest.json",
	}, []byte("package account\n"))
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "stale_policy_invalid" {
		t.Fatalf("Build() error = %#v", err)
	}
}

func TestCreateManualRejectsGeneratedOwnershipTransition(t *testing.T) {
	repositoryPath := t.TempDir()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "api/account.api", "operation:account.get"), Digest: provenance.SHA256([]byte("api"))}
	generated := transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "api-go", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{
			ID: "account-get-logic", Path: "internal/logic/account/getlogic.go", Owner: "nexa.dev/generator/api-go/v1",
			Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain,
		}},
		ManifestPath: ".nexa/generation/api-go.manifest.json",
	}
	first, err := buildTransactionPlan(t, repositoryPath, generated, []byte("package account\n"))
	if err != nil {
		t.Fatal(err)
	}
	previous, ok := first.NextManifest()
	if !ok {
		t.Fatal("generated plan has no next manifest")
	}
	manual := generated
	manual.Previous = &previous
	manual.Expected = append([]transaction.ArtifactInput(nil), generated.Expected...)
	manual.Expected[0].Owner = "nexa.dev/generator/apigo-manual/v1"
	manual.Expected[0].CreateManual = true
	_, err = buildTransactionPlan(t, repositoryPath, manual, []byte("package account\n"))
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "manual_ownership_transition" {
		t.Fatalf("Build() error = %#v", err)
	}
}

func TestCreateManualDoesNotReplaceFileCreatedAfterPlan(t *testing.T) {
	repositoryPath := t.TempDir()
	source := provenance.Source{Ref: mustRepositoryRef(t, "api/account.api", "operation:account.get"), Digest: provenance.SHA256([]byte("api"))}
	manualPath := "internal/logic/account/getlogic.go"
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "api-go", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{
			ID: "account-get-logic", Path: manualPath, Owner: "nexa.dev/generator/apigo-manual/v1",
			Sources:     []provenance.SourceRef{source.Ref},
			StalePolicy: artifact.StaleRetain, CreateManual: true,
		}},
		ManifestPath: ".nexa/generation/api-go.manifest.json",
	}, []byte("package generated\n"))
	if closeErr := root.Close(); err != nil || closeErr != nil {
		t.Fatalf("Build() = %v, close=%v", err, closeErr)
	}
	external := []byte("package customized\n")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repositoryPath, manualPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, manualPath), external, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err == nil {
		t.Fatal("Write() replaced a manual file that appeared during commit")
	}
	if got, err := os.ReadFile(filepath.Join(repositoryPath, manualPath)); err != nil || !bytes.Equal(got, external) {
		t.Fatalf("appearing manual file = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(repositoryPath, ".nexa/generation/api-go.manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("failed manual race published manifest: %v", err)
	}
}

func TestOverwriteManualPlanBindsCandidateAndPriorDigest(t *testing.T) {
	repositoryPath := t.TempDir()
	prior := []byte("package logic\n\nfunc customized() {}\n")
	candidate := []byte("package logic\n\nfunc GetAccount() {}\n")
	request := overwriteManualRequest(t)
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, prior)

	plan, err := buildTransactionPlan(t, repositoryPath, request, candidate)
	if err != nil {
		t.Fatal(err)
	}
	changes := plan.Changes()
	if len(changes) != 1 || changes[0].Kind() != transaction.ChangeUpdate || changes[0].Digest() != provenance.SHA256(candidate) {
		t.Fatalf("overwrite changes = %#v", changes)
	}
	if got, ok := changes[0].PriorDigest(); !ok || got != provenance.SHA256(prior) {
		t.Fatalf("prior digest = %s, %v", got.String(), ok)
	}
	if !bytes.Contains(plan.CanonicalJSON(), []byte(`"overwriteManual":true`)) {
		t.Fatalf("canonical plan does not bind overwrite mode: %s", plan.CanonicalJSON())
	}
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(repositoryPath, request.Expected[0].Path)); err != nil || !bytes.Equal(got, candidate) {
		t.Fatalf("overwritten target = %q, %v", got, err)
	}
}

func TestOverwriteManualIsExcludedFromManifest(t *testing.T) {
	repositoryPath := t.TempDir()
	request := overwriteManualRequest(t)
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, []byte("package logic\n"))

	plan, err := buildTransactionPlan(t, repositoryPath, request, []byte("package logic\n\nfunc GetAccount() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	next, ok := plan.NextManifest()
	if !ok || len(next.Artifacts()) != 0 {
		t.Fatalf("overwrite manual entered next manifest: %#v, %v", next.Artifacts(), ok)
	}
}

func TestOverwriteManualCheckRejectsTargetDriftAfterPlan(t *testing.T) {
	repositoryPath := t.TempDir()
	request := overwriteManualRequest(t)
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, []byte("package logic\n"))
	plan, err := buildTransactionPlan(t, repositoryPath, request, []byte("package logic\n\nfunc GetAccount() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, []byte("package logic\n\nfunc drifted() {}\n"))
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	result, checkErr := transaction.Check(plan, root)
	closeErr := root.Close()
	if checkErr != nil || closeErr != nil {
		t.Fatalf("Check() = %#v, %v, close=%v", result, checkErr, closeErr)
	}
	conflicts := result.Conflicts()
	if len(conflicts) != 1 || conflicts[0].Reason() != "overwrite_manual_target_changed" {
		t.Fatalf("check conflicts = %#v", conflicts)
	}
}

func TestOverwriteManualWriteRejectsTargetDriftAfterPlan(t *testing.T) {
	repositoryPath := t.TempDir()
	request := overwriteManualRequest(t)
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, []byte("package logic\n"))
	plan, err := buildTransactionPlan(t, repositoryPath, request, []byte("package logic\n\nfunc GetAccount() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	drift := []byte("package logic\n\nfunc drifted() {}\n")
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, drift)
	_, err = transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	assertWriteReason(t, err, "current_changed_after_plan")
	if got, readErr := os.ReadFile(filepath.Join(repositoryPath, request.Expected[0].Path)); readErr != nil || !bytes.Equal(got, drift) {
		t.Fatalf("drifted target = %q, %v", got, readErr)
	}
}

func TestOverwriteManualModeMismatchChangesPlanDigest(t *testing.T) {
	repositoryPath := t.TempDir()
	request := overwriteManualRequest(t)
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, []byte("package logic\n"))
	candidate := []byte("package logic\n\nfunc GetAccount() {}\n")
	overwrite, err := buildTransactionPlan(t, repositoryPath, request, candidate)
	if err != nil {
		t.Fatal(err)
	}
	request.Expected[0].OverwriteManual = false
	request.Expected[0].CreateManual = true
	defaultPlan, err := buildTransactionPlan(t, repositoryPath, request, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if overwrite.PlanDigest() == defaultPlan.PlanDigest() {
		t.Fatal("overwrite mode did not change plan digest")
	}
}

func TestOverwriteManualMissingTargetCreatesOnce(t *testing.T) {
	repositoryPath := t.TempDir()
	request := overwriteManualRequest(t)
	candidate := []byte("package logic\n\nfunc GetAccount() {}\n")
	plan, err := buildTransactionPlan(t, repositoryPath, request, candidate)
	if err != nil {
		t.Fatal(err)
	}
	changes := plan.Changes()
	if len(changes) != 1 || changes[0].Kind() != transaction.ChangeCreateManual {
		t.Fatalf("missing overwrite changes = %#v", changes)
	}
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatal(err)
	}
	second, err := buildTransactionPlan(t, repositoryPath, request, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changes()) != 0 {
		t.Fatalf("second plan changes = %#v", second.Changes())
	}
	if _, err := transaction.Write(context.Background(), second, repositoryPath, transaction.WriteOptions{PlanDigest: second.PlanDigest()}); err != nil {
		t.Fatalf("second Write() = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(repositoryPath, request.Expected[0].Path)); err != nil || !bytes.Equal(got, candidate) {
		t.Fatalf("created target = %q, %v", got, err)
	}
}

func TestCreateAndOverwriteManualAreMutuallyExclusive(t *testing.T) {
	repositoryPath := t.TempDir()
	request := overwriteManualRequest(t)
	request.Expected[0].CreateManual = true
	_, err := buildTransactionPlan(t, repositoryPath, request, []byte("package logic\n"))
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "manual_mode_invalid" {
		t.Fatalf("Build() error = %#v", err)
	}
}

func TestOverwriteManualAcceptsDomainNeutralArtifactIdentity(t *testing.T) {
	repositoryPath := t.TempDir()
	source := provenance.Source{Ref: mustRepositoryRef(t, "docs/source.md", "section:guide"), Digest: provenance.SHA256([]byte("guide source"))}
	request := transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "docs-go", Version: "v3.2.1"},
		Sources:   []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{
			ID: "manual-guide", Path: "docs/generated/guide.md", Owner: "nexa.dev/generator/docs-manual/v1",
			Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain, OverwriteManual: true,
		}},
		ManifestPath: ".nexa/generation/docs-go.manifest.json",
	}
	prior := []byte("custom guide\n")
	candidate := []byte("generated guide\n")
	writeRepositoryFile(t, repositoryPath, request.Expected[0].Path, prior)

	plan, err := buildTransactionPlan(t, repositoryPath, request, candidate)
	if err != nil {
		t.Fatal(err)
	}
	changes := plan.Changes()
	if len(changes) != 1 || changes[0].Kind() != transaction.ChangeUpdate || changes[0].Digest() != provenance.SHA256(candidate) {
		t.Fatalf("domain-neutral overwrite changes = %#v", changes)
	}
	if got, ok := changes[0].PriorDigest(); !ok || got != provenance.SHA256(prior) {
		t.Fatalf("prior digest = %s, %v", got.String(), ok)
	}
}

func TestOverwriteManualRejectsInvalidMechanicalInputs(t *testing.T) {
	valid := overwriteManualRequest(t)
	tests := []struct {
		name   string
		reason string
		mutate func(*transaction.PlanRequest)
	}{
		{name: "id", reason: "artifact_id_invalid", mutate: func(r *transaction.PlanRequest) { r.Expected[0].ID = "ManualGuide" }},
		{name: "owner", reason: "artifact_owner_invalid", mutate: func(r *transaction.PlanRequest) { r.Expected[0].Owner = "docs-manual" }},
		{name: "stale policy", reason: "stale_policy_invalid", mutate: func(r *transaction.PlanRequest) { r.Expected[0].StalePolicy = artifact.StaleDeleteIfUnmodified }},
		{name: "sources", reason: "artifact_source_missing", mutate: func(r *transaction.PlanRequest) { r.Expected[0].Sources = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositoryPath := t.TempDir()
			request := valid
			request.Expected = append([]transaction.ArtifactInput(nil), valid.Expected...)
			test.mutate(&request)
			_, err := buildTransactionPlan(t, repositoryPath, request, []byte("package logic\n"))
			var typed *transaction.Error
			if !errors.As(err, &typed) || typed.Reason() != test.reason {
				t.Fatalf("Build() error = %#v", err)
			}
		})
	}
}

func TestOverwriteManualRejectsInvalidPathUsingNormalValidation(t *testing.T) {
	repositoryPath := t.TempDir()
	request := overwriteManualRequest(t)
	request.Expected = append([]transaction.ArtifactInput(nil), request.Expected...)
	request.Expected[0].Path = "../guide.md"
	request.Expected[0].Digest = provenance.SHA256([]byte("generated guide\n"))
	_, err := transaction.Build(context.Background(), repositoryPath, func(string, func(string, []byte) error) (transaction.PlanRequest, error) {
		return request, nil
	})
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "artifact_path_invalid" {
		t.Fatalf("Build() error = %#v", err)
	}
}

func overwriteManualRequest(t *testing.T) transaction.PlanRequest {
	t.Helper()
	source := provenance.Source{Ref: mustRepositoryRef(t, "docs/source.md", "section:guide"), Digest: provenance.SHA256([]byte("guide source"))}
	return transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "docs-go", Version: "v3.2.1"},
		Sources:   []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{
			ID: "manual-guide", Path: "docs/generated/guide.md", Owner: "nexa.dev/generator/docs-manual/v1",
			Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain, OverwriteManual: true,
		}},
		ManifestPath: ".nexa/generation/docs-go.manifest.json",
	}
}

func writeRepositoryFile(t *testing.T, repository, name string, content []byte) {
	t.Helper()
	path := filepath.Join(repository, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
