package sourceplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
)

func TestTreeManifestAnchoringDigestAndImmutability(t *testing.T) {
	inputs := []TreeInput{
		{Path: "bin/run", Content: []byte("#!/bin/sh\n")},
		{Path: "a.txt", Content: []byte("alpha")},
	}
	manifest := manifestForTreeInputs(t, inputs, map[string]FileMode{"bin/run": Mode0755})
	tree, err := NewTree(manifest, inputs, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := NewTree(manifest, []TreeInput{inputs[1], inputs[0]}, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if tree.Len() != 2 || reversed.Len() != 2 || tree.Digest() != reversed.Digest() {
		t.Fatalf("tree values changed with caller order: first=%s reversed=%s", tree.Digest().String(), reversed.Digest().String())
	}

	files := tree.Files()
	if got := []string{files[0].Path(), files[1].Path()}; !reflect.DeepEqual(got, []string{"a.txt", "bin/run"}) {
		t.Fatalf("file order = %#v", got)
	}
	if files[0].Mode() != Mode0644 || files[0].Size() != 5 || files[0].Digest() != provenance.SHA256([]byte("alpha")) || !bytes.Equal(files[0].Bytes(), []byte("alpha")) {
		t.Fatalf("a.txt = %#v", files[0])
	}
	if files[1].Mode() != Mode0755 || files[1].Size() != 10 || files[1].Digest() != provenance.SHA256([]byte("#!/bin/sh\n")) {
		t.Fatalf("bin/run = %#v", files[1])
	}
	if got := tree.Digest().String(); got != "sha256:0729aab75907f4b774998237418a990fcbe050c5634151ad0b57744c42a0ebea" {
		t.Fatalf("tree digest = %s", got)
	}
	if got := independentTreeDigest(files); got != tree.Digest() {
		t.Fatalf("production digest = %s, independent oracle = %s", tree.Digest().String(), got.String())
	}

	inputs[0].Content[0] = 'x'
	files[0] = TreeFile{}
	read, ok := tree.Lookup("a.txt")
	if !ok {
		t.Fatal("a.txt missing")
	}
	returned := read.Bytes()
	returned[0] = 'x'
	if got, _ := tree.Lookup("a.txt"); !bytes.Equal(got.Bytes(), []byte("alpha")) || tree.Digest() != reversed.Digest() {
		t.Fatalf("tree changed through caller mutation: bytes=%q digest=%s", got.Bytes(), tree.Digest().String())
	}
	if _, ok := tree.Lookup("missing"); ok {
		t.Fatal("missing lookup succeeded")
	}
}

func TestTreeEmptyContentAndValidEmptyValue(t *testing.T) {
	if got := DefaultTreeLimits(); got != (TreeLimits{MaxFiles: 10_000, MaxFileBytes: 32 << 20, MaxTotalBytes: 512 << 20}) {
		t.Fatalf("default limits = %#v", got)
	}
	emptyManifest := newTreeManifest(t, []FileSpec{})
	tree, err := NewTree(emptyManifest, []TreeInput{}, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if tree.Len() != 0 || tree.Files() == nil || tree.Digest().String() != "sha256:dfd7ff88a3c48e8f33ff5a830b2e63409855dfc3995b7083a66993d15215e029" {
		t.Fatalf("empty tree = len=%d files=%#v digest=%s", tree.Len(), tree.Files(), tree.Digest().String())
	}

	emptyFileManifest := newTreeManifest(t, []FileSpec{{Path: "empty", Size: 0, Digest: provenance.SHA256(nil), Mode: Mode0644}})
	fromNil, err := NewTree(emptyFileManifest, []TreeInput{{Path: "empty", Content: nil}}, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	fromEmpty, err := NewTree(emptyFileManifest, []TreeInput{{Path: "empty", Content: []byte{}}}, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	if fromNil.Digest() != fromEmpty.Digest() || fromNil.Files()[0].Bytes() == nil || fromEmpty.Files()[0].Bytes() == nil {
		t.Fatalf("nil and empty content differ: nil=%#v empty=%#v", fromNil.Files()[0].Bytes(), fromEmpty.Files()[0].Bytes())
	}

	var zero Tree
	if zero.Len() != 0 || zero.Files() != nil || zero.Digest() != (provenance.Digest{}) {
		t.Fatalf("zero tree accessors are not zero-safe: len=%d files=%#v digest=%q", zero.Len(), zero.Files(), zero.Digest().String())
	}
	if _, ok := zero.Lookup("empty"); ok {
		t.Fatal("zero tree lookup succeeded")
	}
	var zeroFile TreeFile
	if zeroFile.Path() != "" || zeroFile.Mode() != "" || zeroFile.Size() != 0 || zeroFile.Digest() != (provenance.Digest{}) || zeroFile.Bytes() != nil {
		t.Fatalf("zero tree file accessors are not zero-safe: %#v", zeroFile)
	}
}

func TestTreeDigestChangesWithEveryOwnedFileFact(t *testing.T) {
	trees := []Tree{
		mustTree(t, []TreeInput{{Path: "a", Content: []byte("x")}}, nil),
		mustTree(t, []TreeInput{{Path: "b", Content: []byte("x")}}, nil),
		mustTree(t, []TreeInput{{Path: "a", Content: []byte("x")}}, map[string]FileMode{"a": Mode0755}),
		mustTree(t, []TreeInput{{Path: "a", Content: []byte("xy")}}, nil),
		mustTree(t, []TreeInput{{Path: "a", Content: []byte("y")}}, nil),
	}
	seen := map[string]struct{}{}
	for _, tree := range trees {
		if _, duplicate := seen[tree.Digest().String()]; duplicate {
			t.Fatalf("distinct tree facts produced duplicate digest %s", tree.Digest().String())
		}
		seen[tree.Digest().String()] = struct{}{}
	}
}

func TestTreeLimitsAreInclusive(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
	tree, err := NewTree(manifest, []TreeInput{{Path: "a", Content: []byte("a")}}, TreeLimits{MaxFiles: 1, MaxFileBytes: 1, MaxTotalBytes: 1})
	if err != nil || tree.Len() != 1 {
		t.Fatalf("exact limit tree = %#v, err=%v", tree, err)
	}
}

func TestTreeDeclaredPerFileLimitPrecedesDeclaredTotal(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{
		{Path: "a", Size: 2, Digest: provenance.SHA256([]byte("aa")), Mode: Mode0644},
		{Path: "b", Size: 3, Digest: provenance.SHA256([]byte("bbb")), Mode: Mode0644},
	})
	_, err := NewTree(manifest, nil, TreeLimits{MaxFiles: 2, MaxFileBytes: 2, MaxTotalBytes: 1})
	assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_bytes_exceeded", "/files/1/content")
}

func TestTreeOverLimitAllocationDoesNotScaleWithCallerPayload(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
	limits := TreeLimits{MaxFiles: 1, MaxFileBytes: 1, MaxTotalBytes: 1}
	operation := func(content []byte) func() (Tree, error) {
		return func() (Tree, error) {
			return NewTree(manifest, []TreeInput{{Path: "a", Content: content}}, limits)
		}
	}

	small := []byte("aa")
	large := bytes.Repeat([]byte("x"), 1<<20)
	_, err := operation(large)()
	assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_bytes_exceeded", "/files/0/content")
	smallBytes := benchmarkTreeAllocatedBytes(t, operation(small), true)
	largeBytes := benchmarkTreeAllocatedBytes(t, operation(large), true)
	t.Logf("over-limit bytes/op: small=%d large=%d delta=%d", smallBytes, largeBytes, largeBytes-smallBytes)
	if extra := largeBytes - smallBytes; extra > 64<<10 {
		t.Fatalf("over-limit allocation grew with caller payload: small=%d large=%d extra=%d", smallBytes, largeBytes, extra)
	}
}

func TestTreeMetadataReadsDoNotCopyPayload(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 1<<20)
	manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: int64(len(content)), Digest: provenance.SHA256(content), Mode: Mode0644}})
	tree, err := NewTree(manifest, []TreeInput{{Path: "a", Content: content}}, TreeLimits{MaxFiles: 1, MaxFileBytes: int64(len(content)), MaxTotalBytes: int64(len(content))})
	if err != nil {
		t.Fatal(err)
	}

	filesBytes := benchmarkAllocatedBytes(t, func() {
		files := tree.Files()
		runtime.KeepAlive(files)
	})
	providerBytes := benchmarkAllocatedBytes(t, func() {
		provider, providerErr := NewProvider(manifest, tree)
		if providerErr != nil {
			panic(providerErr)
		}
		runtime.KeepAlive(provider)
	})
	t.Logf("metadata bytes/op: files=%d provider=%d", filesBytes, providerBytes)
	const metadataCeiling = 128 << 10
	if filesBytes > metadataCeiling || providerBytes > metadataCeiling {
		t.Fatalf("metadata-only reads copied payload: files=%d provider=%d ceiling=%d", filesBytes, providerBytes, metadataCeiling)
	}
}

