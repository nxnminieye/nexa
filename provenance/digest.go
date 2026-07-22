package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const sha256Prefix = "sha256:"

type Digest struct {
	value string
}

func ParseDigest(value string) (Digest, error) {
	if len(value) != len(sha256Prefix)+sha256.Size*2 || value[:len(sha256Prefix)] != sha256Prefix {
		return Digest{}, invalid("digest", "expected sha256 followed by 64 lowercase hexadecimal characters")
	}
	for _, character := range value[len(sha256Prefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return Digest{}, invalid("digest", "expected sha256 followed by 64 lowercase hexadecimal characters")
		}
	}
	return Digest{value: value}, nil
}

func SHA256(content []byte) Digest {
	sum := sha256.Sum256(content)
	return Digest{value: sha256Prefix + hex.EncodeToString(sum[:])}
}

func (d Digest) String() string {
	return d.value
}

func (d Digest) MarshalJSON() ([]byte, error) {
	if d.value == "" {
		return nil, invalid("digest", "zero value cannot cross a document boundary")
	}
	return json.Marshal(d.value)
}
