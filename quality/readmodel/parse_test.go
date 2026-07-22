package readmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSnapshotSchemaRoundTripAndDefensiveCopy(t *testing.T) {
	first := Schema()
	if len(first) == 0 {
		t.Fatal("Schema() is empty")
	}
	var schemaDocument any
	if err := json.Unmarshal(first, &schemaDocument); err != nil {
		t.Fatalf("Schema() JSON error = %v", err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://nexa.dev/schemas/quality/quality-read-model-v1.schema.json"
	if err := compiler.AddResource(location, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalJSON(Empty())
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(canonical, &document); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(document); err != nil {
		t.Fatalf("canonical schema validation = %v", err)
	}
	first[0] ^= 0xff
	if bytes.Equal(first, Schema()) {
		t.Fatal("Schema() is not defensive")
	}
}

func TestSnapshotRejectsInvalidRelationsAndStrictDocuments(t *testing.T) {
	requirement := mustRef(t, "requirements/a.yaml", "requirement:a")
	testRef := mustRef(t, "quality/tests/a.yaml", "test:a")
	freezeRef := mustRef(t, "quality/freeze/a.yaml", "freeze:a")
	tests := []struct {
		name    string
		spec    SnapshotSpec
		reason  string
		pointer string
	}{
		{name: "requirement unresolved", spec: SnapshotSpec{Requirements: []RequirementCoverageSpec{{FreezeStatus: FreezeNone}}}, reason: "requirement_ref_unresolved", pointer: "/requirements/0/requirement"},
		{name: "duplicate requirement", spec: SnapshotSpec{Requirements: []RequirementCoverageSpec{{Requirement: requirement, FreezeStatus: FreezeNone}, {Requirement: requirement, FreezeStatus: FreezeNone}}}, reason: "requirement_ref_duplicate", pointer: "/requirements/1/requirement"},
		{name: "test unresolved", spec: SnapshotSpec{Requirements: []RequirementCoverageSpec{{Requirement: requirement, TestRefs: []provenance.SourceRef{{}}, FreezeStatus: FreezeNone}}}, reason: "test_ref_unresolved", pointer: "/requirements/0/testRefs/0"},
		{name: "test duplicate", spec: SnapshotSpec{Requirements: []RequirementCoverageSpec{{Requirement: requirement, TestRefs: []provenance.SourceRef{testRef, testRef}, FreezeStatus: FreezeNone}}}, reason: "test_ref_duplicate", pointer: "/requirements/0/testRefs/1"},
		{name: "freeze status invalid", spec: SnapshotSpec{Requirements: []RequirementCoverageSpec{{Requirement: requirement, FreezeStatus: FreezeStatus("invalid")}}}, reason: "freeze_status_invalid", pointer: "/requirements/0/freezeStatus"},
		{name: "changed without freeze", spec: SnapshotSpec{Requirements: []RequirementCoverageSpec{{Requirement: requirement, FreezeStatus: FreezeChanged}}}, reason: "changed_without_freeze_ref", pointer: "/requirements/0/freezeRefs"},
		{name: "none with freeze", spec: SnapshotSpec{Requirements: []RequirementCoverageSpec{{Requirement: requirement, FreezeStatus: FreezeNone, FreezeRefs: []provenance.SourceRef{freezeRef}}}}, reason: "freeze_ref_unexpected", pointer: "/requirements/0/freezeRefs/0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSnapshot(test.spec)
			requireReadModelError(t, err, test.reason, test.pointer)
		})
	}

	valid, err := CanonicalJSON(Empty())
	if err != nil {
		t.Fatal(err)
	}
	documents := []struct {
		name   string
		data   []byte
		reason string
	}{
		{name: "unknown", data: bytes.Replace(valid, []byte(`"revision":""`), []byte(`"revision":"","unknown":true`), 1), reason: "document_unknown_field"},
		{name: "trailing", data: append(append([]byte(nil), valid...), []byte(` {}`)...), reason: "document_trailing_input"},
		{name: "null", data: bytes.Replace(valid, []byte(`"requirements":[]`), []byte(`"requirements":null`), 1), reason: "document_invalid"},
		{name: "invalid source ref", data: []byte(`{"apiVersion":"nexa.dev/quality-read-model/v1","kind":"QualityReadModel","sourceProfile":"","readModelScope":"","revision":"","requirements":[{"requirement":"bad","title":"","status":"","testRefs":[],"evidenceRefs":[],"freezeRefs":[],"freezeStatus":"none","gapCodes":[]}]}`), reason: "requirement_ref_invalid"},
	}
	for _, document := range documents {
		t.Run(document.name, func(t *testing.T) {
			_, err := Parse("quality/read-model.json", document.data)
			requireReadModelError(t, err, document.reason, "")
		})
	}
}

func requireReadModelError(t *testing.T, err error, reason, pointer string) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var typed *Error
	if !errors.As(err, &typed) || typed == nil {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if typed.Code() != "quality_read_model_invalid" || typed.Reason() != reason {
		t.Fatalf("error tuple = (%q,%q,%q)", typed.Code(), typed.Reason(), typed.Pointer())
	}
	if pointer != "" && typed.Pointer() != pointer {
		t.Fatalf("Pointer() = %q, want %q", typed.Pointer(), pointer)
	}
	return typed
}
