package crudproto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/directwrite"
)

func TestFragmentAdapterBuildsCompleteMutationSetAndWritesProjection(t *testing.T) {
	root := fragmentTestRoot(t)
	makeFragmentDirectory(t, root)
	writeFragmentFile(t, root, "ent.stale-record.generated.proto", markedFragment("stale"))
	projection := mustFragmentProjection(t, true, nil)
	spec := fragmentMutationSpec(root)

	mutations, _, err := buildFragmentExecution(context.Background(), spec, projection, fragmentExecutionHooks{})
	if err != nil {
		t.Fatal(err)
	}
	wantWrites := []string{"api/accounts.crud-protocol.lock.json", "api/ent.account.generated.proto"}
	gotWrites := make([]string, len(mutations.Writes))
	for index, write := range mutations.Writes {
		gotWrites[index] = write.Path
	}
	if !reflect.DeepEqual(gotWrites, wantWrites) || !reflect.DeepEqual(mutations.Deletes, []string{"api/ent.stale-record.generated.proto"}) || len(mutations.Scopes) != 1 || mutations.Scopes[0] != spec.OutputScopes[0] {
		t.Fatalf("mutations = %#v", mutations)
	}

	report, err := WriteFragmentProjection(context.Background(), spec, projection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.CompletedWrites, wantWrites) || !reflect.DeepEqual(report.CompletedDeletes, []string{"api/ent.stale-record.generated.proto"}) {
		t.Fatalf("report = %#v", report)
	}
	content := readFragmentFile(t, root, "ent.account.generated.proto")
	line, _, _ := bytes.Cut(content, []byte("\n"))
	if string(line) != generatedFragmentMarker {
		t.Fatalf("marker = %q", line)
	}
	fragments := projection.Fragments()
	if !projection.Valid() || len(fragments) != 1 || !fragments[0].Valid() || fragments[0].SchemaID() == "" || fragments[0].SchemaKey() != "account" || !bytes.Equal(content, fragments[0].ProtoBytes()) || len(projection.EntitySnapshot()) == 0 || len(projection.CRUDSnapshot()) == 0 {
		t.Fatal("sealed public projection accessors lost formal fragment state")
	}
}

func TestFragmentSelectorDeletesOnlyMarkedGrammarMatches(t *testing.T) {
	root := fragmentTestRoot(t)
	makeFragmentDirectory(t, root)
	writeFragmentFile(t, root, "ent.stale-record.generated.proto", markedFragment("stale"))
	writeFragmentFile(t, root, "ent.manual-record.generated.proto", []byte("syntax = \"proto3\";\n"))
	writeFragmentFile(t, root, "ent.bad_key.generated.proto", markedFragment("bad grammar"))
	writeFragmentFile(t, root, "neighbor.proto", markedFragment("neighbor"))

	projection := mustFragmentProjection(t, false, nil)
	mutations, _, err := buildFragmentExecution(context.Background(), fragmentMutationSpec(root), projection, fragmentExecutionHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mutations.Writes) != 0 || !reflect.DeepEqual(mutations.Deletes, []string{"api/ent.stale-record.generated.proto"}) {
		t.Fatalf("selector mutations = %#v", mutations)
	}
	if _, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), projection); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "api", "ent.stale-record.generated.proto")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale fragment remains: %v", err)
	}
	for _, name := range []string{"ent.manual-record.generated.proto", "ent.bad_key.generated.proto", "neighbor.proto"} {
		if _, err := os.Stat(filepath.Join(root, "api", name)); err != nil {
			t.Fatalf("manual neighbor %s changed: %v", name, err)
		}
	}
}

