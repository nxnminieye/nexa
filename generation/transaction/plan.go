package transaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/internal/staging"
	"github.com/nxnminieye/nexa/provenance"
)

const planKind = "GenerationPlan"

type planWire struct {
	APIVersion   string          `json:"apiVersion"`
	Kind         string          `json:"kind"`
	Generator    generatorWire   `json:"generator"`
	Sources      []sourceWire    `json:"sources"`
	Artifacts    []artifactWire  `json:"artifacts"`
	Controls     []controlWire   `json:"controlSources"`
	ManifestPath string          `json:"manifestPath"`
	Previous     json.RawMessage `json:"previousManifest,omitempty"`
	Changes      []changeWire    `json:"changes"`
	Conflicts    []conflictWire  `json:"conflicts"`
	NextManifest json.RawMessage `json:"nextManifest"`
	PlanDigest   string          `json:"planDigest,omitempty"`
}

type generatorWire struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type sourceWire struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type artifactWire struct {
	ID              string               `json:"id"`
	Path            string               `json:"path"`
	Owner           string               `json:"owner"`
	Digest          string               `json:"digest"`
	Sources         []string             `json:"sources"`
	StalePolicy     artifact.StalePolicy `json:"stalePolicy"`
	CreateManual    bool                 `json:"createManual"`
	OverwriteManual bool                 `json:"overwriteManual"`
	PriorDigest     string               `json:"priorDigest,omitempty"`
}
type changeWire struct {
	Kind        ChangeKind        `json:"kind"`
	ID          string            `json:"id"`
	Path        string            `json:"path"`
	Digest      string            `json:"digest"`
	PriorDigest string            `json:"priorDigest,omitempty"`
	ControlRole ControlSourceRole `json:"controlRole,omitempty"`
}
type controlWire struct {
	Role        ControlSourceRole `json:"role"`
	ID          string            `json:"id"`
	Path        string            `json:"path"`
	Owner       string            `json:"owner"`
	Before      *sourceWire       `json:"before,omitempty"`
	AfterDigest string            `json:"afterDigest"`
	Sources     []string          `json:"sources"`
}
type conflictWire struct {
	ID          string            `json:"id"`
	Path        string            `json:"path"`
	Reason      string            `json:"reason"`
	ControlRole ControlSourceRole `json:"controlRole,omitempty"`
}

