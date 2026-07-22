package artifact

import (
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/internal/strictdoc"
	"github.com/nxnminieye/nexa/provenance"
)

type manifestDocument struct {
	APIVersion  *string             `json:"apiVersion,omitempty"`
	Kind        *string             `json:"kind,omitempty"`
	Generator   *generatorDocument  `json:"generator,omitempty"`
	InputDigest *string             `json:"inputDigest,omitempty"`
	Sources     []*sourceDocument   `json:"sources"`
	Artifacts   []*artifactDocument `json:"artifacts"`
}
type generatorDocument struct {
	ID      *string `json:"id,omitempty"`
	Version *string `json:"version,omitempty"`
}
type sourceDocument struct {
	Ref    *string `json:"ref,omitempty"`
	Digest *string `json:"digest,omitempty"`
}
type artifactDocument struct {
	ID          *string   `json:"id,omitempty"`
	Path        *string   `json:"path,omitempty"`
	Owner       *string   `json:"owner,omitempty"`
	Digest      *string   `json:"digest,omitempty"`
	Sources     []*string `json:"sources"`
	StalePolicy *string   `json:"stalePolicy,omitempty"`
}

func Parse(source string, data []byte) (Manifest, error) {
	var strictDocument strictdoc.Document
	var err error
	if strings.EqualFold(path.Ext(source), ".json") {
		strictDocument, err = strictdoc.ParseJSON(source, data)
	} else {
		strictDocument, err = strictdoc.ParseYAML(source, data)
	}
	if err != nil {
		return Manifest{}, documentFailure(source, err)
	}
	documentJSON := strictDocument.JSON()
	normalized, err := normalizedDocument(documentJSON)
	if err != nil {
		return Manifest{}, newArtifactError("artifact_manifest_invalid", "document_invalid", source, "", "artifact manifest document is invalid")
	}
	var failures []*Error
	if err := validateDocumentSchema(normalized); err != nil {
		failures = append(failures, schemaValidationErrors(source, err)...)
	}
	var document manifestDocument
	if err := strictDocument.Decode(&document); err != nil {
		failures = append(failures, documentFailure(source, err))
		_ = json.Unmarshal(documentJSON, &document)
	}
	if document.APIVersion != nil && *document.APIVersion != APIVersion {
		failures = append(failures, newArtifactError("artifact_manifest_invalid", "version_unsupported", source, "/apiVersion", "artifact manifest version is not supported"))
	}
	if document.Kind != nil && *document.Kind != Kind {
		failures = append(failures, newArtifactError("artifact_manifest_invalid", "kind_invalid", source, "/kind", "artifact manifest kind is invalid"))
	}
	spec, storedDigest, semanticFailures := specFromDocument(source, document)
	failures = append(failures, semanticFailures...)
	computedDigest, computeErr := computeInputDigestUnchecked(spec.Generator, spec.Sources)
	if computeErr == nil && validGeneratorSources(spec) && storedDigest.String() != "" && storedDigest != computedDigest {
		failures = append(failures, newArtifactError("artifact_digest_mismatch", "input_digest_mismatch", source, "/inputDigest", "artifact input digest does not match manifest inputs"))
	}
	if err := selectArtifactError(failures, normalized); err != nil {
		return Manifest{}, err
	}
	return manifestFromSpec(spec, storedDigest), nil
}

