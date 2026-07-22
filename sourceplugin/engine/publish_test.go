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

func TestSerialPublishAppliesVerifiedPlanAndPreservesLocalFiles(t *testing.T) {
	oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"config/value.txt": "old\n", "remove.txt": "remove\n"})
	newProvider, newRef := publishProvider(t, "sample", "v0.2.0", map[string]string{"config/value.txt": "new\n", "added.txt": "added\n"})
	resolver, err := release.NewExactResolver(nil, oldProvider, newProvider)
	if err != nil {
		t.Fatal(err)
	}
	engine := publishEngine(t, resolver)
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	installPublishBaseline(t, root, resolver, oldRef, key)
	writePublishTestFile(t, filepath.Join(root, "services/sample/local.txt"), []byte("consumer\n"), 0o644)
	plan := publishPlan(t, engine, root, newRef, key.Target())
	verified := deriveOwnership(t, resolver, newRef, key.Target())
	prepared, err := engine.preparePlanPublish(&repository{root: root}, plan, verified)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	if err := engine.applyPreparedPlan(context.Background(), &repository{root: root}, plan, verified, prepared); err != nil {
		t.Fatal(err)
	}
	assertPublishedFile(t, filepath.Join(root, "services/sample/config/value.txt"), "new\n", 0o644)
	assertPublishedFile(t, filepath.Join(root, "services/sample/added.txt"), "added\n", 0o644)
	assertPublishedFile(t, filepath.Join(root, "services/sample/local.txt"), "consumer\n", 0o644)
	if _, err := os.Lstat(filepath.Join(root, "services/sample/remove.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file remains: %v", err)
	}
	assertInstalledLock(t, root, resolver, verified)
}

func TestPublishChangeRejectsDriftAtItsPublishPoint(t *testing.T) {
	oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"value.txt": "old\n"})
	newProvider, newRef := publishProvider(t, "sample", "v0.2.0", map[string]string{"value.txt": "new\n"})
	resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
	engine := publishEngine(t, resolver)
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	installPublishBaseline(t, root, resolver, oldRef, key)
	plan := publishPlan(t, engine, root, newRef, key.Target())
	prepared, err := engine.preparePlanPublish(&repository{root: root}, plan, deriveOwnership(t, resolver, newRef, key.Target()))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	var replacement Change
	for _, change := range plan.changes {
		if change.path == "value.txt" {
			replacement = change
		}
	}
	writePublishTestFile(t, filepath.Join(root, "services/sample/value.txt"), []byte("late edit\n"), 0o644)
	err = publishPlanChange(root, plan.selection.target, prepared.previewRoot, replacement, sourceplugin.DefaultTreeLimits())
	assertLifecycleError(t, err, ErrConflict, "source_snapshot_changed")
	assertPublishedFile(t, filepath.Join(root, "services/sample/value.txt"), "late edit\n", 0o644)
}

func TestPartialPublishCanBeReplannedFromCurrentTree(t *testing.T) {
	oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"first.txt": "old-1\n", "second.txt": "old-2\n"})
	newProvider, newRef := publishProvider(t, "sample", "v0.2.0", map[string]string{"first.txt": "new-1\n", "second.txt": "new-2\n"})
	resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
	engine := publishEngine(t, resolver)
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	installPublishBaseline(t, root, resolver, oldRef, key)
	plan := publishPlan(t, engine, root, newRef, key.Target())
	prepared, err := engine.preparePlanPublish(&repository{root: root}, plan, deriveOwnership(t, resolver, newRef, key.Target()))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	changes := mutablePublishChanges(plan.changes)
	if len(changes) != 2 {
		t.Fatalf("mutable changes = %d, want 2", len(changes))
	}
	if err := publishPlanChange(root, plan.selection.target, prepared.previewRoot, changes[0], sourceplugin.DefaultTreeLimits()); err != nil {
		t.Fatal(err)
	}
	writePublishTestFile(t, filepath.Join(root, "services/sample/second.txt"), []byte("late edit\n"), 0o644)
	err = publishPlanChange(root, plan.selection.target, prepared.previewRoot, changes[1], sourceplugin.DefaultTreeLimits())
	assertLifecycleError(t, err, ErrConflict, "source_snapshot_changed")
	assertPublishedFile(t, filepath.Join(root, "services/sample/first.txt"), "new-1\n", 0o644)
	assertPublishedFile(t, filepath.Join(root, "services/sample/second.txt"), "late edit\n", 0o644)
	fresh, err := engine.Plan(context.Background(), PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.BeforeDigest() == plan.BeforeDigest() {
		t.Fatal("fresh plan did not describe the current partial tree")
	}
}

func TestAfterDigestMismatchDoesNotPublishNewOwnership(t *testing.T) {
	oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"value.txt": "old\n"})
	newProvider, newRef := publishProvider(t, "sample", "v0.2.0", map[string]string{"value.txt": "new\n"})
	resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
	engine := publishEngine(t, resolver)
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	installPublishBaseline(t, root, resolver, oldRef, key)
	oldOwnership := append([]byte(nil), readPublishedFile(t, filepath.Join(root, filepath.FromSlash(key.RepositoryPath())))...)
	plan := publishPlan(t, engine, root, newRef, key.Target())
	verified := deriveOwnership(t, resolver, newRef, key.Target())
	prepared, err := engine.preparePlanPublish(&repository{root: root}, plan, verified)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.cleanup()
	plan.afterDigest = provenance.SHA256([]byte("wrong final target"))
	err = engine.applyPreparedPlan(context.Background(), &repository{root: root}, plan, verified, prepared)
	assertLifecycleError(t, err, ErrConflict, "source_snapshot_changed")
	if current := readPublishedFile(t, filepath.Join(root, filepath.FromSlash(key.RepositoryPath()))); string(current) != string(oldOwnership) {
		t.Fatal("afterDigest failure published new ownership")
	}
}

