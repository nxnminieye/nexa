package toolchain

import (
	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/provenance"
)

type ModuleRequirement struct {
	Path, Version string
}

type ModuleReplacementKind string

const (
	ModuleReplacementNone       ModuleReplacementKind = "none"
	ModuleReplacementVersion    ModuleReplacementKind = "version"
	ModuleReplacementRepository ModuleReplacementKind = "repository"
)

type ModuleReplacement struct {
	Kind           ModuleReplacementKind
	Path, Version  string
	RepositoryPath string
}

type ModuleContentKind string

const (
	ModuleContentLocal  ModuleContentKind = "local"
	ModuleContentRemote ModuleContentKind = "remote"
)

type ModuleContent struct {
	Kind          ModuleContentKind
	Sum, GoModSum string
}

type ModuleIdentity struct {
	Path, Version string
	Replacement   ModuleReplacement
	Content       ModuleContent
}

type ModuleGraph struct {
	snapshot buildinput.GraphSnapshot
}

func (g ModuleGraph) ConsumerModule() (ModuleRequirement, error) {
	value, err := g.snapshot.ConsumerModule()
	return ModuleRequirement{Path: value.Path, Version: value.Version}, projectBuildInputError(err, "", 0)
}

func (g ModuleGraph) GoVersion() (string, error) {
	value, err := g.snapshot.GoVersion()
	return value, projectBuildInputError(err, "", 0)
}

func (g ModuleGraph) ToolchainVersion() (string, bool, error) {
	value, present, err := g.snapshot.ToolchainVersion()
	return value, present, projectBuildInputError(err, "", 0)
}

func (g ModuleGraph) ToolModule() (ModuleRequirement, error) {
	value, err := g.snapshot.ToolModule()
	return ModuleRequirement{Path: value.Path, Version: value.Version}, projectBuildInputError(err, "", 0)
}

func (g ModuleGraph) HelperDigest() (provenance.Digest, error) {
	value, err := g.snapshot.HelperDigest()
	return value, projectBuildInputError(err, "", 0)
}

func (g ModuleGraph) ModuleSources() ([]provenance.Source, error) {
	value, err := g.snapshot.ModuleSources()
	return value, projectBuildInputError(err, "", 0)
}

func (g ModuleGraph) Modules() ([]ModuleIdentity, error) {
	value, err := g.snapshot.Modules()
	if err != nil {
		return nil, projectBuildInputError(err, "", 0)
	}
	result := make([]ModuleIdentity, len(value))
	for index, item := range value {
		result[index] = moduleIdentityFromInternal(item)
	}
	return result, nil
}

func (g ModuleGraph) CanonicalJSON() ([]byte, error) {
	value, err := buildinput.CanonicalGraphSnapshot(g.snapshot)
	return value, projectBuildInputError(err, "", 0)
}

func (g ModuleGraph) Digest() (provenance.Digest, error) {
	canonical, err := g.CanonicalJSON()
	if err != nil {
		return provenance.Digest{}, err
	}
	return provenance.SHA256(canonical), nil
}

func ModuleGraphSchema() []byte { return buildinput.GraphSchema() }

func moduleIdentityFromInternal(value buildinput.ModuleIdentity) ModuleIdentity {
	return ModuleIdentity{
		Path: value.Path, Version: value.Version,
		Replacement: ModuleReplacement{Kind: ModuleReplacementKind(value.Replacement.Kind), Path: value.Replacement.Path, Version: value.Replacement.Version, RepositoryPath: value.Replacement.RepositoryPath},
		Content:     ModuleContent{Kind: ModuleContentKind(value.Content.Kind), Sum: value.Content.Sum, GoModSum: value.Content.GoModSum},
	}
}

func moduleIdentityToInternal(value ModuleIdentity) buildinput.ModuleIdentity {
	return buildinput.ModuleIdentity{
		Path: value.Path, Version: value.Version,
		Replacement: buildinput.ModuleReplacement{Kind: string(value.Replacement.Kind), Path: value.Replacement.Path, Version: value.Replacement.Version, RepositoryPath: value.Replacement.RepositoryPath},
		Content:     buildinput.ModuleContent{Kind: string(value.Content.Kind), Sum: value.Content.Sum, GoModSum: value.Content.GoModSum},
	}
}
