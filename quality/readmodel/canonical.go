package readmodel

import (
	"encoding/json"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/provenance"
)

type wireRequirement struct {
	Requirement  string       `json:"requirement"`
	Title        string       `json:"title"`
	Status       string       `json:"status"`
	TestRefs     []string     `json:"testRefs"`
	EvidenceRefs []string     `json:"evidenceRefs"`
	FreezeRefs   []string     `json:"freezeRefs"`
	FreezeStatus FreezeStatus `json:"freezeStatus"`
	GapCodes     []string     `json:"gapCodes"`
}

type wireSnapshot struct {
	APIVersion     string            `json:"apiVersion"`
	Kind           string            `json:"kind"`
	SourceProfile  string            `json:"sourceProfile"`
	ReadModelScope string            `json:"readModelScope"`
	Revision       string            `json:"revision"`
	Requirements   []wireRequirement `json:"requirements"`
}

func CanonicalJSON(snapshot Snapshot) ([]byte, error) {
	if snapshot.state == nil {
		return nil, invalid("snapshot_state_invalid", "", "")
	}
	wire := wireOf(snapshot)
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, invalid("document_invalid", "", "")
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		return nil, invalid("document_invalid", "", "")
	}
	return canonical, nil
}

func (s Snapshot) Digest() (provenance.Digest, error) {
	canonical, err := CanonicalJSON(s)
	if err != nil {
		return provenance.Digest{}, err
	}
	return provenance.SHA256(canonical), nil
}

func wireOf(snapshot Snapshot) wireSnapshot {
	result := wireSnapshot{
		APIVersion: APIVersion, Kind: Kind, SourceProfile: snapshot.state.sourceProfile,
		ReadModelScope: snapshot.state.readModelScope, Revision: snapshot.state.revision,
		Requirements: make([]wireRequirement, len(snapshot.state.requirements)),
	}
	for index, row := range snapshot.state.requirements {
		result.Requirements[index] = wireRequirement{
			Requirement: row.Requirement().String(), Title: row.Title(), Status: row.Status(),
			TestRefs: refStrings(row.TestRefs()), EvidenceRefs: refStrings(row.EvidenceRefs()),
			FreezeRefs: refStrings(row.FreezeRefs()), FreezeStatus: row.FreezeStatus(), GapCodes: row.GapCodes(),
		}
	}
	return result
}

func refStrings(refs []provenance.SourceRef) []string {
	result := make([]string, len(refs))
	for index, ref := range refs {
		result[index] = ref.String()
	}
	return result
}
