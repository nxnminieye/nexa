package crudproto

import (
	"context"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/generation/internal/entipc"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/provenance"
)

type EntGraphPlan struct{ state *entGraphPlanState }
type VerifiedProtoArtifact struct{ state *verifiedProtoState }
type VerifiedLockProposal struct{ state *verifiedLockState }

type entGraphPlanState struct {
	requestDigest, sourceDigest, planDigest provenance.Digest
	moduleGraphDigest, buildInputDigest     provenance.Digest
	sources                                 []provenance.Source
	entitySnapshot                          entity.Snapshot
	crudSnapshot                            Snapshot
	canonical                               []byte
	hasCRUD                                 bool
	proto                                   verifiedProtoState
	lock                                    verifiedLockState
}

type verifiedProtoState struct {
	id, path, mediaType, owner string
	content                    []byte
	digest                     provenance.Digest
	sources                    []provenance.SourceRef
}

type verifiedLockState struct {
	id, path, owner string
	before          *provenance.Source
	after           []byte
	digest          provenance.Digest
	sources         []provenance.SourceRef
	changed         bool
}

func verifiedEntGraphPlanFromBuild(plan crudbuild.Plan) (EntGraphPlan, error) {
	if !plan.Valid() {
		return EntGraphPlan{}, newStateError("plan_state_invalid", "/plan")
	}
	snapshotSource, _ := provenance.ParseDomainSource("verified/crud-snapshot.json")
	snapshot, err := crudbuild.ParseSnapshot(snapshotSource, plan.CRUDSnapshot())
	if err != nil {
		return EntGraphPlan{}, wrapError(err)
	}
	entitySource, _ := provenance.ParseDomainSource("verified/entity-snapshot.json")
	entitySnapshot, err := entity.ParseSnapshot(entitySource, plan.EntitySnapshot())
	if err != nil {
		return EntGraphPlan{}, wrapError(err)
	}
	state := &entGraphPlanState{
		requestDigest: plan.RequestDigest(), sourceDigest: plan.SourceDigest(), planDigest: plan.PlanDigest(),
		sources: append([]provenance.Source(nil), plan.Sources()...), canonical: append([]byte(nil), plan.CanonicalJSON()...),
		hasCRUD: len(snapshot.Services()) != 0, entitySnapshot: entitySnapshot,
		crudSnapshot: Snapshot{state: snapshot},
		proto: verifiedProtoState{
			id: plan.ProtoID(), path: plan.ProtoPath(), mediaType: crudbuild.ProtoMediaType, owner: crudbuild.ProtoOwner,
			content: append([]byte(nil), plan.ProtoBytes()...), digest: plan.ProtoDigest(), sources: append([]provenance.SourceRef(nil), plan.ProtoSourceRefs()...),
		},
	}
	proposal := plan.LockProposal()
	state.lock = verifiedLockState{
		id: plan.ProtoID() + ".lock", path: plan.LockPath(), owner: crudbuild.ProtoOwner,
		digest: proposal.Digest(), sources: append([]provenance.SourceRef(nil), plan.LockSourceRefs()...), changed: proposal.Changed(),
	}
	if before := proposal.Before(); before != nil {
		canonical := before.CanonicalJSON()
		for _, source := range state.sources {
			if source.Ref.Path() == state.lock.path && source.Ref.Fragment() == "" && source.Digest == provenance.SHA256(canonical) {
				copyValue := source
				state.lock.before = &copyValue
				break
			}
		}
		if state.lock.before == nil {
			return EntGraphPlan{}, newStateError("lock_source_invalid", "/lockProposal/before")
		}
	}
	after := proposal.After()
	if after.Valid() {
		state.lock.after = append([]byte(nil), after.CanonicalJSON()...)
	}
	return EntGraphPlan{state: state}, nil
}

