package artifact_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/provenance"
)

func TestInvalidManifestDocuments(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	valid := func(overrides string) string {
		return `{"apiVersion":"nexa.dev/artifact-manifest/v1","kind":"ArtifactManifest","generator":{"id":"owner","version":"v1.0.0"},"inputDigest":"sha256:79a04b4cca088844a4c6ba267b3fbb3c965cd6d09d6c5ffd1bd01da186be4691","sources":[],"artifacts":[]` + overrides + `}`
	}
	tests := []struct {
		name, data, reason, pointer string
	}{
		{name: "unknown field", data: valid(`,"extra":true`), reason: "document_unknown_field", pointer: "/extra"},
		{name: "duplicate field", data: `{"apiVersion":"nexa.dev/artifact-manifest/v1","kind":"ArtifactManifest","kind":"ArtifactManifest","generator":{"id":"owner","version":"v1.0.0"},"inputDigest":"sha256:` + validDigest + `","sources":[],"artifacts":[]}`, reason: "document_duplicate_key", pointer: "/kind"},
		{name: "trailing document", data: valid(``) + `{}`, reason: "document_trailing_input"},
		{name: "unsupported version", data: strings.Replace(valid(``), artifact.APIVersion, "nexa.dev/artifact-manifest/v2", 1), reason: "version_unsupported", pointer: "/apiVersion"},
		{name: "invalid kind", data: strings.Replace(valid(``), artifact.Kind, "Manifest", 1), reason: "kind_invalid", pointer: "/kind"},
		{name: "null source list", data: strings.Replace(valid(``), `"sources":[]`, `"sources":null`, 1), reason: "document_invalid", pointer: "/sources"},
		{name: "missing generator id", data: strings.Replace(valid(``), `"id":"owner",`, "", 1), reason: "document_invalid", pointer: "/generator/id"},
		{name: "invalid input digest", data: strings.Replace(valid(``), `sha256:79a04b4cca088844a4c6ba267b3fbb3c965cd6d09d6c5ffd1bd01da186be4691`, `sha256:invalid`, 1), reason: "input_digest_invalid", pointer: "/inputDigest"},
		{name: "hidden generator options", data: strings.Replace(valid(``), `"version":"v1.0.0"`, `"version":"v1.0.0","options":{"mode":"hidden"}`, 1), reason: "document_unknown_field", pointer: "/generator/options"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := artifact.Parse("manifest.json", []byte(test.data))
			manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestParseManifestYAML(t *testing.T) {
	data := []byte(`apiVersion: nexa.dev/artifact-manifest/v1
kind: ArtifactManifest
generator:
  id: api-manifest
  version: v0.1.0
inputDigest: sha256:963ceae87196b5344acb1f71697d9eb61297ffa463d8a2992b4445df9a3e6dff
sources:
  - ref: repo:desc/core.api
    digest: sha256:41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d
artifacts:
  - id: core-api-manifest
    path: generated/api-manifest.json
    owner: api-manifest
    digest: sha256:c7c5c1d70c5dec4416ab6158afd0b223ef40c29b1dc1f97ed9428b94d4cadb1c
    sources: [repo:desc/core.api]
    stalePolicy: delete-if-unmodified
`)
	manifest, err := artifact.Parse("manifest.yaml", data)
	if err != nil {
		t.Fatalf("Parse(YAML) error = %v", err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != canonicalManifest {
		t.Fatalf("canonical YAML projection = %s, want %s", encoded, canonicalManifest)
	}
}

func TestInvalidManifestSemantics(t *testing.T) {
	ref := mustRef(t, "repo:desc/core.api")
	digest := provenance.SHA256([]byte("source"))
	validArtifact := artifact.ArtifactSpec{ID: "output", Path: "generated/output.json", Owner: "owner", Digest: provenance.SHA256([]byte("artifact")), Sources: []provenance.SourceRef{ref}, StalePolicy: artifact.StaleRetain}
	tests := []struct {
		name, reason, pointer string
		mutate                func(*artifact.ManifestSpec)
	}{
		{name: "generator id", reason: "generator_id_invalid", pointer: "/generator/id", mutate: func(spec *artifact.ManifestSpec) { spec.Generator.ID = "Owner" }},
		{name: "generator version", reason: "generator_version_invalid", pointer: "/generator/version", mutate: func(spec *artifact.ManifestSpec) { spec.Generator.Version = "1.0" }},
		{name: "zero source ref leaves artifact unresolved", reason: "artifact_source_unresolved", pointer: "/artifacts/0/sources/0", mutate: func(spec *artifact.ManifestSpec) { spec.Sources[0].Ref = provenance.SourceRef{} }},
		{name: "zero source digest", reason: "source_digest_invalid", pointer: "/sources/0/digest", mutate: func(spec *artifact.ManifestSpec) { spec.Sources[0].Digest = provenance.Digest{} }},
		{name: "duplicate source", reason: "source_duplicate", pointer: "/sources/1/ref", mutate: func(spec *artifact.ManifestSpec) { spec.Sources = append(spec.Sources, spec.Sources[0]) }},
		{name: "artifact id", reason: "artifact_id_invalid", pointer: "/artifacts/0/id", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].ID = "Output" }},
		{name: "artifact path", reason: "artifact_path_invalid", pointer: "/artifacts/0/path", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].Path = "../output" }},
		{name: "artifact owner", reason: "artifact_owner_invalid", pointer: "/artifacts/0/owner", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].Owner = "Owner" }},
		{name: "owner mismatch", reason: "artifact_owner_mismatch", pointer: "/artifacts/0/owner", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].Owner = "different" }},
		{name: "zero artifact digest", reason: "artifact_digest_invalid", pointer: "/artifacts/0/digest", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].Digest = provenance.Digest{} }},
		{name: "duplicate artifact id", reason: "artifact_duplicate", pointer: "/artifacts/1/id", mutate: func(spec *artifact.ManifestSpec) {
			second := spec.Artifacts[0]
			second.Path = "generated/second.json"
			spec.Artifacts = append(spec.Artifacts, second)
		}},
		{name: "duplicate artifact path", reason: "artifact_path_duplicate", pointer: "/artifacts/1/path", mutate: func(spec *artifact.ManifestSpec) {
			second := spec.Artifacts[0]
			second.ID = "second"
			spec.Artifacts = append(spec.Artifacts, second)
		}},
		{name: "duplicate artifact source", reason: "artifact_source_duplicate", pointer: "/artifacts/0/sources/1", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].Sources = append(spec.Artifacts[0].Sources, ref) }},
		{name: "unresolved artifact source", reason: "artifact_source_unresolved", pointer: "/artifacts/0/sources/0", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].Sources[0] = mustRef(t, "repo:desc/missing.api") }},
		{name: "zero stale policy", reason: "stale_policy_invalid", pointer: "/artifacts/0/stalePolicy", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].StalePolicy = "" }},
		{name: "unknown stale policy", reason: "stale_policy_invalid", pointer: "/artifacts/0/stalePolicy", mutate: func(spec *artifact.ManifestSpec) { spec.Artifacts[0].StalePolicy = "delete" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := artifact.ManifestSpec{Generator: artifact.GeneratorSpec{ID: "owner", Version: "v1.0.0"}, Sources: []provenance.Source{{Ref: ref, Digest: digest}}, Artifacts: []artifact.ArtifactSpec{validArtifact}}
			spec.Artifacts[0].Sources = append([]provenance.SourceRef(nil), validArtifact.Sources...)
			test.mutate(&spec)
			_, err := artifact.NewManifest(spec)
			manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", test.reason)
			if manifestError.Pointer() != test.pointer {
				t.Fatalf("Pointer() = %q, want %q", manifestError.Pointer(), test.pointer)
			}
		})
	}
}

