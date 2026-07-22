package sourceplugin

import (
	"bytes"
	"embed"
	"errors"
	"io"
	"io/fs"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nxnminieye/nexa/provenance"
)

//go:embed testdata/embedded-tree
var embeddedTreeFixture embed.FS

func TestEmbeddedTreeLoadsExactInventoryFromNestedPrefixes(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{
		{Path: "a.txt", Size: 6, Digest: provenance.SHA256([]byte("alpha\n")), Mode: Mode0644},
		{Path: "bin/run.sh", Size: 17, Digest: provenance.SHA256([]byte("#!/bin/sh\nexit 0\n")), Mode: Mode0755},
	})
	root, err := LoadEmbeddedTree(manifest, embeddedTreeFixture, "testdata/embedded-tree/root", DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	copyTree, err := LoadEmbeddedTree(manifest, embeddedTreeFixture, "testdata/embedded-tree/copy", DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	rootedFS, err := fs.Sub(embeddedTreeFixture, "testdata/embedded-tree/root")
	if err != nil {
		t.Fatal(err)
	}
	dotTree, err := loadEmbeddedTreeFS(manifest, rootedFS, ".", DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	reversedTree, err := loadEmbeddedTreeFS(manifest, reverseReadDirFS{FS: rootedFS}, ".", DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if root.Digest() != copyTree.Digest() || root.Digest() != dotTree.Digest() || root.Digest() != reversedTree.Digest() || !reflect.DeepEqual(root.Files()[0].Bytes(), []byte("alpha\n")) || root.Files()[1].Mode() != Mode0755 {
		t.Fatalf("prefix changed relative tree: root=%s copy=%s files=%#v", root.Digest().String(), copyTree.Digest().String(), root.Files())
	}
}

func TestEmbeddedTreeSupportsValidEmptyManifestAndEmbedFS(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{})
	tree, err := LoadEmbeddedTree(manifest, embed.FS{}, ".", DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if tree.Len() != 0 || tree.Digest().String() != "sha256:dfd7ff88a3c48e8f33ff5a830b2e63409855dfc3995b7083a66993d15215e029" {
		t.Fatalf("empty embedded tree = len=%d digest=%s", tree.Len(), tree.Digest().String())
	}
}

func TestEmbeddedTreePreflightAndPrefixErrors(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{})
	tests := []struct {
		name    string
		fsys    fs.FS
		prefix  string
		reason  string
		pointer string
	}{
		{name: "invalid", fsys: fstest.MapFS{}, prefix: "../escape", reason: "embedded_prefix_invalid", pointer: "/prefix"},
		{name: "unavailable", fsys: fstest.MapFS{}, prefix: "missing", reason: "embedded_prefix_unavailable", pointer: "/prefix"},
		{name: "file", fsys: fstest.MapFS{"file": &fstest.MapFile{Data: []byte("x")}}, prefix: "file", reason: "embedded_prefix_not_directory", pointer: "/prefix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadEmbeddedTreeFS(manifest, tt.fsys, tt.prefix, DefaultTreeLimits())
			assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", tt.reason, tt.pointer)
		})
	}

	recording := &recordingFS{delegate: fstest.MapFS{}}
	oversized := newTreeManifest(t, []FileSpec{{Path: "a", Size: 2, Digest: provenance.SHA256([]byte("aa")), Mode: Mode0644}})
	_, err := loadEmbeddedTreeFS(oversized, recording, ".", TreeLimits{MaxFiles: 2, MaxFileBytes: 1, MaxTotalBytes: 10})
	assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_bytes_exceeded", "/files/0/content")
	if len(recording.opens) != 0 {
		t.Fatalf("filesystem opened before Manifest preflight: %#v", recording.opens)
	}
}

func TestEmbeddedTreeDeclaredPerFileLimitPrecedesDeclaredTotalWithoutFilesystemAccess(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{
		{Path: "a", Size: 2, Digest: provenance.SHA256([]byte("aa")), Mode: Mode0644},
		{Path: "b", Size: 3, Digest: provenance.SHA256([]byte("bbb")), Mode: Mode0644},
	})
	recording := &recordingFS{delegate: fstest.MapFS{}}
	_, err := loadEmbeddedTreeFS(manifest, recording, ".", TreeLimits{MaxFiles: 2, MaxFileBytes: 2, MaxTotalBytes: 1})
	assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_bytes_exceeded", "/files/1/content")
	if len(recording.opens) != 0 {
		t.Fatalf("filesystem opened before Manifest declared-limit preflight: %#v", recording.opens)
	}
}