func verifiedEntGraphPlanFromSnapshot(plan entipc.PlanSnapshot) (EntGraphPlan, error) {
	if len(plan.CanonicalJSON()) == 0 {
		return EntGraphPlan{}, newStateError("plan_state_invalid", "/plan")
	}
	verifiedCRUD, err := plan.CRUDSnapshot()
	if err != nil {
		return EntGraphPlan{}, wrapError(err)
	}
	state := &entGraphPlanState{
		requestDigest: plan.RequestDigest(), sourceDigest: plan.SourceDigest(), planDigest: plan.PlanDigest(),
		moduleGraphDigest: plan.ModuleGraphDigest(), buildInputDigest: plan.BuildInputDigest(), crudSnapshot: Snapshot{state: verifiedCRUD},
		sources: append([]provenance.Source(nil), plan.Sources()...), canonical: append([]byte(nil), plan.CanonicalJSON()...), hasCRUD: plan.HasCRUD(), entitySnapshot: plan.EntitySnapshot(),
		proto: verifiedProtoState{
			id: plan.ProtoID(), path: plan.ProtoPath(), mediaType: plan.ProtoMediaType(), owner: plan.ProtoOwner(),
			content: append([]byte(nil), plan.ProtoBytes()...), digest: plan.ProtoDigest(), sources: append([]provenance.SourceRef(nil), plan.ProtoSourceRefs()...),
		},
		lock: verifiedLockState{
			id: plan.ProtoID() + ".lock", path: plan.LockPath(), owner: crudbuild.ProtoOwner, after: append([]byte(nil), plan.LockAfter()...),
			digest: plan.LockDigest(), sources: append([]provenance.SourceRef(nil), plan.LockSourceRefs()...), changed: plan.LockChanged(),
		},
	}
	if before := plan.LockBefore(); len(before) != 0 {
		for _, source := range state.sources {
			if source.Ref.Path() == state.lock.path && source.Ref.Fragment() == "" && source.Digest == provenance.SHA256(before) {
				copyValue := source
				state.lock.before = &copyValue
				break
			}
		}
		if state.lock.before == nil {
			return EntGraphPlan{}, newStateError("lock_source_invalid", "/lockProposal/before")
		}
	}
	return EntGraphPlan{state: state}, nil
}

func (p EntGraphPlan) HasCRUD() bool { return p.state != nil && p.state.hasCRUD }

// ServiceID returns the service identity already bound into the verified Proto artifact.
func (p EntGraphPlan) ServiceID() (string, error) {
	if p.state == nil || !strings.HasPrefix(p.state.proto.id, "crud-proto.") {
		return "", newStateError("plan_state_invalid", "/plan")
	}
	value := strings.TrimPrefix(p.state.proto.id, "crud-proto.")
	if value == "" {
		return "", newStateError("plan_state_invalid", "/plan")
	}
	return value, nil
}

func (p EntGraphPlan) EntitySnapshot() (entity.Snapshot, error) {
	if p.state == nil || p.state.entitySnapshot.APIVersion() == "" {
		return entity.Snapshot{}, newStateError("plan_state_invalid", "/plan")
	}
	return p.state.entitySnapshot, nil
}

func (p EntGraphPlan) CRUDSnapshot() (Snapshot, error) {
	if p.state == nil || p.state.crudSnapshot.APIVersion() == "" {
		return Snapshot{}, newStateError("plan_state_invalid", "/plan")
	}
	return p.state.crudSnapshot, nil
}

func (p EntGraphPlan) ModuleGraphDigest() (provenance.Digest, error) {
	if p.state == nil || !validProjectionDigest(p.state.moduleGraphDigest) {
		return provenance.Digest{}, newStateError("plan_state_invalid", "/plan")
	}
	return p.state.moduleGraphDigest, nil
}

func (p EntGraphPlan) BuildInputDigest() (provenance.Digest, error) {
	if p.state == nil || !validProjectionDigest(p.state.buildInputDigest) {
		return provenance.Digest{}, newStateError("plan_state_invalid", "/plan")
	}
	return p.state.buildInputDigest, nil
}