func TestInvalidManifestStoredInputDigest(t *testing.T) {
	data := strings.Replace(canonicalManifest, "963ceae87196b5344acb1f71697d9eb61297ffa463d8a2992b4445df9a3e6dff", strings.Repeat("a", 64), 1)
	_, err := artifact.Parse("manifest.json", []byte(data))
	manifestError := requireArtifactError(t, err, "artifact_digest_mismatch", "input_digest_mismatch")
	if manifestError.Pointer() != "/inputDigest" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
}

func TestInvalidManifestSelectsFirstCandidateAcrossPhases(t *testing.T) {
	data := `{"apiVersion":"nexa.dev/artifact-manifest/v2","kind":"ArtifactManifest","generator":{"id":"Owner","version":"v1.0.0","extra":true},"inputDigest":null,"sources":[],"artifacts":[]}`
	_, err := artifact.Parse("manifest.json", []byte(data))
	manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "version_unsupported")
	if manifestError.Pointer() != "/apiVersion" {
		t.Fatalf("Pointer() = %q, want /apiVersion", manifestError.Pointer())
	}
}

func TestInvalidManifestSelectsEarlierSemanticBeforeLaterSchemaFailure(t *testing.T) {
	data := `{"apiVersion":"nexa.dev/artifact-manifest/v1","kind":"ArtifactManifest","generator":{"id":"owner","version":"v1.0.0"},"inputDigest":"sha256:79a04b4cca088844a4c6ba267b3fbb3c965cd6d09d6c5ffd1bd01da186be4691","sources":[],"artifacts":[{"id":"Output","path":"generated/output.json","owner":"owner","digest":"sha256:` + strings.Repeat("a", 64) + `","sources":[],"stalePolicy":"retain"},{"id":"second","path":"generated/second.json","owner":"owner","digest":"sha256:` + strings.Repeat("b", 64) + `","sources":[],"stalePolicy":"retain","extra":true}]}`
	_, err := artifact.Parse("manifest.json", []byte(data))
	manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "artifact_id_invalid")
	if manifestError.Pointer() != "/artifacts/0/id" {
		t.Fatalf("Pointer() = %q, want /artifacts/0/id", manifestError.Pointer())
	}
}