func Build(ctx context.Context, repositoryPath string, prepare func(string, func(string, []byte) error) (PlanRequest, error)) (plan Plan, resultErr error) {
	if ctx == nil || prepare == nil {
		return Plan{}, planInputError("repository_invalid", "/repository")
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	session, err := staging.Begin(repositoryPath)
	if err != nil {
		return Plan{}, &Error{code: "transaction_plan_invalid", stage: "input", reason: "repository_invalid", pointer: "/repository", message: "repository root is invalid", sentinel: errPlanInvalid, cause: err}
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, session.Close())
		}
	}()
	request, err := prepare(session.Root(), session.Emit)
	if err != nil {
		return Plan{}, err
	}
	canonicalRepositoryPath := session.CanonicalRepositoryRoot()
	repository, err := os.OpenRoot(canonicalRepositoryPath)
	if err != nil {
		return Plan{}, &Error{code: "transaction_plan_invalid", stage: "input", reason: "repository_invalid", pointer: "/repository", message: "repository root is invalid", sentinel: errPlanInvalid, cause: err}
	}
	defer repository.Close()
	plan, err = build(request, repository, session, canonicalRepositoryPath)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func build(request PlanRequest, repository *os.Root, session *staging.Session, repositoryPath string) (Plan, error) {
	stagingRoot, err := filepath.Rel(repositoryPath, session.Root())
	if err != nil || !validRepositoryPath(filepath.ToSlash(stagingRoot)) {
		return Plan{}, planInputError("repository_invalid", "/repository")
	}
	stagingRoot = filepath.ToSlash(stagingRoot)
	if aliasesStagingRoot(request.ManifestPath, stagingRoot) {
		return Plan{}, planInputError("manifest_path_invalid", "/manifestPath")
	}
	for index, input := range request.Expected {
		if aliasesStagingRoot(input.Path, stagingRoot) {
			return Plan{}, planInputError("artifact_path_invalid", "/expected/"+strconv.Itoa(index)+"/path")
		}
	}
	for index, control := range request.ControlSources {
		if aliasesStagingRoot(control.path, stagingRoot) {
			return Plan{}, controlSourceError("path_invalid", "/controlSources/"+strconv.Itoa(index)+"/path")
		}
	}
	if request.Previous != nil && request.Previous.Generator().ID() != request.Generator.ID {
		return Plan{}, planInputError("previous_generator_mismatch", "/previous/generator/id")
	}
	if !validRepositoryPath(request.ManifestPath) {
		return Plan{}, planInputError("manifest_path_invalid", "/manifestPath")
	}
	sources, sourceSet, err := normalizePlanSources(request.Sources)
	if err != nil {
		return Plan{}, err
	}
	inputs, err := normalizeArtifactInputs(request.Expected, sourceSet, request.ManifestPath, session)
	if err != nil {
		return Plan{}, err
	}
	if request.Previous != nil {
		previousIDs, previousPaths := map[string]struct{}{}, map[string]struct{}{}
		for _, item := range request.Previous.Artifacts() {
			previousIDs[item.ID()] = struct{}{}
			previousPaths[item.Path()] = struct{}{}
		}
		for index, input := range request.Expected {
			_, sameID := previousIDs[input.ID]
			_, samePath := previousPaths[input.Path]
			if (input.CreateManual || input.OverwriteManual) && (sameID || samePath) {
				return Plan{}, planInputError("manual_ownership_transition", "/expected/"+strconv.Itoa(index)+"/createManual")
			}
		}
	}
	controls, err := normalizeControlInputs(request.ControlSources, inputs, request.Previous, sourceSet, request.ManifestPath)
	if err != nil {
		return Plan{}, err
	}
	for _, control := range controls {
		if err := session.Emit(control.path, control.after); err != nil {
			return Plan{}, &Error{code: "transaction_plan_invalid", stage: "input", reason: "candidate_invalid", pointer: "/controlSources/" + control.id, message: "generation candidate is invalid", sentinel: errPlanInvalid, cause: err}
		}
	}
	manifestArtifacts := make([]artifact.ArtifactSpec, 0, len(inputs))
	for _, input := range inputs {
		if input.createManual || input.overwriteManual {
			continue
		}
		manifestArtifacts = append(manifestArtifacts, artifact.ArtifactSpec{
			ID: input.id, Path: input.path, Owner: request.Generator.ID, Digest: input.digest,
			Sources: append([]provenance.SourceRef(nil), input.sources...), StalePolicy: input.stalePolicy,
		})
	}
	manifestSources := artifactManifestSources(sources, inputs)
	next, err := artifact.NewManifest(artifact.ManifestSpec{Generator: request.Generator, Sources: manifestSources, Artifacts: manifestArtifacts})
	if err != nil {
		return Plan{}, planInputError("generator_invalid", "/generator")
	}
	state := &planState{
		generator: request.Generator, sources: sources, inputs: inputs, controls: controls, changes: []changeState{}, conflicts: []conflictState{},
		staleProbes: append([]OwnershipProbe(nil), request.StaleOwnershipProbes...), next: next, manifestPath: request.ManifestPath,
		session: session, revalidateSources: request.RevalidateSources,
	}
	state.stagingRoot = stagingRoot
	if err := bindOverwriteManualBaselines(state, repository); err != nil {
		return Plan{}, err
	}
	if len(state.sources) != 0 && state.revalidateSources == nil {
		return Plan{}, planInputError("source_revalidation_missing", "/sources")
	}
	if request.Previous != nil {
		state.previous = *request.Previous
		state.hasPrevious = true
	}
	state.changes, state.conflicts, err = evaluateArtifacts(state, repository, "build")
	if err != nil {
		return Plan{}, err
	}
	controlChanges, controlConflicts, err := evaluateControls(state, repository, "build")
	if err != nil {
		return Plan{}, err
	}
	state.changes = append(state.changes, controlChanges...)
	state.conflicts = append(state.conflicts, controlConflicts...)
	sortPlanFindings(state.changes, state.conflicts)
	if err := finalizePlan(state); err != nil {
		return Plan{}, err
	}
	manifest, err := state.next.CanonicalJSON()
	if err != nil {
		return Plan{}, planInputError("canonical_invalid", "/nextManifest")
	}
	if err := session.Emit(state.manifestPath, manifest); err != nil {
		return Plan{}, &Error{code: "transaction_plan_invalid", stage: "input", reason: "candidate_invalid", pointer: "/manifest", message: "generation candidate is invalid", sentinel: errPlanInvalid, cause: err}
	}
	return Plan{state: state}, nil
}

func aliasesStagingRoot(target, stagingRoot string) bool {
	return target == stagingRoot || strings.HasPrefix(target, stagingRoot+"/")
}

