package release_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestDirectoryCacheNativeRoundTripLayoutModesAndZeroWriteReads(t *testing.T) {
	requireNativeCachePlatform(t)
	parent := realTestPath(t, t.TempDir())
	root := filepath.Join(parent, "cache")
	before := filesystemSnapshot(t, parent)
	cache, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !cache.Limits().Equal(release.DefaultCacheLimits()) {
		t.Fatalf("limits = %#v", cache.Limits())
	}
	if after := filesystemSnapshot(t, parent); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("Open wrote filesystem: before=%v after=%v", before, after)
	}
	provider, ref := releaseProvider(t, "sample.cache", "cache bytes")
	resolved := resolveStatic(t, provider, ref)
	_, err = cache.Load(context.Background(), ref)
	assertReleaseError(t, err, release.ErrReleaseUnavailable, "source_release_unavailable", "cache_miss", "/entry", release.StageCacheLoad)
	if after := filesystemSnapshot(t, parent); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("cache miss wrote filesystem: before=%v after=%v", before, after)
	}
	if err := cache.Store(context.Background(), resolved); err != nil {
		t.Fatalf("store error: %#v", err)
	}
	loaded, err := cache.Load(context.Background(), ref)
	if err != nil || !loaded.Ref().Equal(ref) || loaded.Manifest().Digest() != ref.ManifestDigest() || loaded.Tree().Digest() != ref.TreeDigest() {
		t.Fatalf("loaded = %#v, err=%v", loaded, err)
	}
	entry := filepath.Join(root, cacheEntryName(ref))
	wantManifest, _ := provider.Manifest().CanonicalJSON()
	gotManifest, err := os.ReadFile(filepath.Join(entry, "manifest.json"))
	if err != nil || !bytes.Equal(gotManifest, wantManifest) {
		t.Fatalf("manifest bytes = %q, err=%v", gotManifest, err)
	}
	assertMode(t, root, 0o700)
	assertMode(t, entry, 0o700)
	assertMode(t, filepath.Join(entry, "tree"), 0o755)
	assertMode(t, filepath.Join(entry, "manifest.json"), 0o600)
	assertMode(t, filepath.Join(entry, "complete.json"), 0o600)
	assertMode(t, filepath.Join(entry, "tree", "main.go"), 0o644)
	stable := filesystemSnapshot(t, root)
	if err := cache.Store(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	if after := filesystemSnapshot(t, root); fmt.Sprint(after) != fmt.Sprint(stable) {
		t.Fatalf("identical Store changed cache: before=%v after=%v", stable, after)
	}
}

func TestDirectoryCacheSetsCanonicalModesWithRestrictiveUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)
	root := filepath.Join(realTestPath(t, t.TempDir()), "cache")
	cache, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, ref := releaseProviderAtPath(t, "sample.umask", "nested/main.go", "value")
	if err := cache.Store(context.Background(), resolveStatic(t, provider, ref)); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, cacheEntryName(ref))
	for _, expected := range []struct {
		path string
		mode os.FileMode
	}{
		{path: root, mode: 0o700},
		{path: entry, mode: 0o700},
		{path: filepath.Join(entry, "tree"), mode: 0o755},
		{path: filepath.Join(entry, "tree", "nested"), mode: 0o755},
		{path: filepath.Join(entry, "tree", "nested", "main.go"), mode: 0o644},
		{path: filepath.Join(entry, "manifest.json"), mode: 0o600},
		{path: filepath.Join(entry, "complete.json"), mode: 0o600},
	} {
		info, err := os.Lstat(expected.path)
		if err != nil || info.Mode().Perm() != expected.mode {
			t.Fatalf("mode %s = %v, %v; want %o", expected.path, info, err, expected.mode)
		}
	}
}

func TestDirectoryCacheLimitValidationPreservesStablePointers(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*release.CacheLimits)
		pointer string
	}{
		{
			name: "max file exceeds total",
			mutate: func(limits *release.CacheLimits) {
				limits.Tree.MaxFileBytes = limits.Tree.MaxTotalBytes + 1
			},
			pointer: "/cache/limits/tree/maxFileBytes",
		},
		{
			name: "max files integer overflow",
			mutate: func(limits *release.CacheLimits) {
				limits.Tree.MaxFiles = int(^uint(0) >> 1)
			},
			pointer: "/cache/limits/tree/maxFiles",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := release.DefaultCacheLimits()
			test.mutate(&limits)
			_, err := release.OpenDirectoryCache(filepath.Join(realTestPath(t, t.TempDir()), "cache"), limits)
			assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "cache_limit_invalid", test.pointer, release.StageCacheOpen)
		})
	}
}