func validProjectionDigest(value provenance.Digest) bool {
	_, err := provenance.ParseDigest(value.String())
	return err == nil
}

func (p EntGraphPlan) CanonicalJSON() ([]byte, error) {
	if p.state == nil || len(p.state.canonical) == 0 {
		return nil, newStateError("plan_state_invalid", "/plan")
	}
	return append([]byte(nil), p.state.canonical...), nil
}

func (p EntGraphPlan) RequestDigest() (provenance.Digest, error) {
	if p.state == nil {
		return provenance.Digest{}, newStateError("plan_state_invalid", "/plan")
	}
	return p.state.requestDigest, nil
}

func (p EntGraphPlan) SourceDigest() (provenance.Digest, error) {
	if p.state == nil {
		return provenance.Digest{}, newStateError("plan_state_invalid", "/plan")
	}
	return p.state.sourceDigest, nil
}

func (p EntGraphPlan) PlanDigest() (provenance.Digest, error) {
	if p.state == nil {
		return provenance.Digest{}, newStateError("plan_state_invalid", "/plan")
	}
	return p.state.planDigest, nil
}

func (p EntGraphPlan) Sources() ([]provenance.Source, error) {
	if p.state == nil {
		return nil, newStateError("plan_state_invalid", "/plan")
	}
	return append([]provenance.Source(nil), p.state.sources...), nil
}

func (p EntGraphPlan) ProtoArtifact() (VerifiedProtoArtifact, error) {
	if p.state == nil || !p.state.hasCRUD {
		return VerifiedProtoArtifact{}, newStateError("artifact_absent", "/protoArtifact")
	}
	return VerifiedProtoArtifact{state: &p.state.proto}, nil
}

func (p EntGraphPlan) LockProposal() (VerifiedLockProposal, error) {
	if p.state == nil {
		return VerifiedLockProposal{}, newStateError("plan_state_invalid", "/plan")
	}
	return VerifiedLockProposal{state: &p.state.lock}, nil
}

func (p EntGraphPlan) TransactionInputs(emit func(string, []byte) error) ([]transaction.ArtifactInput, []transaction.ControlSourceMutation, error) {
	if p.state == nil {
		return nil, nil, newStateError("plan_state_invalid", "/plan")
	}
	if !p.state.hasCRUD {
		return nil, nil, nil
	}
	if emit == nil {
		return nil, nil, newStateError("candidate_emitter_invalid", "/emit")
	}
	if err := emit(p.state.proto.path, p.state.proto.content); err != nil {
		return nil, nil, &Error{owner: "crud-proto", code: "crud_build_invalid", stage: "staging", reason: "candidate_emit_failed", pointer: "/protoArtifact", cause: err}
	}
	artifactValue, err := (VerifiedProtoArtifact{state: &p.state.proto}).ArtifactInput()
	if err != nil {
		return nil, nil, err
	}
	mutation, changed, err := (VerifiedLockProposal{state: &p.state.lock}).ControlSourceMutation()
	if err != nil {
		return nil, nil, err
	}
	controls := []transaction.ControlSourceMutation{}
	if changed {
		controls = append(controls, mutation)
	}
	return []transaction.ArtifactInput{artifactValue}, controls, nil
}

func (p EntGraphPlan) StaleOwnershipProbes() ([]transaction.OwnershipProbe, error) {
	if p.state == nil || p.state.proto.id == "" || p.state.proto.path == "" {
		return nil, newStateError("plan_state_invalid", "/plan")
	}
	return []transaction.OwnershipProbe{protoOwnershipProbe{id: p.state.proto.id, path: p.state.proto.path}}, nil
}