func TestTreeContentHashingStartsAfterActualResourceAndSizeGates(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{
		{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644},
		{Path: "b", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644},
	})

	t.Run("actual overflow performs no content hash", func(t *testing.T) {
		var hashed []string
		_, err := newTreeWithDigester(manifest, []TreeInput{
			{Path: "a", Content: []byte("x")},
			{Path: "b", Content: bytes.Repeat([]byte("y"), 1<<20)},
		}, TreeLimits{MaxFiles: 2, MaxFileBytes: 1, MaxTotalBytes: 2}, treeInputsBorrowed, func(content []byte) provenance.Digest {
			hashed = append(hashed, string(content))
			return provenance.SHA256(content)
		})
		assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_bytes_exceeded", "/files/1/content")
		if len(hashed) != 0 {
			t.Fatalf("overflow path hashed %d payloads before the resource gate", len(hashed))
		}
	})

	t.Run("size mismatch performs no content hash", func(t *testing.T) {
		var hashed []string
		_, err := newTreeWithDigester(manifest, []TreeInput{
			{Path: "a", Content: []byte("x")},
			{Path: "b", Content: []byte("bb")},
		}, TreeLimits{MaxFiles: 2, MaxFileBytes: 2, MaxTotalBytes: 2}, treeInputsBorrowed, func(content []byte) provenance.Digest {
			hashed = append(hashed, string(content))
			return provenance.SHA256(content)
		})
		assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_size_mismatch", "/files/1/content")
		if len(hashed) != 0 {
			t.Fatalf("size mismatch path hashed %d payloads before the size gate", len(hashed))
		}
	})

	t.Run("duplicate grouping performs no content hash", func(t *testing.T) {
		var hashed []string
		_, err := newTreeWithDigester(manifest, []TreeInput{
			{Path: "a", Content: []byte("z")},
			{Path: "a", Content: []byte("a")},
		}, TreeLimits{MaxFiles: 2, MaxFileBytes: 2, MaxTotalBytes: 2}, treeInputsBorrowed, func(content []byte) provenance.Digest {
			hashed = append(hashed, string(content))
			return provenance.SHA256(content)
		})
		assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_duplicate", "/files/1/path")
		if len(hashed) != 0 {
			t.Fatalf("duplicate path grouping hashed %d payloads", len(hashed))
		}
	})

	t.Run("valid content hashes in canonical Manifest order", func(t *testing.T) {
		var hashed []string
		tree, err := newTreeWithDigester(manifest, []TreeInput{
			{Path: "b", Content: []byte("b")},
			{Path: "a", Content: []byte("a")},
		}, TreeLimits{MaxFiles: 2, MaxFileBytes: 1, MaxTotalBytes: 2}, treeInputsBorrowed, func(content []byte) provenance.Digest {
			hashed = append(hashed, string(content))
			return provenance.SHA256(content)
		})
		if err != nil {
			t.Fatal(err)
		}
		if tree.Len() != 2 || !reflect.DeepEqual(hashed, []string{"a", "b"}) {
			t.Fatalf("valid hash stage = %#v, tree len=%d", hashed, tree.Len())
		}
	})
}