func TestParseManifestRejectsShorthandSemVer(t *testing.T) {
	for _, version := range []string{"v1", "v1.2"} {
		t.Run(version, func(t *testing.T) {
			data := strings.Replace(canonicalManifest, `"version":"v0.1.0"`, `"version":"`+version+`"`, 1)
			_, err := artifact.Parse("manifest.json", []byte(data))
			manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "generator_version_invalid")
			if manifestError.Pointer() != "/generator/version" {
				t.Fatalf("Pointer() = %q, want /generator/version", manifestError.Pointer())
			}
		})
	}
}

func TestParseInvalidSourceDoesNotSuppressUnresolvedArtifactSource(t *testing.T) {
	data := `{"apiVersion":"nexa.dev/artifact-manifest/v1","kind":"ArtifactManifest","generator":{"id":"owner","version":"v1.0.0"},"inputDigest":"sha256:` + strings.Repeat("a", 64) + `","sources":[{"ref":"not-a-ref","digest":"sha256:` + strings.Repeat("b", 64) + `"}],"artifacts":[{"id":"output","path":"generated/output.json","owner":"owner","digest":"sha256:` + strings.Repeat("c", 64) + `","sources":["repo:desc/missing.api"],"stalePolicy":"retain"}]}`
	_, err := artifact.Parse("manifest.json", []byte(data))
	manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "artifact_source_unresolved")
	if manifestError.Pointer() != "/artifacts/0/sources/0" {
		t.Fatalf("Pointer() = %q, want /artifacts/0/sources/0", manifestError.Pointer())
	}

	withoutArtifact := strings.Replace(data, `"artifacts":[{"id":"output","path":"generated/output.json","owner":"owner","digest":"sha256:`+strings.Repeat("c", 64)+`","sources":["repo:desc/missing.api"],"stalePolicy":"retain"}]`, `"artifacts":[]`, 1)
	_, err = artifact.Parse("manifest.json", []byte(withoutArtifact))
	manifestError = requireArtifactError(t, err, "artifact_manifest_invalid", "source_ref_invalid")
	if manifestError.Pointer() != "/sources/0/ref" {
		t.Fatalf("Pointer() = %q, want /sources/0/ref", manifestError.Pointer())
	}
}

