package transaction

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
)

var transactionIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-.][a-z0-9]+)*$`)
var generationOwnerSegmentPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var generationOwnerVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)

func NewCompatibilityLockMutation(spec CompatibilityLockMutationSpec) (ControlSourceMutation, error) {
	if !transactionIdentifierPattern.MatchString(spec.ID) {
		return ControlSourceMutation{}, controlSourceError("id_invalid", "/id")
	}
	if !validRepositoryPath(spec.Path) {
		return ControlSourceMutation{}, controlSourceError("path_invalid", "/path")
	}
	if !validGenerationOwner(spec.Owner) {
		return ControlSourceMutation{}, controlSourceError("owner_invalid", "/owner")
	}
	if spec.Before != nil && !validWholeDocumentSource(*spec.Before, spec.Path) {
		return ControlSourceMutation{}, controlSourceError("before_source_invalid", "/before")
	}
	if len(spec.After) == 0 {
		return ControlSourceMutation{}, controlSourceError("after_empty", "/after")
	}
	if !validDigest(spec.AfterDigest) || spec.AfterDigest != provenance.SHA256(spec.After) {
		return ControlSourceMutation{}, controlSourceError("after_digest_mismatch", "/afterDigest")
	}
	if len(spec.Sources) == 0 {
		return ControlSourceMutation{}, controlSourceError("source_ref_invalid", "/sources")
	}
	sources := append([]provenance.SourceRef(nil), spec.Sources...)
	seen := make(map[string]struct{}, len(sources))
	for index, source := range sources {
		value := source.String()
		if _, err := provenance.ParseSourceRef(value); err != nil {
			return ControlSourceMutation{}, controlSourceError("source_ref_invalid", "/sources/"+strconv.Itoa(index))
		}
		if _, duplicate := seen[value]; duplicate {
			return ControlSourceMutation{}, controlSourceError("source_ref_duplicate", "/sources/"+strconv.Itoa(index))
		}
		seen[value] = struct{}{}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].String() < sources[j].String() })

	if spec.Before != nil && spec.Before.Digest == spec.AfterDigest {
		return ControlSourceMutation{}, nil
	}
	var before *provenance.Source
	if spec.Before != nil {
		copyValue := *spec.Before
		before = &copyValue
	}
	return ControlSourceMutation{
		role: ControlSourceCompatibilityLock, id: spec.ID, path: spec.Path, owner: spec.Owner,
		before: before, after: append([]byte(nil), spec.After...), afterDigest: spec.AfterDigest, sources: sources,
	}, nil
}

func (m ControlSourceMutation) Role() ControlSourceRole { return m.role }
func (m ControlSourceMutation) ID() string              { return m.id }
func (m ControlSourceMutation) Path() string            { return m.path }
func (m ControlSourceMutation) Owner() string           { return m.owner }

func (m ControlSourceMutation) Before() (provenance.Source, bool) {
	if m.before == nil {
		return provenance.Source{}, false
	}
	return *m.before, true
}

func (m ControlSourceMutation) After() []byte {
	return append([]byte(nil), m.after...)
}

func (m ControlSourceMutation) AfterDigest() provenance.Digest { return m.afterDigest }

func (m ControlSourceMutation) Sources() []provenance.SourceRef {
	return append([]provenance.SourceRef(nil), m.sources...)
}

func validRepositoryPath(value string) bool {
	ref, err := provenance.RepositoryRef(value, "transaction-path")
	return err == nil && ref.Path() == value
}

func validGenerationOwner(value string) bool {
	if strings.ContainsAny(value, "?#") {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) < 3 || !strings.Contains(parts[0], ".") || !transactionIdentifierPattern.MatchString(parts[0]) {
		return false
	}
	for _, part := range parts[1 : len(parts)-1] {
		if !generationOwnerSegmentPattern.MatchString(part) {
			return false
		}
	}
	return generationOwnerVersionPattern.MatchString(parts[len(parts)-1])
}

func validWholeDocumentSource(source provenance.Source, path string) bool {
	ref, err := provenance.ParseSourceRef(source.Ref.String())
	return err == nil && ref.Path() == path && ref.Fragment() == "" && validDigest(source.Digest)
}

func validDigest(digest provenance.Digest) bool {
	_, err := provenance.ParseDigest(digest.String())
	return err == nil
}
