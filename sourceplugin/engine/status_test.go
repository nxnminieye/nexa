package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestStatusAndDiffReportManagedRepositoryBehavior(t *testing.T) {
	provider, ref := testProvider(t, "sample", "v0.1.0", map[string]testFile{
		"bin/run":      {content: "run\n", mode: sourceplugin.Mode0755},
		"config/a.txt": {content: "alpha\n", mode: sourceplugin.Mode0644},
	})
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := engine.New(engine.Options{
		Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: &fixedMergeDriver{},
	})
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	key, err := lock.NewKey("sample", "services/sample")
	if err != nil {
		t.Fatal(err)
	}
	notManaged, err := inspector.Status(context.Background(), engine.ManagedRequest{RepositoryRoot: root, Key: key})
	if err != nil || notManaged.State() != engine.ManagedStateNotManaged || len(notManaged.Deltas()) != 0 || notManaged.SnapshotDigest().String() == "" {
		t.Fatalf("not managed status = %#v err=%v", notManaged, err)
	}
	_, err = inspector.Diff(context.Background(), engine.ManagedRequest{RepositoryRoot: root, Key: key})
	var projected *engine.Error
	if !errors.As(err, &projected) || projected.Code() != "source_not_managed" || projected.Reason() != "lock_missing" {
		t.Fatalf("missing diff error = %#v", err)
	}

	installManaged(t, root, resolver, ref, "default", key)
	clean, err := inspector.Status(context.Background(), engine.ManagedRequest{RepositoryRoot: root, Key: key})
	if err != nil || clean.State() != engine.ManagedStateClean || len(clean.Deltas()) != 0 || clean.SnapshotDigest().String() == "" {
		t.Fatalf("clean status = %#v err=%v", clean, err)
	}

	tests := []struct {
		name string
		edit func(string)
		kind engine.DeltaKind
		path string
	}{
		{"content", func(root string) {
			mustWrite(t, filepath.Join(root, "services/sample/config/a.txt"), []byte("local\n"), 0o644)
		}, engine.DeltaModified, "config/a.txt"},
		{"mode", func(root string) { must(t, os.Chmod(filepath.Join(root, "services/sample/config/a.txt"), 0o755)) }, engine.DeltaModeChanged, "config/a.txt"},
		{"delete", func(root string) { must(t, os.Remove(filepath.Join(root, "services/sample/config/a.txt"))) }, engine.DeltaDeleted, "config/a.txt"},
		{"local add", func(root string) {
			mustWrite(t, filepath.Join(root, "services/sample/local.txt"), []byte("local\n"), 0o644)
		}, engine.DeltaAdded, "local.txt"},
		{"type", func(root string) {
			path := filepath.Join(root, "services/sample/config/a.txt")
			must(t, os.Remove(path))
			must(t, os.Symlink("elsewhere", path))
		}, engine.DeltaTypeChanged, "config/a.txt"},
		{"directory type", func(root string) {
			path := filepath.Join(root, "services/sample/config/a.txt")
			must(t, os.Remove(path))
			must(t, os.Mkdir(path, 0o755))
		}, engine.DeltaTypeChanged, "config/a.txt"},
		{"empty directory", func(root string) {
			must(t, os.Mkdir(filepath.Join(root, "services/sample/empty-local"), 0o755))
		}, engine.DeltaAdded, "empty-local"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caseRoot := t.TempDir()
			installManaged(t, caseRoot, resolver, ref, "default", key)
			test.edit(caseRoot)
			status, err := inspector.Status(context.Background(), engine.ManagedRequest{RepositoryRoot: caseRoot, Key: key})
			if err != nil || status.State() != engine.ManagedStateModified {
				t.Fatalf("status = %#v err=%v", status, err)
			}
			deltas := status.Deltas()
			if len(deltas) != 1 || deltas[0].Path() != test.path || deltas[0].Kind() != test.kind {
				t.Fatalf("deltas = %#v", deltas)
			}
			deltas[0] = engine.Delta{}
			again := status.Deltas()
			if len(again) != 1 || again[0].Path() != test.path {
				t.Fatal("status deltas were mutable")
			}
			diff, err := inspector.Diff(context.Background(), engine.ManagedRequest{RepositoryRoot: caseRoot, Key: key})
			if err != nil || len(diff.Deltas()) != 1 || diff.Deltas()[0].Kind() != test.kind || diff.SnapshotDigest() != status.SnapshotDigest() {
				t.Fatalf("diff = %#v err=%v", diff, err)
			}
		})
	}
}

func TestStatusRejectsInvalidLockWithoutChangingRepository(t *testing.T) {
	provider, _ := testProvider(t, "sample", "v0.1.0", map[string]testFile{"file.txt": {content: "value", mode: sourceplugin.Mode0644}})
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	inspector := newTestEngine(t, resolver, &fixedMergeDriver{})
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	mustWrite(t, filepath.Join(root, filepath.FromSlash(key.RepositoryPath())), []byte("{}\n"), 0o644)
	before := testRepositorySnapshot(t, root)
	_, err = inspector.Status(context.Background(), engine.ManagedRequest{RepositoryRoot: root, Key: key})
	var projected *engine.Error
	if !errors.As(err, &projected) || projected.Code() != "source_lock_invalid" {
		t.Fatalf("invalid lock error = %#v", err)
	}
	if after := testRepositorySnapshot(t, root); before != after {
		t.Fatalf("read command changed repository: before=%q after=%q", before, after)
	}
}

type testFile struct {
	content string
	mode    sourceplugin.FileMode
}

func testProvider(t *testing.T, providerID, version string, files map[string]testFile) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	specs := make([]sourceplugin.FileSpec, 0, len(files))
	paths := make([]string, 0, len(files))
	inputs := make([]sourceplugin.TreeInput, 0, len(files))
	for path, file := range files {
		content := []byte(file.content)
		specs = append(specs, sourceplugin.FileSpec{Path: path, Size: int64(len(content)), Digest: provenance.SHA256(content), Mode: file.mode})
		paths = append(paths, path)
		inputs = append(inputs, sourceplugin.TreeInput{Path: path, Content: content})
	}
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: providerID, ModulePath: "example.test/" + providerID, PackagePath: "example.test/" + providerID + "/source", Version: version},
		Files:    specs,
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: paths}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, inputs, sourceplugin.DefaultTreeLimits())
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

func installManaged(t *testing.T, root string, resolver *release.ExactResolver, ref release.Ref, profile string, key lock.Key) {
	t.Helper()
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := lock.Derive(ref, resolved, profile, key.Target(), lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range verified.TrackedFiles() {
		treeFile, ok := resolved.Tree().Lookup(file.Path())
		if !ok {
			t.Fatalf("missing tree file %s", file.Path())
		}
		mode := os.FileMode(0o644)
		if file.Mode() == sourceplugin.Mode0755 {
			mode = 0o755
		}
		mustWrite(t, filepath.Join(root, filepath.FromSlash(key.Target()), filepath.FromSlash(file.Path())), treeFile.Bytes(), mode)
	}
	canonical, err := verified.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, filepath.FromSlash(key.RepositoryPath())), canonical, 0o644)
}

func mustWrite(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, content, mode))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func testRepositorySnapshot(t *testing.T, root string) string {
	t.Helper()
	var result strings.Builder
	must(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		result.WriteString(filepath.ToSlash(rel))
		result.WriteString("|")
		result.WriteString(info.Mode().String())
		result.WriteString("|")
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result.Write(content)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			result.WriteString(target)
		}
		result.WriteByte('\n')
		return nil
	}))
	return result.String()
}
