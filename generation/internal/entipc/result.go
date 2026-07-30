package entipc

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"unicode/utf8"

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	ResultAPIVersion = "nexa.dev/ent-graph-result/v1"
	ResultKind       = "EntGraphResult"
)

type ResultInput struct{ state *resultInputState }
type resultInputState struct {
	plan   crudbuild.Plan
	domain *DomainFailure
}
type ResultSnapshot struct{ state *resultSnapshotState }
type resultSnapshotState struct {
	plan      *PlanSnapshot
	domain    *DomainFailure
	canonical []byte
}
type PlanSnapshot struct{ state *planSnapshotState }
type planSnapshotState struct {
	canonical                               []byte
	requestDigest, sourceDigest, planDigest provenance.Digest
	moduleGraphDigest, buildInputDigest     provenance.Digest
	sources                                 []provenance.Source
	entitySnapshot                          entity.Snapshot
	crudSnapshot                            crudbuild.Snapshot
	hasCRUD                                 bool
	protoID, protoPath                      string
	protoBytes                              []byte
	protoDigest                             provenance.Digest
	protoMediaType, protoOwner              string
	protoSourceRefs                         []provenance.SourceRef
	protoStalePolicy                        string
	lockPath                                string
	lockBefore, lockAfter                   []byte
	lockChanged                             bool
	lockDigest                              provenance.Digest
	lockSourceRefs                          []provenance.SourceRef
}
type DomainFailure struct{ owner, code, reason, pointer, source string }

type resultWire struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Plan       json.RawMessage `json:"plan,omitempty"`
	Error      *domainWire     `json:"error,omitempty"`
}
type domainWire struct {
	Owner   string `json:"owner"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	Pointer string `json:"pointer"`
	Source  string `json:"source"`
}
type readPlanWire struct {
	APIVersion     string           `json:"apiVersion"`
	Kind           string           `json:"kind"`
	RequestDigest  string           `json:"requestDigest"`
	SourceDigest   string           `json:"sourceDigest"`
	Sources        []readSourceWire `json:"sources"`
	EntitySnapshot json.RawMessage  `json:"entitySnapshot"`
	CRUDSnapshot   json.RawMessage  `json:"crudSnapshot"`
	ProtoArtifact  readProtoWire    `json:"protoArtifact"`
	LockProposal   readLockWire     `json:"lockProposal"`
	PlanDigest     string           `json:"planDigest"`
}
type readSourceWire struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type readProtoWire struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Bytes       []byte   `json:"bytes"`
	Digest      string   `json:"digest"`
	MediaType   string   `json:"mediaType"`
	Owner       string   `json:"owner"`
	SourceRefs  []string `json:"sourceRefs"`
	StalePolicy string   `json:"stalePolicy"`
}
type readLockWire struct {
	Path       string          `json:"path"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	Changed    bool            `json:"changed"`
	Digest     string          `json:"digest,omitempty"`
	SourceRefs []string        `json:"sourceRefs"`
}

func ResultFromPlan(plan crudbuild.Plan) (ResultInput, error) {
	if !plan.Valid() || len(plan.CanonicalJSON()) == 0 {
		return ResultInput{}, resultError("plan_invalid", "/plan", "")
	}
	return ResultInput{state: &resultInputState{plan: plan}}, nil
}

func EncodeResult(request Request, input ResultInput) ([]byte, error) {
	if request.state == nil {
		return nil, resultError("request_context_invalid", "/request", "")
	}
	if input.state == nil {
		return nil, resultError("result_branch_invalid", "", "")
	}
	wire := resultWire{APIVersion: ResultAPIVersion, Kind: ResultKind}
	if input.state.plan.Valid() {
		if input.state.domain != nil {
			return nil, resultError("result_branch_invalid", "", "")
		}
		if input.state.plan.RequestDigest() != request.state.requestDigest {
			return nil, resultError("request_context_invalid", "/request", "")
		}
		wire.Plan = input.state.plan.CanonicalJSON()
	} else if input.state.domain != nil {
		wire.Error = &domainWire{input.state.domain.owner, input.state.domain.code, input.state.domain.reason, input.state.domain.pointer, input.state.domain.source}
	} else {
		return nil, resultError("result_branch_invalid", "", "")
	}
	encoded, err := canonicalJSON(wire)
	if err != nil {
		return nil, resultError("canonical_invalid", "", "")
	}
	return encoded, nil
}

