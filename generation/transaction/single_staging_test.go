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

func TestBuildAndWriteUseOneCandidateStaging(t *testing.T) {
	repository := t.TempDir()
	canonicalRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	var staging string

	plan, err := transaction.Build(context.Background(), repository, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		staging = root
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer plan.Close()
	if matches, globErr := filepath.Glob(filepath.Join(canonicalRepository, ".nexa-generation-staging-*")); globErr != nil || len(matches) != 1 || matches[0] != staging {
		t.Fatalf("candidate staging sessions = %v, %v", matches, globErr)
	}
	if bytes.Contains(plan.CanonicalJSON(), []byte(staging)) {
		t.Fatalf("canonical plan exposed staging path: %s", plan.CanonicalJSON())
	}
	if _, err := os.Stat(filepath.Join(staging, "generated/account.go")); err != nil {
		t.Fatalf("candidate before Write() = %v", err)
	}
	if _, err := transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "generated/account.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published candidate still exists in staging: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(repository, "generated/account.go"))
	if err != nil || string(got) != string(content) {
		t.Fatalf("published artifact = %q, %v", got, err)
	}
}

func TestWriteRejectsRepositoryDifferentFromPlanWithoutPublishing(t *testing.T) {
	for _, mode := range []string{"redirected-alias", "different-repository"} {
		t.Run(mode, func(t *testing.T) {
			firstRepository := t.TempDir()
			secondRepository := t.TempDir()
			alias := filepath.Join(t.TempDir(), "repository-alias")
			if err := os.Symlink(firstRepository, alias); err != nil {
				t.Fatal(err)
			}
			content := []byte("package generated\n")
			source := testSource(t, "facts/account.proto", []byte("v1"))
			plan, err := transaction.Build(context.Background(), alias, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
				if err := emit("generated/account.go", content); err != nil {
					return transaction.PlanRequest{}, err
				}
				return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
					return []provenance.Source{source}, nil
				}), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()

			writeRepository := secondRepository
			if mode == "redirected-alias" {
				if err := os.Remove(alias); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(secondRepository, alias); err != nil {
					t.Fatal(err)
				}
				writeRepository = alias
			}
			_, err = transaction.Write(context.Background(), plan, writeRepository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
			var typed *transaction.Error
			if !errors.As(err, &typed) || typed.Reason() != "repository_invalid" || typed.Pointer() != "/repository" {
				t.Fatalf("Write() error = %#v", err)
			}
			for _, repository := range []string{firstRepository, secondRepository} {
				for _, target := range []string{"generated", ".nexa"} {
					if _, statErr := os.Stat(filepath.Join(repository, target)); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("%s wrote %s: %v", mode, filepath.Join(repository, target), statErr)
					}
				}
			}
		})
	}
}

func TestBuildAndWriteAcceptSymlinkRepositoryAlias(t *testing.T) {
	repository := t.TempDir()
	canonicalRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "repository-alias")
	if err := os.Symlink(repository, alias); err != nil {
		t.Fatal(err)
	}
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	var staging string

	plan, err := transaction.Build(context.Background(), alias, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		staging = root
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() through symlink alias error = %v", err)
	}
	if filepath.Dir(staging) != canonicalRepository {
		t.Fatalf("candidate staging parent = %q, want canonical repository %q", filepath.Dir(staging), canonicalRepository)
	}
	if _, err := transaction.Write(context.Background(), plan, alias, transaction.WriteOptions{PlanDigest: plan.PlanDigest()}); err != nil {
		t.Fatalf("Write() through symlink alias error = %v", err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(repository, "generated/account.go")); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("published artifact = %q, %v", got, err)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate staging remains after Close: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(repository, ".nexa-generation-staging-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("extra candidate state = %v, %v", matches, err)
	}
	if entries, err := os.ReadDir(filepath.Dir(alias)); err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(alias) {
		t.Fatalf("alias parent state = %#v, %v", entries, err)
	}
}

func TestPlanCloseRemovesStagingWithoutLeavingParentDirectories(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	plan, err := transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := plan.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repository, ".nexa")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan-only build left staging parents: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(repository, ".nexa-generation-staging-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("staging sessions after Close = %v, %v", matches, err)
	}
}

