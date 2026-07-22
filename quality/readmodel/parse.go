package readmodel

import (
	"encoding/json"
	"errors"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

func Parse(source string, data []byte) (Snapshot, error) {
	if source == "" {
		return Snapshot{}, invalid("source_invalid", "", "")
	}
	document, err := strictdoc.ParseJSON(source, data)
	if err != nil {
		return Snapshot{}, documentError(err)
	}
	var wire wireSnapshot
	if err := document.Decode(&wire); err != nil {
		return Snapshot{}, documentError(err)
	}
	var normalized any
	if err := json.Unmarshal(document.JSON(), &normalized); err != nil {
		return Snapshot{}, invalid("document_invalid", source, "")
	}
	if wire.APIVersion != APIVersion {
		return Snapshot{}, invalid("version_unsupported", source, "/apiVersion")
	}
	if wire.Kind != Kind {
		return Snapshot{}, invalid("kind_invalid", source, "/kind")
	}
	spec := SnapshotSpec{
		SourceProfile: wire.SourceProfile, ReadModelScope: wire.ReadModelScope, Revision: wire.Revision,
		Requirements: make([]RequirementCoverageSpec, len(wire.Requirements)),
	}
	for index, row := range wire.Requirements {
		pointer := "/requirements/" + itoa(index)
		requirement, err := provenance.ParseSourceRef(row.Requirement)
		if err != nil {
			return Snapshot{}, invalid("requirement_ref_invalid", source, pointer+"/requirement")
		}
		testRefs, err := parseRefs(row.TestRefs, "test", source, pointer+"/testRefs")
		if err != nil {
			return Snapshot{}, err
		}
		evidenceRefs, err := parseRefs(row.EvidenceRefs, "evidence", source, pointer+"/evidenceRefs")
		if err != nil {
			return Snapshot{}, err
		}
		freezeRefs, err := parseRefs(row.FreezeRefs, "freeze", source, pointer+"/freezeRefs")
		if err != nil {
			return Snapshot{}, err
		}
		spec.Requirements[index] = RequirementCoverageSpec{
			Requirement: requirement, Title: row.Title, Status: row.Status,
			TestRefs: testRefs, EvidenceRefs: evidenceRefs, FreezeRefs: freezeRefs,
			FreezeStatus: row.FreezeStatus, GapCodes: row.GapCodes,
		}
	}
	snapshot, err := NewSnapshot(spec)
	if err != nil {
		var typed *Error
		if errors.As(err, &typed) {
			return Snapshot{}, invalid(typed.Reason(), source, typed.Pointer())
		}
		return Snapshot{}, invalid("document_invalid", source, "")
	}
	schema, err := compiledSchema()
	if err != nil || schema.Validate(normalized) != nil {
		return Snapshot{}, invalid("document_invalid", source, "")
	}
	return snapshot, nil
}

func parseRefs(values []string, kind, source, pointer string) ([]provenance.SourceRef, error) {
	result := make([]provenance.SourceRef, len(values))
	for index, value := range values {
		ref, err := provenance.ParseSourceRef(value)
		if err != nil {
			return nil, invalid(kind+"_ref_invalid", source, pointer+"/"+itoa(index))
		}
		result[index] = ref
	}
	return result, nil
}

func documentError(err error) *Error {
	var document *strictdoc.Error
	if !errors.As(err, &document) {
		return invalid("document_invalid", "", "")
	}
	return invalid(document.Code, document.Source, document.Pointer)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[position:])
}