func ParseResult(source provenance.DomainSource, request Request, data []byte) (ResultSnapshot, error) {
	if source.String() == "" {
		return ResultSnapshot{}, resultError("document_invalid", "", "")
	}
	if request.state == nil {
		return ResultSnapshot{}, resultError("request_context_invalid", "/request", source.String())
	}
	if len(data) == 0 {
		return ResultSnapshot{}, resultError("result_empty", "/stdout", source.String())
	}
	if !utf8.Valid(data) {
		return ResultSnapshot{}, resultError("unicode_invalid", "", source.String())
	}
	document, err := strictdoc.ParseJSON(source.String(), data)
	if err != nil {
		return ResultSnapshot{}, projectResultDocumentError(err, source.String())
	}
	root, ok := decodeObject(document.JSON())
	if !ok {
		return ResultSnapshot{}, resultError("document_type_invalid", "", source.String())
	}
	if reason, pointer := exactObjectIssue(root, "", []string{"apiVersion", "kind", "plan", "error"}, []string{"apiVersion", "kind"}); reason != "" {
		return ResultSnapshot{}, resultError(reason, pointer, source.String())
	}
	api, apiOK := root["apiVersion"].(string)
	kind, kindOK := root["kind"].(string)
	if !apiOK {
		return ResultSnapshot{}, resultError("document_type_invalid", "/apiVersion", source.String())
	}
	if !kindOK {
		return ResultSnapshot{}, resultError("document_type_invalid", "/kind", source.String())
	}
	if api != ResultAPIVersion {
		return ResultSnapshot{}, resultError("version_unsupported", "/apiVersion", source.String())
	}
	if kind != ResultKind {
		return ResultSnapshot{}, resultError("kind_invalid", "/kind", source.String())
	}
	planRaw, hasPlan := root["plan"]
	domainRaw, hasDomain := root["error"]
	if hasPlan == hasDomain {
		return ResultSnapshot{}, resultError("result_branch_invalid", "", source.String())
	}
	state := &resultSnapshotState{}
	if hasPlan {
		if planRaw == nil {
			return ResultSnapshot{}, resultError("result_branch_invalid", "/plan", source.String())
		}
		planBytes, err := canonicalJSON(planRaw)
		if err != nil {
			return ResultSnapshot{}, resultError("plan_invalid", "/plan", source.String())
		}
		plan, err := parsePlan(source, request, planBytes)
		if err != nil {
			return ResultSnapshot{}, err
		}
		state.plan = &plan
	} else {
		if domainRaw == nil {
			return ResultSnapshot{}, resultError("result_branch_invalid", "/error", source.String())
		}
		failure, err := parseDomainFailure(domainRaw, request, source.String())
		if err != nil {
			return ResultSnapshot{}, err
		}
		state.domain = &failure
	}
	state.canonical = append([]byte(nil), data...)
	canonical, err := canonicalResultState(state)
	if err != nil || !bytes.Equal(canonical, data) {
		return ResultSnapshot{}, resultError("canonical_invalid", "", source.String())
	}
	return ResultSnapshot{state: state}, nil
}

