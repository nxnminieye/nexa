package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/engine"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestMergeMatrixProducesCompleteDeterministicPlan(t *testing.T) {
	tests := []struct {
		name       string
		old, local *testFile
		new        *testFile
		action     engine.ChangeAction
		conflict   engine.ConflictReason
		merged     string
	}{
		{"unchanged", file("A\n"), file("A\n"), file("A\n"), engine.ChangeConverged, "", "A\n"},
		{"upstream replace", file("A\n"), file("A\n"), file("B\n"), engine.ChangeReplace, "", "B\n"},
		{"upstream delete", file("A\n"), file("A\n"), nil, engine.ChangeDelete, "", ""},
		{"preserve local", file("A\n"), file("L\n"), file("A\n"), engine.ChangePreserveLocal, "", "L\n"},
		{"converged replace", file("A\n"), file("B\n"), file("B\n"), engine.ChangeConverged, "", "B\n"},
		{"converged delete", file("A\n"), nil, nil, engine.ChangeConverged, "", ""},
		{"add", nil, nil, file("B\n"), engine.ChangeAdd, "", "B\n"},
		{"preserve local only", nil, file("L\n"), nil, engine.ChangePreserveLocal, "", "L\n"},
		{"converged add", nil, file("B\n"), file("B\n"), engine.ChangeConverged, "", "B\n"},
		{"materialize collision", nil, file("L\n"), file("B\n"), "", engine.ConflictLocalCollision, ""},
		{"upstream deleted local modified", file("A\n"), file("L\n"), nil, "", engine.ConflictUpstreamDeletedLocalModified, ""},
		{"local deleted upstream modified", file("A\n"), nil, file("B\n"), "", engine.ConflictLocalDeletedUpstreamModified, ""},
		{"clean text merge", file("base\n"), file("local\n"), file("new\n"), engine.ChangeReplace, "", "merged\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldFiles, newFiles := map[string]testFile{}, map[string]testFile{}
			if test.old != nil {
				oldFiles["file.txt"] = *test.old
			}
			if test.new != nil {
				newFiles["file.txt"] = *test.new
			}
			oldProvider, oldRef := testProvider(t, "sample", "v0.1.0", oldFiles)
			newProvider, newRef := testProvider(t, "sample", "v0.2.0", newFiles)
			resolver, err := release.NewExactResolver(nil, oldProvider, newProvider)
			if err != nil {
				t.Fatal(err)
			}
			driver := &fixedMergeDriver{result: []byte("merged\n"), clean: true}
			planner := newTestEngine(t, resolver, driver)
			root := t.TempDir()
			key, err := lock.NewKey("sample", "services/sample")
			if err != nil {
				t.Fatal(err)
			}
			installManaged(t, root, resolver, oldRef, "default", key)
			localPath := filepath.Join(root, "services/sample/file.txt")
			if test.local == nil {
				_ = os.Remove(localPath)
			} else {
				mustWrite(t, localPath, []byte(test.local.content), modeBits(test.local.mode))
			}
			selection, err := engine.NewSelection(engine.SelectionSpec{Release: newRef, ProfileID: "default", Target: key.Target()})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planner.Plan(context.Background(), engine.PlanRequest{RepositoryRoot: root, Selection: selection})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Operation() != engine.PlanUpgrade || plan.BeforeDigest().String() == "" || plan.Digest().String() == "" {
				t.Fatalf("plan metadata = %#v", plan)
			}
			if test.conflict != "" {
				conflicts := plan.Conflicts()
				if plan.CanApply() || len(conflicts) != 1 || conflicts[0].Path() != "file.txt" || conflicts[0].Reason() != test.conflict {
					t.Fatalf("conflicts = %#v", conflicts)
				}
				return
			}
			changes := plan.Changes()
			if !plan.CanApply() || len(changes) != 1 || changes[0].Path() != "file.txt" || changes[0].Action() != test.action || string(changes[0].Result().Bytes()) != test.merged {
				t.Fatalf("changes = %#v", changes)
			}
		})
	}
}

func TestPlanAggregatesModeBinaryTypeAndRenameBehavior(t *testing.T) {
	oldProvider, oldRef := testProvider(t, "sample", "v0.1.0", map[string]testFile{
		"binary":   {content: "\x00old", mode: sourceplugin.Mode0644},
		"mode":     {content: "same", mode: sourceplugin.Mode0644},
		"old-name": {content: "rename", mode: sourceplugin.Mode0644},
		"typed":    {content: "old", mode: sourceplugin.Mode0644},
	})
	newProvider, newRef := testProvider(t, "sample", "v0.2.0", map[string]testFile{
		"binary":   {content: "\x00new", mode: sourceplugin.Mode0644},
		"mode":     {content: "same", mode: sourceplugin.Mode0755},
		"new-name": {content: "rename", mode: sourceplugin.Mode0644},
		"typed":    {content: "new", mode: sourceplugin.Mode0644},
	})
	resolver, err := release.NewExactResolver(nil, oldProvider, newProvider)
	if err != nil {
		t.Fatal(err)
	}
	driver := &fixedMergeDriver{result: []byte("should-not-run"), clean: true}
	planner := newTestEngine(t, resolver, driver)
	root := t.TempDir()
	key, _ := lock.NewKey("sample", "services/sample")
	installManaged(t, root, resolver, oldRef, "default", key)
	target := filepath.Join(root, "services/sample")
	mustWrite(t, filepath.Join(target, "binary"), []byte("\x00local"), 0o644)
	must(t, os.Chmod(filepath.Join(target, "mode"), 0o755))
	must(t, os.Remove(filepath.Join(target, "typed")))
	must(t, os.Symlink("elsewhere", filepath.Join(target, "typed")))
	selection, _ := engine.NewSelection(engine.SelectionSpec{Release: newRef, ProfileID: "default", Target: key.Target()})
	plan, err := planner.Plan(context.Background(), engine.PlanRequest{RepositoryRoot: root, Selection: selection})
	if err != nil {
		t.Fatal(err)
	}
	conflicts := plan.Conflicts()
	if len(conflicts) != 2 || conflicts[0].Path() != "binary" || conflicts[0].Reason() != engine.ConflictBinary ||
		conflicts[1].Path() != "typed" || conflicts[1].Reason() != engine.ConflictType || driver.calls != 0 {
		t.Fatalf("conflicts=%#v merge calls=%d", conflicts, driver.calls)
	}
	changes := plan.Changes()
	if len(changes) != 3 || changes[0].Path() != "mode" || changes[0].Action() != engine.ChangeConverged ||
		changes[1].Path() != "new-name" || changes[1].Action() != engine.ChangeAdd ||
		changes[2].Path() != "old-name" || changes[2].Action() != engine.ChangeDelete {
		t.Fatalf("rename/mode changes = %#v", changes)
	}
}

