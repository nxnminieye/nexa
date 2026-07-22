package crudbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/generation/entity"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	PlanAPIVersion          = "nexa.dev/ent-graph-plan/v1"
	PlanKind                = "EntGraphPlan"
	ProtoMediaType          = "application/protobuf; charset=utf-8"
	ProtoOwner              = "nexa.dev/generator/crud-proto/v1"
	StaleDeleteIfUnmodified = "delete-if-unmodified"
)

type Plan struct{ state *planState }
type planState struct {
	requestDigest, sourceDigest, planDigest provenance.Digest
	sources                                 []provenance.Source
	entitySnapshot, crudSnapshot            []byte
	protoID, protoPath                      string
	protoBytes                              []byte
	protoDigest                             provenance.Digest
	protoSourceRefs                         []provenance.SourceRef
	lockPath                                string
	lockProposal                            LockProposal
	lockSourceRefs                          []provenance.SourceRef
	canonical                               []byte
}

type planWire struct {
	APIVersion     string            `json:"apiVersion"`
	Kind           string            `json:"kind"`
	RequestDigest  string            `json:"requestDigest"`
	SourceDigest   string            `json:"sourceDigest"`
	Sources        []planSourceWire  `json:"sources"`
	EntitySnapshot json.RawMessage   `json:"entitySnapshot"`
	CRUDSnapshot   json.RawMessage   `json:"crudSnapshot"`
	ProtoArtifact  protoArtifactWire `json:"protoArtifact"`
	LockProposal   lockProposalWire  `json:"lockProposal"`
	PlanDigest     string            `json:"planDigest,omitempty"`
}
type planSourceWire struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type protoArtifactWire struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Bytes       []byte   `json:"bytes"`
	Digest      string   `json:"digest"`
	MediaType   string   `json:"mediaType"`
	Owner       string   `json:"owner"`
	SourceRefs  []string `json:"sourceRefs"`
	StalePolicy string   `json:"stalePolicy"`
}
type lockProposalWire struct {
	Path       string          `json:"path"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	Changed    bool            `json:"changed"`
	Digest     string          `json:"digest,omitempty"`
	SourceRefs []string        `json:"sourceRefs"`
}
type sourceSetWire struct {
	APIVersion string           `json:"apiVersion"`
	Sources    []planSourceWire `json:"sources"`
}

func BuildPlan(entities entity.Document, spec Spec) (Plan, error) {
	entitySnapshot, err := entity.CanonicalJSON(entities)
	if err != nil {
		return Plan{}, buildError("document_state_invalid", "/entities")
	}
	document, proposal, err := Build(entities, spec)
	if err != nil {
		return Plan{}, err
	}
	crudSnapshot := document.CanonicalJSON()
	protoBytes, err := Render(document)
	if err != nil {
		return Plan{}, err
	}
	if err := compileProto(spec.ProtoArtifactPath, protoBytes); err != nil {
		return Plan{}, err
	}
	if spec.RequestDigest.String() == "" {
		return Plan{}, buildError("canonical_invalid", "/requestDigest")
	}
	if !validOutputPath(spec.ProtoArtifactPath) {
		return Plan{}, renderError("proto_artifact_path_invalid", "/protoArtifact/path")
	}
	if !validOutputPath(spec.LockPath) {
		return Plan{}, renderError("proto_artifact_path_invalid", "/lockProposal/path")
	}
	sources, protoSourceRefs, err := planSources(entities, spec)
	if err != nil {
		return Plan{}, err
	}
	sourceWire := make([]planSourceWire, len(sources))
	sourceRefs := make([]provenance.SourceRef, len(sources))
	for index, source := range sources {
		sourceWire[index] = planSourceWire{source.Ref.String(), source.Digest.String()}
		sourceRefs[index] = source.Ref
	}
	sourceCanonical, err := canonicalJSON(sourceSetWire{APIVersion: "nexa.dev/ent-graph-plan-source-set/v1", Sources: sourceWire})
	if err != nil {
		return Plan{}, buildError("canonical_invalid", "/sources")
	}
	state := &planState{requestDigest: spec.RequestDigest, sourceDigest: provenance.SHA256(sourceCanonical), sources: sources, entitySnapshot: append([]byte(nil), entitySnapshot...), crudSnapshot: append([]byte(nil), crudSnapshot...), protoID: "crud-proto." + spec.ServiceID, protoPath: spec.ProtoArtifactPath, protoBytes: append([]byte(nil), protoBytes...), protoDigest: provenance.SHA256(protoBytes), protoSourceRefs: append([]provenance.SourceRef(nil), protoSourceRefs...), lockPath: spec.LockPath, lockProposal: proposal, lockSourceRefs: append([]provenance.SourceRef(nil), sourceRefs...)}
	wire := planWireFromState(state)
	preimage, err := canonicalJSON(wire)
	if err != nil {
		return Plan{}, buildError("canonical_invalid", "/plan")
	}
	state.planDigest = provenance.SHA256(preimage)
	wire.PlanDigest = state.planDigest.String()
	state.canonical, err = canonicalJSON(wire)
	if err != nil {
		return Plan{}, buildError("canonical_invalid", "/plan")
	}
	return Plan{state: state}, nil
}

func (p Plan) Valid() bool { return p.state != nil }
func (p Plan) RequestDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.requestDigest
}
func (p Plan) SourceDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.sourceDigest
}
func (p Plan) PlanDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.planDigest
}
func (p Plan) Sources() []provenance.Source {
	if p.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), p.state.sources...)
}
func (p Plan) EntitySnapshot() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.entitySnapshot...)
}
func (p Plan) CRUDSnapshot() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.crudSnapshot...)
}
func (p Plan) ProtoID() string {
	if p.state == nil {
		return ""
	}
	return p.state.protoID
}
func (p Plan) ProtoPath() string {
	if p.state == nil {
		return ""
	}
	return p.state.protoPath
}
func (p Plan) ProtoBytes() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.protoBytes...)
}
func (p Plan) ProtoDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.protoDigest
}
func (p Plan) ProtoSourceRefs() []provenance.SourceRef {
	if p.state == nil {
		return nil
	}
	return append([]provenance.SourceRef(nil), p.state.protoSourceRefs...)
}
func (p Plan) LockPath() string {
	if p.state == nil {
		return ""
	}
	return p.state.lockPath
}
func (p Plan) LockProposal() LockProposal {
	if p.state == nil {
		return LockProposal{}
	}
	return p.state.lockProposal
}
func (p Plan) LockSourceRefs() []provenance.SourceRef {
	if p.state == nil {
		return nil
	}
	return append([]provenance.SourceRef(nil), p.state.lockSourceRefs...)
}
func (p Plan) CanonicalJSON() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.canonical...)
}

func planWireFromState(state *planState) planWire {
	sources := make([]planSourceWire, len(state.sources))
	for i, s := range state.sources {
		sources[i] = planSourceWire{s.Ref.String(), s.Digest.String()}
	}
	protoRefs := refsToStrings(state.protoSourceRefs)
	lockRefs := refsToStrings(state.lockSourceRefs)
	lock := lockProposalWire{Path: state.lockPath, Changed: state.lockProposal.Changed(), SourceRefs: lockRefs}
	if before := state.lockProposal.Before(); before != nil {
		lock.Before = before.CanonicalJSON()
	}
	after := state.lockProposal.After()
	if after.Valid() {
		lock.After = after.CanonicalJSON()
		lock.Digest = state.lockProposal.Digest().String()
	}
	return planWire{APIVersion: PlanAPIVersion, Kind: PlanKind, RequestDigest: state.requestDigest.String(), SourceDigest: state.sourceDigest.String(), Sources: sources, EntitySnapshot: state.entitySnapshot, CRUDSnapshot: state.crudSnapshot, ProtoArtifact: protoArtifactWire{ID: state.protoID, Path: state.protoPath, Bytes: state.protoBytes, Digest: state.protoDigest.String(), MediaType: ProtoMediaType, Owner: ProtoOwner, SourceRefs: protoRefs, StalePolicy: StaleDeleteIfUnmodified}, LockProposal: lock, PlanDigest: state.planDigest.String()}
}
func refsToStrings(values []provenance.SourceRef) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = v.String()
	}
	return result
}
func planSources(entities entity.Document, spec Spec) ([]provenance.Source, []provenance.SourceRef, error) {
	seen := map[string]provenance.Digest{}
	artifactSources := map[string]struct{}{}
	var result []provenance.Source
	add := func(source provenance.Source) error {
		key := source.Ref.String()
		if key == "" || source.Digest.String() == "" {
			return buildError("source_closure_invalid", "/sources")
		}
		if digest, ok := seen[key]; ok {
			if digest != source.Digest {
				return buildError("source_closure_invalid", "/sources")
			}
			return nil
		}
		seen[key] = source.Digest
		result = append(result, source)
		return nil
	}
	addArtifact := func(source provenance.Source) error {
		if err := add(source); err != nil {
			return err
		}
		artifactSources[source.Ref.String()] = struct{}{}
		return nil
	}
	for _, group := range [][]provenance.Source{entities.ExecutionModuleSources(), entities.Sources()} {
		for _, source := range group {
			if err := addArtifact(source); err != nil {
				return nil, nil, err
			}
		}
	}
	if spec.ExistingLockSource != nil {
		if err := add(*spec.ExistingLockSource); err != nil {
			return nil, nil, err
		}
	}
	if spec.PublishedArtifact != nil {
		if err := add(spec.PublishedArtifact.ManifestSource); err != nil {
			return nil, nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref.String() < result[j].Ref.String() })
	artifactRefs := make([]provenance.SourceRef, 0, len(artifactSources))
	for _, source := range result {
		if _, included := artifactSources[source.Ref.String()]; included {
			artifactRefs = append(artifactRefs, source.Ref)
		}
	}
	return result, artifactRefs, nil
}
func validOutputPath(path string) bool {
	ref, err := provenance.RepositoryRef(path, "")
	return err == nil && ref.Path() == path
}
func compileProto(path string, data []byte) error {
	if !validOutputPath(path) {
		return renderError("proto_artifact_path_invalid", "/protoArtifact/path")
	}
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{path: string(data), "nexa/protocol/v1/options.proto": string(genprotocol.OptionsProto())})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	files, err := compiler.Compile(context.Background(), path)
	if err != nil || len(files) != 1 {
		return compileError()
	}
	return nil
}

func PlansEqual(a, b Plan) bool {
	return a.state != nil && b.state != nil && bytes.Equal(a.state.canonical, b.state.canonical)
}