func artifactManifestSources(sources []provenance.Source, inputs []plannedArtifact) []provenance.Source {
	referenced := map[string]struct{}{}
	for _, input := range inputs {
		if input.createManual || input.overwriteManual {
			continue
		}
		for _, ref := range input.sources {
			referenced[ref.String()] = struct{}{}
		}
	}
	result := make([]provenance.Source, 0, len(referenced))
	for _, source := range sources {
		if _, included := referenced[source.Ref.String()]; included {
			result = append(result, source)
		}
	}
	return result
}

func normalizePlanSources(values []provenance.Source) ([]provenance.Source, map[string]struct{}, error) {
	result := append([]provenance.Source(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].Ref.String() < result[j].Ref.String() })
	set := make(map[string]struct{}, len(result))
	for index, value := range result {
		if _, err := provenance.ParseSourceRef(value.Ref.String()); err != nil {
			return nil, nil, planInputError("source_invalid", "/sources/"+strconv.Itoa(index)+"/ref")
		}
		if _, err := provenance.ParseDigest(value.Digest.String()); err != nil {
			return nil, nil, planInputError("source_invalid", "/sources/"+strconv.Itoa(index)+"/digest")
		}
		key := value.Ref.String()
		if _, duplicate := set[key]; duplicate {
			return nil, nil, planInputError("source_duplicate", "/sources/"+strconv.Itoa(index)+"/ref")
		}
		set[key] = struct{}{}
	}
	return result, set, nil
}

