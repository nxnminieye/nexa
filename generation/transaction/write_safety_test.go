package transaction

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/provenance"
)

type stagedProbe func(string, []byte, Ownership) (bool, error)

func (p stagedProbe) Inspect(path string, content []byte, ownership Ownership) (bool, error) {
	return p(path, content, ownership)
}

func buildInternalPlan(t *testing.T, repository string, request PlanRequest, contents ...[]byte) (Plan, error) {
	t.Helper()
	for index := range request.Expected {
		request.Expected[index].Digest = provenance.SHA256(contents[index])
	}
	sources := append([]provenance.Source(nil), request.Sources...)
	request.RevalidateSources = func(context.Context) ([]provenance.Source, error) { return sources, nil }
	return Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (PlanRequest, error) {
		for index, input := range request.Expected {
			if err := emit(input.Path, contents[index]); err != nil {
				return PlanRequest{}, err
			}
		}
		return request, nil
	})
}

func TestStagedBundleValidatesCandidateAndManifestBeforePublish(t *testing.T) {
	repository := t.TempDir()
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ref, err := provenance.ParseSourceRef("repo:schema/account.go#schema%3AAccount")
	if err != nil {
		t.Fatal(err)
	}
	source := provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte("schema"))}
	plan, err := buildInternalPlan(t, repository, PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "test-generator", Version: "v1.0.0"},
		Sources:   []provenance.Source{source},
		Expected: []ArtifactInput{{
			ID: "account", Path: "generated/account.go", Owner: "nexa.dev/generator/test/v1",
			Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleRetain,
		}},
		ManifestPath: ".nexa/generation/test.manifest.json",
	}, []byte("generated\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStagedBundle(context.Background(), plan.state); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lstat("generated/account.go"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate published during staging: %v", err)
	}
}

func TestWriteRejectsStagedOwnershipBeforePublishing(t *testing.T) {
	repository := t.TempDir()
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	plan := buildStagingTestPlan(t, repository, stagedProbe(func(string, []byte, Ownership) (bool, error) { return false, nil }))
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Write(context.Background(), plan, repository, WriteOptions{PlanDigest: plan.PlanDigest()})
	var owner *Error
	if !errors.As(err, &owner) || owner.Reason() != "stage_failed" {
		t.Fatalf("Write() error = %#v", err)
	}
	for _, name := range []string{"generated/account.go", ".nexa/generation/test.manifest.json"} {
		if _, err := os.Lstat(filepath.Join(repository, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s published after semantic rejection: %v", name, err)
		}
	}
}

func TestWriteRechecksPlannedStateAfterStagingBeforePublishing(t *testing.T) {
	repository := t.TempDir()
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	plan := buildStagingTestPlan(t, repository, stagedProbe(func(name string, _ []byte, _ Ownership) (bool, error) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repository, name)), 0o755); err != nil {
			return false, err
		}
		return true, os.WriteFile(filepath.Join(repository, name), []byte("external drift\n"), 0o644)
	}))
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Write(context.Background(), plan, repository, WriteOptions{PlanDigest: plan.PlanDigest()})
	var owner *Error
	if !errors.As(err, &owner) || owner.Reason() != "current_changed_after_plan" {
		t.Fatalf("Write() error = %#v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(repository, "generated/account.go"))
	if readErr != nil || string(content) != "external drift\n" {
		t.Fatalf("drifted artifact = %q, %v", content, readErr)
	}
	if _, err := os.Lstat(filepath.Join(repository, ".nexa/generation/test.manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest published after drift: %v", err)
	}
}

func TestWritePreservesCurrentStateOwnershipCauseWithoutRenderingIt(t *testing.T) {
	repository := t.TempDir()
	oldContent := []byte("old generated\n")
	newContent := []byte("new generated\n")
	if err := os.MkdirAll(filepath.Join(repository, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "generated/account.go"), oldContent, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := provenance.ParseSourceRef("repo:schema/account.go#schema%3AAccount")
	if err != nil {
		t.Fatal(err)
	}
	source := provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte("schema"))}
	previous, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "test-generator", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{ID: "account", Path: "generated/account.go", Owner: "test-generator", Digest: provenance.SHA256(oldContent), Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleDeleteIfUnmodified}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("private /absolute/repository ownership failure")
	failCurrent := false
	probe := stagedProbe(func(_ string, content []byte, _ Ownership) (bool, error) {
		if bytes.Equal(content, oldContent) && failCurrent {
			return false, cause
		}
		return true, nil
	})
	plan, err := buildInternalPlan(t, repository, PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "test-generator", Version: "v1.0.0"}, Sources: []provenance.Source{source}, Previous: &previous,
		Expected:     []ArtifactInput{{ID: "account", Path: "generated/account.go", Owner: "nexa.dev/generator/test/v1", Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleDeleteIfUnmodified, Probe: probe}},
		ManifestPath: ".nexa/generation/test.manifest.json",
	}, newContent)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	failCurrent = true
	_, err = Write(context.Background(), plan, repository, WriteOptions{PlanDigest: plan.PlanDigest()})
	var typed *Error
	if !errors.As(err, &typed) || typed.Pointer() != "/artifacts" || !errors.Is(err, cause) {
		t.Fatalf("Write() error = %#v", err)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("safe error rendered ownership cause: %q", err)
	}
}

