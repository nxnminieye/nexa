package engine

import (
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
)

type ManagedState string

const (
	ManagedStateNotManaged ManagedState = "not-managed"
	ManagedStateClean      ManagedState = "managed-clean"
	ManagedStateModified   ManagedState = "managed-modified"
)

type PlanOperation string

const (
	PlanMaterialize PlanOperation = "materialize"
	PlanNoop        PlanOperation = "noop"
	PlanUpgrade     PlanOperation = "upgrade"
)

type FileType string

const (
	FileAbsent    FileType = "absent"
	FileRegular   FileType = "regular"
	FileDirectory FileType = "directory"
	FileSymlink   FileType = "symlink"
	FileOther     FileType = "other"
)

type DeltaKind string

const (
	DeltaAdded       DeltaKind = "added"
	DeltaModified    DeltaKind = "modified"
	DeltaDeleted     DeltaKind = "deleted"
	DeltaModeChanged DeltaKind = "mode-changed"
	DeltaTypeChanged DeltaKind = "type-changed"
)

type FileState struct {
	typeOf  FileType
	mode    uint32
	size    int64
	digest  provenance.Digest
	content []byte
}

func (f FileState) Type() FileType            { return f.typeOf }
func (f FileState) Mode() uint32              { return f.mode }
func (f FileState) Size() int64               { return f.size }
func (f FileState) Digest() provenance.Digest { return f.digest }
func (f FileState) Bytes() []byte             { return append([]byte(nil), f.content...) }

type Delta struct {
	path   string
	kind   DeltaKind
	before FileState
	after  FileState
}

func (d Delta) Path() string      { return d.path }
func (d Delta) Kind() DeltaKind   { return d.kind }
func (d Delta) Before() FileState { return cloneFileState(d.before) }
func (d Delta) After() FileState  { return cloneFileState(d.after) }

type Status struct {
	state          ManagedState
	deltas         []Delta
	snapshotDigest provenance.Digest
}

func (s Status) State() ManagedState               { return s.state }
func (s Status) Deltas() []Delta                   { return cloneDeltas(s.deltas) }
func (s Status) SnapshotDigest() provenance.Digest { return s.snapshotDigest }

type Diff struct {
	deltas         []Delta
	snapshotDigest provenance.Digest
}

func (d Diff) Deltas() []Delta                   { return cloneDeltas(d.deltas) }
func (d Diff) SnapshotDigest() provenance.Digest { return d.snapshotDigest }

func cloneFileState(state FileState) FileState {
	state.content = append([]byte(nil), state.content...)
	return state
}

func cloneDeltas(input []Delta) []Delta {
	result := append([]Delta(nil), input...)
	for index := range result {
		result[index].before = cloneFileState(result[index].before)
		result[index].after = cloneFileState(result[index].after)
	}
	return result
}

func baselineState(mode sourceplugin.FileMode, size int64, digest provenance.Digest) FileState {
	permissions := uint32(0o644)
	if mode == sourceplugin.Mode0755 {
		permissions = 0o755
	}
	return FileState{typeOf: FileRegular, mode: permissions, size: size, digest: digest}
}
