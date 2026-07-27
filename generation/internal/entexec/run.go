package entexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/frameworkmodule"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
)

type OptionalDigest struct {
	Value   provenance.Digest
	Present bool
}

type Spec struct {
	RepositoryRoot            string
	SchemaDir                 provenance.DomainSource
	BuildTags                 []string
	ExpectedModuleGraphDigest OptionalDigest
	ExpectedBuildInputDigest  OptionalDigest
}

type ProcessPolicySpec struct {
	Tool            ProcessTool
	HostEnvironment []ProcessEnvironment
}

type ProcessPolicy struct {
	tool ProcessTool
	host []ProcessEnvironment
}

type ExecutionRootPolicySpec struct{ Base string }

type ExecutionRootPolicy struct {
	base string
	info os.FileInfo
}

type ExecutionProfileSpec struct {
	Framework frameworkmodule.Identity
	Helper    HelperSource
	Process   ProcessPolicy
	Roots     ExecutionRootPolicy
}

type ExecutionProfile struct {
	framework frameworkmodule.Identity
	helper    HelperSource
	process   ProcessPolicy
	roots     ExecutionRootPolicy
}

type Inspection struct {
	graphDigest, inputDigest provenance.Digest
	moduleSources            []provenance.Source
	executableVersion        string
	valid                    bool
}

type executionRoot struct {
	owner                  *ownedScratchRoot
	root, staging, scratch string
}

type Run struct {
	mu            sync.Mutex
	execution     *executionRoot
	scratch       *Scratch
	normalization Normalization
	repository    string
	moduleDir     string
	schemaDir     provenance.DomainSource
	buildTags     []string
	graph         buildinput.GraphSnapshot
	manifest      buildinput.ManifestSnapshot
	claimed       bool
	cleaned       bool
}

func NewProcessPolicy(spec ProcessPolicySpec) (ProcessPolicy, error) {
	if !validExecutable(spec.Tool.Executable) || !validClosedValue(spec.Tool.Version, 256, false) || !validClosedValue(spec.Tool.Probe.ExpectedVersion, 1024, false) {
		return ProcessPolicy{}, newProcessError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", 0)
	}
	hostNames := map[string]struct{}{"PATH": {}, "GOROOT": {}, "GOMODCACHE": {}, "GOPROXY": {}, "GOSUMDB": {}}
	if len(spec.HostEnvironment) != len(hostNames) {
		return ProcessPolicy{}, newProcessError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "", 0)
	}
	seen := make(map[string]struct{}, len(spec.HostEnvironment))
	for _, value := range spec.HostEnvironment {
		if _, ok := hostNames[value.Name]; !ok || value.Value == "" || strings.ContainsRune(value.Value, '\x00') {
			return ProcessPolicy{}, newProcessError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "", 0)
		}
		if _, duplicate := seen[value.Name]; duplicate {
			return ProcessPolicy{}, newProcessError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "", 0)
		}
		seen[value.Name] = struct{}{}
	}
	probeEnvironment := make([]ProcessEnvironment, 0, len(spec.Tool.Environment))
	host := make(map[string]string, len(spec.HostEnvironment))
	for _, value := range spec.HostEnvironment {
		host[value.Name] = value.Value
	}
	for _, rule := range spec.Tool.Environment {
		value := rule.FixedValue
		switch rule.Source {
		case EnvironmentHost:
			value = host[rule.Name]
		case EnvironmentScratch:
			value = "/nexa-execution"
		}
		probeEnvironment = append(probeEnvironment, ProcessEnvironment{Name: rule.Name, Value: value})
	}
	if err := validateNormalizationPolicy(spec.Tool, probeEnvironment); err != nil {
		return ProcessPolicy{}, err
	}
	return ProcessPolicy{tool: cloneProcessTool(spec.Tool), host: cloneProcessEnvironment(spec.HostEnvironment)}, nil
}

