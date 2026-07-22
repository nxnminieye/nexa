package artifact

import (
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

func validateSpec(source string, spec ManifestSpec) []*Error {
	var failures []*Error
	generatorIDValid := validIdentifier(spec.Generator.ID)
	if !generatorIDValid {
		failures = append(failures, semanticError(source, "/generator/id", "generator_id_invalid"))
	}
	if !validVersion(spec.Generator.Version) {
		failures = append(failures, semanticError(source, "/generator/version", "generator_version_invalid"))
	}
	rawSourceRefs := make(map[string]struct{}, len(spec.Sources))
	validSourceRefs := make(map[string]struct{}, len(spec.Sources))
	for index, item := range spec.Sources {
		base := "/sources/" + strconv.Itoa(index)
		ref := item.Ref.String()
		if _, err := provenance.ParseSourceRef(ref); err != nil {
			failures = append(failures, semanticError(source, base+"/ref", "source_ref_invalid"))
		} else {
			validSourceRefs[ref] = struct{}{}
		}
		if _, duplicate := rawSourceRefs[ref]; duplicate {
			failures = append(failures, semanticError(source, base+"/ref", "source_duplicate"))
		}
		rawSourceRefs[ref] = struct{}{}
		if _, err := provenance.ParseDigest(item.Digest.String()); err != nil {
			failures = append(failures, semanticError(source, base+"/digest", "source_digest_invalid"))
		}
	}
	seenIDs := make(map[string]struct{}, len(spec.Artifacts))
	seenPaths := make(map[string]struct{}, len(spec.Artifacts))
	for index, item := range spec.Artifacts {
		base := "/artifacts/" + strconv.Itoa(index)
		if !validIdentifier(item.ID) {
			failures = append(failures, semanticError(source, base+"/id", "artifact_id_invalid"))
		}
		if _, duplicate := seenIDs[item.ID]; duplicate {
			failures = append(failures, semanticError(source, base+"/id", "artifact_duplicate"))
		}
		seenIDs[item.ID] = struct{}{}
		if !validPath(item.Path) {
			failures = append(failures, semanticError(source, base+"/path", "artifact_path_invalid"))
		}
		if _, duplicate := seenPaths[item.Path]; duplicate {
			failures = append(failures, semanticError(source, base+"/path", "artifact_path_duplicate"))
		}
		seenPaths[item.Path] = struct{}{}
		if !validIdentifier(item.Owner) {
			failures = append(failures, semanticError(source, base+"/owner", "artifact_owner_invalid"))
		} else if generatorIDValid && item.Owner != spec.Generator.ID {
			failures = append(failures, semanticError(source, base+"/owner", "artifact_owner_mismatch"))
		}
		if _, err := provenance.ParseDigest(item.Digest.String()); err != nil {
			failures = append(failures, semanticError(source, base+"/digest", "artifact_digest_invalid"))
		}
		seenRefs := make(map[string]struct{}, len(item.Sources))
		for refIndex, refValue := range item.Sources {
			pointer := base + "/sources/" + strconv.Itoa(refIndex)
			ref := refValue.String()
			if _, err := provenance.ParseSourceRef(ref); err != nil {
				failures = append(failures, semanticError(source, pointer, "source_ref_invalid"))
				continue
			}
			if _, duplicate := seenRefs[ref]; duplicate {
				failures = append(failures, semanticError(source, pointer, "artifact_source_duplicate"))
			}
			seenRefs[ref] = struct{}{}
			if _, exists := validSourceRefs[ref]; !exists {
				failures = append(failures, semanticError(source, pointer, "artifact_source_unresolved"))
			}
		}
		if item.StalePolicy != StaleRetain && item.StalePolicy != StaleDeleteIfUnmodified {
			failures = append(failures, semanticError(source, base+"/stalePolicy", "stale_policy_invalid"))
		}
	}
	return failures
}

func validGeneratorSources(spec ManifestSpec) bool {
	if !validIdentifier(spec.Generator.ID) || !validVersion(spec.Generator.Version) {
		return false
	}
	seen := make(map[string]struct{}, len(spec.Sources))
	for _, source := range spec.Sources {
		ref := source.Ref.String()
		if _, err := provenance.ParseSourceRef(ref); err != nil {
			return false
		}
		if _, duplicate := seen[ref]; duplicate {
			return false
		}
		seen[ref] = struct{}{}
		if _, err := provenance.ParseDigest(source.Digest.String()); err != nil {
			return false
		}
	}
	return true
}

func semanticError(source, pointer, reason string) *Error {
	messages := map[string]string{
		"generator_id_invalid": "artifact generator identifier is invalid", "generator_version_invalid": "artifact generator version is invalid",
		"source_ref_invalid": "artifact source reference is invalid", "source_digest_invalid": "artifact source digest is invalid", "source_duplicate": "artifact source is duplicated",
		"input_digest_invalid": "artifact input digest is invalid", "artifact_id_invalid": "artifact identifier is invalid", "artifact_path_invalid": "artifact path is invalid",
		"artifact_owner_invalid": "artifact owner is invalid", "artifact_owner_mismatch": "artifact owner does not match generator", "artifact_digest_invalid": "artifact digest is invalid",
		"artifact_duplicate": "artifact identifier is duplicated", "artifact_path_duplicate": "artifact path is duplicated", "artifact_source_duplicate": "artifact source is duplicated",
		"artifact_source_unresolved": "artifact source is not declared", "stale_policy_invalid": "artifact stale policy is invalid",
	}
	return newArtifactError("artifact_manifest_invalid", reason, source, pointer, messages[reason])
}

func normalizedSpec(spec ManifestSpec) any {
	sources := make([]any, len(spec.Sources))
	for i := range sources {
		sources[i] = map[string]any{}
	}
	artifacts := make([]any, len(spec.Artifacts))
	for i, item := range spec.Artifacts {
		refs := make([]any, len(item.Sources))
		artifacts[i] = map[string]any{"sources": refs}
	}
	return map[string]any{"generator": map[string]any{}, "sources": sources, "artifacts": artifacts}
}

func selectArtifactError(failures []*Error, normalized any) *Error {
	if len(failures) == 0 {
		return nil
	}
	selected := failures[0]
	for _, failure := range failures[1:] {
		comparison := compareLocations(pointerLocation(failure.pointer), pointerLocation(selected.pointer), normalized)
		if comparison < 0 || comparison == 0 && artifactErrorPriority(failure) < artifactErrorPriority(selected) {
			selected = failure
		}
	}
	return selected
}

func pointerLocation(pointer string) []string {
	if pointer == "" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		parts[i] = strings.ReplaceAll(part, "~0", "~")
	}
	return parts
}

