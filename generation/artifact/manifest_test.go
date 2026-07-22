package artifact_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/provenance"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const canonicalManifest = "{\"apiVersion\":\"nexa.dev/artifact-manifest/v1\",\"kind\":\"ArtifactManifest\",\"generator\":{\"id\":\"api-manifest\",\"version\":\"v0.1.0\"},\"inputDigest\":\"sha256:963ceae87196b5344acb1f71697d9eb61297ffa463d8a2992b4445df9a3e6dff\",\"sources\":[{\"ref\":\"repo:desc/core.api\",\"digest\":\"sha256:41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d\"}],\"artifacts\":[{\"id\":\"core-api-manifest\",\"path\":\"generated/api-manifest.json\",\"owner\":\"api-manifest\",\"digest\":\"sha256:c7c5c1d70c5dec4416ab6158afd0b223ef40c29b1dc1f97ed9428b94d4cadb1c\",\"sources\":[\"repo:desc/core.api\"],\"stalePolicy\":\"delete-if-unmodified\"}]}\n"

func TestManifestCanonicalRoundTrip(t *testing.T) {
	source := provenance.Source{Ref: mustRef(t, "repo:desc/core.api"), Digest: provenance.SHA256([]byte("source"))}
	manifest, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "api-manifest", Version: "v0.1.0"},
		Sources:   []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{
			ID: "core-api-manifest", Path: "generated/api-manifest.json", Owner: "api-manifest",
			Digest: provenance.SHA256([]byte("artifact")), Sources: []provenance.SourceRef{source.Ref},
			StalePolicy: artifact.StaleDeleteIfUnmodified,
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if got := string(encoded); got != canonicalManifest {
		t.Fatalf("CanonicalJSON() =\n%s\nwant\n%s", got, canonicalManifest)
	}

	parsed, err := artifact.Parse("artifact-manifest.json", encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	assertManifestProjection(t, parsed, manifestProjection{
		APIVersion: artifact.APIVersion, GeneratorID: "api-manifest", GeneratorVersion: "v0.1.0",
		InputDigest: "sha256:963ceae87196b5344acb1f71697d9eb61297ffa463d8a2992b4445df9a3e6dff",
		Sources:     []string{"repo:desc/core.api=sha256:41cf6794ba4200b839c53531555f0f3998df4cbb01a4d5cb0b94e3ca5e23947d"},
		Artifacts: []artifactProjection{{
			ID: "core-api-manifest", Path: "generated/api-manifest.json", Owner: "api-manifest",
			Digest:  "sha256:c7c5c1d70c5dec4416ab6158afd0b223ef40c29b1dc1f97ed9428b94d4cadb1c",
			Sources: []string{"repo:desc/core.api"}, StalePolicy: "delete-if-unmodified",
		}},
	})
}

func TestPublicSchemaValidatesArtifactManifestDocuments(t *testing.T) {
	source := provenance.Source{Ref: mustRef(t, "repo:desc/core.api"), Digest: provenance.SHA256([]byte("source"))}
	manifest, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "api-manifest", Version: "v0.1.0"},
		Sources:   []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{
			ID: "core-api-manifest", Path: "generated/api-manifest.json", Owner: "api-manifest",
			Digest: provenance.SHA256([]byte("artifact")), Sources: []provenance.SourceRef{source.Ref},
			StalePolicy: artifact.StaleDeleteIfUnmodified,
		}},
	})
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	canonical, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	schema := compiledArtifactPublicSchema(t)
	var document any
	if err := json.Unmarshal(canonical, &document); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(document); err != nil {
		t.Fatalf("public schema rejected canonical manifest: %v", err)
	}
	if _, err := artifact.Parse("artifact-manifest.json", canonical); err != nil {
		t.Fatalf("Parse(canonical) error = %v", err)
	}

	base := document.(map[string]any)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing", mutate: func(value map[string]any) { delete(value, "artifacts") }},
		{name: "wrong type", mutate: func(value map[string]any) { value["artifacts"] = "none" }},
		{name: "null", mutate: func(value map[string]any) { value["artifacts"] = nil }},
		{name: "unknown field", mutate: func(value map[string]any) { value["profile"] = "sample" }},
		{name: "wrong apiVersion const", mutate: func(value map[string]any) { value["apiVersion"] = "nexa.dev/artifact-manifest/v2" }},
		{name: "wrong kind const", mutate: func(value map[string]any) { value["kind"] = "GeneratedArtifacts" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := make(map[string]any, len(base))
			for key, value := range base {
				candidate[key] = value
			}
			test.mutate(candidate)
			if err := schema.Validate(candidate); err == nil {
				t.Fatal("public schema accepted invalid manifest")
			}
		})
	}

	_, err = artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "api-manifest", Version: "v0.1.0"},
		Sources:   []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{
			ID: "core-api-manifest", Path: "generated/api-manifest.json", Owner: "other-generator",
			Digest: provenance.SHA256([]byte("artifact")), Sources: []provenance.SourceRef{source.Ref},
			StalePolicy: artifact.StaleRetain,
		}},
	})
	requireArtifactError(t, err, "artifact_manifest_invalid", "artifact_owner_mismatch")
}

func compiledArtifactPublicSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var schemaDocument any
	if err := json.Unmarshal(artifact.Schema(), &schemaDocument); err != nil {
		t.Fatalf("Schema() is not JSON: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://nexa.dev/schemas/generation/artifact-manifest-v1.schema.json"
	if err := compiler.AddResource(location, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestManifestCanonicalOrderIgnoresSpecOrder(t *testing.T) {
	alpha := provenance.Source{Ref: mustRef(t, "repo:desc/a.api"), Digest: provenance.SHA256([]byte("a"))}
	zeta := provenance.Source{Ref: mustRef(t, "repo:desc/z.api"), Digest: provenance.SHA256([]byte("z"))}
	first := artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "api-manifest", Version: "v1.2.3"},
		Sources:   []provenance.Source{zeta, alpha},
		Artifacts: []artifact.ArtifactSpec{
			{ID: "zeta", Path: "generated/zeta.json", Owner: "api-manifest", Digest: provenance.SHA256([]byte("zeta")), Sources: []provenance.SourceRef{zeta.Ref, alpha.Ref}, StalePolicy: artifact.StaleRetain},
			{ID: "alpha", Path: "generated/alpha.json", Owner: "api-manifest", Digest: provenance.SHA256([]byte("alpha")), Sources: []provenance.SourceRef{alpha.Ref}, StalePolicy: artifact.StaleDeleteIfUnmodified},
		},
	}
	second := artifact.ManifestSpec{
		Generator: first.Generator,
		Sources:   []provenance.Source{alpha, zeta},
		Artifacts: []artifact.ArtifactSpec{first.Artifacts[1], first.Artifacts[0]},
	}
	second.Artifacts[1].Sources = []provenance.SourceRef{alpha.Ref, zeta.Ref}

	firstManifest, err := artifact.NewManifest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := artifact.NewManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := firstManifest.CanonicalJSON()
	secondJSON, _ := secondManifest.CanonicalJSON()
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical output depends on input order:\n%s\n%s", firstJSON, secondJSON)
	}
}

func TestManifestEmptyListsEncodeAsArrays(t *testing.T) {
	manifest, err := artifact.NewManifest(artifact.ManifestSpec{Generator: artifact.GeneratorSpec{ID: "empty", Version: "v1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"apiVersion\":\"nexa.dev/artifact-manifest/v1\",\"kind\":\"ArtifactManifest\",\"generator\":{\"id\":\"empty\",\"version\":\"v1.0.0\"},\"inputDigest\":\"sha256:239441d9da1a30b1c45a2453e246888b471544b6bec37079fffa938e5b5d7ee1\",\"sources\":[],\"artifacts\":[]}\n"
	if string(encoded) != want {
		t.Fatalf("CanonicalJSON() = %s, want %s", encoded, want)
	}
}

func TestManifestRequiresCompleteSemVerCore(t *testing.T) {
	for _, version := range []string{"v1", "v1.2"} {
		t.Run("reject_"+version, func(t *testing.T) {
			_, err := artifact.NewManifest(artifact.ManifestSpec{Generator: artifact.GeneratorSpec{ID: "owner", Version: version}})
			manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "generator_version_invalid")
			if manifestError.Pointer() != "/generator/version" {
				t.Fatalf("Pointer() = %q, want /generator/version", manifestError.Pointer())
			}
		})
	}
	for _, version := range []string{"v1.2.3", "v1.2.3-alpha.1+build.5"} {
		t.Run("accept_"+version, func(t *testing.T) {
			manifest, err := artifact.NewManifest(artifact.ManifestSpec{Generator: artifact.GeneratorSpec{ID: "owner", Version: version}})
			if err != nil {
				t.Fatalf("NewManifest(%q) error = %v", version, err)
			}
			encoded, err := manifest.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := artifact.Parse("manifest.json", encoded)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", version, err)
			}
			if parsed.Generator().Version() != version {
				t.Fatalf("Version() = %q, want %q", parsed.Generator().Version(), version)
			}
		})
	}
}

func TestImmutableManifestAccessors(t *testing.T) {
	source := provenance.Source{Ref: mustRef(t, "repo:source.api"), Digest: provenance.SHA256([]byte("source"))}
	artifactSources := []provenance.SourceRef{source.Ref}
	spec := artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "owner", Version: "v1.0.0"},
		Sources:   []provenance.Source{source},
		Artifacts: []artifact.ArtifactSpec{{ID: "output", Path: "generated/output.json", Owner: "owner", Digest: provenance.SHA256([]byte("output")), Sources: artifactSources, StalePolicy: artifact.StaleRetain}},
	}
	manifest, err := artifact.NewManifest(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Sources[0] = provenance.Source{}
	spec.Artifacts[0] = artifact.ArtifactSpec{}
	artifactSources[0] = provenance.SourceRef{}

	sources := manifest.Sources()
	sources[0] = provenance.Source{}
	artifacts := manifest.Artifacts()
	artifacts[0] = artifact.Artifact{}
	artifactRefs := manifest.Artifacts()[0].Sources()
	artifactRefs[0] = provenance.SourceRef{}
	assertManifestProjection(t, manifest, manifestProjection{
		APIVersion: artifact.APIVersion, GeneratorID: "owner", GeneratorVersion: "v1.0.0",
		InputDigest: manifest.InputDigest().String(), Sources: []string{source.Ref.String() + "=" + source.Digest.String()},
		Artifacts: []artifactProjection{{ID: "output", Path: "generated/output.json", Owner: "owner", Digest: provenance.SHA256([]byte("output")).String(), Sources: []string{source.Ref.String()}, StalePolicy: "retain"}},
	})

	schema := artifact.Schema()
	if len(schema) == 0 {
		t.Fatal("Schema() returned empty content")
	}
	schema[0] ^= 0xff
	if bytes.Equal(schema, artifact.Schema()) {
		t.Fatal("mutating Schema() changed later result")
	}
}

