package transaction_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

func TestBuildAndCheckIncludeCompatibilityLockOutsideArtifactManifest(t *testing.T) {
	repositoryPath := t.TempDir()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	lockBytes := []byte("lock-v1\n")
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-lock", Path: "api/account.crud.lock.json", Owner: "nexa.dev/generator/crud-proto/v1",
		After: lockBytes, AfterDigest: provenance.SHA256(lockBytes), Sources: []provenance.SourceRef{source.Ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Expected:       []transaction.ArtifactInput{{ID: "account", Path: "api/account.proto", Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified}},
		ControlSources: []transaction.ControlSourceMutation{mutation}, ManifestPath: ".nexa/generation/crud-proto.manifest.json",
	}
	plan, err := buildTransactionPlan(t, repositoryPath, request, []byte("proto"))
	if err != nil {
		t.Fatal(err)
	}
	assertChangeSet(t, plan.Changes(), map[string]transaction.ChangeKind{"account": transaction.ChangeCreate, "account-lock": transaction.ChangeCreate})
	for _, change := range plan.Changes() {
		role, control := change.ControlSourceRole()
		if change.ID() == "account-lock" && (!control || role != transaction.ControlSourceCompatibilityLock) {
			t.Fatalf("lock change role = %q, %v", role, control)
		}
		if change.ID() == "account" && control {
			t.Fatalf("artifact change projected as control role %q", role)
		}
	}
	next, ok := plan.NextManifest()
	if !ok || len(next.Artifacts()) != 1 || next.Artifacts()[0].Path() != "api/account.proto" {
		t.Fatalf("next manifest includes control source: %#v, %v", next.Artifacts(), ok)
	}
	if bytes.Contains(mustCanonicalManifest(t, next), []byte("account.crud.lock.json")) {
		t.Fatal("compatibility lock leaked into Artifact Manifest")
	}
	result, err := transaction.Check(plan, root)
	if err != nil {
		t.Fatal(err)
	}
	assertChangeSet(t, result.Changes(), map[string]transaction.ChangeKind{"account": transaction.ChangeCreate, "account-lock": transaction.ChangeCreate})
	if _, err := os.Stat(filepath.Join(repositoryPath, "api/account.crud.lock.json")); !os.IsNotExist(err) {
		t.Fatalf("Build/Check wrote compatibility lock: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(repositoryPath, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "api/account.crud.lock.json"), []byte("manual"), 0o644); err != nil {
		t.Fatal(err)
	}
	conflicted, err := buildTransactionPlan(t, repositoryPath, request, []byte("proto"))
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicted.Conflicts()) != 1 || conflicted.Conflicts()[0].ID() != "account-lock" || conflicted.Conflicts()[0].Reason() != "initial_path_exists" {
		t.Fatalf("initial lock conflicts = %#v", conflicted.Conflicts())
	}
	if role, ok := conflicted.Conflicts()[0].ControlSourceRole(); !ok || role != transaction.ControlSourceCompatibilityLock {
		t.Fatalf("initial lock conflict role = %q, %v", role, ok)
	}
}