func TestFailedBuildRemovesStagingWithoutLeavingParentDirectories(t *testing.T) {
	repository := t.TempDir()
	cause := errors.New("prepare failed")
	_, err := transaction.Build(context.Background(), repository, func(_ string, _ func(string, []byte) error) (transaction.PlanRequest, error) {
		return transaction.PlanRequest{}, cause
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repository, ".nexa")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed build left staging parents: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(repository, ".nexa-generation-staging-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("staging sessions after failed Build = %v, %v", matches, err)
	}
}

func TestWritePreservesPlanAlreadyAppliedCauseWithoutRenderingStagingPath(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	var stagedArtifact string
	plan, err := transaction.Build(context.Background(), repository, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		stagedArtifact = filepath.Join(root, "generated/account.go")
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			if err := os.Remove(stagedArtifact); err != nil {
				return nil, err
			}
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer plan.Close()

	_, err = transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "current_changed_after_plan" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Write() error = %#v", err)
	}
	if strings.Contains(err.Error(), filepath.Dir(filepath.Dir(stagedArtifact))) {
		t.Fatalf("safe error rendered staging path: %q", err)
	}
}

func TestWritePreservesCancellationAfterPreflight(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	ctx, cancel := context.WithCancel(context.Background())
	plan, err := transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			cancel()
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer plan.Close()

	_, err = transaction.Write(ctx, plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "cancelled" || !errors.Is(err, context.Canceled) {
		t.Fatalf("Write() error = %#v", err)
	}
	if strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("safe error rendered cancellation cause: %q", err)
	}
}

func TestCheckPreservesClosedCandidateCauseWithoutRenderingStagingPath(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	var staging string
	plan, err := transaction.Build(context.Background(), repository, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		staging = root
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	_, err = transaction.Check(plan, root)
	var typed *transaction.Error
	if !errors.As(err, &typed) || !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Check() error = %#v", err)
	}
	if strings.Contains(err.Error(), staging) {
		t.Fatalf("safe error rendered staging path: %q", err)
	}
}

func TestBuildRejectsTargetsInsideItsCandidateSession(t *testing.T) {
	for _, targetKind := range []string{"artifact", "control", "manifest"} {
		t.Run(targetKind, func(t *testing.T) {
			repository := t.TempDir()
			unrelated := filepath.Join(repository, "unrelated")
			if err := os.Mkdir(unrelated, 0o700); err != nil {
				t.Fatal(err)
			}
			source := testSource(t, "facts/account.proto", []byte("v1"))
			var aliasRoot string
			_, err := transaction.Build(context.Background(), repository, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
				aliasRoot = filepath.Base(root)
				artifactPath := "generated/account.go"
				manifestPath := ".nexa/generation/test.manifest.json"
				if targetKind == "artifact" {
					artifactPath = aliasRoot + "/published/account.go"
				}
				if targetKind == "manifest" {
					manifestPath = aliasRoot + "/published/manifest.json"
				}
				content := []byte("package generated\n")
				if err := emit(artifactPath, content); err != nil {
					return transaction.PlanRequest{}, err
				}
				request := singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
					return []provenance.Source{source}, nil
				})
				request.Expected[0].Path = artifactPath
				request.ManifestPath = manifestPath
				if targetKind == "control" {
					lock := []byte("lock\n")
					mutation, mutationErr := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
						ID: "lock", Path: aliasRoot + "/published/lock.json", Owner: "nexa.dev/generator/test/v1",
						After: lock, AfterDigest: provenance.SHA256(lock), Sources: []provenance.SourceRef{source.Ref},
					})
					if mutationErr != nil {
						return transaction.PlanRequest{}, mutationErr
					}
					request.ControlSources = []transaction.ControlSourceMutation{mutation}
				}
				return request, nil
			})
			var typed *transaction.Error
			if !errors.As(err, &typed) {
				t.Fatalf("Build() error = %#v", err)
			}
			if _, statErr := os.Stat(filepath.Join(repository, aliasRoot)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("candidate alias survived failed Build: %v", statErr)
			}
			if info, statErr := os.Stat(unrelated); statErr != nil || !info.IsDir() {
				t.Fatalf("failed Build damaged unrelated directory: %v", statErr)
			}
		})
	}
}

