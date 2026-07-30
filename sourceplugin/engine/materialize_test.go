package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestMaterializeLifecycleAdoptsPreservesAndRepeats(t *testing.T) {
	provider, ref := publishProvider(t, "sample", "v0.1.0", map[string]string{"value.txt": "upstream\n"})
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	engine := publishEngine(t, resolver)
	for _, test := range []struct {
		name      string
		prepare   func(string)
		wantState ManagedState
	}{
		{"absent", func(string) {}, ManagedStateClean},
		{"identical adopted", func(root string) {
			writePublishTestFile(t, filepath.Join(root, "services/sample/value.txt"), []byte("upstream\n"), 0o644)
		}, ManagedStateClean},
		{"unrelated local preserved", func(root string) {
			writePublishTestFile(t, filepath.Join(root, "services/sample/local.txt"), []byte("consumer\n"), 0o644)
		}, ManagedStateModified},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.prepare(root)
			selection := lifecycleSelection(t, ref, "services/sample")
			result, err := engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: selection}})
			if err != nil || result.Operation() != PlanMaterialize || result.Status().State() != test.wantState {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			assertPublishedFile(t, filepath.Join(root, "services/sample/value.txt"), "upstream\n", 0o644)
			targetInfo, err := os.Lstat(filepath.Join(root, "services/sample"))
			if err != nil || targetInfo.Mode().Perm() != 0o755 {
				t.Fatalf("materialized target mode = %v, %v", targetInfo, err)
			}
			if test.wantState == ManagedStateModified {
				assertPublishedFile(t, filepath.Join(root, "services/sample/local.txt"), "consumer\n", 0o644)
			}
			key, _ := lock.NewKey("sample", "services/sample")
			assertInstalledLock(t, root, resolver, deriveOwnership(t, resolver, ref, key.Target()))
			writePublishTestFile(t, filepath.Join(root, "services/sample/value.txt"), []byte("edited\n"), 0o644)
			second, err := engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: selection}})
			if err != nil || second.Operation() != PlanNoop || second.Status().State() != ManagedStateModified {
				t.Fatalf("repeat=%#v err=%v", second, err)
			}
			assertPublishedFile(t, filepath.Join(root, "services/sample/value.txt"), "edited\n", 0o644)
		})
	}
}

func TestMaterializeRejectsConflictPlanDigestAndManagedUpgradeWithoutWrites(t *testing.T) {
	oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"value.txt": "old\n"})
	newProvider, newRef := publishProvider(t, "sample", "v0.2.0", map[string]string{"value.txt": "new\n"})
	resolver, err := release.NewExactResolver(nil, oldProvider, newProvider)
	if err != nil {
		t.Fatal(err)
	}
	engine := publishEngine(t, resolver)
	t.Run("collision", func(t *testing.T) {
		root := t.TempDir()
		writePublishTestFile(t, filepath.Join(root, "services/sample/value.txt"), []byte("local\n"), 0o644)
		before := testPathState(t, root)
		_, err := engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, oldRef, "services/sample")}})
		assertLifecycleError(t, err, ErrConflict, "plan_conflict")
		if after := testPathState(t, root); after != before {
			t.Fatal("collision changed repository")
		}
	})
	t.Run("expected digest", func(t *testing.T) {
		root := t.TempDir()
		before := testPathState(t, root)
		_, err := engine.Materialize(context.Background(), MaterializeRequest{
			PlanRequest:  PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, oldRef, "services/sample")},
			WriteOptions: WriteOptions{ExpectedPlanDigest: provenance.SHA256([]byte("wrong"))},
		})
		assertLifecycleError(t, err, ErrConflict, "plan_digest_mismatch")
		if after := testPathState(t, root); after != before {
			t.Fatal("digest mismatch changed repository")
		}
	})
	t.Run("upgrade required", func(t *testing.T) {
		root := t.TempDir()
		key, _ := lock.NewKey("sample", "services/sample")
		installPublishBaseline(t, root, resolver, oldRef, key)
		before := publishBusinessState(t, root, key)
		_, err := engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}})
		assertLifecycleError(t, err, ErrInput, "upgrade_required")
		if after := publishBusinessState(t, root, key); after != before {
			t.Fatal("wrong materialize changed managed source")
		}
	})
}

