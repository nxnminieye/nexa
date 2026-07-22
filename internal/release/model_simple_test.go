package release

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestManifestUsesStandardStrictJSONRoundTrip(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Candidate: Candidate{
			Repository: "https://github.com/nxnminieye/nexa.git", Module: "github.com/nxnminieye/nexa",
			Version: "v0.1.0-rc.1", Tag: "v0.1.0-rc.1", Commit: strings.Repeat("a", 40),
		},
		Artifacts:    []Artifact{{Path: "dist/nexa.tar.gz", Size: 42, SHA256: "sha256:" + strings.Repeat("b", 64), MediaType: "application/gzip"}},
		Dependencies: []Dependency{{Module: "example.com/dependency", Version: "v1.2.3", LicenseExpression: "Apache-2.0", SHA256: "sha256:" + strings.Repeat("c", 64)}},
	}
	want, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MarshalManifest(manifest)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("MarshalManifest() = %s, %v; want %s", got, err, want)
	}
	parsed, err := ParseManifest(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := MarshalManifest(parsed)
	if err != nil || !bytes.Equal(roundTrip, got) {
		t.Fatalf("manifest round-trip = %s, %v", roundTrip, err)
	}
	for _, invalid := range []string{
		string(got) + `{}`,
		strings.Replace(string(got), `"schemaVersion"`, `"unknown":true,"schemaVersion"`, 1),
	} {
		if _, err := ParseManifest(strings.NewReader(invalid)); err == nil {
			t.Fatalf("invalid manifest accepted: %s", invalid)
		}
	}
}

func TestSPDXAndLegalInventoryUseStandardDataWithoutAttestation(t *testing.T) {
	dependencies := []Dependency{{
		Module: "example.com/dependency", Version: "v1.2.3", LicenseExpression: "Apache-2.0",
		SHA256: "sha256:" + strings.Repeat("d", 64),
	}}
	document, err := BuildSPDX("nexa-v0.1.0-rc.1", "https://github.com/nxnminieye/nexa/releases/v0.1.0-rc.1", time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalSPDX(document)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSPDX(bytes.NewReader(encoded))
	if err != nil || len(parsed.Packages) != 1 || parsed.Packages[0].LicenseDeclared != "Apache-2.0" {
		t.Fatalf("ParseSPDX() = %#v, %v", parsed, err)
	}
	inventory, err := BuildLegalInventory(encoded, []byte("Third-party notices\n"), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.ExternalRequirements) != 1 || inventory.ExternalRequirements[0] != RelicensingRequirementID ||
		inventory.SPDXSHA256 == "" || inventory.NoticeSHA256 == "" || len(inventory.Dependencies) != 1 {
		t.Fatalf("inventory = %#v", inventory)
	}
	legalJSON, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"attestation", "approved", "satisfied", "signed"} {
		if strings.Contains(strings.ToLower(string(legalJSON)), forbidden) {
			t.Fatalf("legal inventory contains authority field %q: %s", forbidden, legalJSON)
		}
	}
}