func TestEmbeddedTreeAllocationRetainsOneBoundedPayload(t *testing.T) {
	const payloadBytes = 1 << 20
	content := bytes.Repeat([]byte("x"), payloadBytes)
	manifest := newTreeManifest(t, []FileSpec{{Path: "payload", Size: payloadBytes, Digest: provenance.SHA256(content), Mode: Mode0644}})
	filesystem := fstest.MapFS{"payload": &fstest.MapFile{Data: content}}
	limits := TreeLimits{MaxFiles: 1, MaxFileBytes: payloadBytes, MaxTotalBytes: payloadBytes}

	allocated := benchmarkTreeAllocatedBytes(t, func() (Tree, error) {
		return loadEmbeddedTreeFS(manifest, filesystem, ".", limits)
	}, false)
	t.Logf("embedded bytes/op: allocated=%d payload=%d", allocated, payloadBytes)
	const allocationCeiling = payloadBytes + payloadBytes/2 + 256<<10
	if allocated > allocationCeiling {
		t.Fatalf("embedded tree allocated multiple payload copies: allocated=%d ceiling=%d payload=%d", allocated, allocationCeiling, payloadBytes)
	}
}

func TestEmbeddedTreeInventoryErrorsAreCanonicalAndDoNotOpenExtras(t *testing.T) {
	t.Run("missing before extra", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "b", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644}})
		_, err := loadEmbeddedTreeFS(manifest, fstest.MapFS{"a": &fstest.MapFile{Data: []byte("a")}}, ".", DefaultTreeLimits())
		assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_missing", "/files/1/path")
	})

	t.Run("extra is not opened", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
		recording := &recordingFS{delegate: fstest.MapFS{
			"a":     &fstest.MapFile{Data: []byte("a")},
			"extra": &fstest.MapFile{Data: []byte("credential-secret")},
		}}
		_, err := loadEmbeddedTreeFS(manifest, recording, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_extra", "/files/1/path")
		if containsString(recording.opens, "extra") || strings.Contains(projected.Error(), "credential-secret") {
			t.Fatalf("extra file was opened or leaked: opens=%#v error=%q", recording.opens, projected.Error())
		}
	})

	t.Run("non regular precedes relation errors", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "z", Size: 1, Digest: provenance.SHA256([]byte("z")), Mode: Mode0644}})
		filesystem := fstest.MapFS{
			"link": &fstest.MapFile{Data: []byte("target"), Mode: fs.ModeSymlink},
			"a":    &fstest.MapFile{Data: []byte("a")},
		}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_non_regular", "/files/1/path")
	})
}

func TestEmbeddedTreeContentErrorsAreBoundedAndClosed(t *testing.T) {
	t.Run("open failure", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
		filesystem := &denyOpenFS{delegate: fstest.MapFS{"a": &fstest.MapFile{Data: []byte("a")}}, denied: "a", secret: "credential-secret"}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_read_failed", "/files/0/content")
		assertNoHostileEmbeddedDiagnostic(t, projected, filesystem.secret)
	})

	t.Run("read failure", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
		filesystem := &readFailureFS{delegate: fstest.MapFS{"a": &fstest.MapFile{Data: []byte("a")}}, failed: "a", secret: "read-token-secret"}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_read_failed", "/files/0/content")
		assertNoHostileEmbeddedDiagnostic(t, projected, filesystem.secret)
	})

	t.Run("size mismatch", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
		_, err := loadEmbeddedTreeFS(manifest, fstest.MapFS{"a": &fstest.MapFile{Data: []byte("aa")}}, ".", DefaultTreeLimits())
		assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_size_mismatch", "/files/0/content")
	})

	t.Run("digest mismatch", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
		_, err := loadEmbeddedTreeFS(manifest, fstest.MapFS{"a": &fstest.MapFile{Data: []byte("b")}}, ".", DefaultTreeLimits())
		assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_digest_mismatch", "/files/0/content")
	})

	t.Run("reader stops at declared size plus one", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
		filesystem := newEndlessFileFS("a")
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_size_mismatch", "/files/0/content")
		if filesystem.bytesRead != 2 {
			t.Fatalf("reader consumed %d bytes, want exact declared size + 1", filesystem.bytesRead)
		}
	})
}

