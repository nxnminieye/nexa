package release

import (
	"strings"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/internal/contract"
)

type RefSpec struct {
	ProviderID     string
	ModulePath     string
	PackagePath    string
	Version        string
	ManifestDigest provenance.Digest
	TreeDigest     provenance.Digest
}

type Ref struct {
	providerID     string
	modulePath     string
	packagePath    string
	version        string
	manifestDigest provenance.Digest
	treeDigest     provenance.Digest
	valid          bool
}

func NewRef(spec RefSpec) (Ref, error) {
	if issue := contract.ValidateIdentity(spec.ProviderID, spec.ModulePath, spec.PackagePath, spec.Version); issue != nil {
		return Ref{}, projectRefIdentityIssue(issue)
	}
	identity, err := sourceplugin.NewIdentity(sourceplugin.IdentitySpec{
		ProviderID: spec.ProviderID, ModulePath: spec.ModulePath,
		PackagePath: spec.PackagePath, Version: spec.Version,
	})
	if err != nil {
		return Ref{}, releaseError(ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/ref", StageRef)
	}
	if !validDigest(spec.ManifestDigest) {
		return Ref{}, releaseError(ErrReleaseInput, "source_release_invalid", "manifest_digest_invalid", "/ref/manifestDigest", StageRef)
	}
	if !validDigest(spec.TreeDigest) {
		return Ref{}, releaseError(ErrReleaseInput, "source_release_invalid", "tree_digest_invalid", "/ref/treeDigest", StageRef)
	}
	return Ref{
		providerID: identity.ProviderID(), modulePath: identity.ModulePath(), packagePath: identity.PackagePath(), version: identity.Version(),
		manifestDigest: spec.ManifestDigest, treeDigest: spec.TreeDigest, valid: true,
	}, nil
}

func projectRefIdentityIssue(issue *contract.IdentityIssue) *Error {
	if issue == nil {
		return nil
	}
	if !issue.Valid() {
		return releaseError(ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/ref", StageRef)
	}
	field := ""
	switch issue.Field {
	case contract.IdentityProviderID:
		field = "providerId"
	case contract.IdentityModulePath:
		field = "modulePath"
	case contract.IdentityPackagePath:
		field = "packagePath"
	case contract.IdentityVersion:
		field = "version"
	default:
		return releaseError(ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/ref", StageRef)
	}
	reason, ok := issue.Reason.MachineReason()
	if !ok {
		return releaseError(ErrReleaseInternal, "source_release_internal", "contract_issue_unmapped", "/ref", StageRef)
	}
	return releaseError(ErrReleaseInput, "source_release_invalid", reason, "/ref/"+field, StageRef)
}

func FromProvider(provider sourceplugin.Provider) (Ref, error) {
	resolved, err := snapshotProvider(provider, "/provider")
	if err != nil {
		return Ref{}, err
	}
	return resolved.ref, nil
}

func (r Ref) ProviderID() string                { return r.providerID }
func (r Ref) ModulePath() string                { return r.modulePath }
func (r Ref) PackagePath() string               { return r.packagePath }
func (r Ref) Version() string                   { return r.version }
func (r Ref) ManifestDigest() provenance.Digest { return r.manifestDigest }
func (r Ref) TreeDigest() provenance.Digest     { return r.treeDigest }
func (r Ref) Equal(other Ref) bool              { return r.valid && other.valid && r.fullKey() == other.fullKey() }
func (r Ref) SameCoordinates(other Ref) bool {
	return r.valid && other.valid && r.coordinateKey() == other.coordinateKey()
}
func (r Ref) coordinateKey() string {
	return strings.Join([]string{r.providerID, r.modulePath, r.packagePath, r.version}, "\x00")
}
func (r Ref) fullKey() string {
	return r.fullKeyFromCoordinate(r.coordinateKey())
}
func (r Ref) fullKeyFromCoordinate(coordinate string) string {
	return coordinate + "\x00" + r.manifestDigest.String() + "\x00" + r.treeDigest.String()
}
func (r Ref) isValid() bool { return r.valid }
func validDigest(digest provenance.Digest) bool {
	_, err := provenance.ParseDigest(digest.String())
	return err == nil
}
