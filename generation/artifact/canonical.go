package artifact

import (
	"encoding/json"
	"sort"

	"github.com/nxnminieye/nexa/provenance"
)

type canonicalGenerator struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type canonicalSource struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type canonicalArtifact struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Owner       string   `json:"owner"`
	Digest      string   `json:"digest"`
	Sources     []string `json:"sources"`
	StalePolicy string   `json:"stalePolicy"`
}

type canonicalManifest struct {
	APIVersion  string              `json:"apiVersion"`
	Kind        string              `json:"kind"`
	Generator   canonicalGenerator  `json:"generator"`
	InputDigest string              `json:"inputDigest"`
	Sources     []canonicalSource   `json:"sources"`
	Artifacts   []canonicalArtifact `json:"artifacts"`
}

type canonicalInput struct {
	APIVersion string             `json:"apiVersion"`
	Generator  canonicalGenerator `json:"generator"`
	Sources    []canonicalSource  `json:"sources"`
}

func (m Manifest) CanonicalJSON() ([]byte, error) {
	if err := validateManifestValue(m); err != nil {
		return nil, err
	}
	document := canonicalManifest{
		APIVersion: m.apiVersion, Kind: Kind,
		Generator:   canonicalGenerator{ID: m.generator.id, Version: m.generator.version},
		InputDigest: m.inputDigest.String(), Sources: canonicalSources(m.sources),
		Artifacts: make([]canonicalArtifact, len(m.artifacts)),
	}
	for index, item := range m.artifacts {
		refs := make([]string, len(item.sources))
		for refIndex, ref := range item.sources {
			refs[refIndex] = ref.String()
		}
		document.Artifacts[index] = canonicalArtifact{ID: item.id, Path: item.path, Owner: item.owner, Digest: item.digest.String(), Sources: refs, StalePolicy: string(item.stalePolicy)}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func ComputeInputDigest(generator GeneratorSpec, sources []provenance.Source) (provenance.Digest, error) {
	spec := ManifestSpec{Generator: generator, Sources: append([]provenance.Source(nil), sources...)}
	failures := validateSpec("", spec)
	if err := selectArtifactError(failures, normalizedSpec(spec)); err != nil {
		return provenance.Digest{}, err
	}
	document := canonicalInput{APIVersion: InputAPIVersion, Generator: canonicalGenerator{ID: generator.ID, Version: generator.Version}, Sources: canonicalSources(sources)}
	encoded, err := json.Marshal(document)
	if err != nil {
		return provenance.Digest{}, newArtifactError("artifact_manifest_invalid", "document_invalid", "", "", "artifact input cannot be encoded")
	}
	return provenance.SHA256(encoded), nil
}

func ValidateInputDigest(manifest Manifest, actual provenance.Digest) error {
	if err := validateManifestValue(manifest); err != nil {
		return err
	}
	if _, err := provenance.ParseDigest(actual.String()); err != nil {
		return newArtifactError("artifact_digest_mismatch", "input_digest_mismatch", "", "/inputDigest", "artifact input digest does not match")
	}
	if actual.String() != manifest.inputDigest.String() {
		return newArtifactError("artifact_digest_mismatch", "input_digest_mismatch", "", "/inputDigest", "artifact input digest does not match")
	}
	return nil
}

func validateManifestValue(manifest Manifest) error {
	if _, err := provenance.ParseDigest(manifest.inputDigest.String()); err != nil {
		return newArtifactError("artifact_manifest_invalid", "input_digest_invalid", "", "/inputDigest", "artifact input digest is invalid")
	}
	if manifest.apiVersion != APIVersion {
		return newArtifactError("artifact_manifest_invalid", "version_unsupported", "", "/apiVersion", "artifact manifest version is not supported")
	}
	return nil
}

func canonicalSources(sources []provenance.Source) []canonicalSource {
	result := make([]canonicalSource, len(sources))
	for index, source := range sources {
		result[index] = canonicalSource{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	return result
}
