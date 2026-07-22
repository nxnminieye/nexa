package lock

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
)

const lockKeyDomain = "nexa-source-lock-key-v1\x00"

type Key struct {
	providerID string
	target     string
	digest     provenance.Digest
	valid      bool
}

func NewKey(providerID, target string) (Key, error) {
	if !contract.ValidStableID(providerID) {
		return Key{}, lockError(ErrLockInput, "source_lock_invalid", "key_provider_invalid", "/providerId", StageKey)
	}
	if err := projectPathIssue(contract.ValidatePortablePath(target), "key_target_invalid", "/target", StageKey); err != nil {
		return Key{}, err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(lockKeyDomain))
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(providerID)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(providerID))
	binary.BigEndian.PutUint64(length[:], uint64(len(target)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write([]byte(target))
	digest, err := provenance.ParseDigest("sha256:" + hex.EncodeToString(hasher.Sum(nil)))
	if err != nil {
		return Key{}, lockError(ErrLockInternal, "source_lock_internal", "canonicalization_failed", "", StageKey)
	}
	return Key{providerID: providerID, target: target, digest: digest, valid: true}, nil
}

func projectPathIssue(issue *contract.PathIssue, invalidReason, pointer string, stage Stage) *Error {
	if issue == nil {
		return nil
	}
	switch issue.Reason {
	case contract.PathInvalid, contract.PathNotNFC, contract.PathReserved:
		return lockError(ErrLockInput, "source_lock_invalid", invalidReason, pointer, stage)
	default:
		return lockError(ErrLockInternal, "source_lock_internal", "owner_projection_failed", pointer, stage)
	}
}

func (k Key) ProviderID() string { return k.providerID }
func (k Key) Target() string     { return k.target }
func (k Key) Equal(other Key) bool {
	return k.valid && other.valid && k.providerID == other.providerID && k.target == other.target && k.digest == other.digest
}
func (k Key) RepositoryPath() string {
	if !k.valid {
		return ""
	}
	return ".nexa/source/locks/" + k.providerID + "-" + strings.TrimPrefix(k.digest.String(), "sha256:") + ".json"
}
