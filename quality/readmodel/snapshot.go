package readmodel

import (
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/provenance"
)

func Empty() Snapshot {
	return Snapshot{state: &snapshotState{requirements: make([]RequirementCoverage, 0)}}
}

func NewSnapshot(spec SnapshotSpec) (Snapshot, error) {
	if !utf8.ValidString(spec.SourceProfile) {
		return Snapshot{}, invalid("source_profile_invalid", "", "/sourceProfile")
	}
	if !utf8.ValidString(spec.ReadModelScope) {
		return Snapshot{}, invalid("read_model_scope_invalid", "", "/readModelScope")
	}
	if !utf8.ValidString(spec.Revision) {
		return Snapshot{}, invalid("revision_invalid", "", "/revision")
	}
	result := Snapshot{state: &snapshotState{
		sourceProfile: spec.SourceProfile, readModelScope: spec.ReadModelScope, revision: spec.Revision,
		requirements: make([]RequirementCoverage, 0, len(spec.Requirements)),
	}}
	seen := make(map[string]struct{}, len(spec.Requirements))
	for index, value := range spec.Requirements {
		pointer := fmt.Sprintf("/requirements/%d", index)
		row, err := newRequirement(value, pointer)
		if err != nil {
			return Snapshot{}, err
		}
		key := row.Requirement().String()
		if _, duplicate := seen[key]; duplicate {
			return Snapshot{}, invalid("requirement_ref_duplicate", "", pointer+"/requirement")
		}
		seen[key] = struct{}{}
		result.state.requirements = append(result.state.requirements, row)
	}
	sort.Slice(result.state.requirements, func(i, j int) bool {
		return result.state.requirements[i].Requirement().String() < result.state.requirements[j].Requirement().String()
	})
	return result, nil
}

func newRequirement(spec RequirementCoverageSpec, pointer string) (RequirementCoverage, error) {
	if spec.Requirement.String() == "" {
		return RequirementCoverage{}, invalid("requirement_ref_unresolved", "", pointer+"/requirement")
	}
	if _, err := provenance.ParseSourceRef(spec.Requirement.String()); err != nil {
		return RequirementCoverage{}, invalid("requirement_ref_invalid", "", pointer+"/requirement")
	}
	if !utf8.ValidString(spec.Title) || !utf8.ValidString(spec.Status) {
		return RequirementCoverage{}, invalid("field_invalid", "", pointer)
	}
	testRefs, err := normalizeRefs(spec.TestRefs, "test", pointer+"/testRefs")
	if err != nil {
		return RequirementCoverage{}, err
	}
	evidenceRefs, err := normalizeRefs(spec.EvidenceRefs, "evidence", pointer+"/evidenceRefs")
	if err != nil {
		return RequirementCoverage{}, err
	}
	freezeRefs, err := normalizeRefs(spec.FreezeRefs, "freeze", pointer+"/freezeRefs")
	if err != nil {
		return RequirementCoverage{}, err
	}
	switch spec.FreezeStatus {
	case FreezeNone:
		if len(freezeRefs) != 0 {
			return RequirementCoverage{}, invalid("freeze_ref_unexpected", "", pointer+"/freezeRefs/0")
		}
	case FreezeFrozen:
		if len(freezeRefs) == 0 {
			return RequirementCoverage{}, invalid("freeze_ref_unresolved", "", pointer+"/freezeRefs")
		}
	case FreezeChanged:
		if len(freezeRefs) == 0 {
			return RequirementCoverage{}, invalid("changed_without_freeze_ref", "", pointer+"/freezeRefs")
		}
	default:
		return RequirementCoverage{}, invalid("freeze_status_invalid", "", pointer+"/freezeStatus")
	}
	gapCodes := cloneStrings(spec.GapCodes)
	seenGaps := make(map[string]struct{}, len(gapCodes))
	for index, code := range gapCodes {
		if code == "" || !utf8.ValidString(code) {
			return RequirementCoverage{}, invalid("gap_code_invalid", "", fmt.Sprintf("%s/gapCodes/%d", pointer, index))
		}
		if _, duplicate := seenGaps[code]; duplicate {
			return RequirementCoverage{}, invalid("gap_code_duplicate", "", fmt.Sprintf("%s/gapCodes/%d", pointer, index))
		}
		seenGaps[code] = struct{}{}
	}
	sort.Strings(gapCodes)
	return RequirementCoverage{state: requirementCoverageState{
		requirement: spec.Requirement, title: spec.Title, status: spec.Status,
		testRefs: testRefs, evidenceRefs: evidenceRefs, freezeRefs: freezeRefs,
		freezeStatus: spec.FreezeStatus, gapCodes: gapCodes,
	}}, nil
}

func normalizeRefs(source []provenance.SourceRef, kind, pointer string) ([]provenance.SourceRef, error) {
	result := cloneRefs(source)
	seen := make(map[string]struct{}, len(result))
	for index, ref := range result {
		if ref.String() == "" {
			return nil, invalid(kind+"_ref_unresolved", "", fmt.Sprintf("%s/%d", pointer, index))
		}
		if _, err := provenance.ParseSourceRef(ref.String()); err != nil {
			return nil, invalid(kind+"_ref_invalid", "", fmt.Sprintf("%s/%d", pointer, index))
		}
		if _, duplicate := seen[ref.String()]; duplicate {
			return nil, invalid(kind+"_ref_duplicate", "", fmt.Sprintf("%s/%d", pointer, index))
		}
		seen[ref.String()] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result, nil
}