func (r ResultSnapshot) Plan() (PlanSnapshot, bool) {
	if r.state == nil || r.state.plan == nil {
		return PlanSnapshot{}, false
	}
	return *r.state.plan, true
}
func (r ResultSnapshot) DomainFailure() (DomainFailure, bool) {
	if r.state == nil || r.state.domain == nil {
		return DomainFailure{}, false
	}
	return *r.state.domain, true
}
func CanonicalResult(result ResultSnapshot) ([]byte, error) {
	if result.state == nil {
		return nil, resultError("canonical_invalid", "", "")
	}
	return canonicalResultState(result.state)
}
func (p PlanSnapshot) CanonicalJSON() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.canonical...)
}
func (p PlanSnapshot) RequestDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.requestDigest
}
func (p PlanSnapshot) PlanDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.planDigest
}
func (p PlanSnapshot) SourceDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.sourceDigest
}
func (p PlanSnapshot) ModuleGraphDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.moduleGraphDigest
}
func (p PlanSnapshot) BuildInputDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.buildInputDigest
}
func (p PlanSnapshot) EntitySnapshot() entity.Snapshot {
	if p.state == nil {
		return entity.Snapshot{}
	}
	return p.state.entitySnapshot
}
func (p PlanSnapshot) CRUDSnapshot() (crudbuild.Snapshot, error) {
	if p.state == nil || !p.state.crudSnapshot.Valid() {
		return crudbuild.Snapshot{}, resultError("plan_invalid", "/plan/crudSnapshot", "")
	}
	return p.state.crudSnapshot, nil
}
func (p PlanSnapshot) Sources() []provenance.Source {
	if p.state == nil {
		return nil
	}
	return append([]provenance.Source(nil), p.state.sources...)
}
func (p PlanSnapshot) HasCRUD() bool { return p.state != nil && p.state.hasCRUD }
func (p PlanSnapshot) ProtoID() string {
	if p.state == nil {
		return ""
	}
	return p.state.protoID
}
func (p PlanSnapshot) ProtoPath() string {
	if p.state == nil {
		return ""
	}
	return p.state.protoPath
}
func (p PlanSnapshot) ProtoBytes() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.protoBytes...)
}
func (p PlanSnapshot) ProtoDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.protoDigest
}
func (p PlanSnapshot) ProtoMediaType() string {
	if p.state == nil {
		return ""
	}
	return p.state.protoMediaType
}
func (p PlanSnapshot) ProtoOwner() string {
	if p.state == nil {
		return ""
	}
	return p.state.protoOwner
}
func (p PlanSnapshot) ProtoSourceRefs() []provenance.SourceRef {
	if p.state == nil {
		return nil
	}
	return append([]provenance.SourceRef(nil), p.state.protoSourceRefs...)
}
func (p PlanSnapshot) ProtoStalePolicy() string {
	if p.state == nil {
		return ""
	}
	return p.state.protoStalePolicy
}
func (p PlanSnapshot) LockPath() string {
	if p.state == nil {
		return ""
	}
	return p.state.lockPath
}
func (p PlanSnapshot) LockBefore() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.lockBefore...)
}
func (p PlanSnapshot) LockAfter() []byte {
	if p.state == nil {
		return nil
	}
	return append([]byte(nil), p.state.lockAfter...)
}
func (p PlanSnapshot) LockChanged() bool { return p.state != nil && p.state.lockChanged }
func (p PlanSnapshot) LockDigest() provenance.Digest {
	if p.state == nil {
		return provenance.Digest{}
	}
	return p.state.lockDigest
}
func (p PlanSnapshot) LockSourceRefs() []provenance.SourceRef {
	if p.state == nil {
		return nil
	}
	return append([]provenance.SourceRef(nil), p.state.lockSourceRefs...)
}

func (d DomainFailure) Owner() string   { return d.owner }
func (d DomainFailure) Code() string    { return d.code }
func (d DomainFailure) Reason() string  { return d.reason }
func (d DomainFailure) Pointer() string { return d.pointer }
func (d DomainFailure) Source() string  { return d.source }