func TestMaterializeNoopCheckAndPlanProjection(t *testing.T) {
	provider, ref := testProvider(t, "sample", "v0.1.0", map[string]testFile{"file.txt": {content: "secret-source-bytes", mode: sourceplugin.Mode0644}})
	resolver, err := release.NewExactResolver(nil, provider)
	if err != nil {
		t.Fatal(err)
	}
	planner := newTestEngine(t, resolver, &fixedMergeDriver{})
	selection, err := engine.NewSelection(engine.SelectionSpec{Release: ref, ProfileID: "default", Target: "services/sample"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plan, err := planner.Plan(context.Background(), engine.PlanRequest{RepositoryRoot: root, Selection: selection})
	if err != nil || plan.Operation() != engine.PlanMaterialize || !plan.CanApply() {
		t.Fatalf("materialize = %#v err=%v", plan, err)
	}
	canonical, err := plan.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal(canonical, &document) != nil || strings.Contains(string(canonical), root) || strings.Contains(string(canonical), "secret-source-bytes") {
		t.Fatalf("unsafe plan projection: %s", canonical)
	}
	key, _ := lock.NewKey("sample", "services/sample")
	installManaged(t, root, resolver, ref, "default", key)
	noop, err := planner.Plan(context.Background(), engine.PlanRequest{RepositoryRoot: root, Selection: selection})
	if err != nil || noop.Operation() != engine.PlanNoop || len(noop.Changes()) != 0 {
		t.Fatalf("noop = %#v err=%v", noop, err)
	}
	check, err := planner.Check(context.Background(), engine.PlanRequest{RepositoryRoot: root, Selection: selection})
	if err != nil || check.Status().State() != engine.ManagedStateClean || check.Plan().Operation() != engine.PlanNoop || !check.CanApply() {
		t.Fatalf("check = %#v err=%v", check, err)
	}
}

func TestNoopPlanStillRejectsCrossLockTargetOverlap(t *testing.T) {
	tests := []struct {
		name        string
		otherTarget string
	}{
		{name: "same target", otherTarget: "services/sample"},
		{name: "prefix target", otherTarget: "services"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestedProvider, requestedRef := testProvider(t, "sample", "v0.1.0", map[string]testFile{
				"sample.txt": {content: "sample\n", mode: sourceplugin.Mode0644},
			})
			otherProvider, otherRef := testProvider(t, "other", "v0.1.0", map[string]testFile{
				"other.txt": {content: "other\n", mode: sourceplugin.Mode0644},
			})
			resolver, err := release.NewExactResolver(nil, requestedProvider, otherProvider)
			if err != nil {
				t.Fatal(err)
			}
			planner := newTestEngine(t, resolver, &fixedMergeDriver{})
			root := t.TempDir()
			otherKey, err := lock.NewKey("other", test.otherTarget)
			if err != nil {
				t.Fatal(err)
			}
			installManaged(t, root, resolver, otherRef, "default", otherKey)
			requestedKey, err := lock.NewKey("sample", "services/sample")
			if err != nil {
				t.Fatal(err)
			}
			installManaged(t, root, resolver, requestedRef, "default", requestedKey)
			selection, err := engine.NewSelection(engine.SelectionSpec{Release: requestedRef, ProfileID: "default", Target: requestedKey.Target()})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := planner.Plan(context.Background(), engine.PlanRequest{RepositoryRoot: root, Selection: selection})
			if err != nil {
				t.Fatal(err)
			}
			conflicts := plan.Conflicts()
			if plan.Operation() != engine.PlanNoop || plan.CanApply() || len(conflicts) != 1 || conflicts[0].Reason() != engine.ConflictTargetOverlap {
				t.Fatalf("noop overlap plan = %#v conflicts=%#v", plan, conflicts)
			}
		})
	}
}

func file(content string) *testFile { return &testFile{content: content, mode: sourceplugin.Mode0644} }

func modeBits(mode sourceplugin.FileMode) os.FileMode {
	if mode == sourceplugin.Mode0755 {
		return 0o755
	}
	return 0o644
}

type fixedMergeDriver struct {
	result []byte
	clean  bool
	err    error
	calls  int
}

func (d *fixedMergeDriver) Merge(_ context.Context, _ engine.TextMergeInput) (engine.TextMergeResult, error) {
	d.calls++
	return engine.NewTextMergeResult(d.result, d.clean), d.err
}

func newTestEngine(t *testing.T, resolver *release.ExactResolver, driver engine.MergeDriver) *engine.Engine {
	t.Helper()
	result, err := engine.New(engine.Options{Resolver: resolver, TreeLimits: sourceplugin.DefaultTreeLimits(), LockLimits: lock.DefaultLimits(), MergeDriver: driver})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