func TestMaterializeValidationAndCacheFailuresLeaveRepositoryUntouched(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		provider, ref := lifecycleValidationProvider(t)
		resolver, err := release.NewExactResolver(nil, provider)
		if err != nil {
			t.Fatal(err)
		}
		recorder := &recordingValidationExecutor{result: ExecutionResult{ExitCode: 7}}
		engine, err := New(Options{
			Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{},
			Executor: recorder, GoToolchain: validationToolchain(t),
		})
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".nexa"), 0o755); err != nil {
			t.Fatal(err)
		}
		key, _ := lock.NewKey("sample", "services/sample")
		before := publishBusinessState(t, root, key)
		_, err = engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "services/sample")}})
		assertLifecycleError(t, err, ErrExternal, "validation_failed")
		if after := publishBusinessState(t, root, key); after != before {
			t.Fatal("validation failure changed target or ownership")
		}
		if info, err := os.Lstat(filepath.Join(root, ".nexa")); err != nil || !info.IsDir() {
			t.Fatalf("pre-existing .nexa was removed: %v, %v", info, err)
		}
		assertPublishStagingClean(t, root)
	})
	t.Run("cache", func(t *testing.T) {
		provider, ref := publishProvider(t, "sample", "v0.1.0", map[string]string{"value.txt": "value\n"})
		limits := release.DefaultCacheLimits()
		cacheParent, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		cacheRoot := filepath.Join(cacheParent, "cache")
		cache, err := release.OpenDirectoryCache(cacheRoot, limits)
		if err != nil {
			t.Fatal(err)
		}
		resolver, err := release.NewExactResolver(cache, provider)
		if err != nil {
			t.Fatal(err)
		}
		engine, err := New(Options{Resolver: resolver, CacheLimits: limits, TreeLimits: limits.Tree, LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(cacheRoot); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cacheRoot, []byte("not a cache directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		key, _ := lock.NewKey("sample", "services/sample")
		before := publishBusinessState(t, root, key)
		if _, err := engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "services/sample")}}); err == nil {
			t.Fatal("cache failure was accepted")
		}
		if after := publishBusinessState(t, root, key); after != before {
			t.Fatal("cache failure changed target or ownership")
		}
		assertPublishStagingClean(t, root)
	})
}

func TestUpgradeRejectsValidationTamperedStagedOwnershipBeforePublishing(t *testing.T) {
	oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"go.mod": "module example.test/old\n\ngo 1.25\n"})
	newProvider, newRef := lifecycleValidationProviderVersion(t, "v0.2.0")
	resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
	executor := lifecycleExecutorFunc(func(execution Execution) (ExecutionResult, error) {
		ownership := filepath.Clean(filepath.Join(execution.Directory, "..", "..", "..", "..", "ownership.json"))
		if err := os.WriteFile(ownership, []byte("tampered\n"), 0o600); err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{}, nil
	})
	engine, err := New(Options{
		Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{},
		Executor: executor, GoToolchain: validationToolchain(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	installPublishBaseline(t, root, resolver, oldRef, key)
	before := publishBusinessState(t, root, key)
	_, err = engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}})
	assertLifecycleError(t, err, ErrExternal, "stage_write_failed")
	if after := publishBusinessState(t, root, key); after != before {
		t.Fatal("tampered staged ownership changed target or installed ownership")
	}
}

func TestMaterializePrePublishDriftPublishesNothing(t *testing.T) {
	provider, ref := lifecycleValidationProvider(t)
	resolver, _ := release.NewExactResolver(nil, provider)
	root := t.TempDir()
	executor := lifecycleExecutorFunc(func(Execution) (ExecutionResult, error) {
		if err := os.MkdirAll(filepath.Join(root, "services/sample"), 0o755); err != nil {
			return ExecutionResult{}, err
		}
		if err := os.WriteFile(filepath.Join(root, "services/sample/late.txt"), []byte("late edit\n"), 0o600); err != nil {
			return ExecutionResult{}, err
		}
		return ExecutionResult{}, nil
	})
	engine, err := New(Options{
		Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{},
		Executor: executor, GoToolchain: validationToolchain(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Materialize(context.Background(), MaterializeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "services/sample")}})
	assertLifecycleError(t, err, ErrConflict, "source_snapshot_changed")
	if _, err := os.Lstat(filepath.Join(root, "services/sample/go.mod")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generated source was published: %v", err)
	}
	assertPublishedFile(t, filepath.Join(root, "services/sample/late.txt"), "late edit\n", 0o600)
	key, _ := lock.NewKey("sample", "services/sample")
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(key.RepositoryPath()))); err == nil {
		t.Fatal("lock was created")
	}
}

func lifecycleValidationProvider(t *testing.T) (sourceplugin.Provider, release.Ref) {
	return lifecycleValidationProviderVersion(t, "v0.1.0")
}

func lifecycleValidationProviderVersion(t *testing.T, version string) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	content := []byte("module example.test/materialized\n\ngo 1.25\n")
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample", ModulePath: "example.test/sample", PackagePath: "example.test/sample/source", Version: version},
		Files:    []sourceplugin.FileSpec{{Path: "go.mod", Mode: sourceplugin.Mode0644, Size: int64(len(content)), Digest: provenance.SHA256(content)}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{"go.mod"}, Validations: []sourceplugin.ValidationRecipeSpec{{ID: "build", Kind: sourceplugin.ValidationGoBuild, WorkingDirectory: ".", Packages: []string{"."}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: "go.mod", Content: content}}, sourceplugin.DefaultTreeLimits())
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

type lifecycleExecutorFunc func(Execution) (ExecutionResult, error)

func (execute lifecycleExecutorFunc) Execute(_ context.Context, execution Execution) (ExecutionResult, error) {
	return execute(execution)
}

func lifecycleSelection(t *testing.T, ref release.Ref, target string) Selection {
	t.Helper()
	result, err := NewSelection(SelectionSpec{Release: ref, ProfileID: "default", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertLifecycleError(t *testing.T, err error, class ErrorClass, reason string) {
	t.Helper()
	var projected *Error
	if !errors.As(err, &projected) || projected.Class() != class || projected.Reason() != reason {
		t.Fatalf("error=%#v want class=%v reason=%s", err, class, reason)
	}
}