func TestTreeValidationOrderAndClosedErrors(t *testing.T) {
	validA := FileSpec{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}
	validB := FileSpec{Path: "b", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644}
	tests := []struct {
		name     string
		manifest Manifest
		inputs   []TreeInput
		limits   TreeLimits
		reason   string
		pointer  string
	}{
		{name: "max files", manifest: newTreeManifest(t, []FileSpec{}), inputs: []TreeInput{}, limits: TreeLimits{MaxFiles: 0, MaxFileBytes: 1, MaxTotalBytes: 1}, reason: "tree_limit_invalid", pointer: "/limits/maxFiles"},
		{name: "max file bytes", manifest: newTreeManifest(t, []FileSpec{}), inputs: []TreeInput{}, limits: TreeLimits{MaxFiles: 1, MaxFileBytes: 0, MaxTotalBytes: 1}, reason: "tree_limit_invalid", pointer: "/limits/maxFileBytes"},
		{name: "max total bytes", manifest: newTreeManifest(t, []FileSpec{}), inputs: []TreeInput{}, limits: TreeLimits{MaxFiles: 1, MaxFileBytes: 1, MaxTotalBytes: 0}, reason: "tree_limit_invalid", pointer: "/limits/maxTotalBytes"},
		{name: "manifest", manifest: Manifest{}, inputs: nil, limits: DefaultTreeLimits(), reason: "manifest_required", pointer: "/manifest"},
		{name: "declared count", manifest: newTreeManifest(t, []FileSpec{validA, validB}), inputs: nil, limits: TreeLimits{MaxFiles: 1, MaxFileBytes: 10, MaxTotalBytes: 10}, reason: "tree_file_count_exceeded", pointer: "/files"},
		{name: "input count", manifest: newTreeManifest(t, []FileSpec{}), inputs: []TreeInput{{Path: "a"}, {Path: "b"}}, limits: TreeLimits{MaxFiles: 1, MaxFileBytes: 10, MaxTotalBytes: 10}, reason: "tree_file_count_exceeded", pointer: "/files"},
		{name: "declared file bytes", manifest: newTreeManifest(t, []FileSpec{{Path: "a", Size: 2, Digest: provenance.SHA256([]byte("aa")), Mode: Mode0644}}), inputs: nil, limits: TreeLimits{MaxFiles: 2, MaxFileBytes: 1, MaxTotalBytes: 10}, reason: "tree_file_bytes_exceeded", pointer: "/files/0/content"},
		{name: "declared total", manifest: newTreeManifest(t, []FileSpec{validA, validB}), inputs: nil, limits: TreeLimits{MaxFiles: 2, MaxFileBytes: 2, MaxTotalBytes: 1}, reason: "tree_total_bytes_exceeded", pointer: "/files"},
		{name: "actual bytes", manifest: newTreeManifest(t, []FileSpec{validA}), inputs: []TreeInput{{Path: "a", Content: []byte("aa")}}, limits: TreeLimits{MaxFiles: 2, MaxFileBytes: 1, MaxTotalBytes: 10}, reason: "tree_file_bytes_exceeded", pointer: "/files/0/content"},
		{name: "missing", manifest: newTreeManifest(t, []FileSpec{validA}), inputs: []TreeInput{}, limits: DefaultTreeLimits(), reason: "tree_file_missing", pointer: "/files/0/path"},
		{name: "extra", manifest: newTreeManifest(t, []FileSpec{}), inputs: []TreeInput{{Path: "a", Content: []byte("a")}}, limits: DefaultTreeLimits(), reason: "tree_file_extra", pointer: "/files/0/path"},
		{name: "size", manifest: newTreeManifest(t, []FileSpec{validA}), inputs: []TreeInput{{Path: "a", Content: []byte("bb")}}, limits: DefaultTreeLimits(), reason: "tree_file_size_mismatch", pointer: "/files/0/content"},
		{name: "digest", manifest: newTreeManifest(t, []FileSpec{validA}), inputs: []TreeInput{{Path: "a", Content: []byte("b")}}, limits: DefaultTreeLimits(), reason: "tree_file_digest_mismatch", pointer: "/files/0/content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTree(tt.manifest, tt.inputs, tt.limits)
			assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", tt.reason, tt.pointer)
		})
	}
}

