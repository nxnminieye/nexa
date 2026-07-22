package artifact

import (
	"regexp"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/semver"
)

const APIVersion = "nexa.dev/artifact-manifest/v1"
const Kind = "ArtifactManifest"
const InputAPIVersion = "nexa.dev/artifact-input/v1"

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-.][a-z0-9]+)*$`)

type StalePolicy string

const (
	StaleRetain             StalePolicy = "retain"
	StaleDeleteIfUnmodified StalePolicy = "delete-if-unmodified"
)

type GeneratorSpec struct {
	ID      string
	Version string
}

type ArtifactSpec struct {
	ID          string
	Path        string
	Owner       string
	Digest      provenance.Digest
	Sources     []provenance.SourceRef
	StalePolicy StalePolicy
}

type ManifestSpec struct {
	Generator GeneratorSpec
	Sources   []provenance.Source
	Artifacts []ArtifactSpec
}

type Manifest struct {
	apiVersion  string
	generator   Generator
	inputDigest provenance.Digest
	sources     []provenance.Source
	artifacts   []Artifact
}

type Generator struct {
	id      string
	version string
}

type Artifact struct {
	id          string
	path        string
	owner       string
	digest      provenance.Digest
	sources     []provenance.SourceRef
	stalePolicy StalePolicy
}

func NewManifest(spec ManifestSpec) (Manifest, error) {
	failures := validateSpec("", spec)
	if err := selectArtifactError(failures, normalizedSpec(spec)); err != nil {
		return Manifest{}, err
	}
	inputDigest, err := ComputeInputDigest(spec.Generator, spec.Sources)
	if err != nil {
		return Manifest{}, err
	}
	return manifestFromSpec(spec, inputDigest), nil
}

func (m Manifest) APIVersion() string             { return m.apiVersion }
func (m Manifest) Generator() Generator           { return m.generator }
func (m Manifest) InputDigest() provenance.Digest { return m.inputDigest }
func (m Manifest) Sources() []provenance.Source {
	return append([]provenance.Source(nil), m.sources...)
}
func (m Manifest) Artifacts() []Artifact     { return cloneArtifacts(m.artifacts) }
func (g Generator) ID() string               { return g.id }
func (g Generator) Version() string          { return g.version }
func (a Artifact) ID() string                { return a.id }
func (a Artifact) Path() string              { return a.path }
func (a Artifact) Owner() string             { return a.owner }
func (a Artifact) Digest() provenance.Digest { return a.digest }
func (a Artifact) Sources() []provenance.SourceRef {
	return append([]provenance.SourceRef(nil), a.sources...)
}
func (a Artifact) StalePolicy() StalePolicy { return a.stalePolicy }

func manifestFromSpec(spec ManifestSpec, inputDigest provenance.Digest) Manifest {
	sources := append([]provenance.Source(nil), spec.Sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Ref.String() < sources[j].Ref.String() })
	artifacts := make([]Artifact, len(spec.Artifacts))
	for index, item := range spec.Artifacts {
		refs := append([]provenance.SourceRef(nil), item.Sources...)
		sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
		artifacts[index] = Artifact{id: item.ID, path: item.Path, owner: item.Owner, digest: item.Digest, sources: refs, stalePolicy: item.StalePolicy}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].id < artifacts[j].id })
	return Manifest{apiVersion: APIVersion, generator: Generator{id: spec.Generator.ID, version: spec.Generator.Version}, inputDigest: inputDigest, sources: sources, artifacts: artifacts}
}

func cloneArtifacts(input []Artifact) []Artifact {
	output := make([]Artifact, len(input))
	for index, item := range input {
		output[index] = item
		output[index].sources = append([]provenance.SourceRef(nil), item.sources...)
	}
	return output
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }
func validVersion(value string) bool {
	if !semver.IsValid(value) {
		return false
	}
	core := strings.TrimPrefix(value, "v")
	if separator := strings.IndexAny(core, "-+"); separator >= 0 {
		core = core[:separator]
	}
	return len(strings.Split(core, ".")) == 3
}
func validPath(value string) bool {
	_, err := provenance.RepositoryRef(value, "artifact-path")
	return err == nil
}