func specFromDocument(source string, document manifestDocument) (ManifestSpec, provenance.Digest, []*Error) {
	var failures []*Error
	spec := ManifestSpec{}
	if document.Generator != nil {
		spec.Generator = GeneratorSpec{ID: stringValue(document.Generator.ID), Version: stringValue(document.Generator.Version)}
	}
	spec.Sources = make([]provenance.Source, len(document.Sources))
	rawSourceRefs := make(map[string]struct{}, len(document.Sources))
	validSourceRefs := make(map[string]struct{}, len(document.Sources))
	for index, item := range document.Sources {
		if item == nil {
			continue
		}
		base := "/sources/" + strconv.Itoa(index)
		refText, digestText := stringValue(item.Ref), stringValue(item.Digest)
		ref, err := provenance.ParseSourceRef(refText)
		if err != nil {
			failures = append(failures, semanticError(source, base+"/ref", "source_ref_invalid"))
		} else {
			validSourceRefs[refText] = struct{}{}
		}
		digest, err := provenance.ParseDigest(digestText)
		if err != nil {
			failures = append(failures, semanticError(source, base+"/digest", "source_digest_invalid"))
		}
		if _, duplicate := rawSourceRefs[refText]; duplicate {
			failures = append(failures, semanticError(source, base+"/ref", "source_duplicate"))
		}
		rawSourceRefs[refText] = struct{}{}
		spec.Sources[index] = provenance.Source{Ref: ref, Digest: digest}
	}
	inputDigest, err := provenance.ParseDigest(stringValue(document.InputDigest))
	if err != nil {
		failures = append(failures, semanticError(source, "/inputDigest", "input_digest_invalid"))
	}
	spec.Artifacts = make([]ArtifactSpec, len(document.Artifacts))
	seenIDs := map[string]struct{}{}
	seenPaths := map[string]struct{}{}
	for index, item := range document.Artifacts {
		if item == nil {
			continue
		}
		base := "/artifacts/" + strconv.Itoa(index)
		result := ArtifactSpec{ID: stringValue(item.ID), Path: stringValue(item.Path), Owner: stringValue(item.Owner), StalePolicy: StalePolicy(stringValue(item.StalePolicy))}
		if !validIdentifier(result.ID) {
			failures = append(failures, semanticError(source, base+"/id", "artifact_id_invalid"))
		}
		if _, duplicate := seenIDs[result.ID]; duplicate {
			failures = append(failures, semanticError(source, base+"/id", "artifact_duplicate"))
		}
		seenIDs[result.ID] = struct{}{}
		if !validPath(result.Path) {
			failures = append(failures, semanticError(source, base+"/path", "artifact_path_invalid"))
		}
		if _, duplicate := seenPaths[result.Path]; duplicate {
			failures = append(failures, semanticError(source, base+"/path", "artifact_path_duplicate"))
		}
		seenPaths[result.Path] = struct{}{}
		if !validIdentifier(result.Owner) {
			failures = append(failures, semanticError(source, base+"/owner", "artifact_owner_invalid"))
		} else if validIdentifier(spec.Generator.ID) && result.Owner != spec.Generator.ID {
			failures = append(failures, semanticError(source, base+"/owner", "artifact_owner_mismatch"))
		}
		result.Digest, err = provenance.ParseDigest(stringValue(item.Digest))
		if err != nil {
			failures = append(failures, semanticError(source, base+"/digest", "artifact_digest_invalid"))
		}
		seenRefs := map[string]struct{}{}
		result.Sources = make([]provenance.SourceRef, len(item.Sources))
		for refIndex, value := range item.Sources {
			pointer := base + "/sources/" + strconv.Itoa(refIndex)
			refText := stringValue(value)
			ref, parseErr := provenance.ParseSourceRef(refText)
			if parseErr != nil {
				failures = append(failures, semanticError(source, pointer, "source_ref_invalid"))
				continue
			}
			result.Sources[refIndex] = ref
			if _, duplicate := seenRefs[refText]; duplicate {
				failures = append(failures, semanticError(source, pointer, "artifact_source_duplicate"))
			}
			seenRefs[refText] = struct{}{}
			if _, exists := validSourceRefs[refText]; !exists {
				failures = append(failures, semanticError(source, pointer, "artifact_source_unresolved"))
			}
		}
		if result.StalePolicy != StaleRetain && result.StalePolicy != StaleDeleteIfUnmodified {
			failures = append(failures, semanticError(source, base+"/stalePolicy", "stale_policy_invalid"))
		}
		spec.Artifacts[index] = result
	}
	if !validIdentifier(spec.Generator.ID) {
		failures = append(failures, semanticError(source, "/generator/id", "generator_id_invalid"))
	}
	if !validVersion(spec.Generator.Version) {
		failures = append(failures, semanticError(source, "/generator/version", "generator_version_invalid"))
	}
	return spec, inputDigest, failures
}

func computeInputDigestUnchecked(generator GeneratorSpec, sources []provenance.Source) (provenance.Digest, error) {
	document := canonicalInput{APIVersion: InputAPIVersion, Generator: canonicalGenerator{ID: generator.ID, Version: generator.Version}, Sources: canonicalSources(sources)}
	encoded, err := json.Marshal(document)
	if err != nil {
		return provenance.Digest{}, err
	}
	return provenance.SHA256(encoded), nil
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func documentFailure(source string, err error) *Error {
	var documentError *strictdoc.Error
	if !errors.As(err, &documentError) {
		return newArtifactError("artifact_manifest_invalid", "document_invalid", source, "", "artifact manifest document is invalid")
	}
	projected := newArtifactError("artifact_manifest_invalid", documentError.Code, documentError.Source, documentError.Pointer, "artifact manifest document is invalid")
	projected.line = documentError.Line
	projected.column = documentError.Column
	return projected
}