func TestDirectoryCacheNativeNoFollowAndTamperMatrix(t *testing.T) {
	requireNativeCachePlatform(t)
	t.Run("root symlink", func(t *testing.T) {
		parent := realTestPath(t, t.TempDir())
		outside := filepath.Join(parent, "outside")
		if err := os.Mkdir(outside, 0o700); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(parent, "cache")
		if err := os.Symlink(outside, root); err != nil {
			t.Fatal(err)
		}
		_, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
		assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "cache_root_type_invalid", "/cache/root", release.StageCacheOpen)
		entries, _ := os.ReadDir(outside)
		if len(entries) != 0 {
			t.Fatalf("followed root symlink: %v", entries)
		}
	})
	t.Run("parent symlink", func(t *testing.T) {
		parent := realTestPath(t, t.TempDir())
		outside := filepath.Join(parent, "outside")
		if err := os.Mkdir(outside, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(parent, "linked")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		_, err := release.OpenDirectoryCache(filepath.Join(link, "cache"), release.DefaultCacheLimits())
		assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "cache_root_type_invalid", "/cache/root", release.StageCacheOpen)
	})

	provider, ref := releaseProvider(t, "sample.tamper", "cache bytes")
	mutations := []struct {
		name    string
		mutate  func(t *testing.T, entry, outside string)
		reason  string
		pointer string
	}{
		{"entry mode", func(t *testing.T, entry, _ string) {
			if err := os.Chmod(entry, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "cache_entry_mode_mismatch", "/entry"},
		{"marker mode", func(t *testing.T, entry, _ string) {
			if err := os.Chmod(filepath.Join(entry, "complete.json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "cache_entry_mode_mismatch", "/entry/complete"},
		{"tree mode", func(t *testing.T, entry, _ string) {
			if err := os.Chmod(filepath.Join(entry, "tree"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, "cache_entry_mode_mismatch", "/entry/tree"},
		{"extra root entry", func(t *testing.T, entry, _ string) { mustWrite(t, filepath.Join(entry, "extra"), []byte("x"), 0o600) }, "cache_entry_incomplete", "/entry"},
		{"bounded extra root entries", func(t *testing.T, entry, _ string) {
			for _, name := range []string{"extra-a", "extra-b", "extra-c"} {
				mustWrite(t, filepath.Join(entry, name), []byte("x"), 0o600)
			}
		}, "cache_entry_incomplete", "/entry"},
		{"marker unknown", func(t *testing.T, entry, _ string) {
			mustWrite(t, filepath.Join(entry, "complete.json"), []byte(`{"extra":true}`), 0o600)
		}, "cache_marker_invalid", "/entry/complete"},
		{"marker ref mismatch", func(t *testing.T, entry, _ string) {
			marker, err := os.ReadFile(filepath.Join(entry, "complete.json"))
			if err != nil {
				t.Fatal(err)
			}
			marker = []byte(strings.Replace(string(marker), `"providerId":"sample.tamper"`, `"providerId":"sample.other"`, 1))
			mustWrite(t, filepath.Join(entry, "complete.json"), marker, 0o600)
		}, "cache_marker_mismatch", "/ref/providerId"},
		{"manifest truncated", func(t *testing.T, entry, _ string) {
			mustWrite(t, filepath.Join(entry, "manifest.json"), []byte("{}\n"), 0o600)
		}, "cache_manifest_invalid", "/entry/manifest"},
		{"manifest digest mismatch", func(t *testing.T, entry, _ string) {
			manifest, err := os.ReadFile(filepath.Join(entry, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			manifest = []byte(strings.Replace(string(manifest), `"id":"default"`, `"id":"other"`, 1))
			mustWrite(t, filepath.Join(entry, "manifest.json"), manifest, 0o600)
		}, "cache_manifest_mismatch", "/entry/manifest"},
		{"content changed", func(t *testing.T, entry, _ string) {
			mustWrite(t, filepath.Join(entry, "tree", "main.go"), []byte("wrong bytes"), 0o644)
		}, "cache_file_digest_mismatch", "/entry/tree/files/0/content"},
		{"content extended", func(t *testing.T, entry, _ string) {
			mustWrite(t, filepath.Join(entry, "tree", "main.go"), []byte("cache bytes extended"), 0o644)
		}, "cache_file_size_mismatch", "/entry/tree/files/0/content"},
		{"file mode", func(t *testing.T, entry, _ string) {
			if err := os.Chmod(filepath.Join(entry, "tree", "main.go"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "cache_file_mode_mismatch", "/entry/tree/files/0/mode"},
		{"extra tree file", func(t *testing.T, entry, _ string) {
			mustWrite(t, filepath.Join(entry, "tree", "extra"), []byte("x"), 0o644)
		}, "cache_inventory_invalid", "/entry/tree"},
		{"tree symlink", func(t *testing.T, entry, outside string) {
			if err := os.RemoveAll(filepath.Join(entry, "tree")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(entry, "tree")); err != nil {
				t.Fatal(err)
			}
		}, "cache_entry_type_invalid", "/entry/tree"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			parent := realTestPath(t, t.TempDir())
			root := filepath.Join(parent, "cache")
			cache, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.Store(context.Background(), resolveStatic(t, provider, ref)); err != nil {
				t.Fatal(err)
			}
			entry := filepath.Join(root, cacheEntryName(ref))
			outside := filepath.Join(parent, "outside")
			if err := os.Mkdir(outside, 0o700); err != nil {
				t.Fatal(err)
			}
			mutation.mutate(t, entry, outside)
			beforeOutside := filesystemSnapshot(t, outside)
			_, err = cache.Load(context.Background(), ref)
			assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", mutation.reason, mutation.pointer, release.StageCacheLoad)
			if afterOutside := filesystemSnapshot(t, outside); fmt.Sprint(afterOutside) != fmt.Sprint(beforeOutside) {
				t.Fatalf("tamper read touched outside: before=%v after=%v", beforeOutside, afterOutside)
			}
		})
	}
}

func TestDirectoryCacheAcceptsOrdinaryExistingDirectoryMode(t *testing.T) {
	requireNativeCachePlatform(t)
	root := filepath.Join(realTestPath(t, t.TempDir()), "cache")
	cache, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, ref := releaseProvider(t, "sample.root-mode", "value")
	if err := cache.Store(context.Background(), resolveStatic(t, provider, ref)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Ref().Equal(ref) {
		t.Fatalf("loaded ref = %#v, want %#v", loaded.Ref(), ref)
	}
}

func TestExactResolverStaticHitNeverReadsTamperedCache(t *testing.T) {
	requireNativeCachePlatform(t)
	provider, ref := releaseProvider(t, "sample.static-cache", "static")
	root := filepath.Join(realTestPath(t, t.TempDir()), "cache")
	cache, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), resolveStatic(t, provider, ref)); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, cacheEntryName(ref), "complete.json"), []byte("tampered\n"), 0o600)
	resolver, err := release.NewExactResolver(cache, provider)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil || !resolved.Ref().Equal(ref) {
		t.Fatalf("static resolve = %#v, err=%v", resolved, err)
	}
	if got, ok := resolver.Cache(); !ok || got != cache {
		t.Fatalf("cache accessor = %#v, %v", got, ok)
	}
}

func TestDirectoryCacheInventoryBoundProjectsConflict(t *testing.T) {
	requireNativeCachePlatform(t)
	provider, ref := releaseProvider(t, "sample.inventory-bound", "value")
	limits := release.DefaultCacheLimits()
	limits.Tree.MaxFiles = 1
	root := filepath.Join(realTestPath(t, t.TempDir()), "cache")
	cache, err := release.OpenDirectoryCache(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), resolveStatic(t, provider, ref)); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(root, cacheEntryName(ref), "tree")
	mustWrite(t, filepath.Join(tree, "extra-a"), []byte("x"), 0o644)
	_, err = cache.Load(context.Background(), ref)
	assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "cache_entry_limit_exceeded", "/cache/limits/tree/maxFiles", release.StageCacheLoad)
	mustWrite(t, filepath.Join(tree, "extra-b"), []byte("x"), 0o644)
	_, err = cache.Load(context.Background(), ref)
	assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "cache_entry_limit_exceeded", "/cache/limits/tree/maxFiles", release.StageCacheLoad)
}

func TestDirectoryCacheDeepSingleFileUsesManifestParentBudget(t *testing.T) {
	requireNativeCachePlatform(t)
	provider, ref := releaseProviderAtPath(t, "sample.deep", "a/b/c/main.go", "deep")
	limits := release.DefaultCacheLimits()
	limits.Tree.MaxFiles = 1
	root := filepath.Join(realTestPath(t, t.TempDir()), "cache")
	cache, err := release.OpenDirectoryCache(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	resolved := resolveStatic(t, provider, ref)
	if err := cache.Store(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.Load(context.Background(), ref)
	if err != nil || !loaded.Ref().Equal(ref) {
		t.Fatalf("deep load = %#v, err=%v", loaded, err)
	}
	entryTree := filepath.Join(root, cacheEntryName(ref), "tree")
	if err := os.Mkdir(filepath.Join(entryTree, "extra-one"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = cache.Load(context.Background(), ref)
	assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "cache_inventory_invalid", "/entry/tree", release.StageCacheLoad)
	if err := os.Mkdir(filepath.Join(entryTree, "extra-two"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = cache.Load(context.Background(), ref)
	assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "cache_inventory_invalid", "/entry/tree", release.StageCacheLoad)
	if err := os.Mkdir(filepath.Join(entryTree, "extra-three"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = cache.Load(context.Background(), ref)
	assertReleaseError(t, err, release.ErrReleaseConflict, "source_release_conflict", "cache_inventory_invalid", "/entry/tree", release.StageCacheLoad)
}

func TestDirectoryCacheNativeConcurrentNoReplaceAndCrashResidue(t *testing.T) {
	requireNativeCachePlatform(t)
	root := filepath.Join(realTestPath(t, t.TempDir()), "cache")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cache, err := release.OpenDirectoryCache(root, release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, ref := releaseProvider(t, "sample.concurrent", "concurrent")
	resolved := resolveStatic(t, provider, ref)
	const workers = 16
	var wait sync.WaitGroup
	failures := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := cache.Store(context.Background(), resolved); err != nil {
				failures <- err
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if _, err := cache.Load(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	residue := filepath.Join(root, ".stage-"+strings.Repeat("0", 64))
	if err := os.Mkdir(residue, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(residue, "secret"), []byte("do not inspect"), 0o600)
	before := filesystemSnapshot(t, residue)
	if _, err := cache.Load(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if after := filesystemSnapshot(t, residue); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("Load changed crash residue: before=%v after=%v", before, after)
	}
}

func TestDirectoryCacheExplicitPolicyDoesNotChangeOwnerValidity(t *testing.T) {
	requireNativeCachePlatform(t)
	data := bytes.Repeat([]byte("x"), (32<<20)+1)
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.large", ModulePath: "example.test/large", PackagePath: "example.test/large/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{{Path: "large.bin", Size: int64(len(data)), Digest: provenance.SHA256(data), Mode: sourceplugin.Mode0644}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{"large.bin"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerLimits := sourceplugin.DefaultTreeLimits()
	ownerLimits.MaxFileBytes = int64(len(data))
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: "large.bin", Content: data}}, ownerLimits)
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
	resolved := resolveStatic(t, provider, ref)
	parent := realTestPath(t, t.TempDir())
	defaultRoot := filepath.Join(parent, "default")
	defaultCache, err := release.OpenDirectoryCache(defaultRoot, release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	err = defaultCache.Store(context.Background(), resolved)
	assertReleaseError(t, err, release.ErrReleaseInput, "source_release_invalid", "cache_limit_exceeded", "/cache/limits/tree/maxFileBytes", release.StageCacheStore)
	if _, statErr := os.Stat(defaultRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("policy rejection touched root: %v", statErr)
	}
	raised := release.DefaultCacheLimits()
	raised.Tree.MaxFileBytes = int64(len(data))
	raised.Tree.MaxTotalBytes = int64(len(data))
	raisedCache, err := release.OpenDirectoryCache(filepath.Join(parent, "raised"), raised)
	if err != nil {
		t.Fatal(err)
	}
	if err := raisedCache.Store(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	loaded, err := raisedCache.Load(context.Background(), ref)
	if err != nil || loaded.Tree().Digest() != tree.Digest() {
		t.Fatalf("raised load = %#v, err=%v", loaded, err)
	}
}

func TestDirectoryCacheSupportsEmptyOwnerTree(t *testing.T) {
	requireNativeCachePlatform(t)
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "sample.empty-cache", ModulePath: "example.test/empty", PackagePath: "example.test/empty/source", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{}, Profiles: []sourceplugin.ProfileSpec{{ID: "empty", Files: []string{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{}, sourceplugin.DefaultTreeLimits())
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
	cache, err := release.OpenDirectoryCache(filepath.Join(realTestPath(t, t.TempDir()), "cache"), release.DefaultCacheLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), resolveStatic(t, provider, ref)); err != nil {
		t.Fatal(err)
	}
	loaded, err := cache.Load(context.Background(), ref)
	if err != nil || loaded.Tree().Len() != 0 || loaded.Tree().Digest() != tree.Digest() {
		t.Fatalf("empty load = %#v, err=%v", loaded, err)
	}
}

func resolveStatic(t *testing.T, provider sourceplugin.Provider, ref release.Ref) release.ResolvedRelease {
	t.Helper()
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func cacheEntryName(ref release.Ref) string {
	return strings.TrimPrefix(ref.ManifestDigest().String(), "sha256:") + "-" + strings.TrimPrefix(ref.TreeDigest().String(), "sha256:")
}

func realTestPath(t *testing.T, path string) string {
	t.Helper()
	parent := path
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		parent = filepath.Dir(path)
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	if parent == path {
		return realParent
	}
	return filepath.Join(realParent, filepath.Base(path))
}

func filesystemSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var result []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		relative, _ := filepath.Rel(root, path)
		result = append(result, fmt.Sprintf("%s:%s:%o:%d", filepath.ToSlash(relative), info.Mode().Type(), info.Mode().Perm(), info.Size()))
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return result
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("mode %s = %o, want %o", path, info.Mode().Perm(), want)
	}
}

func mustWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func requireNativeCachePlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("persistent cache unsupported")
	}
}