func TestPublishFailurePreservesClassificationAndUnderlyingCause(t *testing.T) {
	root := t.TempDir()
	previewRoot := filepath.Join(root, "missing-preview")
	change := Change{
		path:   "value.txt",
		local:  FileState{typeOf: FileAbsent},
		result: FileState{typeOf: FileRegular, mode: 0o644, size: 4, content: []byte("new\n")},
	}

	err := publishPlanChange(root, "services/sample", previewRoot, change, sourceplugin.DefaultTreeLimits())
	assertLifecycleError(t, err, ErrExternal, "stage_write_failed")
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("publish error = %T, want *Error", err)
	}
	if typed.Code() != "source_transaction_failed" || typed.Stage() != "transaction" || typed.Reason() != "stage_write_failed" {
		t.Fatalf("code=%q stage=%q reason=%q", typed.Code(), typed.Stage(), typed.Reason())
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publish error does not retain os.ErrNotExist: %v", err)
	}
}

func TestCanceledContextRemainsDiscoverableThroughEngineError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := validateContext(ctx, "transaction")
	assertLifecycleError(t, err, ErrCanceled, "context_canceled")
	var typed *Error
	if !errors.As(err, &typed) || typed.Code() != "operation_canceled" || typed.Stage() != "transaction" {
		t.Fatalf("error=%#v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("engine error does not retain context cancellation: %v", err)
	}
}

type publishMergeDriver struct{}

func (publishMergeDriver) Merge(_ context.Context, input TextMergeInput) (TextMergeResult, error) {
	return NewTextMergeResult(input.New, true), nil
}

func publishProvider(t *testing.T, providerID, version string, files map[string]string) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	specs := make([]sourceplugin.FileSpec, 0, len(files))
	paths := make([]string, 0, len(files))
	inputs := make([]sourceplugin.TreeInput, 0, len(files))
	for path, value := range files {
		content := []byte(value)
		specs = append(specs, sourceplugin.FileSpec{Path: path, Mode: sourceplugin.Mode0644, Size: int64(len(content)), Digest: provenance.SHA256(content)})
		paths = append(paths, path)
		inputs = append(inputs, sourceplugin.TreeInput{Path: path, Content: content})
	}
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{Identity: sourceplugin.IdentitySpec{ProviderID: providerID, ModulePath: "example.test/" + providerID, PackagePath: "example.test/" + providerID + "/source", Version: version}, Files: specs, Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: paths}}})
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

func publishEngine(t *testing.T, resolver *release.ExactResolver) *Engine {
	t.Helper()
	result, err := New(Options{Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: publishMergeDriver{}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func publishPlan(t *testing.T, engine *Engine, root string, ref release.Ref, target string) Plan {
	t.Helper()
	selection, err := NewSelection(SelectionSpec{Release: ref, ProfileID: "default", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := engine.Plan(context.Background(), PlanRequest{RepositoryRoot: root, Selection: selection})
	if err != nil || !plan.CanApply() {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	return plan
}

func deriveOwnership(t *testing.T, resolver *release.ExactResolver, ref release.Ref, target string) lock.VerifiedLock {
	t.Helper()
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := lock.Derive(ref, resolved, "default", target, lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return verified
}

func installPublishBaseline(t *testing.T, root string, resolver *release.ExactResolver, ref release.Ref, key lock.Key) {
	t.Helper()
	verified := deriveOwnership(t, resolver, ref, key.Target())
	resolved, err := resolver.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range verified.TrackedFiles() {
		file, ok := resolved.Tree().Lookup(baseline.Path())
		if !ok {
			t.Fatalf("tree file missing: %s", baseline.Path())
		}
		writePublishTestFile(t, filepath.Join(root, filepath.FromSlash(key.Target()), filepath.FromSlash(file.Path())), file.Bytes(), 0o644)
	}
	canonical, err := verified.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	writePublishTestFile(t, filepath.Join(root, filepath.FromSlash(key.RepositoryPath())), canonical, 0o600)
}

func assertInstalledLock(t *testing.T, root string, resolver *release.ExactResolver, expected lock.VerifiedLock) {
	t.Helper()
	data := readPublishedFile(t, filepath.Join(root, filepath.FromSlash(expected.Key().RepositoryPath())))
	snapshot, err := lock.Parse(expected.Key().RepositoryPath(), data, lock.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), expected.Release())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Verify(snapshot, expected.Key(), resolved, lock.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
}

func assertPublishStagingClean(t *testing.T, root string) {
	t.Helper()
	staging, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(sourceControlPath), sourceStagingPrefix+"*"))
	if err != nil || len(staging) != 0 {
		t.Fatalf("publish staging remains: %v, %v", staging, err)
	}
}

func assertPublishedFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	data := readPublishedFile(t, path)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content || info.Mode().Perm() != mode {
		t.Fatalf("file %s content=%q mode=%o", path, data, info.Mode().Perm())
	}
}

func readPublishedFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writePublishTestFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func publishBusinessState(t *testing.T, root string, key lock.Key) string {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(key.Target()))
	lockPath := filepath.Join(root, filepath.FromSlash(key.RepositoryPath()))
	return testPathState(t, target) + "\nLOCK\n" + testPathState(t, lockPath)
}

func testPathState(t *testing.T, root string) string {
	t.Helper()
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return "absent"
	} else if err != nil {
		t.Fatal(err)
	}
	var result string
	if err := filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		result += filepath.ToSlash(relative) + "|" + info.Mode().String() + "|"
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			result += string(data)
		}
		result += "\n"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