func TestFragmentAdapterLockBranches(t *testing.T) {
	t.Run("no CRUD and no existing lock", func(t *testing.T) {
		root := fragmentTestRoot(t)
		projection := mustFragmentProjection(t, false, nil)
		mutations, _, err := buildFragmentExecution(context.Background(), fragmentMutationSpec(root), projection, fragmentExecutionHooks{})
		if err != nil || len(mutations.Writes) != 0 {
			t.Fatalf("mutations = %#v, %v", mutations, err)
		}
		report, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), projection)
		if err != nil || len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
			t.Fatalf("report = %#v, %v", report, err)
		}
	})

	t.Run("no CRUD preserves existing lock bytes", func(t *testing.T) {
		root := fragmentTestRoot(t)
		makeFragmentDirectory(t, root)
		initial := mustFragmentProjection(t, true, nil)
		lock := initial.LockProposal().After()
		original := append([]byte(nil), lock.state.CanonicalJSON()...)
		lockPath := filepath.Join(root, "api", "accounts.crud-protocol.lock.json")
		if err := os.WriteFile(lockPath, original, 0o644); err != nil {
			t.Fatal(err)
		}
		projection := mustFragmentProjection(t, false, &lock)
		mutations, _, err := buildFragmentExecution(context.Background(), fragmentMutationSpec(root), projection, fragmentExecutionHooks{})
		if err != nil || len(mutations.Writes) != 0 {
			t.Fatalf("lock scheduled for rewrite: %#v, %v", mutations.Writes, err)
		}
		if _, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), projection); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(lockPath)
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("lock changed: %v", err)
		}
	})

	t.Run("CRUD writes canonical global lock in same set", func(t *testing.T) {
		root := fragmentTestRoot(t)
		projection := mustFragmentProjection(t, true, nil)
		mutations, _, err := buildFragmentExecution(context.Background(), fragmentMutationSpec(root), projection, fragmentExecutionHooks{})
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		for _, write := range mutations.Writes {
			if write.Path == "api/accounts.crud-protocol.lock.json" {
				got = write.Content
			}
		}
		if len(got) == 0 || !bytes.Equal(got, projection.LockProposal().After().state.CanonicalJSON()) {
			t.Fatal("canonical global lock missing from fragment MutationSet")
		}
	})
}

func TestFragmentAdapterRejectsInvalidDestinationAndAdoptionBeforeWriting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FragmentMutationSpec)
		reason string
	}{
		{name: "root level proto entry", mutate: func(spec *FragmentMutationSpec) { spec.ProtoEntry = "accounts.proto" }, reason: "crud_proto_scope_invalid"},
		{name: "scope not parent", mutate: func(spec *FragmentMutationSpec) {
			spec.OutputScopes = []directwrite.OutputScope{{Path: "generated", Mode: directwrite.OutputModeFileSet}}
		}, reason: "crud_proto_scope_invalid"},
		{name: "multiple scopes", mutate: func(spec *FragmentMutationSpec) {
			spec.OutputScopes = append(spec.OutputScopes, directwrite.OutputScope{Path: "other", Mode: directwrite.OutputModeFileSet})
		}, reason: "crud_proto_scope_invalid"},
		{name: "replace tree", mutate: func(spec *FragmentMutationSpec) { spec.OutputScopes[0].Mode = directwrite.OutputModeReplaceTree }, reason: "crud_proto_scope_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := fragmentTestRoot(t)
			marker := filepath.Join(root, "untouched")
			if err := os.WriteFile(marker, []byte("present"), 0o644); err != nil {
				t.Fatal(err)
			}
			spec := fragmentMutationSpec(root)
			test.mutate(&spec)
			report, err := WriteFragmentProjection(context.Background(), spec, mustFragmentProjection(t, true, nil))
			assertFragmentError(t, err, test.reason, root)
			if len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
				t.Fatalf("nonzero report = %#v", report)
			}
			if content, readErr := os.ReadFile(marker); readErr != nil || string(content) != "present" {
				t.Fatalf("repository changed: %q, %v", content, readErr)
			}
		})
	}

	t.Run("unmarked desired file", func(t *testing.T) {
		root := fragmentTestRoot(t)
		makeFragmentDirectory(t, root)
		desired := []byte("syntax = \"proto3\";\n")
		stale := markedFragment("stale")
		writeFragmentFile(t, root, "ent.account.generated.proto", desired)
		writeFragmentFile(t, root, "ent.stale.generated.proto", stale)
		report, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), mustFragmentProjection(t, true, nil))
		assertFragmentError(t, err, "generated_fragment_adoption_denied", root)
		if len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 || !bytes.Equal(readFragmentFile(t, root, "ent.account.generated.proto"), desired) || !bytes.Equal(readFragmentFile(t, root, "ent.stale.generated.proto"), stale) {
			t.Fatal("preflight failure changed repository")
		}
	})
}

