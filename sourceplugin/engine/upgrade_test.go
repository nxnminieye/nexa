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

func TestUpgradeInstallsNewBaselineAndPreservesLocalModification(t *testing.T) {
	oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"preserved.txt": "base\n", "replaced.txt": "old\n"})
	newProvider, newRef := publishProvider(t, "sample", "v0.2.0", map[string]string{"preserved.txt": "base\n", "replaced.txt": "new\n"})
	resolver, err := release.NewExactResolver(nil, oldProvider, newProvider)
	if err != nil {
		t.Fatal(err)
	}
	engine := publishEngine(t, resolver)
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	installPublishBaseline(t, root, resolver, oldRef, key)
	writePublishTestFile(t, filepath.Join(root, "services/sample/preserved.txt"), []byte("local\n"), 0o644)
	result, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}})
	if err != nil || result.Operation() != PlanUpgrade || result.Status().State() != ManagedStateModified {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertPublishedFile(t, filepath.Join(root, "services/sample/preserved.txt"), "local\n", 0o644)
	assertPublishedFile(t, filepath.Join(root, "services/sample/replaced.txt"), "new\n", 0o644)
	assertInstalledLock(t, root, resolver, deriveOwnership(t, resolver, newRef, key.Target()))
}

func TestUpgradeRequiresManagedSource(t *testing.T) {
	provider, ref := publishProvider(t, "sample", "v0.1.0", map[string]string{"value.txt": "value\n"})
	resolver, _ := release.NewExactResolver(nil, provider)
	engine := publishEngine(t, resolver)
	root := t.TempDir()
	_, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, ref, "services/sample")}})
	assertLifecycleError(t, err, ErrNotManaged, "lock_missing")
}

func TestPublicUpgradeCompleteOldLocalNewMatrix(t *testing.T) {
	tests := []struct {
		name             string
		old, local, next *lifecycleFile
		mergeContent     string
		mergeClean       bool
		wantContent      *lifecycleFile
		wantState        ManagedState
		conflict         bool
	}{
		{"unchanged", lifecycleText("A\n"), lifecycleText("A\n"), lifecycleText("A\n"), "", true, lifecycleText("A\n"), ManagedStateClean, false},
		{"upstream replace", lifecycleText("A\n"), lifecycleText("A\n"), lifecycleText("B\n"), "", true, lifecycleText("B\n"), ManagedStateClean, false},
		{"upstream delete", lifecycleText("A\n"), lifecycleText("A\n"), nil, "", true, nil, ManagedStateClean, false},
		{"preserve local", lifecycleText("A\n"), lifecycleText("L\n"), lifecycleText("A\n"), "", true, lifecycleText("L\n"), ManagedStateModified, false},
		{"preserve local delete", lifecycleText("A\n"), nil, lifecycleText("A\n"), "", true, nil, ManagedStateModified, false},
		{"converged replace", lifecycleText("A\n"), lifecycleText("B\n"), lifecycleText("B\n"), "", true, lifecycleText("B\n"), ManagedStateClean, false},
		{"converged delete", lifecycleText("A\n"), nil, nil, "", true, nil, ManagedStateClean, false},
		{"add", nil, nil, lifecycleText("B\n"), "", true, lifecycleText("B\n"), ManagedStateClean, false},
		{"preserve local only", nil, lifecycleText("L\n"), nil, "", true, lifecycleText("L\n"), ManagedStateModified, false},
		{"converged add", nil, lifecycleText("B\n"), lifecycleText("B\n"), "", true, lifecycleText("B\n"), ManagedStateClean, false},
		{"materialize collision", nil, lifecycleText("L\n"), lifecycleText("B\n"), "", true, nil, "", true},
		{"upstream deleted local modified", lifecycleText("A\n"), lifecycleText("L\n"), nil, "", true, nil, "", true},
		{"local deleted upstream modified", lifecycleText("A\n"), nil, lifecycleText("B\n"), "", true, nil, "", true},
		{"clean text merge", lifecycleText("base\n"), lifecycleText("local\n"), lifecycleText("new\n"), "merged\n", true, lifecycleText("merged\n"), ManagedStateModified, false},
		{"text merge conflict", lifecycleText("base\n"), lifecycleText("local\n"), lifecycleText("new\n"), "conflict\n", false, nil, "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldFiles, nextFiles := map[string]lifecycleFile{}, map[string]lifecycleFile{}
			if test.old != nil {
				oldFiles["file.txt"] = *test.old
			}
			if test.next != nil {
				nextFiles["file.txt"] = *test.next
			}
			oldProvider, oldRef := lifecycleProvider(t, "sample", "v0.1.0", oldFiles)
			newProvider, newRef := lifecycleProvider(t, "sample", "v0.2.0", nextFiles)
			resolver, err := release.NewExactResolver(nil, oldProvider, newProvider)
			if err != nil {
				t.Fatal(err)
			}
			engine := lifecycleEngine(t, resolver, lifecycleMergeDriver{content: []byte(test.mergeContent), clean: test.mergeClean})
			root := t.TempDir()
			key, _ := lock.NewKey("sample", "services/sample")
			installPublishBaseline(t, root, resolver, oldRef, key)
			filePath := filepath.Join(root, "services/sample/file.txt")
			if test.local == nil {
				_ = os.Remove(filePath)
			} else {
				writePublishTestFile(t, filePath, test.local.content, os.FileMode(test.local.mode))
			}
			before := testPathState(t, root)
			result, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}})
			if test.conflict {
				assertLifecycleError(t, err, ErrConflict, "plan_conflict")
				if after := testPathState(t, root); after != before {
					t.Fatal("upgrade conflict changed repository")
				}
				return
			}
			if err != nil || result.Operation() != PlanUpgrade || result.Status().State() != test.wantState {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if test.wantContent == nil {
				if _, err := os.Lstat(filePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("file remains: %v", err)
				}
			} else {
				assertLifecycleFile(t, filePath, *test.wantContent)
			}
			assertInstalledLock(t, root, resolver, deriveOwnership(t, resolver, newRef, key.Target()))
		})
	}
}