func TestCheckPreservesControlReadCauseWithoutRenderingRepositoryPath(t *testing.T) {
	repositoryPath := t.TempDir()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	lock := []byte("lock\n")
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-lock", Path: "api/account.lock.json", Owner: "nexa.dev/generator/crud-proto/v1",
		After: lock, AfterDigest: provenance.SHA256(lock), Sources: []provenance.SourceRef{source.Ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := transaction.Build(context.Background(), repositoryPath, func(string, func(string, []byte) error) (transaction.PlanRequest, error) {
		return transaction.PlanRequest{
			Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
			ControlSources: []transaction.ControlSourceMutation{mutation}, ManifestPath: ".nexa/generation/crud-proto.manifest.json",
			RevalidateSources: func(context.Context) ([]provenance.Source, error) { return []provenance.Source{source}, nil },
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = transaction.Check(plan, root)
	var typed *transaction.Error
	if !errors.As(err, &typed) || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Check() error = %#v", err)
	}
	if strings.Contains(err.Error(), repositoryPath) {
		t.Fatalf("safe error rendered repository path: %q", err)
	}
}

func TestBuildKeepsControlOnlySourcesInPlanButExcludesThemFromArtifactManifest(t *testing.T) {
	repositoryPath := t.TempDir()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	artifactSource := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	controlSource := provenance.Source{Ref: mustRepositoryRef(t, "api/account.crud.lock.json", ""), Digest: provenance.SHA256([]byte("lock-v0\n"))}
	lockBytes := []byte("lock-v1\n")
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-lock", Path: "api/account.crud.next.lock.json", Owner: "nexa.dev/generator/crud-proto/v1",
		After: lockBytes, AfterDigest: provenance.SHA256(lockBytes), Sources: []provenance.SourceRef{artifactSource.Ref, controlSource.Ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"},
		Sources:   []provenance.Source{controlSource, artifactSource},
		Expected: []transaction.ArtifactInput{{
			ID: "account", Path: "api/account.proto", Owner: "nexa.dev/generator/crud-proto/v1",
			Sources: []provenance.SourceRef{artifactSource.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified,
		}},
		ControlSources: []transaction.ControlSourceMutation{mutation},
		ManifestPath:   ".nexa/generation/crud-proto.manifest.json",
	}, []byte("proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(plan.CanonicalJSON(), []byte(controlSource.Ref.String())) {
		t.Fatal("control-only source was removed from transaction plan")
	}
	next, ok := plan.NextManifest()
	if !ok {
		t.Fatal("next Artifact Manifest is absent")
	}
	if got := next.Sources(); len(got) != 1 || got[0] != artifactSource {
		t.Fatalf("next manifest sources = %#v, want only artifact source %#v", got, artifactSource)
	}
	artifacts := next.Artifacts()
	if len(artifacts) != 1 || len(artifacts[0].Sources()) != 1 || artifacts[0].Sources()[0] != artifactSource.Ref {
		t.Fatalf("next artifact sources = %#v", artifacts)
	}
	if bytes.Contains(mustCanonicalManifest(t, next), []byte(controlSource.Ref.String())) {
		t.Fatal("control-only source leaked into Artifact Manifest")
	}
}

func TestBuildPlansCompatibilityLockUpdateAndRejectsPathAliases(t *testing.T) {
	repositoryPath := t.TempDir()
	lockPath := "api/account.crud.lock.json"
	beforeBytes := []byte("lock-v1\n")
	if err := os.MkdirAll(filepath.Join(repositoryPath, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, lockPath), beforeBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	before := provenance.Source{Ref: mustRepositoryRef(t, lockPath, ""), Digest: provenance.SHA256(beforeBytes)}
	after := []byte("lock-v2\n")
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-lock", Path: lockPath, Owner: "nexa.dev/generator/crud-proto/v1", Before: &before,
		After: after, AfterDigest: provenance.SHA256(after), Sources: []provenance.SourceRef{source.Ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		ControlSources: []transaction.ControlSourceMutation{mutation}, ManifestPath: ".nexa/generation/crud-proto.manifest.json",
	}
	plan, err := buildTransactionPlan(t, repositoryPath, base)
	if err != nil {
		t.Fatal(err)
	}
	assertChangeSet(t, plan.Changes(), map[string]transaction.ChangeKind{"account-lock": transaction.ChangeUpdate})
	prior, ok := plan.Changes()[0].PriorDigest()
	if !ok || prior != before.Digest {
		t.Fatalf("lock prior digest = %s, %v", prior.String(), ok)
	}

	for name, aliasPath := range map[string]struct{ path, reason string }{
		"manifest": {base.ManifestPath, "manifest_path_alias"},
		"artifact": {"api/account.proto", "artifact_path_alias"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
				ID: "alias-lock", Path: aliasPath.path, Owner: "nexa.dev/generator/crud-proto/v1", After: after,
				AfterDigest: provenance.SHA256(after), Sources: []provenance.SourceRef{source.Ref},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := base
			request.ControlSources = []transaction.ControlSourceMutation{candidate}
			if name == "artifact" {
				request.Expected = []transaction.ArtifactInput{{ID: "account", Path: aliasPath.path, Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain}}
			}
			contents := [][]byte(nil)
			if len(request.Expected) != 0 {
				contents = append(contents, []byte("proto"))
			}
			_, err = buildTransactionPlan(t, repositoryPath, request, contents...)
			var owner *transaction.Error
			if !errors.As(err, &owner) || owner.Code() != "transaction_control_source_invalid" || owner.Reason() != aliasPath.reason {
				t.Fatalf("Build alias error = %#v", err)
			}
		})
	}
}

func mustCanonicalManifest(t *testing.T, manifest artifact.Manifest) []byte {
	t.Helper()
	value, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
