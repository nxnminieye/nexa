package provenance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
)

func TestDigestRoundTrip(t *testing.T) {
	digest := provenance.SHA256([]byte("stable"))
	parsed, err := provenance.ParseDigest(digest.String())
	if err != nil || parsed != digest {
		t.Fatalf("round trip = %v, %v", parsed, err)
	}

	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `"`+digest.String()+`"`; got != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func TestDigestRejectsInvalidValues(t *testing.T) {
	validHex := strings.Repeat("a", 64)
	tests := map[string]string{
		"wrong algorithm": "sha512:" + validHex,
		"short digest":    "sha256:" + strings.Repeat("a", 63),
		"long digest":     "sha256:" + strings.Repeat("a", 65),
		"uppercase hex":   "sha256:" + strings.Repeat("A", 64),
		"non-hex":         "sha256:" + strings.Repeat("g", 64),
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := provenance.ParseDigest(value); err == nil {
				t.Fatalf("ParseDigest(%q) succeeded", value)
			}
		})
	}
}

func TestDigestZeroValueCannotBeSerialized(t *testing.T) {
	var digest provenance.Digest
	if _, err := json.Marshal(digest); err == nil {
		t.Fatal("zero digest serialized successfully")
	}
}