func NewExecutionRootPolicy(spec ExecutionRootPolicySpec) (ExecutionRootPolicy, error) {
	if spec.Base == "" || !filepath.IsAbs(spec.Base) || filepath.Clean(spec.Base) != spec.Base {
		return ExecutionRootPolicy{}, executionRootError("temp_root_invalid", "/executionRoot/base")
	}
	info, err := os.Lstat(spec.Base)
	if err != nil {
		return ExecutionRootPolicy{}, executionRootError("temp_root_invalid", "/executionRoot/base")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ExecutionRootPolicy{}, executionRootError("temp_root_symlink", "/executionRoot/base")
	}
	if !info.IsDir() {
		return ExecutionRootPolicy{}, executionRootError("temp_root_not_directory", "/executionRoot/base")
	}
	canonical, err := canonicalExistingDirectory(spec.Base)
	if err != nil || canonical != spec.Base {
		return ExecutionRootPolicy{}, executionRootError("temp_root_invalid", "/executionRoot/base")
	}
	return ExecutionRootPolicy{base: canonical, info: info}, nil
}

func NewExecutionProfile(spec ExecutionProfileSpec) (ExecutionProfile, error) {
	if _, err := spec.Framework.Module(); err != nil {
		return ExecutionProfile{}, projectError("tool_module_invalid", "/toolModule/path")
	}
	path, data, err := validateHelper(spec.Helper)
	if err != nil {
		return ExecutionProfile{}, err
	}
	if spec.Process.tool.ID == "" || spec.Roots.base == "" {
		return ExecutionProfile{}, executionRootError("temp_root_invalid", "/executionRoot/base")
	}
	return ExecutionProfile{
		framework: spec.Framework,
		helper:    HelperSource{Path: path, Bytes: data, Digest: spec.Helper.Digest},
		process:   ProcessPolicy{tool: cloneProcessTool(spec.Process.tool), host: cloneProcessEnvironment(spec.Process.host)},
		roots:     spec.Roots,
	}, nil
}

func (p ExecutionRootPolicy) create() (*executionRoot, error) {
	current, err := os.Lstat(p.base)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || p.info == nil || !os.SameFile(current, p.info) {
		return nil, executionRootError("temp_root_identity_drift", "/executionRoot/base")
	}
	owner, err := createOwnedScratchRoot(p.base, nil)
	if err != nil {
		return nil, executionRootError("execution_root_create_failed", "/executionRoot")
	}
	fail := func() (*executionRoot, error) {
		if cleanupErr := owner.cleanup(); cleanupErr != nil {
			return nil, executionCleanupError("partial_execution_root_cleanup_failed")
		}
		return nil, executionRootError("execution_root_layout_failed", "/executionRoot")
	}
	for _, name := range []string{"staging", "scratch"} {
		if err := owner.rootHandle.Mkdir(name, 0o700); err != nil {
			return fail()
		}
		info, err := owner.rootHandle.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fail()
		}
	}
	return &executionRoot{owner: owner, root: owner.rootPath, staging: filepath.Join(owner.rootPath, "staging"), scratch: filepath.Join(owner.rootPath, "scratch")}, nil
}

func (r *executionRoot) cleanup() error {
	if r == nil || r.owner == nil {
		return executionCleanupError("execution_root_identity_invalid")
	}
	if err := r.owner.cleanup(); err != nil {
		return executionCleanupError("execution_root_cleanup_failed")
	}
	return nil
}

func Inspect(ctx context.Context, spec Spec) (Inspection, error) {
	profile, err := productionExecutionProfile()
	if err != nil {
		return Inspection{}, err
	}
	return inspectWithProfile(ctx, spec, profile)
}

func inspectWithProfile(ctx context.Context, spec Spec, profile ExecutionProfile) (inspection Inspection, resultErr error) {
	execution, err := profile.roots.create()
	if err != nil {
		return Inspection{}, err
	}
	defer func() {
		if cleanupErr := execution.cleanup(); cleanupErr != nil && resultErr == nil {
			inspection = Inspection{}
			resultErr = cleanupErr
		}
	}()
	scratch, normalization, err := executeInspectionChain(ctx, spec, profile, execution)
	if scratch != nil {
		defer func() {
			if cleanupErr := scratch.Cleanup(); cleanupErr != nil && resultErr == nil {
				inspection = Inspection{}
				resultErr = cleanupErr
			}
		}()
	}
	if err != nil {
		return Inspection{}, err
	}
	graph, _ := normalization.Graph()
	manifest, _ := normalization.Manifest()
	graphJSON, err := buildinput.CanonicalGraphSnapshot(graph)
	if err != nil {
		return Inspection{}, readbackError("module_graph_state_invalid", "/moduleGraph")
	}
	moduleSources, err := graph.ModuleSources()
	if err != nil {
		return Inspection{}, readbackError("module_graph_state_invalid", "/moduleGraph")
	}
	inputDigest, err := buildinput.ManifestDigest(manifest)
	if err != nil {
		return Inspection{}, newProcessError("build_input_readback_invalid", "retain-readback", "manifest_state_invalid", "/buildInputs", "", 0)
	}
	version, err := normalization.ExecutableVersion()
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{graphDigest: provenance.SHA256(graphJSON), inputDigest: inputDigest, moduleSources: moduleSources, executableVersion: version, valid: true}, nil
}

