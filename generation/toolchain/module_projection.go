package toolchain

import (
	"runtime/debug"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	MaxModuleFileBytes   = entexec.MaxModuleFileBytes
	MaxModuleSumBytes    = entexec.MaxModuleSumBytes
	MaxHelperSourceBytes = entexec.MaxHelperSourceBytes
)

type HelperSource struct {
	Path   string
	Bytes  []byte
	Digest provenance.Digest
}

type FrameworkModuleIdentity struct{ identity frameworkmodule.Identity }

func CurrentFrameworkModuleIdentity() (FrameworkModuleIdentity, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		_, err := frameworkmodule.Select(nil)
		return FrameworkModuleIdentity{}, projectFrameworkIdentityError(err)
	}
	identity, err := frameworkmodule.Select(info)
	if err != nil {
		return FrameworkModuleIdentity{}, projectFrameworkIdentityError(err)
	}
	return frameworkModuleIdentityFromInternal(identity), nil
}

func frameworkModuleIdentityFromInternal(identity frameworkmodule.Identity) FrameworkModuleIdentity {
	return FrameworkModuleIdentity{identity: identity}
}

func (i FrameworkModuleIdentity) Module() (ModuleRequirement, error) {
	value, err := i.identity.Module()
	if err != nil {
		return ModuleRequirement{}, projectFrameworkIdentityError(err)
	}
	return ModuleRequirement{Path: value.Path, Version: value.Version}, nil
}

func (i FrameworkModuleIdentity) ReplacementKind() (string, error) {
	kind, _, _, _, err := i.identity.Replacement()
	if err != nil {
		return "", projectFrameworkIdentityError(err)
	}
	return string(kind), nil
}

type ModuleLocateSpec struct {
	RepositoryRoot string
	SchemaDir      provenance.DomainSource
}
type ModuleLocation struct{ location entexec.Location }
type ModuleLocator interface {
	Locate(ModuleLocateSpec) (ModuleLocation, error)
}

func LocateModule(spec ModuleLocateSpec) (ModuleLocation, error) {
	value, err := entexec.Locate(entexec.LocateSpec{RepositoryRoot: spec.RepositoryRoot, SchemaDir: spec.SchemaDir})
	if err != nil {
		return ModuleLocation{}, projectEntExecError(err)
	}
	return ModuleLocation{location: value}, nil
}

func (l ModuleLocation) ModuleDir() (string, error) {
	value, err := l.location.ModuleDir()
	return value, projectEntExecError(err)
}
func (l ModuleLocation) SchemaDir() (provenance.DomainSource, error) {
	value, err := l.location.SchemaDir()
	return value, projectEntExecError(err)
}
func (l ModuleLocation) SchemaImportPath() (string, error) {
	value, err := l.location.SchemaImportPath()
	return value, projectEntExecError(err)
}

type ScratchModuleSpec struct {
	RepositoryRoot, StagingRoot, ScratchParent string
	Location                                   ModuleLocation
	BuildTags                                  []string
	Framework                                  FrameworkModuleIdentity
	Helper                                     HelperSource
}

type ScratchProjector interface {
	Project(ScratchModuleSpec) (*ScratchModule, error)
}
type ScratchModule struct{ scratch *entexec.Scratch }

func ProjectScratchModule(spec ScratchModuleSpec) (*ScratchModule, error) {
	value, err := entexec.Project(entexec.ProjectSpec{
		RepositoryRoot: spec.RepositoryRoot, StagingRoot: spec.StagingRoot, ScratchParent: spec.ScratchParent,
		Location: spec.Location.location, BuildTags: append([]string(nil), spec.BuildTags...), Framework: spec.Framework.identity,
		Helper: entexec.HelperSource{Path: spec.Helper.Path, Bytes: append([]byte(nil), spec.Helper.Bytes...), Digest: spec.Helper.Digest},
	})
	if err != nil {
		return nil, projectEntExecError(err)
	}
	return &ScratchModule{scratch: value}, nil
}

func (m *ScratchModule) Root() (string, error) {
	if m == nil {
		_, err := (&entexec.Scratch{}).Root()
		return "", projectEntExecError(err)
	}
	value, err := m.scratch.Root()
	return value, projectEntExecError(err)
}
func (m *ScratchModule) Location() (ModuleLocation, error) {
	if m == nil {
		return ModuleLocation{}, newError("module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch", "", "", 0)
	}
	value, err := m.scratch.Location()
	return ModuleLocation{location: value}, projectEntExecError(err)
}
func (m *ScratchModule) ModuleDir() (string, error) {
	if m == nil {
		return "", newError("module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch", "", "", 0)
	}
	value, err := m.scratch.ModuleDir()
	return value, projectEntExecError(err)
}
func (m *ScratchModule) SchemaDir() (provenance.DomainSource, error) {
	if m == nil {
		return provenance.DomainSource{}, newError("module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch", "", "", 0)
	}
	value, err := m.scratch.SchemaDir()
	return value, projectEntExecError(err)
}
func (m *ScratchModule) SchemaImportPath() (string, error) {
	if m == nil {
		return "", newError("module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch", "", "", 0)
	}
	value, err := m.scratch.SchemaImportPath()
	return value, projectEntExecError(err)
}
func (m *ScratchModule) NormalizedBuildTags() ([]string, error) {
	if m == nil {
		return nil, newError("module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch", "", "", 0)
	}
	value, err := m.scratch.NormalizedBuildTags()
	return value, projectEntExecError(err)
}
func (m *ScratchModule) Cleanup() error {
	if m == nil {
		return newError("module_graph_readback_invalid", "readback", "scratch_state_invalid", "/scratch", "", "", 0)
	}
	return projectEntExecError(m.scratch.Cleanup())
}
