package transaction_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

type acceptingOwnershipProbe struct{}

func (acceptingOwnershipProbe) Inspect(string, []byte, transaction.Ownership) (bool, error) {
	return true, nil
}

func TestWritePublishesStagedArtifactsAndManifestWithoutPlanReplay(t *testing.T) {
	repositoryPath := t.TempDir()
	plan := buildCreateTransactionPlan(t, repositoryPath)
	result, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Clean() || result.PlanDigest() != plan.PlanDigest() {
		t.Fatalf("Write result = clean:%v digest:%s", result.Clean(), result.PlanDigest().String())
	}
	assertFile(t, repositoryPath, "api/account.proto", []byte("proto-v1\n"), 0o644)
	assertFile(t, repositoryPath, "api/account.crud.lock.json", []byte("lock-v1\n"), 0o644)
	if _, err := os.Stat(filepath.Join(repositoryPath, ".nexa/generation/crud-proto.manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err == nil {
		t.Fatal("Write() replayed consumed candidates")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoNewTransactionState(t, repositoryPath)
}

func TestWriteRejectsTreeChangedAfterPlanWithoutPublishing(t *testing.T) {
	repositoryPath := t.TempDir()
	plan := buildCreateTransactionPlan(t, repositoryPath)
	if err := os.MkdirAll(filepath.Join(repositoryPath, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "api/account.proto"), []byte("manual"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	assertWriteReason(t, err, "current_changed_after_plan")
	assertFile(t, repositoryPath, "api/account.proto", []byte("manual"), 0o644)
	for _, name := range []string{"api/account.crud.lock.json", ".nexa/generation/crud-proto.manifest.json"} {
		if _, err := os.Stat(filepath.Join(repositoryPath, name)); !os.IsNotExist(err) {
			t.Fatalf("Write created %s after drift: %v", name, err)
		}
	}
	if closeErr := plan.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	assertNoNewTransactionState(t, repositoryPath)
}

func TestWriteIgnoresAbandonedStagingFromEarlierInvocation(t *testing.T) {
	repositoryPath := t.TempDir()
	abandoned := filepath.Join(repositoryPath, ".nexa/generation/.staging-abandoned")
	if err := os.MkdirAll(abandoned, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(abandoned, "sentinel"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := buildCreateTransactionPlan(t, repositoryPath)
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, repositoryPath, ".nexa/generation/.staging-abandoned/sentinel", []byte("old\n"), 0o600)
	assertFile(t, repositoryPath, "api/account.proto", []byte("proto-v1\n"), 0o644)
}

func TestWriteRejectsSymlinkParentWithoutWritingRedirectTarget(t *testing.T) {
	repositoryPath := t.TempDir()
	plan := buildCreateTransactionPlan(t, repositoryPath)
	redirect := filepath.Join(repositoryPath, "redirect")
	if err := os.Mkdir(redirect, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("redirect", filepath.Join(repositoryPath, "api")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Write(context.Background(), plan, repositoryPath, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err == nil {
		t.Fatal("Write() followed a symlink parent")
	}
	for _, name := range []string{"account.proto", "account.crud.lock.json"} {
		if _, err := os.Stat(filepath.Join(redirect, name)); !os.IsNotExist(err) {
			t.Fatalf("redirect target %s was written: %v", name, err)
		}
	}
}

func buildCreateTransactionPlan(t *testing.T, repositoryPath string) transaction.Plan {
	t.Helper()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	lockBytes := []byte("lock-v1\n")
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: "account-lock", Path: "api/account.crud.lock.json", Owner: "nexa.dev/generator/crud-proto/v1", After: lockBytes,
		AfterDigest: provenance.SHA256(lockBytes), Sources: []provenance.SourceRef{source.Ref},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Expected:       []transaction.ArtifactInput{{ID: "account", Path: "api/account.proto", Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified}},
		ControlSources: []transaction.ControlSourceMutation{mutation}, ManifestPath: ".nexa/generation/crud-proto.manifest.json",
	}, []byte("proto-v1\n"))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func buildUpdateTransactionPlan(t *testing.T, repositoryPath string, previous artifact.Manifest, content []byte) transaction.Plan {
	t.Helper()
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	plan, err := buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{ID: "account", Path: "api/account.proto", Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified, Probe: acceptingOwnershipProbe{}}},
		Previous: &previous, ManifestPath: ".nexa/generation/crud-proto.manifest.json",
	}, content)
	if err != nil {
		t.Fatal(err)
	}
	if conflicts := plan.Conflicts(); len(conflicts) != 0 {
		t.Fatalf("update plan conflict: %s at %s", conflicts[0].Reason(), conflicts[0].Path())
	}
	return plan
}

func assertWriteReason(t *testing.T, err error, reason string) {
	t.Helper()
	var owner *transaction.Error
	if !errors.As(err, &owner) || owner.Stage() != "write" || owner.Reason() != reason {
		t.Fatalf("Write() error = %#v", err)
	}
}

func assertNoNewTransactionState(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".nexa/generation/.staging-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("transaction staging remains: %v, %v", matches, err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".nexa/generation/transaction.lock")); !os.IsNotExist(err) {
		t.Fatalf("transaction lock exists: %v", err)
	}
}

func assertFile(t *testing.T, root, name string, want []byte, mode os.FileMode) {
	t.Helper()
	absolute := filepath.Join(root, name)
	got, err := os.ReadFile(absolute)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %o, want %o", name, info.Mode().Perm(), mode)
	}
}