func executeInspectionChain(ctx context.Context, spec Spec, profile ExecutionProfile, execution *executionRoot) (*Scratch, Normalization, error) {
	location, err := Locate(LocateSpec{RepositoryRoot: spec.RepositoryRoot, SchemaDir: spec.SchemaDir})
	if err != nil {
		return nil, Normalization{}, err
	}
	scratch, err := Project(ProjectSpec{
		RepositoryRoot: spec.RepositoryRoot, StagingRoot: execution.staging, ScratchParent: execution.scratch,
		Location: location, BuildTags: append([]string(nil), spec.BuildTags...), Framework: profile.framework, Helper: profile.helper,
	})
	if err != nil {
		return nil, Normalization{}, err
	}
	if err := reidentifyScratchForExecution(scratch); err != nil {
		return scratch, Normalization{}, err
	}
	environment, err := executionEnvironment(profile.process, execution.staging)
	if err != nil {
		return scratch, Normalization{}, err
	}
	normalization, err := Normalize(ctx, NormalizeSpec{Scratch: scratch, Tool: cloneProcessTool(profile.process.tool), Environment: environment})
	if err != nil {
		return scratch, Normalization{}, err
	}
	if err := verifyExpectedDigests(spec, normalization); err != nil {
		return scratch, Normalization{}, err
	}
	return scratch, normalization, nil
}

func reidentifyScratchForExecution(scratch *Scratch) error {
	if scratch == nil || scratch.state == nil || scratch.state.owner == nil {
		return projectError("scratch_write_failed", "/scratch")
	}
	data, err := scratch.state.owner.rootHandle.ReadFile("go.mod")
	if err != nil {
		return projectError("scratch_write_failed", "/scratch")
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil || file.Module == nil || file.Module.Mod.Path != ScratchModulePath || file.AddModuleStmt(scratchExecutionModulePath) != nil {
		return projectError("scratch_write_failed", "/scratch")
	}
	formatted, err := file.Format()
	if err != nil {
		return projectError("scratch_write_failed", "/scratch")
	}
	if err := scratch.state.owner.rootHandle.Remove("go.mod"); err != nil {
		return projectError("scratch_write_failed", "/scratch")
	}
	if err := writeScratchFile(scratch.state.owner.rootHandle, "go.mod", formatted, 0o644); err != nil {
		return projectError("scratch_write_failed", "/scratch")
	}
	return nil
}

func executionEnvironment(policy ProcessPolicy, staging string) ([]ProcessEnvironment, error) {
	host := make(map[string]string, len(policy.host))
	for _, value := range policy.host {
		host[value.Name] = value.Value
	}
	directories := map[string]string{
		"HOME": filepath.Join(staging, "home"), "TMPDIR": filepath.Join(staging, "tmp"),
		"GOPATH": filepath.Join(staging, "gopath"), "GOCACHE": filepath.Join(staging, "gocache"),
	}
	for _, path := range directories {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, executionRootError("execution_root_layout_failed", "/executionRoot")
		}
	}
	values := make([]ProcessEnvironment, 0, len(policy.tool.Environment))
	for _, rule := range policy.tool.Environment {
		value := rule.FixedValue
		switch rule.Source {
		case EnvironmentHost:
			value = host[rule.Name]
		case EnvironmentScratch:
			value = directories[rule.Name]
		}
		values = append(values, ProcessEnvironment{Name: rule.Name, Value: value})
	}
	return values, nil
}

