package api

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/nxnminieye/nexa/provenance"
)

const SourceSetAPIVersion = "nexa.dev/source-set/v1"

type sourceSetEntry struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

type sourceSet struct {
	APIVersion string           `json:"apiVersion"`
	Sources    []sourceSetEntry `json:"sources"`
}

// ComputeSourceDigest binds a sorted, duplicate-free provenance source set.
// It deliberately does not serialize HTTP schemas or operations.
func ComputeSourceDigest(input []provenance.Source) (provenance.Digest, error) {
	sources := append([]provenance.Source(nil), input...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Ref.String() < sources[j].Ref.String() })
	entries := make([]sourceSetEntry, len(sources))
	previous := ""
	for index, source := range sources {
		ref, refErr := provenance.ParseSourceRef(source.Ref.String())
		digest, digestErr := provenance.ParseDigest(source.Digest.String())
		if refErr != nil || digestErr != nil || ref != source.Ref || digest != source.Digest || source.Ref.String() == previous {
			return provenance.Digest{}, errors.New("source set is invalid")
		}
		previous = source.Ref.String()
		entries[index] = sourceSetEntry{Ref: source.Ref.String(), Digest: source.Digest.String()}
	}
	encoded, err := json.Marshal(sourceSet{APIVersion: SourceSetAPIVersion, Sources: entries})
	if err != nil {
		return provenance.Digest{}, err
	}
	return provenance.SHA256(encoded), nil
}
