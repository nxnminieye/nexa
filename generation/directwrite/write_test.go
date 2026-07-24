package directwrite

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteAppliesModesWithoutTouchingManualOrHardlinkAliases(t *testing.T) {
	root := canonicalTempDir(t)
	mustWrite(t, root, "mixed/generated.go", "old")
	mustWrite(t, root, "mixed/stale.go", "stale")
	mustWrite(t, root, "manual/keep.go", "manual")
	if err := os.Link(filepath.Join(root, "mixed/generated.go"), filepath.Join(root, "manual/alias.go")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "tree/stale.go", "stale tree")
	mustWrite(t, root, "outside.txt", "outside")
	if err := os.Symlink(filepath.Join(root, "outside.txt"), filepath.Join(root, "tree/stale-link")); err != nil {
		t.Fatal(err)
	}

	report, err := Write(context.Background(), root, MutationSet{
		Scopes: []OutputScope{
			{Path: "tree", Mode: OutputModeReplaceTree},
			{Path: "mixed", Mode: OutputModeFileSet},
		},
		Writes: []OutputFile{
			{Path: "tree/new/empty.go", Content: nil},
			{Path: "mixed/generated.go", Content: []byte("new")},
		},
		Deletes: []string{"mixed/stale.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.CompletedWrites, []string{"mixed/generated.go", "tree/new/empty.go"}) || !reflect.DeepEqual(report.CompletedDeletes, []string{"mixed/stale.go"}) {
		t.Fatalf("report = %#v", report)
	}
	assertContent(t, root, "mixed/generated.go", "new")
	assertContent(t, root, "manual/alias.go", "old")
	assertContent(t, root, "manual/keep.go", "manual")
	assertContent(t, root, "outside.txt", "outside")
	assertContent(t, root, "tree/new/empty.go", "")
	assertAbsent(t, root, "mixed/stale.go")
	assertAbsent(t, root, "tree/stale.go")
	assertAbsent(t, root, "tree/stale-link")
	info, err := os.Stat(filepath.Join(root, "mixed/generated.go"))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, err = %v", info.Mode(), err)
	}
	for _, forbidden := range []string{".nexa", "nexactl-generation-stage", "nexactl-generation-work"} {
		assertAbsent(t, root, forbidden)
	}
}

func TestWriteRejectsInvalidStaticSetsBeforeMutation(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		set     MutationSet
		kind    ErrorKind
	}{
		{name: "absolute scope", set: MutationSet{Scopes: []OutputScope{{Path: "/tmp/out", Mode: OutputModeFileSet}}}, kind: ErrorInvalidScope},
		{name: "traversal", set: MutationSet{Scopes: []OutputScope{{Path: "../out", Mode: OutputModeFileSet}}}, kind: ErrorInvalidScope},
		{name: "git alias", set: MutationSet{Scopes: []OutputScope{{Path: ".GiT/generated", Mode: OutputModeFileSet}}}, kind: ErrorInvalidScope},
		{name: "overlap", set: MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}, {Path: "gen/tree", Mode: OutputModeReplaceTree}}}, kind: ErrorInvalidScope},
		{name: "casefold scope", set: MutationSet{Scopes: []OutputScope{{Path: "Gen", Mode: OutputModeFileSet}, {Path: "gen", Mode: OutputModeFileSet}}}, kind: ErrorInvalidScope},
		{name: "unicode casefold action", set: MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}}, Writes: []OutputFile{{Path: "gen/STRASSE.go"}, {Path: "gen/stra\u00dfe.go"}}}, kind: ErrorInvalidMutation},
		{name: "unicode normalization action", set: MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}}, Writes: []OutputFile{{Path: "gen/caf\u00e9.go"}, {Path: "gen/cafe\u0301.go"}}}, kind: ErrorInvalidMutation},
		{name: "action ancestor", set: MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}}, Writes: []OutputFile{{Path: "gen/a"}, {Path: "gen/a/b.go"}}}, kind: ErrorInvalidMutation},
		{name: "write delete collision", set: MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}}, Writes: []OutputFile{{Path: "gen/a.go"}}, Deletes: []string{"gen/A.go"}}, kind: ErrorInvalidMutation},
		{name: "replace explicit delete", set: MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeReplaceTree}}, Deletes: []string{"gen/a.go"}}, kind: ErrorInvalidMutation},
		{name: "scope symlink", prepare: func(t *testing.T, root string) {
			if err := os.Symlink(filepath.Join(root, "manual"), filepath.Join(root, "gen")); err != nil {
				t.Fatal(err)
			}
		}, set: MutationSet{Scopes: []OutputScope{{Path: "gen/output", Mode: OutputModeFileSet}}, Writes: []OutputFile{{Path: "gen/output/a.go"}}}, kind: ErrorPathDenied},
		{name: "manual casefold neighbor", prepare: func(t *testing.T, root string) {
			mustWrite(t, root, "gen/foo.go", "manual")
		}, set: MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}}, Writes: []OutputFile{{Path: "gen/Foo.go"}}}, kind: ErrorPathDenied},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := canonicalTempDir(t)
			mustWrite(t, root, "manual", "unchanged")
			if test.prepare != nil {
				test.prepare(t, root)
			}
			report, err := Write(context.Background(), root, test.set)
			var typed *Error
			if !errors.As(err, &typed) || typed.Kind() != test.kind {
				t.Fatalf("error = %#v, want kind %s", err, test.kind)
			}
			if len(report.CompletedWrites) != 0 || len(report.CompletedDeletes) != 0 {
				t.Fatalf("preflight report = %#v", report)
			}
			assertContent(t, root, "manual", "unchanged")
		})
	}
}