func TestTreeCoordinatesAreCallerOrderIndependent(t *testing.T) {
	t.Run("raw invalid path multiset", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{})
		for _, invalid := range []string{string([]byte{0xff}), "e\u0301"} {
			first := []TreeInput{{Path: invalid}, {Path: "z"}}
			wantIndex := 0
			if invalid > "z" {
				wantIndex = 1
			}
			for _, inputs := range [][]TreeInput{first, {first[1], first[0]}} {
				_, err := NewTree(manifest, inputs, DefaultTreeLimits())
				assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_path_invalid", filePointer(wantIndex, "path"))
			}
		}
	})

	t.Run("canonical duplicate multiset", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
		first := []TreeInput{{Path: "a", Content: []byte("z")}, {Path: "a", Content: []byte("a")}}
		for _, inputs := range [][]TreeInput{first, {first[1], first[0]}} {
			_, err := NewTree(manifest, inputs, DefaultTreeLimits())
			assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_duplicate", "/files/1/path")
		}
	})

	t.Run("case fold precedes prefix and uses smallest pair", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{})
		first := []TreeInput{{Path: "b/child"}, {Path: "a"}, {Path: "A"}, {Path: "b"}, {Path: "B"}}
		for _, inputs := range [][]TreeInput{first, reverseTreeInputs(first)} {
			_, err := NewTree(manifest, inputs, DefaultTreeLimits())
			assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_collision", "/files/2/path")
		}
	})

	t.Run("prefix uses smallest lexical pair", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{})
		first := []TreeInput{{Path: "z/child"}, {Path: "a/child"}, {Path: "z"}, {Path: "a"}}
		for _, inputs := range [][]TreeInput{first, reverseTreeInputs(first)} {
			_, err := NewTree(manifest, inputs, DefaultTreeLimits())
			assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_collision", "/files/1/path")
		}
	})

	t.Run("missing precedes extra on final union", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{{Path: "b", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644}})
		first := []TreeInput{{Path: "a", Content: []byte("a")}, {Path: "c", Content: []byte("c")}}
		for _, inputs := range [][]TreeInput{first, reverseTreeInputs(first)} {
			_, err := NewTree(manifest, inputs, DefaultTreeLimits())
			assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_missing", "/files/1/path")
		}
	})
}