func artifactErrorPriority(err *Error) int {
	switch err.reason {
	case "version_unsupported", "kind_invalid":
		return 0
	case "document_invalid", "document_unknown_field", "document_duplicate_key", "document_trailing_input", "document_alias_forbidden", "document_merge_key_forbidden", "document_tag_forbidden":
		return 1
	default:
		return 2
	}
}

func compareLocations(left, right []string, normalized any) int {
	limit := min(len(left), len(right))
	parent := normalized
	for index := 0; index < limit; index++ {
		if _, array := parent.([]any); array {
			leftNumber, leftErr := strconv.Atoi(left[index])
			rightNumber, rightErr := strconv.Atoi(right[index])
			if leftErr != nil || rightErr != nil {
				return strings.Compare(left[index], right[index])
			}
			if leftNumber < rightNumber {
				return -1
			}
			if leftNumber > rightNumber {
				return 1
			}
		} else if comparison := strings.Compare(left[index], right[index]); comparison != 0 {
			return comparison
		}
		parent = childAt(parent, left[index])
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func childAt(parent any, component string) any {
	switch value := parent.(type) {
	case map[string]any:
		return value[component]
	case []any:
		index, err := strconv.Atoi(component)
		if err == nil && index >= 0 && index < len(value) {
			return value[index]
		}
	}
	return nil
}