func ResultFromDomainError(err error) (ResultInput, bool, error) {
	if projection, ok := entity.ProjectEntHelperError(err); ok {
		return domainResult("entity", projection.Code(), projection.Reason(), projection.Pointer(), projection.Source()), true, nil
	}
	if projection, ok := crudbuild.ProjectEntHelperError(err); ok {
		return domainResult("crudproto", projection.Code(), projection.Reason(), projection.Pointer(), projection.Source()), true, nil
	}
	return ResultInput{}, false, nil
}
func domainResult(owner, code, reason, pointer, source string) ResultInput {
	failure := &DomainFailure{owner: owner, code: code, reason: reason, pointer: pointer, source: source}
	return ResultInput{state: &resultInputState{domain: failure}}
}

func parsePlan(source provenance.DomainSource, request Request, data []byte) (PlanSnapshot, error) {
	var root map[string]any
	if json.Unmarshal(data, &root) != nil || root == nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan", source.String())
	}
	required := []string{"apiVersion", "kind", "requestDigest", "sourceDigest", "sources", "entitySnapshot", "crudSnapshot", "protoArtifact", "lockProposal", "planDigest"}
	if reason, pointer := exactObjectIssue(root, "/plan", required, required); reason != "" {
		return PlanSnapshot{}, resultError("plan_invalid", pointer, source.String())
	}
	var wire readPlanWire
	if json.Unmarshal(data, &wire) != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan", source.String())
	}
	if wire.APIVersion != crudbuild.PlanAPIVersion || wire.Kind != crudbuild.PlanKind {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan", source.String())
	}
	requestDigest, err := provenance.ParseDigest(wire.RequestDigest)
	if err != nil || requestDigest != request.state.requestDigest {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/requestDigest", source.String())
	}
	sourceDigest, err := provenance.ParseDigest(wire.SourceDigest)
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/sourceDigest", source.String())
	}
	sources, err := parsePlanSources(wire.Sources)
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/sources", source.String())
	}
	sourceSet := struct {
		APIVersion string           `json:"apiVersion"`
		Sources    []readSourceWire `json:"sources"`
	}{"nexa.dev/ent-graph-plan-source-set/v1", wire.Sources}
	sourceJSON, _ := canonicalJSON(sourceSet)
	if provenance.SHA256(sourceJSON) != sourceDigest {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/sourceDigest", source.String())
	}
	entitySource, _ := provenance.ParseDomainSource("stdout/entity-snapshot.json")
	entitySnapshot, err := entity.ParseSnapshot(entitySource, wire.EntitySnapshot)
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/entitySnapshot", source.String())
	}
	expectedSources, err := requestPlanSources(request, entitySnapshot.ProjectedSources())
	if err != nil || !equalPlanSources(sources, expectedSources) {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/sources", source.String())
	}
	artifactSources, err := requestArtifactSources(request, entitySnapshot.ProjectedSources())
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/protoArtifact/sourceRefs", source.String())
	}
	crudSource, _ := provenance.ParseDomainSource("stdout/crud-snapshot.json")
	crudSnapshot, err := crudbuild.ParseSnapshot(crudSource, wire.CRUDSnapshot)
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/crudSnapshot", source.String())
	}
	if reason, pointer := validateProtoArtifact(wire.ProtoArtifact, artifactSources); reason != "" {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/protoArtifact"+pointer, source.String())
	}
	if reason, pointer := validateLockProposal(wire.LockProposal, sources); reason != "" {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/lockProposal"+pointer, source.String())
	}
	planDigest, err := provenance.ParseDigest(wire.PlanDigest)
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/planDigest", source.String())
	}
	delete(root, "planDigest")
	preimage, err := canonicalJSON(root)
	if err != nil || provenance.SHA256(preimage) != planDigest {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/planDigest", source.String())
	}
	canonical, err := canonicalJSON(mapWithPlanDigest(root, wire.PlanDigest))
	if err != nil || !bytes.Equal(canonical, data) {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan", source.String())
	}
	buildSpec, err := request.BuildSpec()
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/requestDigest", source.String())
	}
	rebuilt, err := crudbuild.RebuildPlanFromSnapshot(entitySnapshot, buildSpec)
	if err != nil {
		return PlanSnapshot{}, resultError("plan_invalid", "/plan/crudSnapshot", source.String())
	}
	if pointer := requestBoundPlanMismatch(wire, rebuilt); pointer != "" {
		return PlanSnapshot{}, resultError("plan_invalid", pointer, source.String())
	}
	protoDigest, _ := provenance.ParseDigest(wire.ProtoArtifact.Digest)
	lockDigest, _ := provenance.ParseDigest(wire.LockProposal.Digest)
	return PlanSnapshot{state: &planSnapshotState{
		canonical: append([]byte(nil), data...), requestDigest: requestDigest, sourceDigest: sourceDigest, planDigest: planDigest, sources: sources,
		moduleGraphDigest: request.state.moduleGraphDigest, buildInputDigest: request.state.buildInputDigest, entitySnapshot: entitySnapshot, crudSnapshot: crudSnapshot,
		hasCRUD: len(crudSnapshot.Services()) != 0,
		protoID: wire.ProtoArtifact.ID, protoPath: wire.ProtoArtifact.Path, protoBytes: append([]byte(nil), wire.ProtoArtifact.Bytes...), protoDigest: protoDigest,
		protoMediaType: wire.ProtoArtifact.MediaType, protoOwner: wire.ProtoArtifact.Owner, protoSourceRefs: sourceRefsFromStrings(wire.ProtoArtifact.SourceRefs), protoStalePolicy: wire.ProtoArtifact.StalePolicy,
		lockPath: wire.LockProposal.Path, lockBefore: append([]byte(nil), wire.LockProposal.Before...), lockAfter: append([]byte(nil), wire.LockProposal.After...),
		lockChanged: wire.LockProposal.Changed, lockDigest: lockDigest, lockSourceRefs: sourceRefsFromStrings(wire.LockProposal.SourceRefs),
	}}, nil
}

