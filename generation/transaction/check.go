package transaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/provenance"
)

const resultKind = "GenerationResult"

type resultWire struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	PlanDigest string         `json:"planDigest"`
	Status     string         `json:"status"`
	Changes    []changeWire   `json:"changes"`
	Conflicts  []conflictWire `json:"conflicts"`
}

func Check(plan Plan, repository *os.Root) (Result, error) {
	if plan.state == nil {
		return Result{}, evaluationError("check", "plan_invalid", "/plan")
	}
	if repository == nil {
		return Result{}, evaluationError("check", "repository_invalid", "/repository")
	}
	changes, conflicts, err := evaluateArtifacts(plan.state, repository, "check")
	if err != nil {
		return Result{}, err
	}
	controlChanges, controlConflicts, err := evaluateControls(plan.state, repository, "check")
	if err != nil {
		return Result{}, err
	}
	changes = append(changes, controlChanges...)
	conflicts = append(conflicts, controlConflicts...)
	sortPlanFindings(changes, conflicts)
	state := &resultState{planDigest: plan.state.planDigest, changes: changes, conflicts: conflicts}
	if err := finalizeResult(state, "check"); err != nil {
		return Result{}, err
	}
	return Result{state: state}, nil
}

func evaluateArtifacts(state *planState, repository *os.Root, stage string) ([]changeState, []conflictState, error) {
	previous := map[string]artifact.Artifact{}
	if state.hasPrevious {
		for _, item := range state.previous.Artifacts() {
			previous[item.ID()] = item
		}
	}
	expected := make(map[string]plannedArtifact, len(state.inputs))
	expectedPaths := make(map[string]struct{}, len(state.inputs))
	probes := append([]OwnershipProbe(nil), state.staleProbes...)
	changes := []changeState{}
	conflicts := []conflictState{}
	for _, input := range state.inputs {
		content, candidateErr := state.session.Read(input.path)
		if candidateErr != nil {
			return nil, nil, evaluationCauseError(stage, "plan_invalid", "/plan", candidateErr)
		}
		if provenance.SHA256(content) != input.digest {
			return nil, nil, evaluationError(stage, "plan_invalid", "/plan")
		}
		expected[input.id] = input
		expectedPaths[input.path] = struct{}{}
		if input.createManual {
			if _, err := repository.Lstat(input.path); errors.Is(err, os.ErrNotExist) {
				changes = append(changes, changeState{kind: ChangeCreateManual, id: input.id, path: input.path, digest: input.digest})
			} else if err != nil {
				return nil, nil, evaluationCauseError(stage, "current_read_failed", "/artifacts/"+input.id+"/path", err)
			}
			continue
		}
		if input.overwriteManual {
			current, exists, unsafe, err := readOverwriteManualTarget(repository, input.path)
			if err != nil {
				return nil, nil, evaluationCauseError(stage, "current_read_failed", "/artifacts/"+input.id+"/path", err)
			}
			if unsafe || exists != input.overwriteExists || (exists && provenance.SHA256(current) != input.overwritePrior) {
				conflicts = append(conflicts, conflictState{id: input.id, path: input.path, reason: "overwrite_manual_target_changed"})
				continue
			}
			if !exists {
				changes = append(changes, changeState{kind: ChangeCreateManual, id: input.id, path: input.path, digest: input.digest})
			} else if !bytes.Equal(current, content) {
				changes = append(changes, changeState{kind: ChangeUpdate, id: input.id, path: input.path, digest: input.digest, prior: input.overwritePrior, hasPrior: true})
			}
			continue
		}
		if input.probe != nil {
			probes = append(probes, input.probe)
		}
		prior, hasPrior := previous[input.id]
		if !hasPrior || prior.Path() != input.path {
			exists, matches, unsafe, err := inspectCurrentArtifact(repository, input.path, content)
			if err != nil {
				return nil, nil, evaluationCauseError(stage, "current_read_failed", "/artifacts/"+input.id+"/path", err)
			}
			switch {
			case unsafe:
				conflicts = append(conflicts, conflictState{id: input.id, path: input.path, reason: "current_path_unsafe"})
			case !exists:
				changes = append(changes, changeState{kind: ChangeCreate, id: input.id, path: input.path, digest: input.digest})
			case !matches:
				conflicts = append(conflicts, conflictState{id: input.id, path: input.path, reason: "existing_unowned"})
			}
			continue
		}
		current, exists, matches, priorMatches, unsafe, err := inspectPriorArtifact(repository, input.path, content, prior.Digest())
		if err != nil {
			return nil, nil, evaluationCauseError(stage, "current_read_failed", "/artifacts/"+input.id+"/path", err)
		}
		switch {
		case unsafe:
			conflicts = append(conflicts, conflictState{id: input.id, path: input.path, reason: "current_path_unsafe"})
		case !exists:
			changes = append(changes, changeState{kind: ChangeCreate, id: input.id, path: input.path, digest: input.digest})
		case matches:
		case !priorMatches:
			conflicts = append(conflicts, conflictState{id: input.id, path: input.path, reason: "existing_unowned"})
		default:
			owned, probeErr := inspectOwnership(probes, input.path, current, state.previous, prior)
			if probeErr != nil {
				return nil, nil, evaluationCauseError(stage, "ownership_probe_failed", "/artifacts/"+input.id+"/path", probeErr)
			}
			if !owned {
				conflicts = append(conflicts, conflictState{id: input.id, path: input.path, reason: "existing_unowned"})
			} else {
				changes = append(changes, changeState{kind: ChangeUpdate, id: input.id, path: input.path, digest: input.digest, prior: prior.Digest(), hasPrior: true})
			}
		}
	}
	if state.hasPrevious {
		for _, prior := range state.previous.Artifacts() {
			if _, remains := expected[prior.ID()]; remains || prior.StalePolicy() != artifact.StaleDeleteIfUnmodified {
				continue
			}
			if _, reused := expectedPaths[prior.Path()]; reused {
				continue
			}
			current, exists, priorMatches, unsafe, err := inspectStaleArtifact(repository, prior.Path(), prior.Digest())
			if err != nil {
				return nil, nil, evaluationCauseError(stage, "current_read_failed", "/artifacts/"+prior.ID()+"/path", err)
			}
			if !exists {
				continue
			}
			if unsafe {
				conflicts = append(conflicts, conflictState{id: prior.ID(), path: prior.Path(), reason: "current_path_unsafe"})
				continue
			}
			owned := false
			if priorMatches {
				owned, err = inspectOwnership(probes, prior.Path(), current, state.previous, prior)
				if err != nil {
					return nil, nil, evaluationCauseError(stage, "ownership_probe_failed", "/artifacts/"+prior.ID()+"/path", err)
				}
			}
			if !priorMatches || !owned {
				conflicts = append(conflicts, conflictState{id: prior.ID(), path: prior.Path(), reason: "stale_unowned"})
				continue
			}
			changes = append(changes, changeState{kind: ChangeDelete, id: prior.ID(), path: prior.Path(), digest: prior.Digest(), prior: prior.Digest(), hasPrior: true})
		}
	}
	sortPlanFindings(changes, conflicts)
	return changes, conflicts, nil
}