func TestWriteReturnsPartialReportAndDoesNotRollback(t *testing.T) {
	root := canonicalTempDir(t)
	files := &failWriteFileSystem{osFileSystem: osFileSystem{}, failAt: 2}
	report, err := write(context.Background(), root, MutationSet{
		Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}},
		Writes: []OutputFile{{Path: "gen/a.go", Content: []byte("a")}, {Path: "gen/b.go", Content: []byte("b")}},
	}, files)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind() != ErrorPartialWrite || !reflect.DeepEqual(typed.Report(), report) {
		t.Fatalf("error/report = %#v / %#v", err, report)
	}
	if !reflect.DeepEqual(report.CompletedWrites, []string{"gen/a.go"}) {
		t.Fatalf("report = %#v", report)
	}
	assertContent(t, root, "gen/a.go", "a")
	assertAbsent(t, root, "gen/b.go")
}

func TestWriteCancellationIsTyped(t *testing.T) {
	root := canonicalTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := Write(ctx, root, MutationSet{Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}}, Writes: []OutputFile{{Path: "gen/a.go"}}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind() != ErrorCanceled || !errors.Is(err, context.Canceled) || len(report.CompletedWrites) != 0 {
		t.Fatalf("error/report = %#v / %#v", err, report)
	}
	assertAbsent(t, root, "gen/a.go")
}

func TestWriteCancellationAfterCompletedMutationPreservesReport(t *testing.T) {
	root := canonicalTempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	files := &cancelWriteFileSystem{osFileSystem: osFileSystem{}, cancel: cancel}
	report, err := write(ctx, root, MutationSet{
		Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}},
		Writes: []OutputFile{{Path: "gen/a.go", Content: []byte("a")}, {Path: "gen/b.go", Content: []byte("b")}},
	}, files)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind() != ErrorCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %#v", err)
	}
	if !reflect.DeepEqual(report.CompletedWrites, []string{"gen/a.go"}) || !reflect.DeepEqual(typed.Report(), report) {
		t.Fatalf("report = %#v, typed = %#v", report, typed.Report())
	}
	assertContent(t, root, "gen/a.go", "a")
	assertAbsent(t, root, "gen/b.go")
}

func TestWriteNeverInvokesGitOrCreatesGenerationWorkDirectories(t *testing.T) {
	root := canonicalTempDir(t)
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "git-invoked")
	script := filepath.Join(bin, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf invoked > \"$DIRECTWRITE_GIT_MARKER\"\nexit 91\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("DIRECTWRITE_GIT_MARKER", marker)
	if _, err := Write(context.Background(), root, MutationSet{
		Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}},
		Writes: []OutputFile{{Path: "gen/a.go", Content: []byte("a")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Git was invoked: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "nexactl-generation-") {
			t.Fatalf("generation work directory created: %s", entry.Name())
		}
	}
}

type failWriteFileSystem struct {
	osFileSystem
	failAt int
	calls  int
}

func (f *failWriteFileSystem) WriteExclusive(name string, content []byte, mode fs.FileMode) error {
	f.calls++
	if f.calls == f.failAt {
		return errors.New("injected write failure")
	}
	return f.osFileSystem.WriteExclusive(name, content, mode)
}

type cancelWriteFileSystem struct {
	osFileSystem
	cancel context.CancelFunc
	calls  int
}

func (f *cancelWriteFileSystem) WriteExclusive(name string, content []byte, mode fs.FileMode) error {
	f.calls++
	err := f.osFileSystem.WriteExclusive(name, content, mode)
	if f.calls == 1 {
		f.cancel()
	}
	return err
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
}

func mustWrite(t *testing.T, root, relative, content string) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, root, relative, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v; want %q", relative, data, err, want)
	}
}

func assertAbsent(t *testing.T, root, relative string) {
	t.Helper()
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists or stat failed: %v", relative, err)
	}
}

func TestCleanRelativePathRejectsPlatformAndNonCanonicalForms(t *testing.T) {
	for _, value := range []string{"", ".", "a//b", "a/./b", "a/../b", `a\\b`, "a/.GIT/b", "a/nu\x00ll"} {
		if _, err := cleanRelativePath(value); err == nil {
			t.Fatalf("cleanRelativePath(%q) succeeded", value)
		}
	}
}