func TestPublicUpgradeModeBinaryTypeRenameAndBoundaryConflicts(t *testing.T) {
	t.Run("mode convergence", func(t *testing.T) {
		oldProvider, oldRef := lifecycleProvider(t, "sample", "v0.1.0", map[string]lifecycleFile{"file": {content: []byte("same"), mode: 0o644}})
		newProvider, newRef := lifecycleProvider(t, "sample", "v0.2.0", map[string]lifecycleFile{"file": {content: []byte("same"), mode: 0o755}})
		resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
		engine := lifecycleEngine(t, resolver, lifecycleMergeDriver{clean: true})
		root := t.TempDir()
		key, _ := lock.NewKey("sample", "services/sample")
		installPublishBaseline(t, root, resolver, oldRef, key)
		if err := os.Chmod(filepath.Join(root, "services/sample/file"), 0o755); err != nil {
			t.Fatal(err)
		}
		result, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}})
		if err != nil || result.Status().State() != ManagedStateClean {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		assertLifecycleFile(t, filepath.Join(root, "services/sample/file"), lifecycleFile{content: []byte("same"), mode: 0o755})
	})
	for _, test := range []struct {
		name                string
		old, local, next    []byte
		localMode, nextMode uint32
		symlink             bool
	}{
		{"binary conflict", []byte("\x00old"), []byte("\x00local"), []byte("\x00new"), 0o644, 0o644, false},
		{"mode conflict", []byte("old"), []byte("local"), []byte("new"), 0o755, 0o644, false},
		{"type conflict", []byte("old"), nil, []byte("new"), 0o644, 0o644, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldProvider, oldRef := lifecycleProvider(t, "sample", "v0.1.0", map[string]lifecycleFile{"file": {content: test.old, mode: 0o644}})
			newProvider, newRef := lifecycleProvider(t, "sample", "v0.2.0", map[string]lifecycleFile{"file": {content: test.next, mode: test.nextMode}})
			resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
			engine := lifecycleEngine(t, resolver, lifecycleMergeDriver{clean: true})
			root := t.TempDir()
			key, _ := lock.NewKey("sample", "services/sample")
			installPublishBaseline(t, root, resolver, oldRef, key)
			path := filepath.Join(root, "services/sample/file")
			if test.symlink {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("elsewhere", path); err != nil {
					t.Fatal(err)
				}
			} else {
				writePublishTestFile(t, path, test.local, os.FileMode(test.localMode))
				if err := os.Chmod(path, os.FileMode(test.localMode)); err != nil {
					t.Fatal(err)
				}
			}
			before := testPathState(t, root)
			_, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}})
			assertLifecycleError(t, err, ErrConflict, "plan_conflict")
			if after := testPathState(t, root); after != before {
				t.Fatal("binary/type conflict changed repository")
			}
		})
	}
	t.Run("rename is delete add", func(t *testing.T) {
		oldProvider, oldRef := lifecycleProvider(t, "sample", "v0.1.0", map[string]lifecycleFile{"old-name": {content: []byte("same"), mode: 0o644}})
		newProvider, newRef := lifecycleProvider(t, "sample", "v0.2.0", map[string]lifecycleFile{"new-name": {content: []byte("same"), mode: 0o644}})
		resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
		engine := lifecycleEngine(t, resolver, lifecycleMergeDriver{clean: true})
		root := t.TempDir()
		key, _ := lock.NewKey("sample", "services/sample")
		installPublishBaseline(t, root, resolver, oldRef, key)
		if _, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(root, "services/sample/old-name")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old-name remains: %v", err)
		}
		assertLifecycleFile(t, filepath.Join(root, "services/sample/new-name"), lifecycleFile{content: []byte("same"), mode: 0o644})
	})
	for _, cross := range []bool{false, true} {
		t.Run(map[bool]string{false: "tampered lock", true: "cross provider"}[cross], func(t *testing.T) {
			oldID, newID := "sample", "sample"
			if cross {
				oldID, newID = "alpha", "beta"
			}
			oldProvider, oldRef := lifecycleProvider(t, oldID, "v0.1.0", map[string]lifecycleFile{"file": {content: []byte("old"), mode: 0o644}})
			newProvider, newRef := lifecycleProvider(t, newID, "v0.2.0", map[string]lifecycleFile{"file": {content: []byte("new"), mode: 0o644}})
			resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
			engine := lifecycleEngine(t, resolver, lifecycleMergeDriver{clean: true})
			root := t.TempDir()
			oldKey, _ := lock.NewKey(oldID, "services/sample")
			installPublishBaseline(t, root, resolver, oldRef, oldKey)
			if !cross {
				writePublishTestFile(t, filepath.Join(root, filepath.FromSlash(oldKey.RepositoryPath())), []byte("{}\n"), 0o644)
			}
			before := testPathState(t, root)
			_, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, oldKey.Target())}})
			if err == nil {
				t.Fatal("boundary conflict was accepted")
			}
			if after := testPathState(t, root); after != before {
				t.Fatal("boundary conflict changed repository")
			}
		})
	}
	t.Run("missing old release", func(t *testing.T) {
		oldProvider, oldRef := lifecycleProvider(t, "sample", "v0.1.0", map[string]lifecycleFile{"file": {content: []byte("old"), mode: 0o644}})
		newProvider, newRef := lifecycleProvider(t, "sample", "v0.2.0", map[string]lifecycleFile{"file": {content: []byte("new"), mode: 0o644}})
		installResolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
		missingOldResolver, _ := release.NewExactResolver(nil, newProvider)
		engine := lifecycleEngine(t, missingOldResolver, lifecycleMergeDriver{clean: true})
		root := t.TempDir()
		key, _ := lock.NewKey("sample", "services/sample")
		installPublishBaseline(t, root, installResolver, oldRef, key)
		before := testPathState(t, root)
		if _, err := engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}}); err == nil {
			t.Fatal("missing old release was accepted")
		}
		if after := testPathState(t, root); after != before {
			t.Fatal("missing old release changed repository")
		}
	})
	t.Run("validation failure", func(t *testing.T) {
		oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", map[string]string{"go.mod": "module example.test/materialized\n\ngo 1.25\n"})
		newProvider, newRef := lifecycleValidationProviderVersion(t, "v0.2.0")
		resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
		recorder := &recordingValidationExecutor{result: ExecutionResult{ExitCode: 9}}
		engine, err := New(Options{Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: lifecycleMergeDriver{clean: true}, Executor: recorder, GoToolchain: validationToolchain(t)})
		if err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		key, _ := lock.NewKey("sample", "services/sample")
		installPublishBaseline(t, root, resolver, oldRef, key)
		before := publishBusinessState(t, root, key)
		_, err = engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: lifecycleSelection(t, newRef, key.Target())}})
		assertLifecycleError(t, err, ErrExternal, "validation_failed")
		if after := publishBusinessState(t, root, key); after != before {
			t.Fatal("upgrade validation failure changed repository")
		}
	})
}