func TestValidateInputDigest(t *testing.T) {
	manifest, err := artifact.NewManifest(artifact.ManifestSpec{Generator: artifact.GeneratorSpec{ID: "owner", Version: "v1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := artifact.ValidateInputDigest(manifest, manifest.InputDigest()); err != nil {
		t.Fatalf("ValidateInputDigest(valid) error = %v", err)
	}
	err = artifact.ValidateInputDigest(manifest, provenance.SHA256([]byte("different")))
	manifestError := requireArtifactError(t, err, "artifact_digest_mismatch", "input_digest_mismatch")
	if manifestError.Pointer() != "/inputDigest" {
		t.Fatalf("Pointer() = %q", manifestError.Pointer())
	}
	if err := artifact.ValidateInputDigest(artifact.Manifest{}, provenance.Digest{}); err == nil {
		t.Fatal("ValidateInputDigest accepted zero manifest and zero digest")
	}
}

func TestInvalidSourceDoesNotSuppressUnresolvedArtifactSource(t *testing.T) {
	missing := mustRef(t, "repo:desc/missing.api")
	_, err := artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "owner", Version: "v1.0.0"},
		Sources:   []provenance.Source{{Ref: provenance.SourceRef{}, Digest: provenance.SHA256([]byte("invalid"))}},
		Artifacts: []artifact.ArtifactSpec{{
			ID: "output", Path: "generated/output.json", Owner: "owner",
			Digest: provenance.SHA256([]byte("output")), Sources: []provenance.SourceRef{missing}, StalePolicy: artifact.StaleRetain,
		}},
	})
	manifestError := requireArtifactError(t, err, "artifact_manifest_invalid", "artifact_source_unresolved")
	if manifestError.Pointer() != "/artifacts/0/sources/0" {
		t.Fatalf("Pointer() = %q, want /artifacts/0/sources/0", manifestError.Pointer())
	}

	_, err = artifact.NewManifest(artifact.ManifestSpec{
		Generator: artifact.GeneratorSpec{ID: "owner", Version: "v1.0.0"},
		Sources:   []provenance.Source{{Ref: provenance.SourceRef{}, Digest: provenance.SHA256([]byte("invalid"))}},
	})
	manifestError = requireArtifactError(t, err, "artifact_manifest_invalid", "source_ref_invalid")
	if manifestError.Pointer() != "/sources/0/ref" {
		t.Fatalf("Pointer() = %q, want /sources/0/ref", manifestError.Pointer())
	}
}

func TestZeroManifestCannotBeSerialized(t *testing.T) {
	_, err := (artifact.Manifest{}).CanonicalJSON()
	requireArtifactError(t, err, "artifact_manifest_invalid", "input_digest_invalid")
}

type manifestProjection struct {
	APIVersion, GeneratorID, GeneratorVersion, InputDigest string
	Sources                                                []string
	Artifacts                                              []artifactProjection
}

type artifactProjection struct {
	ID, Path, Owner, Digest, StalePolicy string
	Sources                              []string
}

func assertManifestProjection(t *testing.T, manifest artifact.Manifest, want manifestProjection) {
	t.Helper()
	got := manifestProjection{
		APIVersion: manifest.APIVersion(), GeneratorID: manifest.Generator().ID(), GeneratorVersion: manifest.Generator().Version(), InputDigest: manifest.InputDigest().String(),
	}
	for _, source := range manifest.Sources() {
		got.Sources = append(got.Sources, source.Ref.String()+"="+source.Digest.String())
	}
	for _, item := range manifest.Artifacts() {
		projected := artifactProjection{ID: item.ID(), Path: item.Path(), Owner: item.Owner(), Digest: item.Digest().String(), StalePolicy: string(item.StalePolicy())}
		for _, ref := range item.Sources() {
			projected.Sources = append(projected.Sources, ref.String())
		}
		got.Artifacts = append(got.Artifacts, projected)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest projection = %#v, want %#v", got, want)
	}
}

func mustRef(t *testing.T, value string) provenance.SourceRef {
	t.Helper()
	ref, err := provenance.ParseSourceRef(value)
	if err != nil {
		t.Fatalf("ParseSourceRef(%q): %v", value, err)
	}
	return ref
}
