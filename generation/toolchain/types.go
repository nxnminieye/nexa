package toolchain

import "context"

const (
	MaxStdinBytes                   = 1 << 20
	MaxStdoutBytes                  = 16 << 20
	MaxStderrBytes                  = 64 << 10
	BuildInputManifestAPIVersion    = "nexa.dev/retained-build-input-manifest/v1"
	BuildInputManifestKind          = "RetainedBuildInputManifest"
	MaxRetainedModuleRoots          = 64
	MaxRetainedParentDepth          = 64
	MaxRetainedBuildInputs          = 8192
	MaxRetainedBuildInputBytes      = 16 << 20
	MaxRetainedBuildInputTotalBytes = 256 << 20
	MaxModuleListOutputBytes        = 16 << 20
	MaxPackageListOutputBytes       = 64 << 20
	MaxDiagnosticBytes              = 4 << 10
)

type EnvironmentValueSource string

const (
	EnvironmentHost    EnvironmentValueSource = "host"
	EnvironmentScratch EnvironmentValueSource = "scratch"
	EnvironmentFixed   EnvironmentValueSource = "fixed"
)

type EnvironmentRule struct {
	Name       string
	Source     EnvironmentValueSource
	FixedValue string
}

type ExecutableProbe struct {
	Args            []string
	ExpectedVersion string
}

type Tool struct {
	ID, Version, Executable        string
	Args, InputScopes, WriteScopes []string
	Environment                    []EnvironmentRule
	Probe                          ExecutableProbe
}

type EnvVar struct {
	Name, Value string
}

type Request struct {
	RepositoryRoot, StagingRoot, WorkDir string
	Scratch                              *ScratchModule
	Tool                                 Tool
	Args                                 []string
	Environment                          []EnvVar
	Stdin                                []byte
}

type Result struct {
	ToolID, Version, ExecutableVersion string
	ExitCode                           int
	Stdout                             []byte
	Diagnostic                         string
}

type Runner interface {
	Run(context.Context, Request) (Result, error)
}

// DirectRequest runs a trusted consumer-selected tool in the consumer repository.
// It deliberately has no staging, scratch, or repository-copy coordinate.
type DirectRequest struct {
	RepositoryRoot string
	Tool           Tool
	Args           []string
	Environment    []EnvVar
	Stdin          []byte
}

// DirectRunner is the additive write-in-place process boundary.
type DirectRunner interface {
	RunDirect(context.Context, DirectRequest) (Result, error)
}

// DirectRunnerFunc adapts a function to DirectRunner.
type DirectRunnerFunc func(context.Context, DirectRequest) (Result, error)

func (f DirectRunnerFunc) RunDirect(ctx context.Context, request DirectRequest) (Result, error) {
	return f(ctx, request)
}

func cloneTool(input Tool) Tool {
	result := input
	result.Args = append([]string(nil), input.Args...)
	result.InputScopes = append([]string(nil), input.InputScopes...)
	result.WriteScopes = append([]string(nil), input.WriteScopes...)
	result.Environment = append([]EnvironmentRule(nil), input.Environment...)
	result.Probe.Args = append([]string(nil), input.Probe.Args...)
	return result
}

func cloneEnvironment(input []EnvVar) []EnvVar {
	return append([]EnvVar(nil), input...)
}
