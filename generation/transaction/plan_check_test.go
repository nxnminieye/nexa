package transaction_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

type ownershipProbeFunc func(string, []byte, transaction.Ownership) (bool, error)

func (f ownershipProbeFunc) Inspect(path string, content []byte, expected transaction.Ownership) (bool, error) {
	return f(path, content, expected)
}

func TestBuildAndCheckRequireSemanticOwnershipForUpdatesAndStaleDeletes(t *testing.T) {
	repositoryPath := t.TempDir()
	accountPath := "generated/account.proto"
	stalePath := "generated/obsolete.proto"
	oldAccount := []byte("old-account")
	oldStale := []byte("old-obsolete")
	for path, content := range map[string][]byte{accountPath: oldAccount, stalePath: oldStale} {
		absolute := filepath.Join(repositoryPath, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	oldSource := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("old-schema"))}
	previous, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v0.9.0"}, Sources: []provenance.Source{oldSource},
		Artifacts: []artifact.ArtifactSpec{
			{ID: "account", Path: accountPath, Owner: "crud-proto", Digest: provenance.SHA256(oldAccount), Sources: []provenance.SourceRef{oldSource.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
			{ID: "obsolete", Path: stalePath, Owner: "crud-proto", Digest: provenance.SHA256(oldStale), Sources: []provenance.SourceRef{oldSource.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	previousProbe := ownershipProbeFunc(func(path string, content []byte, expected transaction.Ownership) (bool, error) {
		ids := map[string]string{accountPath: "account", stalePath: "obsolete"}
		contents := map[string][]byte{accountPath: oldAccount, stalePath: oldStale}
		id, ok := ids[path]
		if !ok {
			return false, nil
		}
		return bytes.Equal(content, contents[path]) && expected.GeneratorID == "crud-proto" && expected.ArtifactID == id && expected.InputDigest == previous.InputDigest(), nil
	})
	newSource := provenance.Source{Ref: oldSource.Ref, Digest: provenance.SHA256([]byte("new-schema"))}
	newInputDigest, err := artifact.ComputeInputDigest(artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, []provenance.Source{newSource})
	if err != nil {
		t.Fatal(err)
	}
	currentProbe := ownershipProbeFunc(func(path string, content []byte, expected transaction.Ownership) (bool, error) {
		return path == accountPath && bytes.Equal(content, []byte("new-account")) && expected.GeneratorID == "crud-proto" && expected.ArtifactID == "account" && expected.InputDigest == newInputDigest, nil
	})
	request := transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{newSource}, Previous: &previous,
		Expected: []transaction.ArtifactInput{{
			ID: "account", Path: accountPath, Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{newSource.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified, Probe: currentProbe,
		}},
		StaleOwnershipProbes: []transaction.OwnershipProbe{previousProbe},
		ManifestPath:         ".nexa/generation/crud-proto.manifest.json",
	}
	plan, err := buildTransactionPlan(t, repositoryPath, request, []byte("new-account"))
	if err != nil {
		t.Fatal(err)
	}
	assertChangeSet(t, plan.Changes(), map[string]transaction.ChangeKind{"account": transaction.ChangeUpdate, "obsolete": transaction.ChangeDelete})
	if len(plan.Conflicts()) != 0 {
		t.Fatalf("Build conflicts = %#v", plan.Conflicts())
	}
	result, err := transaction.Check(plan, root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Clean() || result.PlanDigest() != plan.PlanDigest() {
		t.Fatalf("Check result = clean:%v digest:%s", result.Clean(), result.PlanDigest().String())
	}
	assertChangeSet(t, result.Changes(), map[string]transaction.ChangeKind{"account": transaction.ChangeUpdate, "obsolete": transaction.ChangeDelete})
	if len(result.CanonicalJSON()) == 0 || len(result.Conflicts()) != 0 {
		t.Fatalf("Check result = conflicts:%#v canonical:%q", result.Conflicts(), result.CanonicalJSON())
	}
	for path, want := range map[string][]byte{accountPath: oldAccount, stalePath: oldStale} {
		got, err := os.ReadFile(filepath.Join(repositoryPath, path))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("Check changed %s: %q, %v", path, got, err)
		}
	}

	if err := os.WriteFile(filepath.Join(repositoryPath, accountPath), []byte("manual-account"), 0o644); err != nil {
		t.Fatal(err)
	}
	drift, err := transaction.Check(plan, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift.Conflicts()) != 1 || drift.Conflicts()[0].ID() != "account" || drift.Conflicts()[0].Reason() != "existing_unowned" {
		t.Fatalf("drift conflicts = %#v", drift.Conflicts())
	}
}

func TestBuildUsesDefensivelyCopiedStaleOnlyOwnershipProbes(t *testing.T) {
	repositoryPath := t.TempDir()
	stalePath := "generated/account.proto"
	staleContent := []byte("syntax = \"proto3\";\n")
	if err := os.MkdirAll(filepath.Join(repositoryPath, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, stalePath), staleContent, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	previous, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"},
		Sources:   []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{
			ID: "crud-proto.accounts", Path: stalePath, Owner: "crud-proto", Digest: provenance.SHA256(staleContent),
			Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	probe := ownershipProbeFunc(func(path string, _ []byte, expected transaction.Ownership) (bool, error) {
		return path == stalePath && expected.GeneratorID == "crud-proto" && expected.ArtifactID == "crud-proto.accounts" && expected.InputDigest == previous.InputDigest(), nil
	})
	probes := []transaction.OwnershipProbe{probe}
	plan, err := buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator:            artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"},
		Sources:              []provenance.Source{source},
		Previous:             &previous,
		StaleOwnershipProbes: probes,
		ManifestPath:         ".nexa/generation/crud-proto.accounts.manifest.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes()) != 1 || plan.Changes()[0].Kind() != transaction.ChangeDelete || len(plan.Conflicts()) != 0 {
		t.Fatalf("plan changes=%#v conflicts=%#v", plan.Changes(), plan.Conflicts())
	}
	probes[0] = ownershipProbeFunc(func(string, []byte, transaction.Ownership) (bool, error) { return false, nil })
	result, err := transaction.Check(plan, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changes()) != 1 || result.Changes()[0].Kind() != transaction.ChangeDelete || len(result.Conflicts()) != 0 {
		t.Fatalf("check changes=%#v conflicts=%#v", result.Changes(), result.Conflicts())
	}
}

func TestBuildNeverDeletesAPathUsedByTheNextManifest(t *testing.T) {
	repositoryPath := t.TempDir()
	path := "generated/account.proto"
	content := []byte("account")
	if err := os.MkdirAll(filepath.Join(repositoryPath, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, path), content, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := provenance.Source{Ref: mustRepositoryRef(t, "ent/schema/account.go", "schema:Account"), Digest: provenance.SHA256([]byte("schema"))}
	previous, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{ID: "old-account", Path: path, Owner: "crud-proto", Digest: provenance.SHA256(content), Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified}},
	})
	if err != nil {
		t.Fatal(err)
	}
	probe := ownershipProbeFunc(func(_ string, _ []byte, expected transaction.Ownership) (bool, error) {
		return expected.GeneratorID == "crud-proto" && expected.ArtifactID == "old-account" && expected.InputDigest == previous.InputDigest(), nil
	})
	plan, err := buildTransactionPlan(t, repositoryPath, transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "crud-proto", Version: "v1.0.0"}, Sources: []provenance.Source{source}, Previous: &previous,
		Expected:     []transaction.ArtifactInput{{ID: "new-account", Path: path, Owner: "nexa.dev/generator/crud-proto/v1", Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified, Probe: probe}},
		ManifestPath: ".nexa/generation/crud-proto.manifest.json",
	}, content)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Changes() {
		if change.Kind() == transaction.ChangeDelete && change.Path() == path {
			t.Fatalf("Build planned deletion of next-manifest path: %#v", change)
		}
	}
}

func assertChangeSet(t *testing.T, changes []transaction.Change, want map[string]transaction.ChangeKind) {
	t.Helper()
	if len(changes) != len(want) {
		t.Fatalf("changes = %#v, want %#v", changes, want)
	}
	for _, change := range changes {
		if want[change.ID()] != change.Kind() {
			t.Fatalf("change %q = %q, want %q", change.ID(), change.Kind(), want[change.ID()])
		}
	}
}
