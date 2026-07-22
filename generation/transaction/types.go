package transaction

import (
	"context"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/internal/staging"
	"github.com/nxnminieye/nexa/provenance"
)

const PlanAPIVersion = "nexa.dev/generation-plan/v2"
const ResultAPIVersion = "nexa.dev/generation-result/v1"

type ChangeKind string

const (
	ChangeCreate       ChangeKind = "create"
	ChangeUpdate       ChangeKind = "update"
	ChangeDelete       ChangeKind = "delete"
	ChangeCreateManual ChangeKind = "create-manual"
)

type ArtifactInput struct {
	ID              string
	Path            string
	Owner           string
	Digest          provenance.Digest
	Sources         []provenance.SourceRef
	StalePolicy     artifact.StalePolicy
	Probe           OwnershipProbe
	CreateManual    bool
	OverwriteManual bool
}

type Ownership struct {
	GeneratorID string
	ArtifactID  string
	InputDigest provenance.Digest
}

type OwnershipProbe interface {
	Inspect(path string, content []byte, expected Ownership) (bool, error)
}

type WriteOptions struct {
	PlanDigest provenance.Digest
}

type ControlSourceRole string

const ControlSourceCompatibilityLock ControlSourceRole = "compatibility-lock"

type CompatibilityLockMutationSpec struct {
	ID          string
	Path        string
	Owner       string
	Before      *provenance.Source
	After       []byte
	AfterDigest provenance.Digest
	Sources     []provenance.SourceRef
}

type ControlSourceMutation struct {
	role        ControlSourceRole
	id          string
	path        string
	owner       string
	before      *provenance.Source
	after       []byte
	afterDigest provenance.Digest
	sources     []provenance.SourceRef
}

type PlanRequest struct {
	Generator            artifact.GeneratorSpec
	Sources              []provenance.Source
	Expected             []ArtifactInput
	ControlSources       []ControlSourceMutation
	StaleOwnershipProbes []OwnershipProbe
	Previous             *artifact.Manifest
	ManifestPath         string
	RevalidateSources    func(context.Context) ([]provenance.Source, error)
}

type Plan struct{ state *planState }
type Change struct{ state changeState }
type Conflict struct{ state conflictState }
type Result struct{ state *resultState }

type planState struct {
	generator         artifact.GeneratorSpec
	sources           []provenance.Source
	inputs            []plannedArtifact
	controls          []plannedControl
	staleProbes       []OwnershipProbe
	changes           []changeState
	conflicts         []conflictState
	next              artifact.Manifest
	previous          artifact.Manifest
	hasPrevious       bool
	manifestPath      string
	planDigest        provenance.Digest
	canonical         []byte
	session           *staging.Session
	stagingRoot       string
	revalidateSources func(context.Context) ([]provenance.Source, error)
}

type plannedArtifact struct {
	id, path, owner string
	digest          provenance.Digest
	sources         []provenance.SourceRef
	stalePolicy     artifact.StalePolicy
	probe           OwnershipProbe
	createManual    bool
	overwriteManual bool
	overwritePrior  provenance.Digest
	overwriteExists bool
}

type plannedControl struct {
	role        ControlSourceRole
	id          string
	path        string
	owner       string
	before      *provenance.Source
	after       []byte
	afterDigest provenance.Digest
	sources     []provenance.SourceRef
}

type changeState struct {
	kind       ChangeKind
	id, path   string
	digest     provenance.Digest
	prior      provenance.Digest
	hasPrior   bool
	control    ControlSourceRole
	hasControl bool
}

type conflictState struct {
	id, path, reason string
	control          ControlSourceRole
	hasControl       bool
}

type resultState struct {
	planDigest provenance.Digest
	changes    []changeState
	conflicts  []conflictState
	canonical  []byte
}

func (p Plan) PlanDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.planDigest
}

func (p Plan) Close() error {
	if p.state == nil || p.state.session == nil {
		return nil
	}
	return p.state.session.Close()
}

func (p Plan) Changes() []Change {
	if p.state == nil {
		return nil
	}
	result := make([]Change, len(p.state.changes))
	for index, value := range p.state.changes {
		result[index] = Change{state: value}
	}
	return result
}

func (p Plan) Conflicts() []Conflict {
	if p.state == nil {
		return nil
	}
	result := make([]Conflict, len(p.state.conflicts))
	for index, value := range p.state.conflicts {
		result[index] = Conflict{state: value}
	}
	return result
}

func (p Plan) NextManifest() (artifact.Manifest, bool) {
	if p.state == nil || p.state.next.APIVersion() != artifact.APIVersion {
		return artifact.Manifest{}, false
	}
	return p.state.next, true
}

func (p Plan) CanonicalJSON() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.canonical...)
}

func (c Change) Kind() ChangeKind          { return c.state.kind }
func (c Change) ID() string                { return c.state.id }
func (c Change) Path() string              { return c.state.path }
func (c Change) Digest() provenance.Digest { return c.state.digest }
func (c Change) PriorDigest() (provenance.Digest, bool) {
	return c.state.prior, c.state.hasPrior
}
func (c Change) ControlSourceRole() (ControlSourceRole, bool) {
	return c.state.control, c.state.hasControl
}

func (c Conflict) ID() string     { return c.state.id }
func (c Conflict) Path() string   { return c.state.path }
func (c Conflict) Reason() string { return c.state.reason }
func (c Conflict) ControlSourceRole() (ControlSourceRole, bool) {
	return c.state.control, c.state.hasControl
}

func (r Result) PlanDigest() provenance.Digest {
	if r.state == nil {
		return provenance.Digest{}
	}
	return r.state.planDigest
}

func (r Result) Clean() bool {
	return r.state != nil && len(r.state.changes) == 0 && len(r.state.conflicts) == 0
}

func (r Result) Changes() []Change {
	if r.state == nil {
		return nil
	}
	result := make([]Change, len(r.state.changes))
	for index, value := range r.state.changes {
		result[index] = Change{state: value}
	}
	return result
}

func (r Result) Conflicts() []Conflict {
	if r.state == nil {
		return nil
	}
	result := make([]Conflict, len(r.state.conflicts))
	for index, value := range r.state.conflicts {
		result[index] = Conflict{state: value}
	}
	return result
}

func (r Result) CanonicalJSON() []byte {
	if r.state == nil {
		return nil
	}
	return append([]byte(nil), r.state.canonical...)
}