func TestFragmentGuardRejectsPhaseChangesWithoutGeneratorWrites(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		change  func(*testing.T, string)
	}{
		{
			name:    "desired identity replacement",
			prepare: writeInitialFragments,
			change:  func(t *testing.T, root string) { replaceFragmentIdentity(t, root, "ent.account.generated.proto") },
		},
		{
			name:    "desired marker replacement",
			prepare: writeInitialFragments,
			change: func(t *testing.T, root string) {
				writeFragmentFile(t, root, "ent.account.generated.proto", []byte("manual\n"))
			},
		},
		{
			name:    "desired presence removed",
			prepare: writeInitialFragments,
			change: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "api", "ent.account.generated.proto")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stale identity replacement",
			prepare: func(t *testing.T, root string) {
				writeInitialFragments(t, root)
				writeFragmentFile(t, root, "ent.stale.generated.proto", markedFragment("stale"))
			},
			change: func(t *testing.T, root string) { replaceFragmentIdentity(t, root, "ent.stale.generated.proto") },
		},
		{
			name: "stale marker replacement",
			prepare: func(t *testing.T, root string) {
				writeInitialFragments(t, root)
				writeFragmentFile(t, root, "ent.stale.generated.proto", markedFragment("stale"))
			},
			change: func(t *testing.T, root string) {
				writeFragmentFile(t, root, "ent.stale.generated.proto", []byte("manual\n"))
			},
		},
		{
			name: "recheck candidate symlink",
			prepare: func(t *testing.T, root string) {
				writeInitialFragments(t, root)
				writeFragmentFile(t, root, "manual.txt", []byte("manual\n"))
			},
			change: func(t *testing.T, root string) {
				target := filepath.Join(root, "api", "ent.account.generated.proto")
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("manual.txt", target); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "absent desired created",
			prepare: func(*testing.T, string) {},
			change: func(t *testing.T, root string) {
				makeFragmentDirectory(t, root)
				writeFragmentFile(t, root, "ent.account.generated.proto", []byte("manual\n"))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := fragmentTestRoot(t)
			test.prepare(t, root)
			var afterExternalChange map[string]string
			report, err := writeFragmentProjection(context.Background(), fragmentMutationSpec(root), mustFragmentProjection(t, true, nil), fragmentExecutionHooks{
				afterScan: func() error {
					test.change(t, root)
					afterExternalChange = snapshotOptionalFragmentDirectory(t, root)
					return nil
				},
			})
			assertFragmentError(t, err, "fragment_guard_mismatch", root)
			if len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
				t.Fatalf("nonzero report = %#v", report)
			}
			if actual := snapshotOptionalFragmentDirectory(t, root); !reflect.DeepEqual(actual, afterExternalChange) {
				t.Fatalf("writer ran after guard mismatch\nwant=%v\ngot=%v", afterExternalChange, actual)
			}
		})
	}
}

func TestFragmentGuardRejectsStaticNonRegularAndSymlinkPaths(t *testing.T) {
	t.Run("candidate symlink", func(t *testing.T) {
		root := fragmentTestRoot(t)
		makeFragmentDirectory(t, root)
		writeFragmentFile(t, root, "manual.txt", []byte("manual\n"))
		if err := os.Symlink("manual.txt", filepath.Join(root, "api", "ent.account.generated.proto")); err != nil {
			t.Fatal(err)
		}
		report, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), mustFragmentProjection(t, true, nil))
		assertFragmentError(t, err, "fragment_changed_during_scan", root)
		if len(report.CompletedWrites) != 0 || string(readFragmentFile(t, root, "manual.txt")) != "manual\n" {
			t.Fatal("symlink target changed")
		}
	})

	t.Run("candidate directory", func(t *testing.T) {
		root := fragmentTestRoot(t)
		makeFragmentDirectory(t, root)
		if err := os.Mkdir(filepath.Join(root, "api", "ent.account.generated.proto"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), mustFragmentProjection(t, true, nil))
		assertFragmentError(t, err, "fragment_type_invalid", root)
	})

	t.Run("scope symlink", func(t *testing.T) {
		root := fragmentTestRoot(t)
		outside := fragmentTestRoot(t)
		if err := os.Symlink(outside, filepath.Join(root, "api")); err != nil {
			t.Fatal(err)
		}
		_, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), mustFragmentProjection(t, true, nil))
		assertFragmentError(t, err, "crud_proto_scope_invalid", root)
		entries, readErr := os.ReadDir(outside)
		if readErr != nil || len(entries) != 0 {
			t.Fatalf("scope symlink target changed: %v %#v", readErr, entries)
		}
	})
}