func sourceRefsFromStrings(values []string) []provenance.SourceRef {
	result := make([]provenance.SourceRef, len(values))
	for index, value := range values {
		result[index], _ = provenance.ParseSourceRef(value)
	}
	return result
}

func requestBoundPlanMismatch(wire readPlanWire, rebuilt crudbuild.Plan) string {
	if !bytes.Equal(wire.CRUDSnapshot, rebuilt.CRUDSnapshot()) {
		return "/plan/crudSnapshot"
	}
	if wire.ProtoArtifact.ID != rebuilt.ProtoID() {
		return "/plan/protoArtifact/id"
	}
	if wire.ProtoArtifact.Path != rebuilt.ProtoPath() {
		return "/plan/protoArtifact/path"
	}
	if !bytes.Equal(wire.ProtoArtifact.Bytes, rebuilt.ProtoBytes()) {
		return "/plan/protoArtifact/bytes"
	}
	if wire.ProtoArtifact.Digest != rebuilt.ProtoDigest().String() {
		return "/plan/protoArtifact/digest"
	}
	if wire.LockProposal.Path != rebuilt.LockPath() {
		return "/plan/lockProposal/path"
	}
	proposal := rebuilt.LockProposal()
	before := proposal.Before()
	if before == nil && len(wire.LockProposal.Before) != 0 || before != nil && !bytes.Equal(wire.LockProposal.Before, before.CanonicalJSON()) {
		return "/plan/lockProposal/before"
	}
	after := proposal.After()
	if !after.Valid() && len(wire.LockProposal.After) != 0 || after.Valid() && !bytes.Equal(wire.LockProposal.After, after.CanonicalJSON()) {
		return "/plan/lockProposal/after"
	}
	if wire.LockProposal.Changed != proposal.Changed() {
		return "/plan/lockProposal/changed"
	}
	wantDigest := ""
	if after.Valid() {
		wantDigest = proposal.Digest().String()
	}
	if wire.LockProposal.Digest != wantDigest {
		return "/plan/lockProposal/digest"
	}
	return ""
}