func TestPartialPublishIsPreservedForFreshReplan(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, ".nexa/generation/.staging-test"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{"first.next": []byte("first generated\n"), "second.next": []byte("second generated\n")} {
		if err := os.WriteFile(filepath.Join(repository, ".nexa/generation/.staging-test", name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first := stagedChange{target: "generated/first.go", stage: ".nexa/generation/.staging-test/first.next"}
	second := stagedChange{target: "generated/second.go", stage: ".nexa/generation/.staging-test/second.next"}
	if err := applyStagedChange(root, &first); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "generated/second.go"), []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyStagedChange(root, &second); !errors.Is(err, errStageCurrentChanged) {
		t.Fatalf("second publish error = %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(repository, "generated/first.go")); err != nil || string(content) != "first generated\n" {
		t.Fatalf("first published artifact = %q, %v", content, err)
	}
	ref, err := provenance.ParseSourceRef("repo:schema/account.go#schema%3AAccount")
	if err != nil {
		t.Fatal(err)
	}
	source := provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte("schema"))}
	plan, err := buildInternalPlan(t, repository, PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "test-generator", Version: "v1.0.0"}, Sources: []provenance.Source{source},
		Expected: []ArtifactInput{
			{ID: "first", Path: "generated/first.go", Owner: "nexa.dev/generator/test/v1", Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleRetain},
			{ID: "second", Path: "generated/second.go", Owner: "nexa.dev/generator/test/v1", Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleRetain},
		},
		ManifestPath: ".nexa/generation/test.manifest.json",
	}, []byte("first generated\n"), []byte("second generated\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts()) == 0 {
		t.Fatal("fresh plan ignored partially published current state")
	}
}

func buildStagingTestPlan(t *testing.T, repository string, probe OwnershipProbe) Plan {
	t.Helper()
	ref, err := provenance.ParseSourceRef("repo:schema/account.go#schema%3AAccount")
	if err != nil {
		t.Fatal(err)
	}
	source := provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte("schema"))}
	plan, err := buildInternalPlan(t, repository, PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "test-generator", Version: "v1.0.0"},
		Sources:   []provenance.Source{source},
		Expected: []ArtifactInput{{
			ID: "account", Path: "generated/account.go", Owner: "nexa.dev/generator/test/v1",
			Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleRetain, Probe: probe,
		}},
		ManifestPath: ".nexa/generation/test.manifest.json",
	}, []byte("generated\n"))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestApplyStagedChangeRechecksCreateUpdateAndDeletePreconditions(t *testing.T) {
	tests := []struct {
		name  string
		entry stagedChange
		next  []byte
		seed  []byte
	}{
		{name: "create", entry: stagedChange{target: "api/value.go", stage: ".nexa/generation/.staging-test/next"}, next: []byte("generated"), seed: []byte("appeared")},
		{name: "update", entry: stagedChange{target: "api/value.go", stage: ".nexa/generation/.staging-test/next", hadPrior: true, expectsPrior: true, priorDigest: provenance.SHA256([]byte("planned"))}, next: []byte("generated"), seed: []byte("changed")},
		{name: "delete", entry: stagedChange{target: "api/value.go", delete: true, hadPrior: true, expectsPrior: true, priorDigest: provenance.SHA256([]byte("planned"))}, seed: []byte("changed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			if err := os.MkdirAll(repository+"/.nexa/generation/.staging-test", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(repository+"/api", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(repository+"/api/value.go", test.seed, 0o644); err != nil {
				t.Fatal(err)
			}
			if !test.entry.delete {
				if err := os.WriteFile(repository+"/"+test.entry.stage, test.next, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(repository)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			err = applyStagedChange(root, &test.entry)
			if !errors.Is(err, errStageCurrentChanged) {
				t.Fatalf("apply error = %v", err)
			}
			got, err := os.ReadFile(repository + "/api/value.go")
			if err != nil || string(got) != string(test.seed) {
				t.Fatalf("target = %q, %v", got, err)
			}
		})
	}
}