func TestUpgradeFileDirectoryEvolutionIsAStablePlanConflict(t *testing.T) {
	for _, test := range []struct {
		name     string
		oldFiles map[string]string
		newFiles map[string]string
	}{
		{name: "file to directory", oldFiles: map[string]string{"a": "old\n"}, newFiles: map[string]string{"a/b": "new\n"}},
		{name: "directory to file", oldFiles: map[string]string{"a/b": "old\n"}, newFiles: map[string]string{"a": "new\n"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldProvider, oldRef := publishProvider(t, "sample", "v0.1.0", test.oldFiles)
			newProvider, newRef := publishProvider(t, "sample", "v0.2.0", test.newFiles)
			resolver, _ := release.NewExactResolver(nil, oldProvider, newProvider)
			engine := publishEngine(t, resolver)
			root := t.TempDir()
			key, _ := lock.NewKey("sample", "services/sample")
			installPublishBaseline(t, root, resolver, oldRef, key)
			before := publishBusinessState(t, root, key)
			assertTypeConflict := func(plan Plan) {
				t.Helper()
				if plan.CanApply() {
					t.Fatal("file/directory evolution produced an applicable plan")
				}
				for _, conflict := range plan.Conflicts() {
					if conflict.Path() == "a" && conflict.Reason() == ConflictType {
						return
					}
				}
				t.Fatalf("type conflict missing: %#v", plan.Conflicts())
			}
			selection := lifecycleSelection(t, newRef, key.Target())
			plan, err := engine.Plan(context.Background(), PlanRequest{RepositoryRoot: root, Selection: selection})
			if err != nil {
				t.Fatal(err)
			}
			assertTypeConflict(plan)
			_, err = engine.Upgrade(context.Background(), UpgradeRequest{PlanRequest: PlanRequest{RepositoryRoot: root, Selection: selection}})
			assertLifecycleError(t, err, ErrConflict, "plan_conflict")
			if after := publishBusinessState(t, root, key); after != before {
				t.Fatal("type conflict changed target or ownership")
			}
			fresh, err := engine.Plan(context.Background(), PlanRequest{RepositoryRoot: root, Selection: selection})
			if err != nil {
				t.Fatal(err)
			}
			assertTypeConflict(fresh)
		})
	}
}