func validateProtoArtifact(w readProtoWire, sources []provenance.Source) (string, string) {
	if w.ID == "" || !validOutputPathIPC(w.Path) || w.MediaType != crudbuild.ProtoMediaType || w.Owner != crudbuild.ProtoOwner || w.StalePolicy != crudbuild.StaleDeleteIfUnmodified {
		return "proto_artifact_invalid", ""
	}
	digest, err := provenance.ParseDigest(w.Digest)
	if err != nil || digest != provenance.SHA256(w.Bytes) {
		return "proto_artifact_invalid", "/digest"
	}
	if !validSourceRefs(w.SourceRefs, sources) {
		return "proto_artifact_invalid", "/sourceRefs"
	}
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{w.Path: string(w.Bytes)})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	files, err := compiler.Compile(context.Background(), w.Path)
	if err != nil || len(files) != 1 {
		return "proto_artifact_invalid", "/bytes"
	}
	return "", ""
}
func validateLockProposal(w readLockWire, sources []provenance.Source) (string, string) {
	if !validOutputPathIPC(w.Path) || !validSourceRefs(w.SourceRefs, sources) {
		return "lock_proposal_invalid", ""
	}
	lockSource, _ := provenance.ParseDomainSource("stdout/crud-lock.json")
	if len(w.Before) > 0 {
		if _, err := crudbuild.ParseLock(lockSource, w.Before); err != nil {
			return "lock_proposal_invalid", "/before"
		}
	}
	if len(w.After) > 0 {
		lock, err := crudbuild.ParseLock(lockSource, w.After)
		if err != nil {
			return "lock_proposal_invalid", "/after"
		}
		digest, err := provenance.ParseDigest(w.Digest)
		if err != nil || digest != provenance.SHA256(lock.CanonicalJSON()) {
			return "lock_proposal_invalid", "/digest"
		}
	} else if w.Digest != "" {
		return "lock_proposal_invalid", "/digest"
	}
	return "", ""
}
func parsePlanSources(values []readSourceWire) ([]provenance.Source, error) {
	result := make([]provenance.Source, len(values))
	for i, value := range values {
		ref, err := provenance.ParseSourceRef(value.Ref)
		if err != nil {
			return nil, err
		}
		digest, err := provenance.ParseDigest(value.Digest)
		if err != nil {
			return nil, err
		}
		result[i] = provenance.Source{Ref: ref, Digest: digest}
		if i > 0 && result[i-1].Ref.String() >= ref.String() {
			return nil, errResult
		}
	}
	return result, nil
}
func requestPlanSources(request Request, projected []provenance.Source) ([]provenance.Source, error) {
	values := append([]provenance.Source(nil), request.state.moduleSources...)
	values = append(values, projected...)
	if request.state.existingLock != nil {
		values = append(values, request.state.existingLock.Source)
	}
	if request.state.publishedArtifact != nil {
		values = append(values, request.state.publishedArtifact.ManifestSource)
	}
	return normalizePlanSourceClosure(values)
}
func requestArtifactSources(request Request, projected []provenance.Source) ([]provenance.Source, error) {
	values := append([]provenance.Source(nil), request.state.moduleSources...)
	values = append(values, projected...)
	return normalizePlanSourceClosure(values)
}
func normalizePlanSourceClosure(values []provenance.Source) ([]provenance.Source, error) {
	sort.Slice(values, func(i, j int) bool { return values[i].Ref.String() < values[j].Ref.String() })
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1].Ref != value.Ref {
			result = append(result, value)
			continue
		}
		if result[len(result)-1].Digest != value.Digest {
			return nil, errResult
		}
	}
	return result, nil
}
func equalPlanSources(left, right []provenance.Source) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
func validSourceRefs(refs []string, sources []provenance.Source) bool {
	if len(refs) != len(sources) || !sort.StringsAreSorted(refs) {
		return false
	}
	for index, value := range refs {
		if value != sources[index].Ref.String() {
			return false
		}
	}
	return true
}
func validOutputPathIPC(path string) bool {
	ref, err := provenance.RepositoryRef(path, "")
	return err == nil && ref.Path() == path
}
func mapWithPlanDigest(root map[string]any, digest string) map[string]any {
	copyValue := map[string]any{}
	for key, value := range root {
		copyValue[key] = value
	}
	copyValue["planDigest"] = digest
	return copyValue
}
func canonicalResultState(state *resultSnapshotState) ([]byte, error) {
	wire := resultWire{APIVersion: ResultAPIVersion, Kind: ResultKind}
	if state.plan != nil {
		wire.Plan = state.plan.CanonicalJSON()
	} else if state.domain != nil {
		wire.Error = &domainWire{state.domain.owner, state.domain.code, state.domain.reason, state.domain.pointer, state.domain.source}
	} else {
		return nil, resultError("result_branch_invalid", "", "")
	}
	return canonicalJSON(wire)
}
func projectResultDocumentError(err error, source string) error {
	if typed, ok := err.(*strictdoc.Error); ok {
		return resultError(typed.Code, typed.Pointer, source)
	}
	return resultError("document_invalid", "", source)
}

