package sourceplugin

import (
	"testing"

	"github.com/nxnminieye/nexa/provenance"
)

func TestProviderAcceptsExactAndValidEmptySnapshots(t *testing.T) {
	manifest := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0755}})
	tree, err := NewTree(manifest, []TreeInput{{Path: "a", Content: []byte("a")}}, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	if provider.Manifest().Digest() != manifest.Digest() || provider.Tree().Digest() != tree.Digest() {
		t.Fatalf("provider snapshot changed values: manifest=%s tree=%s", provider.Manifest().Digest().String(), provider.Tree().Digest().String())
	}

	emptyManifest := newTreeManifest(t, []FileSpec{})
	emptyTree, err := NewTree(emptyManifest, []TreeInput{}, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	emptyProvider, err := NewProvider(emptyManifest, emptyTree)
	if err != nil || emptyProvider.Manifest().Digest() != emptyManifest.Digest() || emptyProvider.Tree().Digest() != emptyTree.Digest() {
		t.Fatalf("valid empty provider = %#v, err=%v", emptyProvider, err)
	}
}

func TestProviderValidationOrderAndClosedErrors(t *testing.T) {
	a0644 := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0644}})
	a0755 := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("a")), Mode: Mode0755}})
	aa := newTreeManifest(t, []FileSpec{{Path: "a", Size: 2, Digest: provenance.SHA256([]byte("aa")), Mode: Mode0644}})
	ab := newTreeManifest(t, []FileSpec{{Path: "a", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644}})
	b := newTreeManifest(t, []FileSpec{{Path: "b", Size: 1, Digest: provenance.SHA256([]byte("b")), Mode: Mode0644}})
	empty := newTreeManifest(t, []FileSpec{})
	treeA := mustNewTree(t, a0644, []TreeInput{{Path: "a", Content: []byte("a")}})
	treeA0755 := mustNewTree(t, a0755, []TreeInput{{Path: "a", Content: []byte("a")}})
	treeAA := mustNewTree(t, aa, []TreeInput{{Path: "a", Content: []byte("aa")}})
	treeBAtA := mustNewTree(t, ab, []TreeInput{{Path: "a", Content: []byte("b")}})
	treeB := mustNewTree(t, b, []TreeInput{{Path: "b", Content: []byte("b")}})
	emptyTree := mustNewTree(t, empty, []TreeInput{})

	tests := []struct {
		name     string
		manifest Manifest
		tree     Tree
		reason   string
		pointer  string
	}{
		{name: "manifest before tree", manifest: Manifest{}, tree: Tree{}, reason: "provider_manifest_required", pointer: "/manifest"},
		{name: "tree", manifest: empty, tree: Tree{}, reason: "provider_tree_required", pointer: "/tree"},
		{name: "missing before extra", manifest: b, tree: treeA, reason: "provider_file_missing", pointer: "/files/1/path"},
		{name: "extra", manifest: empty, tree: treeA, reason: "provider_file_extra", pointer: "/files/0/path"},
		{name: "missing", manifest: a0644, tree: emptyTree, reason: "provider_file_missing", pointer: "/files/0/path"},
		{name: "mode", manifest: a0644, tree: treeA0755, reason: "provider_file_mode_mismatch", pointer: "/files/0/mode"},
		{name: "size", manifest: a0644, tree: treeAA, reason: "provider_file_size_mismatch", pointer: "/files/0/size"},
		{name: "digest", manifest: a0644, tree: treeBAtA, reason: "provider_file_digest_mismatch", pointer: "/files/0/digest"},
		{name: "tree b extra after a missing", manifest: a0644, tree: treeB, reason: "provider_file_missing", pointer: "/files/0/path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProvider(tt.manifest, tt.tree)
			assertTask2Error(t, err, ErrProviderInvalid, "source_provider_invalid", tt.reason, tt.pointer)
		})
	}

	_ = treeA0755
}

func mustNewTree(t *testing.T, manifest Manifest, inputs []TreeInput) Tree {
	t.Helper()
	tree, err := NewTree(manifest, inputs, DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	return tree
}