func verifyExpectedDigests(spec Spec, normalization Normalization) error {
	graph, err := normalization.Graph()
	if err != nil {
		return err
	}
	graphJSON, err := buildinput.CanonicalGraphSnapshot(graph)
	if err != nil {
		return err
	}
	manifest, err := normalization.Manifest()
	if err != nil {
		return err
	}
	inputDigest, err := buildinput.ManifestDigest(manifest)
	if err != nil {
		return err
	}
	if spec.ExpectedModuleGraphDigest.Present && spec.ExpectedModuleGraphDigest.Value != provenance.SHA256(graphJSON) {
		return newProcessError("build_input_invalid", "retain", "module_graph_digest_mismatch", "/buildInputs/moduleGraphDigest", "", 0)
	}
	if spec.ExpectedBuildInputDigest.Present && spec.ExpectedBuildInputDigest.Value != inputDigest {
		return newProcessError("build_input_invalid", "retain", "manifest_canonical_invalid", "/buildInputs", "", 0)
	}
	return nil
}

func Begin(ctx context.Context, spec Spec) (*Run, error) {
	profile, err := productionExecutionProfile()
	if err != nil {
		return nil, err
	}
	return beginWithProfile(ctx, spec, profile)
}

func beginWithProfile(ctx context.Context, spec Spec, profile ExecutionProfile) (*Run, error) {
	cwd, tmp, err := validateHelperProcessRoots(spec.RepositoryRoot)
	if err != nil {
		return nil, err
	}
	_ = cwd
	rootPolicy, err := NewExecutionRootPolicy(ExecutionRootPolicySpec{Base: tmp})
	if err != nil {
		return nil, err
	}
	profile.roots = rootPolicy
	execution, err := profile.roots.create()
	if err != nil {
		return nil, err
	}
	scratch, normalization, err := executeInspectionChain(ctx, spec, profile, execution)
	if err != nil {
		if scratch != nil {
			_ = scratch.Cleanup()
		}
		_ = execution.cleanup()
		return nil, err
	}
	location, _ := scratch.Location()
	moduleDir, _ := location.ModuleDir()
	graph, _ := normalization.Graph()
	manifest, _ := normalization.Manifest()
	return &Run{
		execution: execution, scratch: scratch, normalization: normalization,
		repository: spec.RepositoryRoot, moduleDir: moduleDir, schemaDir: spec.SchemaDir,
		buildTags: append([]string(nil), spec.BuildTags...), graph: graph, manifest: manifest,
	}, nil
}

func validateHelperProcessRoots(repository string) (string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", executionRootError("helper_cwd_invalid", "/helperProcess/cwd")
	}
	cwd, err = canonicalExistingDirectory(cwd)
	if err != nil {
		return "", "", executionRootError("helper_cwd_invalid", "/helperProcess/cwd")
	}
	repository, err = canonicalExistingDirectory(repository)
	if err != nil || pathsOverlap(cwd, repository) {
		return "", "", executionRootError("helper_cwd_inside_repository", "/helperProcess/cwd")
	}
	moduleBytes, err := os.ReadFile(filepath.Join(cwd, "go.mod"))
	if err != nil {
		return "", "", executionRootError("helper_cwd_invalid", "/helperProcess/cwd")
	}
	file, err := modfile.Parse("go.mod", moduleBytes, nil)
	if err != nil || file.Module == nil || (file.Module.Mod.Path != ScratchModulePath && file.Module.Mod.Path != scratchExecutionModulePath) {
		return "", "", executionRootError("helper_cwd_invalid", "/helperProcess/cwd")
	}
	tmp := os.Getenv("TMPDIR")
	tmp, err = canonicalExistingDirectory(tmp)
	if err != nil {
		return "", "", executionRootError("helper_tmpdir_invalid", "/helperProcess/tmpdir")
	}
	if tmp == cwd || !pathContainedBy(tmp, cwd) {
		return "", "", executionRootError("helper_tmpdir_outside_cwd", "/helperProcess/tmpdir")
	}
	return cwd, tmp, nil
}

func (r *Run) valid() bool { return r != nil && r.execution != nil && r.scratch != nil && !r.cleaned }

func (r *Run) VerifyPreLoad() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid() || r.claimed {
		return readbackError("normalization_state_invalid", "/normalization")
	}
	return VerifyDrift(r.scratch, r.normalization)
}

