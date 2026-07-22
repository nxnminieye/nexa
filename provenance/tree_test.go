package provenance_test

import (
	"testing"

	"github.com/nxnminieye/nexa/provenance"
)

func TestTreeDigestIsCanonicalAndDoesNotMutateInput(t *testing.T) {
	first := provenance.TreeEntry{
		Path:   "backend/a.api",
		Digest: provenance.SHA256([]byte("a")),
		Size:   1,
	}
	second := provenance.TreeEntry{
		Path:   "frontend/b.ts",
		Digest: provenance.SHA256([]byte("bb")),
		Size:   2,
	}
	forward := []provenance.TreeEntry{first, second}
	reverse := []provenance.TreeEntry{second, first}

	forwardDigest, err := provenance.TreeDigest(forward)
	if err != nil {
		t.Fatal(err)
	}
	reverseDigest, err := provenance.TreeDigest(reverse)
	if err != nil {
		t.Fatal(err)
	}
	if forwardDigest != reverseDigest {
		t.Fatalf("digests differ by input order: %s != %s", forwardDigest, reverseDigest)
	}
	if reverse[0] != second || reverse[1] != first {
		t.Fatalf("TreeDigest mutated input: %#v", reverse)
	}
}

func TestTreeDigestChangesWithEveryEntryField(t *testing.T) {
	base := []provenance.TreeEntry{{
		Path:   "backend/a.api",
		Digest: provenance.SHA256([]byte("a")),
		Size:   1,
	}}
	baseDigest, err := provenance.TreeDigest(base)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]provenance.TreeEntry{
		"path":        {Path: "backend/b.api", Digest: base[0].Digest, Size: base[0].Size},
		"file digest": {Path: base[0].Path, Digest: provenance.SHA256([]byte("b")), Size: base[0].Size},
		"size":        {Path: base[0].Path, Digest: base[0].Digest, Size: 2},
	}
	for name, changed := range tests {
		t.Run(name, func(t *testing.T) {
			changedDigest, err := provenance.TreeDigest([]provenance.TreeEntry{changed})
			if err != nil {
				t.Fatal(err)
			}
			if changedDigest == baseDigest {
				t.Fatalf("digest did not change when %s changed", name)
			}
		})
	}
}

func TestTreeDigestRejectsInvalidEntries(t *testing.T) {
	valid := provenance.TreeEntry{
		Path:   "backend/a.api",
		Digest: provenance.SHA256([]byte("a")),
		Size:   1,
	}
	tests := map[string][]provenance.TreeEntry{
		"duplicate path":  {valid, valid},
		"absolute path":   {{Path: "/backend/a.api", Digest: valid.Digest, Size: 1}},
		"dot component":   {{Path: "backend/../a.api", Digest: valid.Digest, Size: 1}},
		"empty component": {{Path: "backend//a.api", Digest: valid.Digest, Size: 1}},
		"backslash":       {{Path: `backend\a.api`, Digest: valid.Digest, Size: 1}},
		"negative size":   {{Path: valid.Path, Digest: valid.Digest, Size: -1}},
		"zero digest":     {{Path: valid.Path, Size: 1}},
	}

	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := provenance.TreeDigest(entries); err == nil {
				t.Fatal("invalid tree entries were accepted")
			}
		})
	}
}

func TestTreeDigestRejectsPortableWindowsVolumePath(t *testing.T) {
	entry := provenance.TreeEntry{
		Path:   "C:/outside/file.api",
		Digest: provenance.SHA256([]byte("outside")),
		Size:   7,
	}
	if _, err := provenance.TreeDigest([]provenance.TreeEntry{entry}); err == nil {
		t.Fatal("tree accepted a Windows volume path")
	}
}