func (a VerifiedProtoArtifact) ID() (string, error) {
	if a.state == nil {
		return "", newStateError("artifact_state_invalid", "/protoArtifact")
	}
	return a.state.id, nil
}
func (a VerifiedProtoArtifact) Path() (string, error) {
	if a.state == nil {
		return "", newStateError("artifact_state_invalid", "/protoArtifact")
	}
	return a.state.path, nil
}
func (a VerifiedProtoArtifact) Bytes() ([]byte, error) {
	if a.state == nil {
		return nil, newStateError("artifact_state_invalid", "/protoArtifact")
	}
	return append([]byte(nil), a.state.content...), nil
}
func (a VerifiedProtoArtifact) Digest() (provenance.Digest, error) {
	if a.state == nil {
		return provenance.Digest{}, newStateError("artifact_state_invalid", "/protoArtifact")
	}
	return a.state.digest, nil
}
func (a VerifiedProtoArtifact) MediaType() (string, error) {
	if a.state == nil {
		return "", newStateError("artifact_state_invalid", "/protoArtifact")
	}
	return a.state.mediaType, nil
}
func (a VerifiedProtoArtifact) Owner() (string, error) {
	if a.state == nil {
		return "", newStateError("artifact_state_invalid", "/protoArtifact")
	}
	return a.state.owner, nil
}

func (a VerifiedProtoArtifact) ArtifactInput() (transaction.ArtifactInput, error) {
	if a.state == nil || a.state.id == "" || a.state.path == "" || a.state.digest != provenance.SHA256(a.state.content) || a.state.owner != crudbuild.ProtoOwner {
		return transaction.ArtifactInput{}, newStateError("artifact_state_invalid", "/protoArtifact")
	}
	return transaction.ArtifactInput{
		ID: a.state.id, Path: a.state.path, Owner: a.state.owner, Digest: a.state.digest,
		Sources: append([]provenance.SourceRef(nil), a.state.sources...), StalePolicy: artifact.StaleDeleteIfUnmodified,
		Probe: protoOwnershipProbe{id: a.state.id, path: a.state.path},
	}, nil
}

func (p VerifiedLockProposal) Changed() (bool, error) {
	if p.state == nil {
		return false, newStateError("lock_state_invalid", "/lockProposal")
	}
	return p.state.changed, nil
}
func (p VerifiedLockProposal) Digest() (provenance.Digest, error) {
	if p.state == nil {
		return provenance.Digest{}, newStateError("lock_state_invalid", "/lockProposal")
	}
	return p.state.digest, nil
}

func (p VerifiedLockProposal) ControlSourceMutation() (transaction.ControlSourceMutation, bool, error) {
	if p.state == nil {
		return transaction.ControlSourceMutation{}, false, newStateError("lock_state_invalid", "/lockProposal")
	}
	if !p.state.changed {
		return transaction.ControlSourceMutation{}, false, nil
	}
	mutation, err := transaction.NewCompatibilityLockMutation(transaction.CompatibilityLockMutationSpec{
		ID: p.state.id, Path: p.state.path, Owner: p.state.owner, Before: p.state.before,
		After: append([]byte(nil), p.state.after...), AfterDigest: p.state.digest, Sources: append([]provenance.SourceRef(nil), p.state.sources...),
	})
	if err != nil {
		return transaction.ControlSourceMutation{}, false, newStateError("transaction_projection_invalid", "/lockProposal")
	}
	return mutation, true, nil
}

type protoOwnershipProbe struct{ id, path string }

func (p protoOwnershipProbe) Inspect(path string, content []byte, expected transaction.Ownership) (bool, error) {
	if path != p.path || expected.GeneratorID != "crud-proto" || expected.ArtifactID != p.id {
		return false, nil
	}
	if _, err := provenance.ParseDigest(expected.InputDigest.String()); err != nil {
		return false, nil
	}
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{
		path:                             string(content),
		"nexa/protocol/v1/options.proto": string(genprotocol.OptionsProto()),
	})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	files, err := compiler.Compile(context.Background(), path)
	return err == nil && len(files) == 1, nil
}