func (r *Run) ClaimLoad() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid() || r.claimed {
		return readbackError("normalization_state_invalid", "/normalization")
	}
	r.claimed = true
	return nil
}

func (r *Run) VerifyCoordinates(repositoryRoot, scratchRoot, moduleDir string, schemaDir provenance.DomainSource, buildTags []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	root, _ := r.scratch.Root()
	left, right := append([]string(nil), buildTags...), append([]string(nil), r.buildTags...)
	sort.Strings(left)
	sort.Strings(right)
	if !r.valid() || repositoryRoot != r.repository || scratchRoot != root || moduleDir != r.moduleDir || schemaDir != r.schemaDir || !equalStringSlices(left, right) {
		return readbackError("normalization_state_invalid", "/normalization")
	}
	return nil
}

func (r *Run) ModuleSources() ([]provenance.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid() {
		return nil, readbackError("normalization_state_invalid", "/normalization")
	}
	return r.graph.ModuleSources()
}

func (r *Run) LocalModules() ([]buildinput.RetainedLocalModule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid() {
		return nil, readbackError("normalization_state_invalid", "/normalization")
	}
	return r.manifest.LocalModules()
}

func (r *Run) Inputs() ([]buildinput.RetainedBuildInput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid() {
		return nil, readbackError("normalization_state_invalid", "/normalization")
	}
	return r.manifest.Inputs()
}

func (r *Run) ReadRetainedInput(input buildinput.RetainedBuildInput) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid() {
		return nil, readbackError("normalization_state_invalid", "/normalization")
	}
	inputs, _ := r.manifest.Inputs()
	found := false
	for _, candidate := range inputs {
		if candidate == input {
			found = true
			break
		}
	}
	if !found {
		return nil, newProcessError("build_input_invalid", "retain", "retained_input_read_failed", "/retainedInputs/0", "", 0)
	}
	root := ""
	if input.Module.Role == "scratch-main" {
		root, _ = r.scratch.Root()
	} else if input.Module.HasRepositoryPath {
		root = filepath.Join(r.repository, filepath.FromSlash(input.Module.RepositoryPath))
	}
	if root == "" {
		return nil, newProcessError("build_input_invalid", "retain", "retained_input_read_failed", "/retainedInputs/0", "", 0)
	}
	return readRetainedExact(root, input)
}

func readRetainedExact(root string, input buildinput.RetainedBuildInput) ([]byte, error) {
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, newProcessError("build_input_invalid", "retain", "retained_input_read_failed", "/retainedInputs/0", "", 0)
	}
	defer handle.Close()
	info, err := handle.Lstat(filepath.FromSlash(input.Path))
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != input.Size {
		return nil, newProcessError("build_input_invalid", "retain", "retained_input_digest_drift", "/retainedInputs/0", "", 0)
	}
	file, err := handle.Open(filepath.FromSlash(input.Path))
	if err != nil {
		return nil, newProcessError("build_input_invalid", "retain", "retained_input_read_failed", "/retainedInputs/0", "", 0)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, input.Size+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) != input.Size || provenance.SHA256(data) != input.Digest {
		return nil, newProcessError("build_input_invalid", "retain", "retained_input_digest_drift", "/retainedInputs/0", "", 0)
	}
	return data, nil
}

func (r *Run) VerifyPostLoad() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid() || !r.claimed {
		return readbackError("normalization_state_invalid", "/normalization")
	}
	return VerifyDrift(r.scratch, r.normalization)
}

