package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestDetachPreservesCleanAndModifiedSourceAndIsNotManagedOnRepeat(t *testing.T) {
	provider, ref := publishProvider(t, "sample", "v0.1.0", map[string]string{"value.txt": "base\n"})
	resolver, _ := release.NewExactResolver(nil, provider)
	emptyResolver, _ := release.NewExactResolver(nil)
	engine := publishEngine(t, emptyResolver)
	for _, modified := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "modified"}[modified], func(t *testing.T) {
			root := t.TempDir()
			key, _ := lock.NewKey("sample", "services/sample")
			installPublishBaseline(t, root, resolver, ref, key)
			if modified {
				writePublishTestFile(t, filepath.Join(root, "services/sample/value.txt"), []byte("local\n"), 0o755)
			}
			before := testPathState(t, filepath.Join(root, "services/sample"))
			result, err := engine.Detach(context.Background(), DetachRequest{ManagedRequest: ManagedRequest{RepositoryRoot: root, Key: key}})
			if err != nil || result.Status().State() != ManagedStateNotManaged {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if after := testPathState(t, filepath.Join(root, "services/sample")); after != before {
				t.Fatal("detach changed source")
			}
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(key.RepositoryPath()))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("lock remains: %v", err)
			}
			_, err = engine.Detach(context.Background(), DetachRequest{ManagedRequest: ManagedRequest{RepositoryRoot: root, Key: key}})
			assertLifecycleError(t, err, ErrNotManaged, "lock_missing")
		})
	}
}

func TestDetachDoesNotResolveValidateOrCleanCachedRelease(t *testing.T) {
	provider, ref := lifecycleValidationProvider(t)
	sourceResolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := sourceResolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	limits := release.DefaultCacheLimits()
	cacheParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cache, err := release.OpenDirectoryCache(filepath.Join(cacheParent, "cache"), limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	emptyResolver, err := release.NewExactResolver(cache)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{
		Resolver: emptyResolver, CacheLimits: limits, TreeLimits: limits.Tree,
		LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	key, err := lock.NewKey("sample", "services/sample")
	if err != nil {
		t.Fatal(err)
	}
	installPublishBaseline(t, root, sourceResolver, ref, key)
	sourceBefore := testPathState(t, filepath.Join(root, filepath.FromSlash(key.Target())))
	if _, err := engine.Detach(context.Background(), DetachRequest{ManagedRequest: ManagedRequest{RepositoryRoot: root, Key: key}}); err != nil {
		t.Fatal(err)
	}
	if sourceAfter := testPathState(t, filepath.Join(root, filepath.FromSlash(key.Target()))); sourceAfter != sourceBefore {
		t.Fatal("detach changed source")
	}
	if _, err := cache.Load(context.Background(), ref); err != nil {
		t.Fatalf("detach changed cached release: %v", err)
	}
}
