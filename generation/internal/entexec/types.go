package entexec

import (
	"os"
	"sync"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	MaxStdinBytes        = 1 << 20
	MaxStdoutBytes       = 16 << 20
	MaxStderrBytes       = 64 << 10
	MaxModuleFileBytes   = 1 << 20
	MaxModuleSumBytes    = 16 << 20
	MaxHelperSourceBytes = 256 << 10
	ScratchModulePath    = "github.com/nxnminieye/nexa/generation/enthelperexec"
)

type HelperSource struct {
	Path   string
	Bytes  []byte
	Digest provenance.Digest
}

type LocateSpec struct {
	RepositoryRoot string
	SchemaDir      provenance.DomainSource
}

type fileIdentity struct {
	repositoryPath string
	digest         provenance.Digest
	size           int64
	info           os.FileInfo
}

type locationState struct {
	repositoryRoot, moduleDir, schemaImportPath string
	schemaDir                                   provenance.DomainSource
	consumerModule                              buildinput.ModuleRequirement
	moduleFile                                  fileIdentity
	moduleSum                                   fileIdentity
	hasModuleSum                                bool
}

type Location struct{ state *locationState }

type ProjectSpec struct {
	RepositoryRoot, StagingRoot, ScratchParent string
	Location                                   Location
	BuildTags                                  []string
	Framework                                  frameworkmodule.Identity
	Helper                                     HelperSource
	projectionHook                             func(projectionEvent)
}

type projectionEvent struct{ Name, Root string }

type ownedScratchRoot struct {
	rootPath   string
	rootHandle *os.Root
	rootInfo   os.FileInfo
	closed     bool
}

type directoryIdentity struct {
	repositoryPath string
	info           os.FileInfo
}

type localModuleBinding struct {
	root, expectedModule, pointer  string
	moduleFile                     fileIdentity
	repositoryInfo, moduleRootInfo os.FileInfo
	components                     []directoryIdentity
	moduleRoot                     *os.Root
}

type scratchState struct {
	mu                    sync.Mutex
	root, parent, staging string
	location              Location
	buildTags             []string
	toolModule            buildinput.ModuleRequirement
	helperDigest          provenance.Digest
	owner                 *ownedScratchRoot
	cleaned               bool
	running               bool
	cleanupErr            error
}

type Scratch struct{ state *scratchState }

type NormalizeSpec struct {
	Scratch     *Scratch
	Tool        ProcessTool
	Environment []ProcessEnvironment
	processHook func(processEvent)
}

type normalizedFile struct {
	present bool
	digest  provenance.Digest
	size    int64
	info    os.FileInfo
}

type resolvedModuleState struct {
	digest provenance.Digest
	size   int
	bytes  []byte
}

type normalizationState struct {
	compilation       buildinput.Compilation
	scratch           *Scratch
	tool              ProcessTool
	environment       []ProcessEnvironment
	executableVersion string
	goMod             normalizedFile
	goSum             normalizedFile
	modules           resolvedModuleState
}

type Normalization struct{ state *normalizationState }

const (
	EnvironmentHost    = "host"
	EnvironmentScratch = "scratch"
	EnvironmentFixed   = "fixed"
)

type ProcessEnvironmentRule struct {
	Name, Source, FixedValue string
}

type ProcessEnvironment struct {
	Name, Value string
}

type ProcessProbe struct {
	Args            []string
	ExpectedVersion string
}

type ProcessTool struct {
	ID, Version, Executable        string
	Args, InputScopes, WriteScopes []string
	Environment                    []ProcessEnvironmentRule
	Probe                          ProcessProbe
}

type ProcessSpec struct {
	RepositoryRoot, StagingRoot, WorkDir string
	Direct                               bool
	Scratch                              *Scratch
	Tool                                 ProcessTool
	Args                                 []string
	Environment                          []ProcessEnvironment
	Stdin                                []byte
	processHook                          func(processEvent)
}

type processEvent struct {
	Name string
	Args []string
}

type ProcessResult struct {
	ToolID, Version, ExecutableVersion string
	ExitCode                           int
	Stdout                             []byte
}

func (l Location) ModuleDir() (string, error) {
	if l.state == nil {
		return "", readbackError("location_state_invalid", "/location")
	}
	return l.state.moduleDir, nil
}

func (l Location) SchemaDir() (provenance.DomainSource, error) {
	if l.state == nil {
		return provenance.DomainSource{}, readbackError("location_state_invalid", "/location")
	}
	return l.state.schemaDir, nil
}

func (l Location) SchemaImportPath() (string, error) {
	if l.state == nil {
		return "", readbackError("location_state_invalid", "/location")
	}
	return l.state.schemaImportPath, nil
}

func (l Location) ConsumerModule() (buildinput.ModuleRequirement, error) {
	if l.state == nil {
		return buildinput.ModuleRequirement{}, readbackError("location_state_invalid", "/location")
	}
	return l.state.consumerModule, nil
}

func (s *Scratch) stateForRead() (*scratchState, error) {
	if s == nil || s.state == nil {
		return nil, readbackError("scratch_state_invalid", "/scratch")
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.cleaned {
		return nil, readbackError("scratch_state_invalid", "/scratch")
	}
	return s.state, nil
}

func (s *Scratch) Root() (string, error) {
	state, err := s.stateForRead()
	if err != nil {
		return "", err
	}
	return state.root, nil
}

func (s *Scratch) Location() (Location, error) {
	state, err := s.stateForRead()
	if err != nil {
		return Location{}, err
	}
	return state.location, nil
}

func (s *Scratch) ModuleDir() (string, error) {
	location, err := s.Location()
	if err != nil {
		return "", err
	}
	return location.ModuleDir()
}

func (s *Scratch) SchemaDir() (provenance.DomainSource, error) {
	location, err := s.Location()
	if err != nil {
		return provenance.DomainSource{}, err
	}
	return location.SchemaDir()
}

func (s *Scratch) SchemaImportPath() (string, error) {
	location, err := s.Location()
	if err != nil {
		return "", err
	}
	return location.SchemaImportPath()
}

func (s *Scratch) NormalizedBuildTags() ([]string, error) {
	state, err := s.stateForRead()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), state.buildTags...), nil
}

func (n Normalization) Graph() (buildinput.GraphSnapshot, error) {
	if n.state == nil {
		return buildinput.GraphSnapshot{}, readbackError("normalization_state_invalid", "/normalization")
	}
	value, err := n.state.compilation.Graph()
	if err != nil {
		return buildinput.GraphSnapshot{}, readbackError("normalization_state_invalid", "/normalization")
	}
	return value, nil
}

func (n Normalization) Manifest() (buildinput.ManifestSnapshot, error) {
	if n.state == nil {
		return buildinput.ManifestSnapshot{}, readbackError("normalization_state_invalid", "/normalization")
	}
	value, err := n.state.compilation.Manifest()
	if err != nil {
		return buildinput.ManifestSnapshot{}, readbackError("normalization_state_invalid", "/normalization")
	}
	return value, nil
}

func (n Normalization) ExecutableVersion() (string, error) {
	if n.state == nil {
		return "", readbackError("normalization_state_invalid", "/normalization")
	}
	value, err := n.state.compilation.ExecutableVersion()
	if err != nil {
		return "", readbackError("normalization_state_invalid", "/normalization")
	}
	return value, nil
}
