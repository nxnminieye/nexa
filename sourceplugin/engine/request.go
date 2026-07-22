package engine

import (
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin/lock"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

type SelectionSpec struct {
	Release   release.Ref
	ProfileID string
	Target    string
}

type Selection struct {
	release   release.Ref
	profileID string
	target    string
	valid     bool
}

func NewSelection(spec SelectionSpec) (Selection, error) {
	if spec.Release.ProviderID() == "" || spec.Release.ModulePath() == "" || spec.Release.PackagePath() == "" ||
		spec.Release.Version() == "" || !validDigest(spec.Release.ManifestDigest()) || !validDigest(spec.Release.TreeDigest()) {
		return Selection{}, newError(ErrInput, "source_request_invalid", "release_required", "/selection/release", "request")
	}
	if spec.ProfileID == "" {
		return Selection{}, newError(ErrInput, "source_request_invalid", "profile_required", "/selection/profileId", "request")
	}
	if _, err := lock.NewKey(spec.Release.ProviderID(), spec.Target); err != nil {
		return Selection{}, newError(ErrInput, "source_request_invalid", "target_invalid", "/selection/target", "request")
	}
	return Selection{release: spec.Release, profileID: spec.ProfileID, target: spec.Target, valid: true}, nil
}

func (s Selection) Release() release.Ref { return s.release }
func (s Selection) ProfileID() string    { return s.profileID }
func (s Selection) Target() string       { return s.target }

type PlanRequest struct {
	RepositoryRoot string
	Selection      Selection
}

type ManagedRequest struct {
	RepositoryRoot string
	Key            lock.Key
}

func validDigest(digest provenance.Digest) bool {
	_, err := provenance.ParseDigest(digest.String())
	return err == nil
}