func (r *Run) Cleanup() error {
	if r == nil {
		return readbackError("normalization_state_invalid", "/normalization")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cleaned {
		return nil
	}
	if r.scratch != nil {
		if err := r.scratch.Cleanup(); err != nil {
			return err
		}
	}
	if err := r.execution.cleanup(); err != nil {
		return err
	}
	r.cleaned = true
	return nil
}

func (i Inspection) ModuleGraphDigest() (provenance.Digest, error) {
	if !i.valid {
		return provenance.Digest{}, readbackError("normalization_state_invalid", "/normalization")
	}
	return i.graphDigest, nil
}

func (i Inspection) BuildInputDigest() (provenance.Digest, error) {
	if !i.valid {
		return provenance.Digest{}, readbackError("normalization_state_invalid", "/normalization")
	}
	return i.inputDigest, nil
}

func (i Inspection) ModuleSources() ([]provenance.Source, error) {
	if !i.valid {
		return nil, readbackError("normalization_state_invalid", "/normalization")
	}
	return append([]provenance.Source(nil), i.moduleSources...), nil
}

func (i Inspection) ExecutableVersion() (string, error) {
	if !i.valid {
		return "", readbackError("normalization_state_invalid", "/normalization")
	}
	return i.executableVersion, nil
}

func productionExecutionProfile() (ExecutionProfile, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ExecutionProfile{}, newError("framework_identity_invalid", "framework-identity", "build_info_unavailable", "/framework/buildInfo")
	}
	framework, err := frameworkmodule.Select(info)
	if err != nil {
		var typed *frameworkmodule.Error
		if errors.As(err, &typed) {
			return ExecutionProfile{}, newError(typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer())
		}
		return ExecutionProfile{}, newError("framework_identity_invalid", "framework-identity", "framework_identity_state_invalid", "/framework")
	}
	executable, err := exec.LookPath("go")
	if err != nil {
		return ExecutionProfile{}, newProcessError("tool_unavailable", "probe", "executable_missing", "/tool/executable", "go", 0)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return ExecutionProfile{}, newProcessError("tool_unavailable", "probe", "executable_missing", "/tool/executable", "go", 0)
	}
	tool := productionGoTool(executable)
	home, _ := os.UserHomeDir()
	modCache := os.Getenv("GOMODCACHE")
	if modCache == "" {
		modCache = filepath.Join(home, "go", "pkg", "mod")
	}
	host := []ProcessEnvironment{
		{Name: "PATH", Value: os.Getenv("PATH")}, {Name: "GOROOT", Value: runtime.GOROOT()},
		{Name: "GOMODCACHE", Value: modCache}, {Name: "GOPROXY", Value: envOr("GOPROXY", "https://proxy.golang.org,direct")},
		{Name: "GOSUMDB", Value: envOr("GOSUMDB", "sum.golang.org")},
	}
	process, err := NewProcessPolicy(ProcessPolicySpec{Tool: tool, HostEnvironment: host})
	if err != nil {
		return ExecutionProfile{}, err
	}
	base, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return ExecutionProfile{}, executionRootError("temp_root_invalid", "/executionRoot/base")
	}
	roots, err := NewExecutionRootPolicy(ExecutionRootPolicySpec{Base: base})
	if err != nil {
		return ExecutionProfile{}, err
	}
	helperBytes := []byte("package main\n\nimport _ \"github.com/nxnminieye/nexa/generation/toolchain\"\n\nfunc main() {}\n")
	return NewExecutionProfile(ExecutionProfileSpec{
		Framework: framework,
		Helper:    HelperSource{Path: "cmd/enthelper/main.go", Bytes: helperBytes, Digest: provenance.SHA256(helperBytes)},
		Process:   process, Roots: roots,
	})
}

func productionGoTool(executable string) ProcessTool {
	return ProcessTool{
		ID: "go", Version: runtime.Version(), Executable: executable,
		InputScopes: []string{"repository", "scratch"}, WriteScopes: []string{"scratch"},
		Environment: []ProcessEnvironmentRule{
			{Name: "PATH", Source: EnvironmentHost}, {Name: "GOROOT", Source: EnvironmentHost}, {Name: "GOMODCACHE", Source: EnvironmentHost},
			{Name: "GOPROXY", Source: EnvironmentHost}, {Name: "GOSUMDB", Source: EnvironmentHost}, {Name: "HOME", Source: EnvironmentScratch},
			{Name: "TMPDIR", Source: EnvironmentScratch}, {Name: "GOPATH", Source: EnvironmentScratch}, {Name: "GOCACHE", Source: EnvironmentScratch},
			{Name: "GOWORK", Source: EnvironmentFixed, FixedValue: "off"}, {Name: "GOENV", Source: EnvironmentFixed, FixedValue: "off"},
			{Name: "GOTOOLCHAIN", Source: EnvironmentFixed, FixedValue: "local"}, {Name: "GOFLAGS", Source: EnvironmentFixed},
			{Name: "CGO_ENABLED", Source: EnvironmentFixed, FixedValue: "0"},
		},
		Probe: ProcessProbe{Args: []string{"version"}, ExpectedVersion: "go version " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH},
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
