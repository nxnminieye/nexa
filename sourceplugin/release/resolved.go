package release

import (
	"errors"

	"github.com/nxnminieye/nexa/sourceplugin"
)

type ResolvedRelease struct {
	ref      Ref
	manifest sourceplugin.Manifest
	tree     sourceplugin.Tree
	provider sourceplugin.Provider
	valid    bool
}

func (r ResolvedRelease) Ref() Ref                        { return r.ref }
func (r ResolvedRelease) Manifest() sourceplugin.Manifest { return r.manifest }
func (r ResolvedRelease) Tree() sourceplugin.Tree         { return r.tree }
func (r ResolvedRelease) Provider() sourceplugin.Provider { return r.provider }
func (r ResolvedRelease) isValid() bool                   { return r.valid && r.ref.isValid() }

func snapshotProvider(provider sourceplugin.Provider, pointer string) (ResolvedRelease, *Error) {
	snapshot, err := sourceplugin.SnapshotProvider(provider)
	if err != nil {
		return ResolvedRelease{}, projectProviderError(err, pointer)
	}
	manifest := snapshot.Manifest()
	tree := snapshot.Tree()
	identity := manifest.Identity()
	ref, refErr := NewRef(RefSpec{
		ProviderID: identity.ProviderID(), ModulePath: identity.ModulePath(), PackagePath: identity.PackagePath(), Version: identity.Version(),
		ManifestDigest: manifest.Digest(), TreeDigest: tree.Digest(),
	})
	if refErr != nil {
		return ResolvedRelease{}, releaseError(ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", pointer, StageProviderSnapshot)
	}
	return ResolvedRelease{ref: ref, manifest: manifest, tree: tree, provider: snapshot, valid: true}, nil
}

func projectProviderError(err error, pointer string) *Error {
	var owner *sourceplugin.Error
	if !errors.As(err, &owner) || owner == nil || owner.Class() != sourceplugin.ErrProviderInvalid || owner.Code() != "source_provider_invalid" {
		return releaseError(ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", pointer, StageProviderSnapshot)
	}
	return projectProviderReason(owner.Reason(), pointer)
}

func projectProviderReason(reason, pointer string) *Error {
	switch reason {
	case "provider_nil":
		return releaseError(ErrReleaseInput, "source_release_invalid", "provider_nil", pointer, StageProviderSnapshot)
	case "provider_manifest_panic":
		return releaseError(ErrReleaseInternal, "source_release_internal", "provider_manifest_panic", pointer, StageProviderSnapshot)
	case "provider_tree_panic":
		return releaseError(ErrReleaseInternal, "source_release_internal", "provider_tree_panic", pointer, StageProviderSnapshot)
	case "provider_manifest_required", "provider_tree_required", "provider_file_missing", "provider_file_extra",
		"provider_file_mode_mismatch", "provider_file_size_mismatch", "provider_file_digest_mismatch":
		return releaseError(ErrReleaseInput, "source_release_invalid", "provider_invalid", pointer, StageProviderSnapshot)
	default:
		return releaseError(ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", pointer, StageProviderSnapshot)
	}
}