func TestWriteRejectsSourceDriftBeforePublishing(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	original := testSource(t, "facts/account.proto", []byte("v1"))
	current := original
	plan, err := transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(original, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return []provenance.Source{current}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer plan.Close()
	current = testSource(t, "facts/account.proto", []byte("v2"))

	_, err = transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "current_changed_after_plan" || typed.Pointer() != "/sources" {
		t.Fatalf("Write() error = %#v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repository, "generated/account.go")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("artifact was published: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(repository, ".nexa/generation/test.manifest.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("manifest was published: %v", statErr)
	}
}

func TestWritePreservesSourceReloadCause(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	cause := &sourceReloadError{path: "/private/source.proto"}
	plan, err := transaction.Build(context.Background(), repository, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return nil, cause
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer plan.Close()

	_, err = transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	var found *sourceReloadError
	if !errors.Is(err, cause) || !errors.As(err, &found) || found != cause {
		t.Fatalf("Write() did not preserve cause: %#v", err)
	}
}

func TestWriteRejectsCandidateTamper(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	var staging string
	plan, err := transaction.Build(context.Background(), repository, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		staging = root
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer plan.Close()
	if err := os.WriteFile(filepath.Join(staging, "generated/account.go"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "stage_failed" {
		t.Fatalf("Write() error = %#v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repository, "generated/account.go")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("artifact was published: %v", statErr)
	}
}

func TestWriteRejectsManifestCandidateTamper(t *testing.T) {
	repository := t.TempDir()
	content := []byte("package generated\n")
	source := testSource(t, "facts/account.proto", []byte("v1"))
	var staging string
	plan, err := transaction.Build(context.Background(), repository, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		staging = root
		if err := emit("generated/account.go", content); err != nil {
			return transaction.PlanRequest{}, err
		}
		return singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
			return []provenance.Source{source}, nil
		}), nil
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	defer plan.Close()
	manifest := filepath.Join(staging, ".nexa/generation/test.manifest.json")
	if err := os.WriteFile(manifest, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = transaction.Write(context.Background(), plan, repository, transaction.WriteOptions{PlanDigest: plan.PlanDigest()})
	var typed *transaction.Error
	if !errors.As(err, &typed) || typed.Reason() != "stage_failed" {
		t.Fatalf("Write() error = %#v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repository, "generated/account.go")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("artifact was published: %v", statErr)
	}
}

func TestPlanCloseIsIdempotentAndSessionScoped(t *testing.T) {
	repository := t.TempDir()
	source := testSource(t, "facts/account.proto", []byte("v1"))
	build := func(id, artifactPath string) (transaction.Plan, string) {
		t.Helper()
		content := []byte("package " + id + "\n")
		var staging string
		plan, err := transaction.Build(context.Background(), repository, func(root string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
			staging = root
			if err := emit(artifactPath, content); err != nil {
				return transaction.PlanRequest{}, err
			}
			request := singleArtifactRequest(source, provenance.SHA256(content), func(context.Context) ([]provenance.Source, error) {
				return []provenance.Source{source}, nil
			})
			request.Expected[0].ID = id
			request.Expected[0].Path = artifactPath
			request.ManifestPath = ".nexa/generation/" + id + ".manifest.json"
			return request, nil
		})
		if err != nil {
			t.Fatalf("Build(%s) error = %v", id, err)
		}
		return plan, staging
	}
	first, firstRoot := build("first", "generated/first.go")
	second, secondRoot := build("second", "generated/second.go")
	defer second.Close()

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second first.Close() error = %v", err)
	}
	if _, err := os.Stat(firstRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first staging still exists: %v", err)
	}
	if _, err := os.Stat(secondRoot); err != nil {
		t.Fatalf("second staging was removed: %v", err)
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := transaction.Check(first, root); err == nil {
		t.Fatal("Check() after Close succeeded")
	}
	if _, err := transaction.Write(context.Background(), first, repository, transaction.WriteOptions{PlanDigest: first.PlanDigest()}); err == nil {
		t.Fatal("Write() after Close succeeded")
	}
	if _, err := os.Stat(firstRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed session was recreated: %v", err)
	}
}

func singleArtifactRequest(source provenance.Source, digest provenance.Digest, reload func(context.Context) ([]provenance.Source, error)) transaction.PlanRequest {
	return transaction.PlanRequest{
		Generator: artifact.GeneratorSpec{ID: "test", Version: "v1.0.0"},
		Sources:   []provenance.Source{source},
		Expected: []transaction.ArtifactInput{{
			ID: "account", Path: "generated/account.go", Owner: "nexa.dev/generator/test/v1",
			Digest: digest, Sources: []provenance.SourceRef{source.Ref}, StalePolicy: artifact.StaleRetain,
		}},
		ManifestPath:      ".nexa/generation/test.manifest.json",
		RevalidateSources: reload,
	}
}

func testSource(t *testing.T, name string, content []byte) provenance.Source {
	t.Helper()
	ref, err := provenance.RepositoryRef(name, "")
	if err != nil {
		t.Fatal(err)
	}
	return provenance.Source{Ref: ref, Digest: provenance.SHA256(content)}
}

type sourceReloadError struct{ path string }

func (e *sourceReloadError) Error() string { return "cannot reload " + e.path }