func TestEmbeddedTreeContentValidationIsStageMajor(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{
		{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644},
		{Path: "b", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644},
	})

	t.Run("higher path size failure precedes lower path digest failure", func(t *testing.T) {
		filesystem := fstest.MapFS{
			"a": &fstest.MapFile{Data: []byte("x")},
			"b": &fstest.MapFile{Data: []byte("bb")},
		}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_size_mismatch", "/files/1/content")
	})

	t.Run("higher path read failure precedes lower path digest failure", func(t *testing.T) {
		filesystem := &readFailureFS{
			delegate: fstest.MapFS{
				"a": &fstest.MapFile{Data: []byte("x")},
				"b": &fstest.MapFile{Data: []byte("b")},
			},
			failed: "b",
			secret: "higher-path-read-secret",
		}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_file_read_failed", "/files/1/content")
		assertNoHostileEmbeddedDiagnostic(t, projected, filesystem.secret)
	})
}

func TestEmbeddedTreeTraversalIsBoundedPositiveNAndClosesFailures(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{})
	probe := &probeInventoryFS{entries: []fs.DirEntry{
		staticDirEntry{name: "b", mode: 0},
		staticDirEntry{name: "a", mode: 0},
	}, readErr: errors.New("credential-secret")}
	_, err := loadEmbeddedTreeFS(manifest, probe, ".", TreeLimits{MaxFiles: 1, MaxFileBytes: 1, MaxTotalBytes: 1})
	projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_exceeded", "/tree")
	if len(probe.readDirN) == 0 {
		t.Fatal("directory was not read")
	}
	for _, n := range probe.readDirN {
		if n <= 0 {
			t.Fatalf("ReadDir called with unbounded n=%d", n)
		}
	}
	assertNoHostileEmbeddedDiagnostic(t, projected, "credential-secret")

	t.Run("nested open error", func(t *testing.T) {
		filesystem := &denyOpenFS{delegate: fstest.MapFS{"dir/file": &fstest.MapFile{Data: []byte("x")}}, denied: "dir", secret: "token-secret"}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_read_failed", "/tree")
		assertNoHostileEmbeddedDiagnostic(t, projected, filesystem.secret)
	})

	t.Run("traversal failure precedes non regular semantics", func(t *testing.T) {
		filesystem := &denyOpenFS{delegate: fstest.MapFS{
			"dir/file": &fstest.MapFile{Data: []byte("x")},
			"link":     &fstest.MapFile{Data: []byte("target"), Mode: fs.ModeSymlink},
		}, denied: "dir", secret: "nested-token-secret"}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_read_failed", "/tree")
		assertNoHostileEmbeddedDiagnostic(t, projected, filesystem.secret)
	})

	t.Run("nested read directory error", func(t *testing.T) {
		filesystem := nestedReadFailureFS{delegate: fstest.MapFS{"dir/file": &fstest.MapFile{Data: []byte("x")}}, failed: "dir", secret: "directory-token-secret"}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_read_failed", "/tree")
		assertNoHostileEmbeddedDiagnostic(t, projected, filesystem.secret)
	})

	t.Run("entry info error", func(t *testing.T) {
		filesystem := &probeInventoryFS{entries: []fs.DirEntry{failingInfoEntry{name: "secret-name", err: errors.New("token-secret")}}}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", DefaultTreeLimits())
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_read_failed", "/tree")
		assertNoHostileEmbeddedDiagnostic(t, projected, "secret-name")
		assertNoHostileEmbeddedDiagnostic(t, projected, "token-secret")
	})

	t.Run("directory observation bound", func(t *testing.T) {
		filesystem := fstest.MapFS{
			"a/file": &fstest.MapFile{Data: []byte("a")},
			"b/file": &fstest.MapFile{Data: []byte("b")},
			"c/file": &fstest.MapFile{Data: []byte("c")},
		}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", TreeLimits{MaxFiles: 1, MaxFileBytes: 1, MaxTotalBytes: 1})
		assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_exceeded", "/tree")
	})
}