func TestTreeContentFailuresAreCategoryMajor(t *testing.T) {
	t.Run("size beats lower path digest", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{
			{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644},
			{Path: "b", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644},
		})
		first := []TreeInput{{Path: "a", Content: []byte("x")}, {Path: "b", Content: []byte("bb")}}
		for _, inputs := range [][]TreeInput{first, reverseTreeInputs(first)} {
			_, err := NewTree(manifest, inputs, DefaultTreeLimits())
			assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_size_mismatch", "/files/1/content")
		}
	})

	t.Run("overflow beats lower path size", func(t *testing.T) {
		manifest := newTreeManifest(t, []FileSpec{
			{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644},
			{Path: "b", Size: 2, Digest: provenance.SHA256([]byte("bb")), Mode: Mode0644},
		})
		limits := TreeLimits{MaxFiles: 3, MaxFileBytes: 2, MaxTotalBytes: 10}
		first := []TreeInput{{Path: "a", Content: []byte("aa")}, {Path: "b", Content: []byte("bbb")}}
		for _, inputs := range [][]TreeInput{first, reverseTreeInputs(first)} {
			_, err := NewTree(manifest, inputs, limits)
			assertTask2Error(t, err, ErrTreeInvalid, "source_tree_invalid", "tree_file_bytes_exceeded", "/files/1/content")
		}
	})
}