func readOverwriteManualTarget(root *os.Root, path string) ([]byte, bool, bool, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, true, true, nil
	}
	content, err := readRootFile(root, path)
	return content, true, false, err
}

func inspectPriorArtifact(root *os.Root, path string, expected []byte, prior provenance.Digest) ([]byte, bool, bool, bool, bool, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, false, false, false, nil
	}
	if err != nil {
		return nil, false, false, false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, true, false, false, true, nil
	}
	if info.Size() == int64(len(expected)) {
		file, err := root.Open(path)
		if err != nil {
			return nil, true, false, false, false, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return nil, true, false, false, false, errors.Join(readErr, closeErr)
		}
		if bytes.Equal(data, expected) {
			return nil, true, true, false, false, nil
		}
		if digestBytes(data) == prior.String() {
			return data, true, false, true, false, nil
		}
		return nil, true, false, false, false, nil
	}
	current, matches, err := readIfDigestMatches(root, path, prior)
	return current, true, false, matches, false, err
}

func inspectStaleArtifact(root *os.Root, path string, prior provenance.Digest) ([]byte, bool, bool, bool, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, false, false, nil
	}
	if err != nil {
		return nil, false, false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, true, false, true, nil
	}
	current, matches, err := readIfDigestMatches(root, path, prior)
	return current, true, matches, false, err
}

func readIfDigestMatches(root *os.Root, path string, expected provenance.Digest) ([]byte, bool, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, false, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return nil, false, errors.Join(copyErr, closeErr)
	}
	if "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expected.String() {
		return nil, false, nil
	}
	file, err = root.Open(path)
	if err != nil {
		return nil, false, err
	}
	data, readErr := io.ReadAll(file)
	closeErr = file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, false, err
	}
	if provenance.SHA256(data) != expected {
		return nil, false, nil
	}
	return data, true, nil
}

func digestBytes(value []byte) string { return provenance.SHA256(value).String() }

func inspectOwnership(probes []OwnershipProbe, path string, content []byte, previous artifact.Manifest, prior artifact.Artifact) (bool, error) {
	expected := Ownership{GeneratorID: previous.Generator().ID(), ArtifactID: prior.ID(), InputDigest: previous.InputDigest()}
	for _, probe := range probes {
		if probe == nil {
			continue
		}
		known, err := probe.Inspect(path, append([]byte(nil), content...), expected)
		if err != nil {
			return false, err
		}
		if known {
			return true, nil
		}
	}
	return false, nil
}

func sortPlanFindings(changes []changeState, conflicts []conflictState) {
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].id != changes[j].id {
			return changes[i].id < changes[j].id
		}
		return changes[i].path < changes[j].path
	})
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].id != conflicts[j].id {
			return conflicts[i].id < conflicts[j].id
		}
		return conflicts[i].path < conflicts[j].path
	})
}

func finalizeResult(state *resultState, stage string) error {
	status := "clean"
	if len(state.conflicts) != 0 {
		status = "conflict"
	} else if len(state.changes) != 0 {
		status = "changes-required"
	}
	wire := resultWire{APIVersion: ResultAPIVersion, Kind: resultKind, PlanDigest: state.planDigest.String(), Status: status}
	wire.Changes = make([]changeWire, len(state.changes))
	for index, value := range state.changes {
		wire.Changes[index] = changeWire{Kind: value.kind, ID: value.id, Path: value.path, Digest: value.digest.String()}
		if value.hasPrior {
			wire.Changes[index].PriorDigest = value.prior.String()
		}
		if value.hasControl {
			wire.Changes[index].ControlRole = value.control
		}
	}
	wire.Conflicts = make([]conflictWire, len(state.conflicts))
	for index, value := range state.conflicts {
		wire.Conflicts[index] = conflictWire{ID: value.id, Path: value.path, Reason: value.reason}
		if value.hasControl {
			wire.Conflicts[index].ControlRole = value.control
		}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return evaluationCauseError(stage, "canonical_invalid", "/result", err)
	}
	state.canonical, err = jcs.Transform(encoded)
	if err != nil {
		return evaluationCauseError(stage, "canonical_invalid", "/result", err)
	}
	return nil
}