func TestFragmentGuardHonorsCancellationDuringEveryReadPhase(t *testing.T) {
	t.Run("before scan", func(t *testing.T) {
		root := fragmentTestRoot(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		report, err := WriteFragmentProjection(ctx, fragmentMutationSpec(root), mustFragmentProjection(t, true, nil))
		assertCanceledWithoutWrites(t, root, report, err)
	})

	t.Run("directory scan", func(t *testing.T) {
		root := fragmentTestRoot(t)
		makeFragmentDirectory(t, root)
		writeFragmentFile(t, root, "cancel.txt", nil)
		ctx, cancel := context.WithCancel(context.Background())
		report, err := writeFragmentProjection(ctx, fragmentMutationSpec(root), mustFragmentProjection(t, false, nil), fragmentExecutionHooks{onScanEntry: func(string) { cancel() }})
		assertCanceledWithoutWrites(t, root, report, err)
	})

	t.Run("marker read", func(t *testing.T) {
		root := fragmentTestRoot(t)
		makeFragmentDirectory(t, root)
		writeFragmentFile(t, root, "ent.stale.generated.proto", markedFragment("body"))
		ctx, cancel := context.WithCancel(context.Background())
		report, err := writeFragmentProjection(ctx, fragmentMutationSpec(root), mustFragmentProjection(t, false, nil), fragmentExecutionHooks{beforeMarkerRead: func(string) { cancel() }})
		assertCanceledWithoutWrites(t, root, report, err)
	})

	t.Run("final recheck", func(t *testing.T) {
		root := fragmentTestRoot(t)
		writeInitialFragments(t, root)
		before := snapshotFragmentDirectory(t, root)
		ctx, cancel := context.WithCancel(context.Background())
		report, err := writeFragmentProjection(ctx, fragmentMutationSpec(root), mustFragmentProjection(t, true, nil), fragmentExecutionHooks{onGuardRecheck: func(string) { cancel() }})
		assertCanceledWithoutWrites(t, root, report, err)
		if got := snapshotFragmentDirectory(t, root); !reflect.DeepEqual(got, before) {
			t.Fatal("final recheck cancellation changed repository")
		}
	})
}

func TestFragmentMarkerReadIsExactBoundedAndCancelable(t *testing.T) {
	wantWindow := len(generatedFragmentMarker) + 1
	reader := &trackingReader{remaining: 1 << 20, fill: 'x'}
	marked, err := readExactFragmentMarker(context.Background(), reader, "api/ent.oversized.generated.proto")
	if err != nil || marked || reader.maxRequest != wantWindow || reader.totalRead != wantWindow {
		t.Fatalf("bounded marker = marked:%v err:%v max:%d total:%d", marked, err, reader.maxRequest, reader.totalRead)
	}
	for _, test := range []struct {
		name    string
		content string
		marked  bool
	}{
		{name: "exact line", content: generatedFragmentMarker + "\nbody", marked: true},
		{name: "missing newline", content: generatedFragmentMarker, marked: false},
		{name: "CRLF", content: generatedFragmentMarker + "\r\n", marked: false},
		{name: "prefix", content: generatedFragmentMarker[:len(generatedFragmentMarker)-1] + "\n", marked: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := readExactFragmentMarker(context.Background(), strings.NewReader(test.content), "api/test.proto")
			if err != nil || got != test.marked {
				t.Fatalf("marked = %v, %v, want %v", got, err, test.marked)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = readExactFragmentMarker(ctx, &cancelingReader{cancel: cancel}, "api/ent.cancel.generated.proto")
	assertFragmentError(t, err, "context_canceled", "")
}

func TestFragmentDirectoryEntryLimitCountsEveryEntryWithoutTruncation(t *testing.T) {
	for _, count := range []int{maxFragmentDirectoryEntries, maxFragmentDirectoryEntries + 1} {
		t.Run(fmt.Sprintf("entries-%d", count), func(t *testing.T) {
			root := fragmentTestRoot(t)
			makeFragmentDirectory(t, root)
			for index := 0; index < count; index++ {
				writeFragmentFile(t, root, fmt.Sprintf("manual-%04d.txt", index), nil)
			}
			report, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), mustFragmentProjection(t, false, nil))
			if count == maxFragmentDirectoryEntries {
				if err != nil || len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
					t.Fatalf("4096 entries = %#v, %v", report, err)
				}
				return
			}
			assertFragmentError(t, err, "fragment_directory_entry_limit_exceeded", root)
			if len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
				t.Fatalf("4097 entries returned nonzero report: %#v", report)
			}
			if _, statErr := os.Stat(filepath.Join(root, "api", "accounts.crud-protocol.lock.json")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("entry-limit failure wrote lock: %v", statErr)
			}
		})
	}
}

