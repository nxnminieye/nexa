package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"sort"
	"strconv"
)

const treeDigestDomain = "nexa-tree-v1\x00"

type TreeEntry struct {
	Path   string
	Digest Digest
	Size   int64
}

func TreeDigest(entries []TreeEntry) (Digest, error) {
	canonical := append([]TreeEntry(nil), entries...)
	sort.Slice(canonical, func(left, right int) bool {
		return canonical[left].Path < canonical[right].Path
	})

	digest := sha256.New()
	_, _ = digest.Write([]byte(treeDigestDomain))
	for index, entry := range canonical {
		if err := validateTreeEntry(entry); err != nil {
			return Digest{}, err
		}
		if index > 0 && canonical[index-1].Path == entry.Path {
			return Digest{}, invalid("tree", "duplicate entry path")
		}
		writeLengthPrefixed(digest, entry.Path)
		writeLengthPrefixed(digest, strconv.FormatInt(entry.Size, 10))
		writeLengthPrefixed(digest, entry.Digest.String())
	}

	return Digest{value: sha256Prefix + hex.EncodeToString(digest.Sum(nil))}, nil
}

func validateTreeEntry(entry TreeEntry) error {
	if err := validateRepositoryPath(entry.Path); err != nil {
		return err
	}
	if entry.Digest.value == "" {
		return invalid("tree entry", "digest is required")
	}
	if entry.Size < 0 {
		return invalid("tree entry", "size cannot be negative")
	}
	return nil
}

func writeLengthPrefixed(destination hash.Hash, value string) {
	_, _ = destination.Write([]byte(strconv.Itoa(len(value))))
	_, _ = destination.Write([]byte{':'})
	_, _ = destination.Write([]byte(value))
}