func TestInvalidManifestDoesNotProjectInputMismatchFromInvalidSource(t *testing.T) {
	data := `{"apiVersion":"nexa.dev/artifact-manifest/v1","kind":"ArtifactManifest","generator":{"id":"owner","version":"v1.0.0"},"inputDigest":"sha256:` + strings.Repeat("a", 64) + `","sources":[{"ref":"not-a-ref","digest":"sha256:` + strings.Repeat("b", 64) + `"}],"artifacts":[]}`
	_, err := artifact.Parse("manifest.json", []byte(data))
	manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "source_ref_invalid")
	if manifestError.Pointer() != "/sources/0/ref" {
		t.Fatalf("Pointer() = %q, want /sources/0/ref", manifestError.Pointer())
	}
}

func TestInvalidManifestUsesContainerAwarePointerOrder(t *testing.T) {
	artifacts := make([]string, 11)
	for index := range artifacts {
		artifacts[index] = `{"id":"output-` + string(rune('a'+index)) + `","path":"generated/output-` + string(rune('a'+index)) + `.json","owner":"owner","digest":"sha256:` + strings.Repeat("a", 64) + `","sources":[],"stalePolicy":"retain"}`
	}
	artifacts[2] = strings.Replace(artifacts[2], `"id":"output-c"`, `"id":"Output-c"`, 1)
	artifacts[10] = strings.Replace(artifacts[10], `"id":"output-k"`, `"id":"Output-k"`, 1)
	data := `{"apiVersion":"nexa.dev/artifact-manifest/v1","kind":"ArtifactManifest","generator":{"id":"owner","version":"v1.0.0"},"inputDigest":"sha256:79a04b4cca088844a4c6ba267b3fbb3c965cd6d09d6c5ffd1bd01da186be4691","sources":[],"artifacts":[` + strings.Join(artifacts, ",") + `]}`
	_, err := artifact.Parse("manifest.json", []byte(data))
	manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "artifact_id_invalid")
	if manifestError.Pointer() != "/artifacts/2/id" {
		t.Fatalf("Pointer() = %q, want /artifacts/2/id", manifestError.Pointer())
	}
}

func TestArtifactErrorDoesNotExposeRawCause(t *testing.T) {
	_, first := artifact.Parse("manifest.json", []byte(`{"apiVersion":`))
	_, second := artifact.Parse("other.json", []byte(`{"apiVersion":`))
	firstError := requireArtifactError(t, first, "artifact_manifest_invalid", "document_invalid")
	secondError := requireArtifactError(t, second, "artifact_manifest_invalid", "document_invalid")
	if firstError.Unwrap() == nil || !errors.Is(firstError, secondError.Unwrap()) {
		t.Fatal("errors do not unwrap to a stable sentinel")
	}
	if strings.Contains(firstError.Error(), "unexpected") || strings.Contains(firstError.Error(), "EOF") {
		t.Fatalf("Error() leaks parser details: %q", firstError.Error())
	}
}

func requireArtifactError(t *testing.T, err error, code, reason string) *artifact.Error {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil")
	}
	var manifestError *artifact.Error
	if !errors.As(err, &manifestError) {
		t.Fatalf("error type = %T, want *artifact.Error", err)
	}
	if manifestError.Code() != code || manifestError.Reason() != reason {
		t.Fatalf("error = code %q reason %q, want %q %q", manifestError.Code(), manifestError.Reason(), code, reason)
	}
	return manifestError
}