func TestFragmentWriteOverwritesGuardedDesiredAndConverges(t *testing.T) {
	root := fragmentTestRoot(t)
	writeInitialFragments(t, root)
	writeFragmentFile(t, root, "ent.account.generated.proto", markedFragment("old generated bytes"))
	writeFragmentFile(t, root, "ent.stale.generated.proto", markedFragment("stale"))
	writeFragmentFile(t, root, "manual.proto", []byte("manual\n"))
	projection := mustFragmentProjection(t, true, nil)
	if _, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), projection); err != nil {
		t.Fatal(err)
	}
	first := snapshotFragmentDirectory(t, root)
	if _, stale := first["ent.stale.generated.proto"]; stale || first["manual.proto"] != "manual\n" || first["ent.account.generated.proto"] != string(projection.Fragments()[0].ProtoBytes()) {
		t.Fatalf("first run tree = %#v", first)
	}
	if _, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), projection); err != nil {
		t.Fatal(err)
	}
	second := snapshotFragmentDirectory(t, root)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second run changed tree\nfirst=%v\nsecond=%v", first, second)
	}
}

func mustFragmentProjection(t *testing.T, withCRUD bool, existing *Lock) FragmentProjection {
	t.Helper()
	projection, err := BuildFragmentProjection(projectionEntityDocument(t, withCRUD), BuildOptions{
		ServiceID: "accounts", ProtoPackage: "accounts.v1", GoPackage: "example.com/accounts/v1;accountsv1", ExistingLock: existing,
	})
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func fragmentMutationSpec(root string) FragmentMutationSpec {
	return FragmentMutationSpec{
		RepositoryRoot: root,
		ProtoEntry:     "api/accounts.proto",
		OutputScopes:   []directwrite.OutputScope{{Path: "api", Mode: directwrite.OutputModeFileSet}},
	}
}

func fragmentTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func makeFragmentDirectory(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFragmentFile(t *testing.T, root, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "api", name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFragmentFile(t *testing.T, root, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "api", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func markedFragment(body string) []byte {
	return []byte(generatedFragmentMarker + "\n" + body + "\n")
}

func writeInitialFragments(t *testing.T, root string) {
	t.Helper()
	if _, err := WriteFragmentProjection(context.Background(), fragmentMutationSpec(root), mustFragmentProjection(t, true, nil)); err != nil {
		t.Fatal(err)
	}
}

func replaceFragmentIdentity(t *testing.T, root, name string) {
	t.Helper()
	fragmentPath := filepath.Join(root, "api", name)
	content, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fragmentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragmentPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFragmentError(t *testing.T, err error, reason, forbiddenRoot string) {
	t.Helper()
	var owner *Error
	if !errors.As(err, &owner) || owner.Code() != "crud_host_invalid" || owner.Reason() != reason {
		t.Fatalf("fragment error = %#v, want crud_host_invalid/%s", err, reason)
	}
	if forbiddenRoot != "" && (strings.Contains(owner.Source(), forbiddenRoot) || strings.Contains(err.Error(), forbiddenRoot)) {
		t.Fatalf("absolute root leaked: source=%q error=%q", owner.Source(), err.Error())
	}
}

func assertCanceledWithoutWrites(t *testing.T, root string, report directwrite.WriteReport, err error) {
	t.Helper()
	assertFragmentError(t, err, "context_canceled", root)
	if len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
		t.Fatalf("cancellation report = %#v", report)
	}
}

func snapshotFragmentDirectory(t *testing.T, root string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "api"))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(root, "api", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = string(content)
	}
	return result
}

func snapshotOptionalFragmentDirectory(t *testing.T, root string) map[string]string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "api")); errors.Is(err, os.ErrNotExist) {
		return map[string]string{}
	} else if err != nil {
		t.Fatal(err)
	}
	return snapshotFragmentDirectory(t, root)
}

type trackingReader struct {
	remaining  int
	maxRequest int
	totalRead  int
	fill       byte
}

func (r *trackingReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.maxRequest {
		r.maxRequest = len(buffer)
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(buffer)
	if n > r.remaining {
		n = r.remaining
	}
	for index := 0; index < n; index++ {
		buffer[index] = r.fill
	}
	r.remaining -= n
	r.totalRead += n
	return n, nil
}

type cancelingReader struct {
	cancel context.CancelFunc
	read   bool
}

func (r *cancelingReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	buffer[0] = '/'
	r.cancel()
	return 1, nil
}