func TestEmbeddedTreeBatchBoundPrecedesInfoAndRawErrorInEveryOrder(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{})
	for _, reverse := range []bool{false, true} {
		t.Run("reverse="+strconv.FormatBool(reverse), func(t *testing.T) {
			infoCalls := 0
			entries := []fs.DirEntry{
				failingInfoEntry{name: "first-secret-name", err: errors.New("info-token-secret"), calls: &infoCalls},
				staticDirEntry{name: "bound"},
			}
			if reverse {
				entries[0], entries[1] = entries[1], entries[0]
			}
			filesystem := &probeInventoryFS{entries: entries, readErr: errors.New("read-dir-token-secret")}
			_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", TreeLimits{MaxFiles: 1, MaxFileBytes: 1, MaxTotalBytes: 1})
			projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_exceeded", "/tree")
			if infoCalls != 0 {
				t.Fatalf("Info called %d times before full-batch bound decision", infoCalls)
			}
			if len(filesystem.readDirN) == 0 {
				t.Fatal("directory was not read")
			}
			for _, n := range filesystem.readDirN {
				if n <= 0 {
					t.Fatalf("ReadDir called with unbounded n=%d", n)
				}
			}
			for _, hostile := range []string{"first-secret-name", "info-token-secret", "read-dir-token-secret"} {
				assertNoHostileEmbeddedDiagnostic(t, projected, hostile)
			}
		})
	}

	t.Run("raw error precedes Info when batch stays within bounds", func(t *testing.T) {
		infoCalls := 0
		filesystem := &probeInventoryFS{
			entries: []fs.DirEntry{failingInfoEntry{name: "info-secret-name", err: errors.New("info-token-secret"), calls: &infoCalls}},
			readErr: errors.New("read-dir-token-secret"),
		}
		_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", TreeLimits{MaxFiles: 2, MaxFileBytes: 1, MaxTotalBytes: 1})
		projected := assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_read_failed", "/tree")
		if infoCalls != 0 {
			t.Fatalf("Info called %d times before raw batch error projection", infoCalls)
		}
		for _, hostile := range []string{"info-secret-name", "info-token-secret", "read-dir-token-secret"} {
			assertNoHostileEmbeddedDiagnostic(t, projected, hostile)
		}
	})

	for _, reverse := range []bool{false, true} {
		t.Run("canonical-info/reverse="+strconv.FormatBool(reverse), func(t *testing.T) {
			firstCalls, secondCalls := 0, 0
			entries := []fs.DirEntry{
				failingInfoEntry{name: "a", err: errors.New("a-info-secret"), calls: &firstCalls},
				failingInfoEntry{name: "b", err: errors.New("b-info-secret"), calls: &secondCalls},
			}
			if reverse {
				entries[0], entries[1] = entries[1], entries[0]
			}
			filesystem := &probeInventoryFS{entries: entries}
			_, err := loadEmbeddedTreeFS(manifest, filesystem, ".", TreeLimits{MaxFiles: 2, MaxFileBytes: 1, MaxTotalBytes: 1})
			assertTask2Error(t, err, ErrTreeLoadFailed, "source_tree_load_failed", "embedded_inventory_read_failed", "/tree")
			if firstCalls != 1 || secondCalls != 0 {
				t.Fatalf("canonical Info calls = a:%d b:%d", firstCalls, secondCalls)
			}
		})
	}
}

