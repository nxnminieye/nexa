package transaction

import (
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/provenance"
)

func normalizeControlInputs(values []ControlSourceMutation, artifacts []plannedArtifact, previous *artifact.Manifest, sources map[string]struct{}, manifestPath string) ([]plannedControl, error) {
	artifactPaths := make(map[string]struct{}, len(artifacts))
	for _, item := range artifacts {
		artifactPaths[item.path] = struct{}{}
	}
	if previous != nil {
		for _, item := range previous.Artifacts() {
			artifactPaths[item.Path()] = struct{}{}
		}
	}
	result := make([]plannedControl, len(values))
	seenIDs := make(map[string]struct{}, len(values))
	seenPaths := make(map[string]struct{}, len(values))
	for index, value := range values {
		base := "/controlSources/" + strconv.Itoa(index)
		if value.role != ControlSourceCompatibilityLock {
			return nil, controlSourceError("role_invalid", base+"/role")
		}
		if !transactionIdentifierPattern.MatchString(value.id) {
			return nil, controlSourceError("id_invalid", base+"/id")
		}
		if _, duplicate := seenIDs[value.id]; duplicate {
			return nil, controlSourceError("id_duplicate", base+"/id")
		}
		seenIDs[value.id] = struct{}{}
		if !validRepositoryPath(value.path) || strings.HasPrefix(value.path, ".nexa/generation/transactions/") {
			return nil, controlSourceError("path_invalid", base+"/path")
		}
		if value.path == manifestPath {
			return nil, controlSourceError("manifest_path_alias", base+"/path")
		}
		if _, alias := artifactPaths[value.path]; alias {
			return nil, controlSourceError("artifact_path_alias", base+"/path")
		}
		if _, duplicate := seenPaths[value.path]; duplicate {
			return nil, controlSourceError("control_path_duplicate", base+"/path")
		}
		seenPaths[value.path] = struct{}{}
		if !validGenerationOwner(value.owner) {
			return nil, controlSourceError("owner_invalid", base+"/owner")
		}
		if value.before != nil && !validWholeDocumentSource(*value.before, value.path) {
			return nil, controlSourceError("before_source_invalid", base+"/before")
		}
		if len(value.after) == 0 {
			return nil, controlSourceError("after_empty", base+"/after")
		}
		if !validDigest(value.afterDigest) || provenance.SHA256(value.after) != value.afterDigest {
			return nil, controlSourceError("after_digest_mismatch", base+"/afterDigest")
		}
		if len(value.sources) == 0 {
			return nil, controlSourceError("source_ref_invalid", base+"/sources")
		}
		refs := append([]provenance.SourceRef(nil), value.sources...)
		seenRefs := make(map[string]struct{}, len(refs))
		for refIndex, ref := range refs {
			key := ref.String()
			if _, err := provenance.ParseSourceRef(key); err != nil {
				return nil, controlSourceError("source_ref_invalid", base+"/sources/"+strconv.Itoa(refIndex))
			}
			if _, duplicate := seenRefs[key]; duplicate {
				return nil, controlSourceError("source_ref_duplicate", base+"/sources/"+strconv.Itoa(refIndex))
			}
			if _, declared := sources[key]; !declared {
				return nil, controlSourceError("source_ref_unresolved", base+"/sources/"+strconv.Itoa(refIndex))
			}
			seenRefs[key] = struct{}{}
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		var before *provenance.Source
		if value.before != nil {
			copyValue := *value.before
			before = &copyValue
		}
		result[index] = plannedControl{
			role: value.role, id: value.id, path: value.path, owner: value.owner, before: before,
			after: append([]byte(nil), value.after...), afterDigest: value.afterDigest, sources: refs,
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result, nil
}

func evaluateControls(state *planState, repository *os.Root, stage string) ([]changeState, []conflictState, error) {
	changes := make([]changeState, 0, len(state.controls))
	conflicts := make([]conflictState, 0)
	for _, control := range state.controls {
		info, err := repository.Lstat(control.path)
		if control.before == nil {
			switch {
			case errors.Is(err, os.ErrNotExist):
				changes = append(changes, changeState{kind: ChangeCreate, id: control.id, path: control.path, digest: control.afterDigest, control: control.role, hasControl: true})
			case err != nil:
				return nil, nil, evaluationCauseError(stage, "current_read_failed", "/controlSources/"+control.id+"/path", err)
			default:
				conflicts = append(conflicts, controlConflict(control, "initial_path_exists"))
			}
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			conflicts = append(conflicts, controlConflict(control, "current_missing"))
			continue
		}
		if err != nil {
			return nil, nil, evaluationCauseError(stage, "current_read_failed", "/controlSources/"+control.id+"/path", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			conflicts = append(conflicts, controlConflict(control, "current_digest_mismatch"))
			continue
		}
		_, matches, err := readIfDigestMatches(repository, control.path, control.before.Digest)
		if err != nil {
			return nil, nil, evaluationCauseError(stage, "current_read_failed", "/controlSources/"+control.id+"/path", err)
		}
		if !matches {
			conflicts = append(conflicts, controlConflict(control, "current_digest_mismatch"))
			continue
		}
		changes = append(changes, changeState{
			kind: ChangeUpdate, id: control.id, path: control.path, digest: control.afterDigest,
			prior: control.before.Digest, hasPrior: true, control: control.role, hasControl: true,
		})
	}
	return changes, conflicts, nil
}

func controlConflict(control plannedControl, reason string) conflictState {
	return conflictState{id: control.id, path: control.path, reason: reason, control: control.role, hasControl: true}
}
