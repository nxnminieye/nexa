package lock

import (
	_ "embed"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

const APIVersion = "nexa.dev/source-lock/v1"
const Kind = "SourceLock"

//go:embed source-lock-v1.schema.json
var embeddedSchema []byte

type Limits struct {
	MaxDocumentBytes  int64
	MaxProfileClosure int
	MaxTrackedFiles   int
	MaxTargetBytes    int
}

func DefaultLimits() Limits {
	return Limits{MaxDocumentBytes: 128 << 20, MaxProfileClosure: 4096, MaxTrackedFiles: 10_000, MaxTargetBytes: 1024}
}

type BaselineFile struct {
	path   string
	mode   sourceplugin.FileMode
	size   int64
	digest provenance.Digest
}

func (f BaselineFile) Path() string                { return f.path }
func (f BaselineFile) Mode() sourceplugin.FileMode { return f.mode }
func (f BaselineFile) Size() int64                 { return f.size }
func (f BaselineFile) Digest() provenance.Digest   { return f.digest }

type Snapshot struct {
	key            Key
	release        release.Ref
	profileID      string
	profileClosure []string
	target         string
	trackedFiles   []BaselineFile
	canonical      []byte
	digest         provenance.Digest
	source         string
	valid          bool
}

func Schema() []byte { return append([]byte(nil), embeddedSchema...) }

func (s Snapshot) APIVersion() string {
	if !s.valid {
		return ""
	}
	return APIVersion
}
func (s Snapshot) Kind() string {
	if !s.valid {
		return ""
	}
	return Kind
}
func (s Snapshot) Key() Key                 { return s.key }
func (s Snapshot) Release() release.Ref     { return s.release }
func (s Snapshot) ProfileID() string        { return s.profileID }
func (s Snapshot) ProfileClosure() []string { return append([]string(nil), s.profileClosure...) }
func (s Snapshot) Target() string           { return s.target }
func (s Snapshot) TrackedFiles() []BaselineFile {
	return append([]BaselineFile(nil), s.trackedFiles...)
}
func (s Snapshot) CanonicalJSON() ([]byte, error) {
	if !s.valid {
		return nil, lockError(ErrLockInput, "source_lock_invalid", "document_invalid", "", StageParse)
	}
	return append([]byte(nil), s.canonical...), nil
}
func (s Snapshot) Digest() provenance.Digest { return s.digest }
func (s Snapshot) Source() string            { return s.source }

type VerifiedLock struct {
	snapshot Snapshot
	valid    bool
}

func (v VerifiedLock) Snapshot() Snapshot           { return cloneSnapshot(v.snapshot) }
func (v VerifiedLock) APIVersion() string           { return v.snapshot.APIVersion() }
func (v VerifiedLock) Kind() string                 { return v.snapshot.Kind() }
func (v VerifiedLock) Key() Key                     { return v.snapshot.Key() }
func (v VerifiedLock) Release() release.Ref         { return v.snapshot.Release() }
func (v VerifiedLock) ProfileID() string            { return v.snapshot.ProfileID() }
func (v VerifiedLock) ProfileClosure() []string     { return v.snapshot.ProfileClosure() }
func (v VerifiedLock) Target() string               { return v.snapshot.Target() }
func (v VerifiedLock) TrackedFiles() []BaselineFile { return v.snapshot.TrackedFiles() }
func (v VerifiedLock) CanonicalJSON() ([]byte, error) {
	if !v.valid {
		return nil, lockError(ErrLockInput, "source_lock_invalid", "document_invalid", "", StageVerify)
	}
	return v.snapshot.CanonicalJSON()
}
func (v VerifiedLock) Digest() provenance.Digest {
	if !v.valid {
		return provenance.Digest{}
	}
	return v.snapshot.Digest()
}
func (v VerifiedLock) Source() string {
	if !v.valid {
		return ""
	}
	return v.snapshot.Source()
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.profileClosure = append([]string(nil), snapshot.profileClosure...)
	snapshot.trackedFiles = append([]BaselineFile(nil), snapshot.trackedFiles...)
	snapshot.canonical = append([]byte(nil), snapshot.canonical...)
	return snapshot
}
