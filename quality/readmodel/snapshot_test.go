package readmodel

import (
	"bytes"
	"testing"

	"github.com/nxnminieye/nexa/provenance"
)

func TestSnapshotNewCanonicalSortingDefensiveCopiesAndDigest(t *testing.T) {
	requirementA := mustRef(t, "requirements/a.yaml", "requirement:a")
	requirementB := mustRef(t, "requirements/b.yaml", "requirement:b")
	testA := mustRef(t, "quality/tests/a.yaml", "test:a")
	testB := mustRef(t, "quality/tests/b.yaml", "test:b")
	evidenceA := mustRef(t, "quality/evidence/a.json", "evidence:a")
	freezeA := mustRef(t, "quality/freeze/a.yaml", "freeze:a")

	requirements := []RequirementCoverageSpec{
		{
			Requirement: requirementB, Title: "B", Status: "covered",
			TestRefs: []provenance.SourceRef{testB, testA}, EvidenceRefs: []provenance.SourceRef{evidenceA},
			FreezeRefs: []provenance.SourceRef{freezeA}, FreezeStatus: FreezeFrozen,
			GapCodes: []string{"z-gap", "a-gap"},
		},
		{Requirement: requirementA, Title: "A", Status: "open", FreezeStatus: FreezeNone},
	}
	snapshot, err := NewSnapshot(SnapshotSpec{
		SourceProfile: "production", ReadModelScope: "git:main", Revision: "abc123", Requirements: requirements,
	})
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	requirements[0].Title = "mutated"
	requirements[0].TestRefs[0] = requirementA

	rows := snapshot.Requirements()
	if len(rows) != 2 || rows[0].Requirement() != requirementA || rows[1].Requirement() != requirementB {
		t.Fatalf("requirements order = %#v", rows)
	}
	if rows[1].Title() != "B" || rows[1].Status() != "covered" || rows[1].FreezeStatus() != FreezeFrozen {
		t.Fatalf("row values = title:%q status:%q freeze:%q", rows[1].Title(), rows[1].Status(), rows[1].FreezeStatus())
	}
	if got := rows[1].TestRefs(); len(got) != 2 || got[0] != testA || got[1] != testB {
		t.Fatalf("test refs = %#v", got)
	}
	if got := rows[1].GapCodes(); len(got) != 2 || got[0] != "a-gap" || got[1] != "z-gap" {
		t.Fatalf("gap codes = %#v", got)
	}
	rows[0] = RequirementCoverage{}
	tests := rows[1].TestRefs()
	tests[0] = requirementB
	if snapshot.Requirements()[0].Requirement() != requirementA || snapshot.Requirements()[1].TestRefs()[0] != testA {
		t.Fatal("accessor mutation changed snapshot")
	}

	canonical, err := CanonicalJSON(snapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	parsed, err := Parse("quality/read-model.json", canonical)
	if err != nil {
		t.Fatalf("Parse(canonical) error = %v", err)
	}
	parsedCanonical, err := CanonicalJSON(parsed)
	if err != nil || !bytes.Equal(parsedCanonical, canonical) {
		t.Fatalf("round trip = %s, %v; want %s", parsedCanonical, err, canonical)
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	reordered, err := NewSnapshot(SnapshotSpec{
		SourceProfile: "production", ReadModelScope: "git:main", Revision: "abc123",
		Requirements: []RequirementCoverageSpec{requirements[1], {
			Requirement: requirementB, Title: "B", Status: "covered",
			TestRefs: []provenance.SourceRef{testA, testB}, EvidenceRefs: []provenance.SourceRef{evidenceA},
			FreezeRefs: []provenance.SourceRef{freezeA}, FreezeStatus: FreezeFrozen, GapCodes: []string{"a-gap", "z-gap"},
		}},
	})
	if err != nil {
		t.Fatalf("NewSnapshot(reordered) error = %v", err)
	}
	reorderedDigest, err := reordered.Digest()
	if err != nil || reorderedDigest != digest {
		t.Fatalf("digest = %s, %v; want %s", reorderedDigest.String(), err, digest.String())
	}
}

func TestSnapshotEmptyIsExplicitCanonicalProjection(t *testing.T) {
	empty := Empty()
	if empty.SourceProfile() != "" || empty.ReadModelScope() != "" || empty.Revision() != "" {
		t.Fatalf("empty identity = (%q,%q,%q)", empty.SourceProfile(), empty.ReadModelScope(), empty.Revision())
	}
	if rows := empty.Requirements(); rows == nil || len(rows) != 0 {
		t.Fatalf("empty requirements = %#v, want nonnil empty", rows)
	}
	canonical, err := CanonicalJSON(empty)
	if err != nil {
		t.Fatalf("CanonicalJSON(Empty) error = %v", err)
	}
	parsed, err := Parse("quality/empty.json", canonical)
	if err != nil || len(parsed.Requirements()) != 0 {
		t.Fatalf("Parse(Empty) = %#v, %v", parsed.Requirements(), err)
	}
}

func mustRef(t *testing.T, path, fragment string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