func TestSPDXAndLegalInventoryAcceptParentAndSubmoduleCoordinates(t *testing.T) {
	dependencies := []Dependency{
		{
			Module: "example.com/parent", Version: "v1.2.3", LicenseExpression: "Apache-2.0",
			SHA256: "sha256:" + strings.Repeat("a", 64),
		},
		{
			Module: "example.com/parent/pkg/child", Version: "v1.2.3", LicenseExpression: "MIT",
			SHA256: "sha256:" + strings.Repeat("b", 64),
		},
	}

	document, err := BuildSPDX("parent-submodule-test", "https://example.com/spdx/parent-submodule", time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalSPDX(document)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSPDX(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := BuildLegalInventory(encoded, []byte("Third-party notices\n"), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Packages) != 2 || len(inventory.Dependencies) != 2 {
		t.Fatalf("packages = %d, inventory dependencies = %d", len(parsed.Packages), len(inventory.Dependencies))
	}
}

func TestSPDXBuildsAndStrictlyValidatesPackageRelationships(t *testing.T) {
	dependencies := []Dependency{
		{Module: "example.com/alpha", Version: "v1.0.0", LicenseExpression: "MIT", SHA256: "sha256:" + strings.Repeat("a", 64)},
		{Module: "example.com/beta", Version: "v1.2.0", LicenseExpression: "Apache-2.0", SHA256: "sha256:" + strings.Repeat("b", 64)},
	}
	document, err := BuildSPDX("relationship-test", "https://example.com/spdx/relationships", time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Relationships) != len(document.Packages) {
		t.Fatalf("relationships = %d, packages = %d", len(document.Relationships), len(document.Packages))
	}
	for index, relationship := range document.Relationships {
		want := SPDXRelationship{
			SPDXElementID:      "SPDXRef-DOCUMENT",
			RelationshipType:   "DESCRIBES",
			RelatedSPDXElement: document.Packages[index].SPDXID,
		}
		if relationship != want {
			t.Fatalf("relationship[%d] = %#v, want %#v", index, relationship, want)
		}
	}

	invalidDocuments := map[string]SPDXDocument{}
	missing := document
	missing.Relationships = nil
	invalidDocuments["missing"] = missing
	extra := cloneSPDXDocumentRelationships(document)
	extra.Relationships = append(extra.Relationships, extra.Relationships[0])
	invalidDocuments["extra"] = extra
	wrongSource := cloneSPDXDocumentRelationships(document)
	wrongSource.Relationships[0].SPDXElementID = document.Packages[0].SPDXID
	invalidDocuments["wrong source"] = wrongSource
	wrongType := cloneSPDXDocumentRelationships(document)
	wrongType.Relationships[0].RelationshipType = "CONTAINS"
	invalidDocuments["wrong type"] = wrongType
	wrongTarget := cloneSPDXDocumentRelationships(document)
	wrongTarget.Relationships[0].RelatedSPDXElement = "SPDXRef-Package-unknown"
	invalidDocuments["wrong target"] = wrongTarget
	duplicate := cloneSPDXDocumentRelationships(document)
	duplicate.Relationships[1] = duplicate.Relationships[0]
	invalidDocuments["duplicate"] = duplicate
	outOfOrder := cloneSPDXDocumentRelationships(document)
	outOfOrder.Relationships[0], outOfOrder.Relationships[1] = outOfOrder.Relationships[1], outOfOrder.Relationships[0]
	invalidDocuments["out of order"] = outOfOrder

	for name, candidate := range invalidDocuments {
		t.Run(name, func(t *testing.T) {
			if err := validateSPDX(candidate); err == nil {
				t.Fatal("validateSPDX accepted invalid relationships")
			}
			if _, err := MarshalSPDX(candidate); err == nil {
				t.Fatal("MarshalSPDX accepted invalid relationships")
			}
			encoded, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseSPDX(bytes.NewReader(encoded)); err == nil {
				t.Fatal("ParseSPDX accepted invalid relationships")
			}
		})
	}
}

func cloneSPDXDocumentRelationships(document SPDXDocument) SPDXDocument {
	clone := document
	clone.Relationships = append([]SPDXRelationship(nil), document.Relationships...)
	return clone
}

func TestModelRejectsPathMajorMismatchAndNonRepositoryURL(t *testing.T) {
	base := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Candidate:     Candidate{Repository: "https://example.com/repo.git", Module: "example.com/mod/v2", Version: "v2.0.0", Tag: "v2.0.0", Commit: strings.Repeat("a", 40)},
		Artifacts:     []Artifact{}, Dependencies: []Dependency{},
	}
	for _, mutate := range []func(*Manifest){
		func(value *Manifest) { value.Candidate.Version, value.Candidate.Tag = "v1.0.0", "v1.0.0" },
		func(value *Manifest) { value.Candidate.Repository = "https://example.com/repo.git?token=secret" },
		func(value *Manifest) { value.Candidate.Repository = "https://user@example.com/repo.git" },
	} {
		candidate := base
		mutate(&candidate)
		if _, err := MarshalManifest(candidate); err == nil {
			t.Fatalf("invalid candidate accepted: %#v", candidate.Candidate)
		}
	}
	base.Dependencies = []Dependency{{Module: "example.com/dependency/v2", Version: "v1.0.0", LicenseExpression: "Apache-2.0", SHA256: "sha256:" + strings.Repeat("b", 64)}}
	if _, err := MarshalManifest(base); err == nil {
		t.Fatal("dependency path-major mismatch accepted")
	}
}

func TestSPDXRejectsIDCollisionExpressionAndDependencyDrift(t *testing.T) {
	dependencies := []Dependency{
		{Module: "example.com/mod/a_b", Version: "v1.0.0", LicenseExpression: "MIT", SHA256: "sha256:" + strings.Repeat("a", 64)},
		{Module: "example.com/mod/a-b", Version: "v1.0.0", LicenseExpression: "Apache-2.0", SHA256: "sha256:" + strings.Repeat("b", 64)},
	}
	document, err := BuildSPDX("collision-test", "https://example.com/spdx/collision", time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if document.Packages[0].SPDXID == document.Packages[1].SPDXID {
		t.Fatalf("SPDX IDs collide: %#v", document.Packages)
	}
	document.Packages[0].LicenseDeclared = "MIT OR"
	if _, err := MarshalSPDX(document); err == nil {
		t.Fatal("invalid SPDX license expression accepted")
	}

	document, err = BuildSPDX("drift-test", "https://example.com/spdx/drift", time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC), dependencies[:1])
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalSPDX(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLegalInventory(encoded, []byte("NOTICE\n"), dependencies[1:]); err == nil {
		t.Fatal("dependency/SPDX drift accepted")
	}
}

func TestSPDXExpressionUsesOfficialLicenseList(t *testing.T) {
	for _, expression := range []string{
		"Not-A-Real-SPDX-License",
		"foo:bar",
		"LicenseRef-X",
		"DocumentRef-X:LicenseRef-Y",
		"LicenseRef-X WITH Anything",
		"MIT WITH Anything",
	} {
		if validSPDXExpression(expression) {
			t.Fatalf("unsupported SPDX expression accepted: %q", expression)
		}
	}
	for _, expression := range []string{
		"MIT",
		"Apache-2.0 OR BSD-3-Clause",
		"GPL-2.0-only WITH Classpath-exception-2.0",
	} {
		if !validSPDXExpression(expression) {
			t.Fatalf("valid SPDX expression rejected: %q", expression)
		}
	}

	document, err := BuildSPDX("spdx-id-test", "https://example.com/spdx/id", time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC), []Dependency{{
		Module: "example.com/dependency", Version: "v1.0.0", LicenseExpression: "MIT", SHA256: "sha256:" + strings.Repeat("a", 64),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []string{"SPDXRef-DOCUMENT", "", "Package-1", "SPDXRef-invalid_value"} {
		candidate := document
		candidate.Packages = append([]SPDXPackage(nil), document.Packages...)
		candidate.Packages[0].SPDXID = identifier
		if _, err := MarshalSPDX(candidate); err == nil {
			t.Fatalf("invalid package SPDXID accepted: %q", identifier)
		}
	}
}