type lifecycleFile struct {
	content []byte
	mode    uint32
}

func lifecycleText(value string) *lifecycleFile {
	return &lifecycleFile{content: []byte(value), mode: 0o644}
}

type lifecycleMergeDriver struct {
	content []byte
	clean   bool
}

func (driver lifecycleMergeDriver) Merge(context.Context, TextMergeInput) (TextMergeResult, error) {
	return NewTextMergeResult(driver.content, driver.clean), nil
}

func lifecycleProvider(t *testing.T, providerID, version string, files map[string]lifecycleFile) (sourceplugin.Provider, release.Ref) {
	t.Helper()
	specs := make([]sourceplugin.FileSpec, 0, len(files))
	paths := make([]string, 0, len(files))
	inputs := make([]sourceplugin.TreeInput, 0, len(files))
	for path, file := range files {
		mode := sourceplugin.Mode0644
		if file.mode == 0o755 {
			mode = sourceplugin.Mode0755
		}
		specs = append(specs, sourceplugin.FileSpec{Path: path, Mode: mode, Size: int64(len(file.content)), Digest: provenance.SHA256(file.content)})
		paths = append(paths, path)
		inputs = append(inputs, sourceplugin.TreeInput{Path: path, Content: file.content})
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

func lifecycleEngine(t *testing.T, resolver *release.ExactResolver, driver MergeDriver) *Engine {
	t.Helper()
	engine, err := New(Options{Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
func assertLifecycleFile(t *testing.T, path string, file lifecycleFile) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(file.content) || uint32(info.Mode().Perm()) != file.mode {
		t.Fatalf("file=%q mode=%o", data, info.Mode().Perm())
	}
}