func newTreeManifest(t *testing.T, files []FileSpec) Manifest {
	t.Helper()
	manifest, err := NewManifest(ManifestSpec{
		Identity: IdentitySpec{
			ProviderID: "sample.tree", ModulePath: "example.com/sample/tree",
			PackagePath: "example.com/sample/tree/source", Version: "v0.1.0",
		},
		Files: files, Profiles: []ProfileSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func manifestForTreeInputs(t *testing.T, inputs []TreeInput, modes map[string]FileMode) Manifest {
	t.Helper()
	files := make([]FileSpec, len(inputs))
	for index, input := range inputs {
		mode := Mode0644
		if configured := modes[input.Path]; configured != "" {
			mode = configured
		}
		files[index] = FileSpec{Path: input.Path, Size: int64(len(input.Content)), Digest: provenance.SHA256(input.Content), Mode: mode}
	}
	return newTreeManifest(t, files)
}

func mustTree(t *testing.T, inputs []TreeInput, modes map[string]FileMode) Tree {
	t.Helper()
	tree, err := NewTree(manifestForTreeInputs(t, inputs, modes), inputs, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func reverseTreeInputs(inputs []TreeInput) []TreeInput {
	result := append([]TreeInput(nil), inputs...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func independentTreeDigest(files []TreeFile) provenance.Digest {
	ordered := append([]TreeFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path() < ordered[j].Path() })
	hash := sha256.New()
	hash.Write([]byte("nexa-source-tree-v1\x00"))
	for _, file := range ordered {
		for _, value := range []string{file.Path(), string(file.Mode()), decimalInt64(file.Size()), file.Digest().String()} {
			var size [8]byte
			binary.BigEndian.PutUint64(size[:], uint64(len(value)))
			hash.Write(size[:])
			hash.Write([]byte(value))
		}
	}
	digest, err := provenance.ParseDigest("sha256:" + hex.EncodeToString(hash.Sum(nil)))
	if err != nil {
		panic(err)
	}
	return digest
}

func decimalInt64(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

func benchmarkTreeAllocatedBytes(t *testing.T, operation func() (Tree, error), wantError bool) int64 {
	t.Helper()
	result := testing.Benchmark(func(b *testing.B) {
		var tree Tree
		var err error
		for index := 0; index < b.N; index++ {
			tree, err = operation()
			if (err != nil) != wantError {
				b.Fatalf("error = %v, wantError=%t", err, wantError)
			}
		}
		runtime.KeepAlive(tree)
		runtime.KeepAlive(err)
	})
	return result.AllocedBytesPerOp()
}

func benchmarkAllocatedBytes(t *testing.T, operation func()) int64 {
	t.Helper()
	result := testing.Benchmark(func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			operation()
		}
		runtime.KeepAlive(operation)
	})
	return result.AllocedBytesPerOp()
}

func assertTask2Error(t *testing.T, err error, class ErrorClass, code, reason, pointer string) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("operation succeeded, want %s/%s", code, reason)
	}
	var projected *Error
	if !errors.As(err, &projected) {
		t.Fatalf("error type = %T, want *sourceplugin.Error", err)
	}
	if projected.Class() != class || projected.Code() != code || projected.Reason() != reason || projected.Pointer() != pointer {
		t.Fatalf("error = class=%v code=%q reason=%q pointer=%q, want %v %q/%q at %q", projected.Class(), projected.Code(), projected.Reason(), projected.Pointer(), class, code, reason, pointer)
	}
	if projected.Error() != class.Error() || !errors.Is(projected, class) {
		t.Fatalf("error class projection = %q/%v, want %q", projected.Error(), projected.Class(), class.Error())
	}
	if class == ErrTreeLoadFailed {
		if !errors.Is(projected, ErrTreeInvalid) {
			t.Fatal("tree load failure does not match ErrTreeInvalid")
		}
	} else if errors.Is(projected, ErrTreeLoadFailed) || (class != ErrTreeInvalid && errors.Is(projected, ErrTreeInvalid)) {
		t.Fatalf("unexpected tree class lattice for %v", class)
	}
	if class != ErrProviderInvalid && errors.Is(projected, ErrProviderInvalid) {
		t.Fatal("non-provider error matches ErrProviderInvalid")
	}
	if projected.Source() != "" || projected.Line() != 0 || projected.Column() != 0 || projected.Cycle() != nil || errors.Unwrap(projected) != nil {
		t.Fatalf("task 2 error leaked diagnostics: source=%q line=%d column=%d cycle=%#v unwrap=%v", projected.Source(), projected.Line(), projected.Column(), projected.Cycle(), errors.Unwrap(projected))
	}
	return projected
}
