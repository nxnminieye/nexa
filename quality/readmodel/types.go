package readmodel

import "github.com/nxnminieye/nexa/provenance"

const APIVersion = "nexa.dev/quality-read-model/v1"
const Kind = "QualityReadModel"

type FreezeStatus string

const (
	FreezeNone    FreezeStatus = "none"
	FreezeFrozen  FreezeStatus = "frozen"
	FreezeChanged FreezeStatus = "changed"
)

type RequirementCoverageSpec struct {
	Requirement  provenance.SourceRef
	Title        string
	Status       string
	TestRefs     []provenance.SourceRef
	EvidenceRefs []provenance.SourceRef
	FreezeRefs   []provenance.SourceRef
	FreezeStatus FreezeStatus
	GapCodes     []string
}

type SnapshotSpec struct {
	SourceProfile  string
	ReadModelScope string
	Revision       string
	Requirements   []RequirementCoverageSpec
}

type requirementCoverageState struct {
	requirement  provenance.SourceRef
	title        string
	status       string
	testRefs     []provenance.SourceRef
	evidenceRefs []provenance.SourceRef
	freezeRefs   []provenance.SourceRef
	freezeStatus FreezeStatus
	gapCodes     []string
}

type RequirementCoverage struct {
	state requirementCoverageState
}

func (r RequirementCoverage) Requirement() provenance.SourceRef { return r.state.requirement }
func (r RequirementCoverage) Title() string                     { return r.state.title }
func (r RequirementCoverage) Status() string                    { return r.state.status }
func (r RequirementCoverage) TestRefs() []provenance.SourceRef  { return cloneRefs(r.state.testRefs) }
func (r RequirementCoverage) EvidenceRefs() []provenance.SourceRef {
	return cloneRefs(r.state.evidenceRefs)
}
func (r RequirementCoverage) FreezeRefs() []provenance.SourceRef {
	return cloneRefs(r.state.freezeRefs)
}
func (r RequirementCoverage) FreezeStatus() FreezeStatus { return r.state.freezeStatus }
func (r RequirementCoverage) GapCodes() []string         { return cloneStrings(r.state.gapCodes) }

type snapshotState struct {
	sourceProfile  string
	readModelScope string
	revision       string
	requirements   []RequirementCoverage
}

type Snapshot struct {
	state *snapshotState
}

func (s Snapshot) SourceProfile() string {
	if s.state == nil {
		return ""
	}
	return s.state.sourceProfile
}
func (s Snapshot) ReadModelScope() string {
	if s.state == nil {
		return ""
	}
	return s.state.readModelScope
}
func (s Snapshot) Revision() string {
	if s.state == nil {
		return ""
	}
	return s.state.revision
}
func (s Snapshot) Requirements() []RequirementCoverage {
	if s.state == nil {
		return nil
	}
	result := make([]RequirementCoverage, len(s.state.requirements))
	for index, requirement := range s.state.requirements {
		result[index] = cloneRequirement(requirement)
	}
	return result
}

func cloneRequirement(value RequirementCoverage) RequirementCoverage {
	state := value.state
	state.testRefs = cloneRefs(state.testRefs)
	state.evidenceRefs = cloneRefs(state.evidenceRefs)
	state.freezeRefs = cloneRefs(state.freezeRefs)
	state.gapCodes = cloneStrings(state.gapCodes)
	return RequirementCoverage{state: state}
}

func cloneRefs(source []provenance.SourceRef) []provenance.SourceRef {
	result := make([]provenance.SourceRef, len(source))
	copy(result, source)
	return result
}

func cloneStrings(source []string) []string {
	result := make([]string, len(source))
	copy(result, source)
	return result
}
