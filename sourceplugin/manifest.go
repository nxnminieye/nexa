package sourceplugin

import "github.com/nxnminieye/nexa/provenance"

const APIVersion = "nexa.dev/source-bundle/v1"
const Kind = "SourceBundle"

type FileMode string

const (
	Mode0644 FileMode = "0644"
	Mode0755 FileMode = "0755"
)

type IdentitySpec struct {
	ProviderID  string
	ModulePath  string
	PackagePath string
	Version     string
}

type Identity struct {
	providerID  string
	modulePath  string
	packagePath string
	version     string
}

func NewIdentity(spec IdentitySpec) (Identity, error) {
	if err := validateIdentity(spec); err != nil {
		return Identity{}, err
	}
	return Identity{
		providerID: spec.ProviderID, modulePath: spec.ModulePath,
		packagePath: spec.PackagePath, version: spec.Version,
	}, nil
}

func (i Identity) ProviderID() string  { return i.providerID }
func (i Identity) ModulePath() string  { return i.modulePath }
func (i Identity) PackagePath() string { return i.packagePath }
func (i Identity) Version() string     { return i.version }
func (i Identity) Equal(other Identity) bool {
	return i.providerID != "" && other.providerID != "" &&
		i.providerID == other.providerID && i.modulePath == other.modulePath &&
		i.packagePath == other.packagePath && i.version == other.version
}

type FileSpec struct {
	Path   string
	Size   int64
	Digest provenance.Digest
	Mode   FileMode
}

type File struct {
	path   string
	size   int64
	digest provenance.Digest
	mode   FileMode
}

func (f File) Path() string              { return f.path }
func (f File) Size() int64               { return f.size }
func (f File) Digest() provenance.Digest { return f.digest }
func (f File) Mode() FileMode            { return f.mode }

type BundleRequirementSpec struct {
	ProviderID     string
	ModulePath     string
	PackagePath    string
	Version        string
	ProfileID      string
	ManifestDigest provenance.Digest
	TreeDigest     provenance.Digest
}

type BundleRequirement struct {
	providerID     string
	modulePath     string
	packagePath    string
	version        string
	profileID      string
	manifestDigest provenance.Digest
	treeDigest     provenance.Digest
}

func (r BundleRequirement) ProviderID() string                { return r.providerID }
func (r BundleRequirement) ModulePath() string                { return r.modulePath }
func (r BundleRequirement) PackagePath() string               { return r.packagePath }
func (r BundleRequirement) Version() string                   { return r.version }
func (r BundleRequirement) ProfileID() string                 { return r.profileID }
func (r BundleRequirement) ManifestDigest() provenance.Digest { return r.manifestDigest }
func (r BundleRequirement) TreeDigest() provenance.Digest     { return r.treeDigest }

type ValidationKind string

const (
	ValidationGoTest  ValidationKind = "go-test"
	ValidationGoBuild ValidationKind = "go-build"
)

type ValidationRecipeSpec struct {
	ID               string
	Kind             ValidationKind
	WorkingDirectory string
	Packages         []string
}

type ValidationRecipe struct {
	id               string
	kind             ValidationKind
	workingDirectory string
	packages         []string
}

func (r ValidationRecipe) ID() string               { return r.id }
func (r ValidationRecipe) Kind() ValidationKind     { return r.kind }
func (r ValidationRecipe) WorkingDirectory() string { return r.workingDirectory }
func (r ValidationRecipe) Packages() []string       { return append([]string(nil), r.packages...) }

type ProfileSpec struct {
	ID               string
	Files            []string
	RequiresProfiles []string
	RequiresBundles  []BundleRequirementSpec
	Validations      []ValidationRecipeSpec
}

type Profile struct {
	id               string
	filePaths        []string
	requiredProfiles []string
	requirements     []BundleRequirement
	validations      []ValidationRecipe
}

func (p Profile) ID() string { return p.id }
func (p Profile) FilePaths() []string {
	return append([]string(nil), p.filePaths...)
}
func (p Profile) RequiredProfileIDs() []string {
	return append([]string(nil), p.requiredProfiles...)
}
func (p Profile) BundleRequirements() []BundleRequirement {
	return append([]BundleRequirement(nil), p.requirements...)
}
func (p Profile) Validations() []ValidationRecipe {
	result := make([]ValidationRecipe, len(p.validations))
	for index, recipe := range p.validations {
		result[index] = cloneValidation(recipe)
	}
	return result
}

type ManifestSpec struct {
	Identity IdentitySpec
	Files    []FileSpec
	Profiles []ProfileSpec
}

type Manifest struct {
	identity     Identity
	files        []File
	profiles     []Profile
	fileIndex    map[string]int
	profileIndex map[string]int
	canonical    []byte
	digest       provenance.Digest
	diagnostics  diagnosticLocations
}

func NewManifest(spec ManifestSpec) (Manifest, error) {
	return newManifest(spec, diagnosticLocations{})
}

func (m Manifest) APIVersion() string {
	if len(m.canonical) == 0 {
		return ""
	}
	return APIVersion
}

func (m Manifest) Kind() string {
	if len(m.canonical) == 0 {
		return ""
	}
	return Kind
}

func (m Manifest) Identity() Identity { return m.identity }
func (m Manifest) Files() []File      { return append([]File(nil), m.files...) }
func (m Manifest) Profiles() []Profile {
	result := make([]Profile, len(m.profiles))
	for index, profile := range m.profiles {
		result[index] = cloneProfile(profile)
	}
	return result
}

func (m Manifest) LookupFile(path string) (File, bool) {
	index, ok := m.fileIndex[path]
	if !ok {
		return File{}, false
	}
	return m.files[index], true
}

func (m Manifest) LookupProfile(id string) (Profile, bool) {
	index, ok := m.profileIndex[id]
	if !ok {
		return Profile{}, false
	}
	return cloneProfile(m.profiles[index]), true
}

func (m Manifest) CanonicalJSON() ([]byte, error) {
	if len(m.canonical) == 0 {
		return nil, newSourceError("source_manifest_invalid", "document_invalid", "")
	}
	return append([]byte(nil), m.canonical...), nil
}

func (m Manifest) Digest() provenance.Digest { return m.digest }

func cloneProfile(profile Profile) Profile {
	result := Profile{
		id:               profile.id,
		filePaths:        append([]string(nil), profile.filePaths...),
		requiredProfiles: append([]string(nil), profile.requiredProfiles...),
		requirements:     append([]BundleRequirement(nil), profile.requirements...),
		validations:      make([]ValidationRecipe, len(profile.validations)),
	}
	for index, recipe := range profile.validations {
		result.validations[index] = cloneValidation(recipe)
	}
	return result
}

func cloneValidation(recipe ValidationRecipe) ValidationRecipe {
	recipe.packages = append([]string(nil), recipe.packages...)
	return recipe
}