func normalizeArtifactInputs(values []ArtifactInput, sources map[string]struct{}, manifestPath string, session *staging.Session) ([]plannedArtifact, error) {
	result := make([]plannedArtifact, len(values))
	seenIDs, seenPaths := map[string]struct{}{}, map[string]struct{}{}
	for index, value := range values {
		base := "/expected/" + strconv.Itoa(index)
		if value.CreateManual && value.OverwriteManual {
			return nil, planInputError("manual_mode_invalid", base+"/overwriteManual")
		}
		if !transactionIdentifierPattern.MatchString(value.ID) {
			return nil, planInputError("artifact_id_invalid", base+"/id")
		}
		if _, duplicate := seenIDs[value.ID]; duplicate {
			return nil, planInputError("artifact_id_duplicate", base+"/id")
		}
		seenIDs[value.ID] = struct{}{}
		if !validRepositoryPath(value.Path) {
			return nil, planInputError("artifact_path_invalid", base+"/path")
		}
		if value.Path == manifestPath {
			return nil, planInputError("manifest_path_alias", "/manifestPath")
		}
		if _, duplicate := seenPaths[value.Path]; duplicate {
			return nil, planInputError("artifact_path_duplicate", base+"/path")
		}
		seenPaths[value.Path] = struct{}{}
		if !validGenerationOwner(value.Owner) {
			return nil, planInputError("artifact_owner_invalid", base+"/owner")
		}
		if value.StalePolicy != artifact.StaleRetain && value.StalePolicy != artifact.StaleDeleteIfUnmodified {
			return nil, planInputError("stale_policy_invalid", base+"/stalePolicy")
		}
		if (value.CreateManual || value.OverwriteManual) && value.StalePolicy != artifact.StaleRetain {
			return nil, planInputError("stale_policy_invalid", base+"/stalePolicy")
		}
		refs := append([]provenance.SourceRef(nil), value.Sources...)
		if value.OverwriteManual && len(refs) == 0 {
			return nil, planInputError("artifact_source_missing", base+"/sources")
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		seenRefs := map[string]struct{}{}
		for refIndex, ref := range refs {
			key := ref.String()
			if _, err := provenance.ParseSourceRef(key); err != nil {
				return nil, planInputError("artifact_source_invalid", base+"/sources/"+strconv.Itoa(refIndex))
			}
			if _, duplicate := seenRefs[key]; duplicate {
				return nil, planInputError("artifact_source_duplicate", base+"/sources/"+strconv.Itoa(refIndex))
			}
			if _, declared := sources[key]; !declared {
				return nil, planInputError("artifact_source_unresolved", base+"/sources/"+strconv.Itoa(refIndex))
			}
			seenRefs[key] = struct{}{}
		}
		content, err := session.Read(value.Path)
		if err != nil || !validDigest(value.Digest) || provenance.SHA256(content) != value.Digest {
			return nil, planInputError("candidate_invalid", base+"/digest")
		}
		result[index] = plannedArtifact{id: value.ID, path: value.Path, owner: value.Owner, digest: value.Digest, sources: refs, stalePolicy: value.StalePolicy, probe: value.Probe, createManual: value.CreateManual, overwriteManual: value.OverwriteManual}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

func bindOverwriteManualBaselines(state *planState, repository *os.Root) error {
	for index := range state.inputs {
		input := &state.inputs[index]
		if !input.overwriteManual {
			continue
		}
		info, err := repository.Lstat(input.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return evaluationCauseError("build", "current_read_failed", "/artifacts/"+input.id+"/path", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return planInputError("overwrite_manual_invalid", "/expected/"+strconv.Itoa(index)+"/path")
		}
		content, err := readRootFile(repository, input.path)
		if err != nil {
			return evaluationCauseError("build", "current_read_failed", "/artifacts/"+input.id+"/path", err)
		}
		input.overwriteExists = true
		input.overwritePrior = provenance.SHA256(content)
	}
	return nil
}

func inspectCurrentArtifact(root *os.Root, path string, expected []byte) (bool, bool, bool, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, false, nil
	}
	if err != nil {
		return false, false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return true, false, true, nil
	}
	if info.Size() != int64(len(expected)) {
		return true, false, false, nil
	}
	file, err := root.Open(path)
	if err != nil {
		return true, false, false, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return true, false, false, errors.Join(readErr, closeErr)
	}
	return true, bytes.Equal(data, expected), false, nil
}

func finalizePlan(state *planState) error {
	nextJSON, err := state.next.CanonicalJSON()
	if err != nil {
		return planInputError("canonical_invalid", "/nextManifest")
	}
	var previousJSON []byte
	if state.hasPrevious {
		previousJSON, err = state.previous.CanonicalJSON()
		if err != nil {
			return planInputError("canonical_invalid", "/previous")
		}
	}
	wire := planWireFromState(state, bytes.TrimSpace(nextJSON), bytes.TrimSpace(previousJSON))
	preimage, err := canonicalPlan(wire)
	if err != nil {
		return planInputError("canonical_invalid", "/plan")
	}
	state.planDigest = provenance.SHA256(preimage)
	wire.PlanDigest = state.planDigest.String()
	state.canonical, err = canonicalPlan(wire)
	if err != nil {
		return planInputError("canonical_invalid", "/plan")
	}
	return nil
}

func planWireFromState(state *planState, next, previous []byte) planWire {
	sources := make([]sourceWire, len(state.sources))
	for index, source := range state.sources {
		sources[index] = sourceWire{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	artifacts := make([]artifactWire, len(state.inputs))
	for index, input := range state.inputs {
		refs := make([]string, len(input.sources))
		for refIndex, ref := range input.sources {
			refs[refIndex] = ref.String()
		}
		artifacts[index] = artifactWire{
			ID: input.id, Path: input.path, Owner: input.owner, Digest: input.digest.String(), Sources: refs, StalePolicy: input.stalePolicy, CreateManual: input.createManual, OverwriteManual: input.overwriteManual,
		}
		if input.overwriteExists {
			artifacts[index].PriorDigest = input.overwritePrior.String()
		}
	}
	controls := make([]controlWire, len(state.controls))
	for index, input := range state.controls {
		refs := make([]string, len(input.sources))
		for refIndex, ref := range input.sources {
			refs[refIndex] = ref.String()
		}
		controls[index] = controlWire{Role: input.role, ID: input.id, Path: input.path, Owner: input.owner, AfterDigest: input.afterDigest.String(), Sources: refs}
		if input.before != nil {
			controls[index].Before = &sourceWire{Ref: input.before.Ref.String(), Digest: input.before.Digest.String()}
		}
	}
	changes := make([]changeWire, len(state.changes))
	for index, value := range state.changes {
		changes[index] = changeWire{Kind: value.kind, ID: value.id, Path: value.path, Digest: value.digest.String()}
		if value.hasPrior {
			changes[index].PriorDigest = value.prior.String()
		}
		if value.hasControl {
			changes[index].ControlRole = value.control
		}
	}
	conflicts := make([]conflictWire, len(state.conflicts))
	for index, value := range state.conflicts {
		conflicts[index] = conflictWire{ID: value.id, Path: value.path, Reason: value.reason}
		if value.hasControl {
			conflicts[index].ControlRole = value.control
		}
	}
	return planWire{APIVersion: PlanAPIVersion, Kind: planKind, Generator: generatorWire{ID: state.generator.ID, Version: state.generator.Version}, Sources: sources, Artifacts: artifacts, Controls: controls, ManifestPath: state.manifestPath, Previous: append(json.RawMessage(nil), previous...), Changes: changes, Conflicts: conflicts, NextManifest: append(json.RawMessage(nil), next...)}
}

func canonicalPlan(value planWire) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return jcs.Transform(encoded)
}