func assertNoHostileEmbeddedDiagnostic(t *testing.T, projected *Error, hostile string) {
	t.Helper()
	for _, value := range []string{projected.Error(), projected.Code(), projected.Reason(), projected.Pointer(), projected.Source(), projected.Class().Error()} {
		if strings.Contains(value, hostile) {
			t.Fatalf("public error leaked %q through %q", hostile, value)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type recordingFS struct {
	delegate fs.FS
	opens    []string
}

func (f *recordingFS) Open(name string) (fs.File, error) {
	f.opens = append(f.opens, name)
	return f.delegate.Open(name)
}

type denyOpenFS struct {
	delegate fs.FS
	denied   string
	secret   string
}

type reverseReadDirFS struct{ fs.FS }

func (f reverseReadDirFS) Open(name string) (fs.File, error) {
	opened, err := f.FS.Open(name)
	if err != nil {
		return nil, err
	}
	if directory, ok := opened.(fs.ReadDirFile); ok {
		return &reverseReadDirFile{File: opened, directory: directory}, nil
	}
	return opened, nil
}

type reverseReadDirFile struct {
	fs.File
	directory fs.ReadDirFile
}

func (f *reverseReadDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := f.directory.ReadDir(n)
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return entries, err
}

type readFailureFS struct {
	delegate fs.FS
	failed   string
	secret   string
}

func (f *readFailureFS) Open(name string) (fs.File, error) {
	opened, err := f.delegate.Open(name)
	if err != nil || name != f.failed {
		return opened, err
	}
	return &readFailureFile{File: opened, secret: f.secret}, nil
}

type readFailureFile struct {
	fs.File
	secret string
}

func (f *readFailureFile) Read([]byte) (int, error) { return 0, errors.New(f.secret) }

type nestedReadFailureFS struct {
	delegate fs.FS
	failed   string
	secret   string
}

func (f nestedReadFailureFS) Open(name string) (fs.File, error) {
	opened, err := f.delegate.Open(name)
	if err != nil || name != f.failed {
		return opened, err
	}
	directory, ok := opened.(fs.ReadDirFile)
	if !ok {
		return opened, nil
	}
	return &failedReadDirFile{File: opened, directory: directory, secret: f.secret}, nil
}

type failedReadDirFile struct {
	fs.File
	directory fs.ReadDirFile
	secret    string
}

func (f *failedReadDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, _ := f.directory.ReadDir(n)
	return entries, errors.New(f.secret)
}

func (f *denyOpenFS) Open(name string) (fs.File, error) {
	if name == f.denied {
		return nil, errors.New(f.secret)
	}
	return f.delegate.Open(name)
}

type probeInventoryFS struct {
	entries  []fs.DirEntry
	readErr  error
	readDirN []int
	done     bool
}

func (f *probeInventoryFS) Open(name string) (fs.File, error) {
	if name != "." {
		return nil, fs.ErrNotExist
	}
	return &probeDirectory{owner: f}, nil
}

type probeDirectory struct{ owner *probeInventoryFS }

func (f *probeDirectory) Stat() (fs.FileInfo, error) {
	return staticFileInfo{name: ".", mode: fs.ModeDir}, nil
}
func (f *probeDirectory) Read([]byte) (int, error) { return 0, errors.New("is a directory") }
func (f *probeDirectory) Close() error             { return nil }
func (f *probeDirectory) ReadDir(n int) ([]fs.DirEntry, error) {
	f.owner.readDirN = append(f.owner.readDirN, n)
	if f.owner.done {
		return nil, io.EOF
	}
	f.owner.done = true
	return append([]fs.DirEntry(nil), f.owner.entries...), f.owner.readErr
}

type staticDirEntry struct {
	name string
	mode fs.FileMode
}

func (e staticDirEntry) Name() string      { return e.name }
func (e staticDirEntry) IsDir() bool       { return e.mode.IsDir() }
func (e staticDirEntry) Type() fs.FileMode { return e.mode.Type() }
func (e staticDirEntry) Info() (fs.FileInfo, error) {
	return staticFileInfo{name: e.name, mode: e.mode}, nil
}

type failingInfoEntry struct {
	name  string
	err   error
	calls *int
}

func (e failingInfoEntry) Name() string      { return e.name }
func (e failingInfoEntry) IsDir() bool       { return false }
func (e failingInfoEntry) Type() fs.FileMode { return 0 }
func (e failingInfoEntry) Info() (fs.FileInfo, error) {
	if e.calls != nil {
		*e.calls = *e.calls + 1
	}
	return nil, e.err
}

type staticFileInfo struct {
	name string
	mode fs.FileMode
}

func (i staticFileInfo) Name() string       { return i.name }
func (i staticFileInfo) Size() int64        { return 0 }
func (i staticFileInfo) Mode() fs.FileMode  { return i.mode }
func (i staticFileInfo) ModTime() time.Time { return time.Time{} }
func (i staticFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i staticFileInfo) Sys() any           { return nil }

type endlessFileFS struct {
	path      string
	bytesRead int
}

func newEndlessFileFS(path string) *endlessFileFS { return &endlessFileFS{path: path} }

func (f *endlessFileFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &singleEntryDirectory{entry: staticDirEntry{name: f.path}}, nil
	}
	if name == f.path {
		return &endlessFile{owner: f, name: name}, nil
	}
	return nil, fs.ErrNotExist
}

type singleEntryDirectory struct {
	entry fs.DirEntry
	done  bool
}

func (f *singleEntryDirectory) Stat() (fs.FileInfo, error) {
	return staticFileInfo{name: ".", mode: fs.ModeDir}, nil
}
func (f *singleEntryDirectory) Read([]byte) (int, error) { return 0, errors.New("is a directory") }
func (f *singleEntryDirectory) Close() error             { return nil }
func (f *singleEntryDirectory) ReadDir(n int) ([]fs.DirEntry, error) {
	if f.done {
		return nil, io.EOF
	}
	f.done = true
	return []fs.DirEntry{f.entry}, nil
}

type endlessFile struct {
	owner *endlessFileFS
	name  string
}

func (f *endlessFile) Stat() (fs.FileInfo, error) { return staticFileInfo{name: f.name}, nil }
func (f *endlessFile) Close() error               { return nil }
func (f *endlessFile) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 'x'
	}
	f.owner.bytesRead += len(buffer)
	return len(buffer), nil
}