func parseDomainFailure(raw any, request Request, source string) (DomainFailure, error) {
	value, ok := raw.(map[string]any)
	if !ok {
		return DomainFailure{}, resultError("domain_error_invalid", "/error", source)
	}
	members := []string{"owner", "code", "reason", "pointer", "source"}
	if reason, pointer := exactObjectIssue(value, "/error", members, members); reason != "" {
		return DomainFailure{}, resultError("domain_error_invalid", pointer, source)
	}
	stringsByName := map[string]string{}
	for _, name := range members {
		item, ok := value[name].(string)
		if !ok {
			return DomainFailure{}, resultError("domain_error_invalid", "/error/"+name, source)
		}
		stringsByName[name] = item
	}
	owner, code, reason, pointer, domainSource := stringsByName["owner"], stringsByName["code"], stringsByName["reason"], stringsByName["pointer"], stringsByName["source"]
	switch owner {
	case "entity":
		_, validation := entity.ParseEntHelperErrorProjection(code, reason, pointer, domainSource)
		if validation != nil {
			return DomainFailure{}, resultError(domainReasonFromEntityField(validation.Field()), "/error/"+string(validation.Field()), source)
		}
		if domainSource != "" && domainSource != request.state.schemaDir.String() {
			return DomainFailure{}, resultError("domain_source_mismatch", "/error/source", source)
		}
	case "crudproto":
		_, validation := crudbuild.ParseEntHelperErrorProjection(code, reason, pointer, domainSource)
		if validation != nil {
			return DomainFailure{}, resultError(domainReasonFromCRUDField(validation.Field()), "/error/"+string(validation.Field()), source)
		}
	default:
		return DomainFailure{}, resultError("domain_owner_invalid", "/error/owner", source)
	}
	return DomainFailure{owner: owner, code: code, reason: reason, pointer: pointer, source: domainSource}, nil
}
func domainReasonFromEntityField(field entity.EntHelperErrorField) string {
	switch field {
	case entity.EntHelperErrorFieldCode:
		return "domain_code_invalid"
	case entity.EntHelperErrorFieldReason:
		return "domain_reason_invalid"
	case entity.EntHelperErrorFieldPointer:
		return "domain_pointer_invalid"
	case entity.EntHelperErrorFieldSource:
		return "domain_source_invalid"
	}
	return "domain_error_invalid"
}
func domainReasonFromCRUDField(field crudbuild.DomainField) string {
	switch field {
	case crudbuild.DomainFieldCode:
		return "domain_code_invalid"
	case crudbuild.DomainFieldReason:
		return "domain_reason_invalid"
	case crudbuild.DomainFieldPointer:
		return "domain_pointer_invalid"
	case crudbuild.DomainFieldSource:
		return "domain_source_invalid"
	}
	return "domain_error_invalid"
}
