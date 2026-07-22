package entexec

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	maxModuleListBytes  = 16 << 20
	maxPackageListBytes = 64 << 20
	entModulePath       = "entgo.io/ent"
	entModuleVersion    = "v0.14.5"
)

func Normalize(ctx context.Context, spec NormalizeSpec) (Normalization, error) {
	return normalize(ctx, spec, entModuleVersion)
}

func NormalizeConsumerEnt(ctx context.Context, spec NormalizeSpec) (Normalization, error) {
	return normalize(ctx, spec, "")
}

func normalize(ctx context.Context, spec NormalizeSpec, requiredEntVersion string) (Normalization, error) {
	facts, err := normalizationFactsFor(spec.Scratch)
	if err != nil {
		return Normalization{}, err
	}
	if err := validateNormalizationPolicy(spec.Tool, spec.Environment); err != nil {
		return Normalization{}, err
	}
	processSpec := ProcessSpec{
		RepositoryRoot: facts.repositoryRoot, StagingRoot: facts.staging, Scratch: spec.Scratch,
		Tool: spec.Tool, Environment: append([]ProcessEnvironment(nil), spec.Environment...), processHook: spec.processHook,
	}
	prepared, err := prepareProcess(ctx, processSpec)
	if err != nil {
		return Normalization{}, err
	}
	defer prepared.release()
	execution := preparedExecution{process: &prepared}
	if _, err := probePreparedProcess(ctx, &prepared); err != nil {
		return Normalization{}, err
	}

	normalizeArgs := []string{"list", "-mod=mod", "-deps"}
	packageArgs := []string{"list", "-mod=readonly", "-deps", "-json"}
	if len(facts.buildTags) > 0 {
		tags := "-tags=" + strings.Join(facts.buildTags, ",")
		normalizeArgs = append(normalizeArgs, tags)
		packageArgs = append(packageArgs, tags)
	}
	normalizeArgs = append(normalizeArgs, "./cmd/enthelper", facts.schemaImportPath)
	packageArgs = append(packageArgs, facts.schemaImportPath)

	first, err := runNormalizationProcess(ctx, execution, normalizeArgs, MaxStdoutBytes)
	if err != nil {
		return Normalization{}, err
	}
	verified, err := runNormalizationProcess(ctx, execution, []string{"mod", "verify"}, MaxStdoutBytes)
	if err != nil {
		return Normalization{}, err
	}
	moduleResult, err := runNormalizationDiscovery(ctx, execution, []string{"list", "-mod=readonly", "-m", "-json", "all"}, "module", maxModuleListBytes)
	if err != nil {
		return Normalization{}, err
	}
	packageResult, err := runNormalizationDiscovery(ctx, execution, packageArgs, "package", maxPackageListBytes)
	if err != nil {
		return Normalization{}, err
	}
	if first.ExecutableVersion != verified.ExecutableVersion || first.ExecutableVersion != moduleResult.ExecutableVersion || first.ExecutableVersion != packageResult.ExecutableVersion {
		return Normalization{}, newProcessError("build_input_invalid", "retain", "tool_version_mismatch", "/buildInputs/goExecutableVersion", packageResult.ToolID, 0)
	}

	goMod, err := readNormalizedBoundary(spec.Scratch, "go.mod", true, MaxModuleFileBytes, "normalized_module_file_invalid", "/normalized/goMod")
	if err != nil {
		return Normalization{}, err
	}
	goSum, err := readNormalizedBoundary(spec.Scratch, "go.sum", false, MaxModuleSumBytes, "normalized_module_sum_invalid", "/normalized/goSum")
	if err != nil {
		return Normalization{}, err
	}
	discovery := buildinput.DiscoveryInput{
		RepositoryRoot: facts.repositoryRoot, ScratchRoot: facts.root,
		SchemaDir: facts.schemaDir, SchemaImportPath: facts.schemaImportPath,
		BuildTags: facts.buildTags, ToolModule: facts.toolModule, HelperDigest: facts.helperDigest,
		GoExecutableVersion: moduleResult.ExecutableVersion,
		ModuleList:          append([]byte(nil), moduleResult.Stdout...), PackageList: append([]byte(nil), packageResult.Stdout...),
	}
	compilation, err := buildinput.Compile(discovery)
	if err != nil {
		return Normalization{}, projectNormalizationCompileError(err, packageResult.ToolID)
	}
	if err := validateNormalizedGraph(compilation, facts, requiredEntVersion); err != nil {
		return Normalization{}, err
	}
	moduleBytes := append([]byte(nil), moduleResult.Stdout...)
	return Normalization{state: &normalizationState{
		compilation: compilation, scratch: spec.Scratch, tool: cloneProcessTool(spec.Tool),
		environment: cloneProcessEnvironment(spec.Environment), executableVersion: moduleResult.ExecutableVersion,
		goMod: goMod, goSum: goSum,
		modules: resolvedModuleState{digest: provenance.SHA256(moduleBytes), size: len(moduleBytes), bytes: moduleBytes},
	}}, nil
}

func validateNormalizationPolicy(tool ProcessTool, environment []ProcessEnvironment) error {
	if tool.ID != "go" || len(tool.Args) != 0 || !reflectStrings(tool.InputScopes, []string{"repository", "scratch"}) || !reflectStrings(tool.WriteScopes, []string{"scratch"}) || !reflectStrings(tool.Probe.Args, []string{"version"}) {
		return newProcessError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", 0)
	}
	wantRules := map[string]ProcessEnvironmentRule{
		"PATH": {Name: "PATH", Source: EnvironmentHost}, "GOROOT": {Name: "GOROOT", Source: EnvironmentHost},
		"GOMODCACHE": {Name: "GOMODCACHE", Source: EnvironmentHost}, "GOPROXY": {Name: "GOPROXY", Source: EnvironmentHost},
		"GOSUMDB": {Name: "GOSUMDB", Source: EnvironmentHost}, "HOME": {Name: "HOME", Source: EnvironmentScratch},
		"TMPDIR": {Name: "TMPDIR", Source: EnvironmentScratch}, "GOPATH": {Name: "GOPATH", Source: EnvironmentScratch},
		"GOCACHE": {Name: "GOCACHE", Source: EnvironmentScratch}, "GOWORK": {Name: "GOWORK", Source: EnvironmentFixed, FixedValue: "off"},
		"GOENV": {Name: "GOENV", Source: EnvironmentFixed, FixedValue: "off"}, "GOTOOLCHAIN": {Name: "GOTOOLCHAIN", Source: EnvironmentFixed, FixedValue: "local"},
		"GOFLAGS": {Name: "GOFLAGS", Source: EnvironmentFixed, FixedValue: ""}, "CGO_ENABLED": {Name: "CGO_ENABLED", Source: EnvironmentFixed, FixedValue: "0"},
	}
	if len(tool.Environment) != len(wantRules) {
		return newProcessError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", 0)
	}
	seenRules := make(map[string]struct{}, len(tool.Environment))
	for _, rule := range tool.Environment {
		want, ok := wantRules[rule.Name]
		if !ok || rule != want {
			return newProcessError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", 0)
		}
		if _, duplicate := seenRules[rule.Name]; duplicate {
			return newProcessError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", 0)
		}
		seenRules[rule.Name] = struct{}{}
	}
	if len(environment) != len(wantRules) {
		return newProcessError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "go", 0)
	}
	seenValues := make(map[string]struct{}, len(environment))
	for _, value := range environment {
		rule, ok := wantRules[value.Name]
		if !ok || rule.Source == EnvironmentFixed && value.Value != rule.FixedValue || rule.Source != EnvironmentFixed && value.Value == "" {
			return newProcessError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "go", 0)
		}
		if _, duplicate := seenValues[value.Name]; duplicate {
			return newProcessError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "go", 0)
		}
		seenValues[value.Name] = struct{}{}
	}
	return nil
}

func reflectStrings(left, right []string) bool {
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

type normalizationFacts struct {
	repositoryRoot, staging, root, schemaImportPath string
	schemaDir                                       provenance.DomainSource
	buildTags                                       []string
	toolModule                                      buildinput.ModuleRequirement
	consumerModule                                  buildinput.ModuleRequirement
	helperDigest                                    provenance.Digest
}

func normalizationFactsFor(scratch *Scratch) (normalizationFacts, error) {
	if scratch == nil || scratch.state == nil {
		return normalizationFacts{}, readbackError("scratch_state_invalid", "/scratch")
	}
	scratch.state.mu.Lock()
	defer scratch.state.mu.Unlock()
	state := scratch.state
	if state.cleaned || state.owner == nil || state.location.state == nil || state.running {
		return normalizationFacts{}, readbackError("scratch_state_invalid", "/scratch")
	}
	if err := state.owner.validatePathIdentity(); err != nil {
		return normalizationFacts{}, readbackError("scratch_state_invalid", "/scratch")
	}
	return normalizationFacts{
		repositoryRoot: state.location.state.repositoryRoot, staging: state.staging, root: state.root,
		schemaDir: state.location.state.schemaDir, schemaImportPath: state.location.state.schemaImportPath,
		buildTags: append([]string(nil), state.buildTags...), toolModule: state.toolModule,
		consumerModule: state.location.state.consumerModule, helperDigest: state.helperDigest,
	}, nil
}

func runNormalizationProcess(ctx context.Context, execution preparedExecution, args []string, stdoutLimit int) (ProcessResult, error) {
	copyProcess := *execution.process
	copyProcess.toolArgs = append([]string(nil), args...)
	copyProcess.stdin = nil
	return runPreparedProcessWithStdoutLimit(ctx, preparedExecution{process: &copyProcess}, stdoutLimit)
}

func runNormalizationDiscovery(ctx context.Context, execution preparedExecution, args []string, stream string, limit int) (ProcessResult, error) {
	result, err := runNormalizationProcess(ctx, execution, args, limit)
	if err != nil {
		return ProcessResult{}, projectDiscoveryProcessError(err, stream)
	}
	if len(result.Stdout) > limit {
		return ProcessResult{}, discoveryError(stream+"_list_output_limit_exceeded", stream, result.ToolID, 0, "")
	}
	return result, nil
}

func projectDiscoveryProcessError(err error, stream string) error {
	var typed *Error
	if errors.As(err, &typed) && typed.Code() == "tool_output_invalid" && typed.Stage() == "stream" && typed.Reason() == "stdout_limit_exceeded" {
		return discoveryError(stream+"_list_output_limit_exceeded", stream, typed.ToolID(), 0, "")
	}
	if errors.As(err, &typed) && typed.Code() == "tool_failed" && typed.Stage() == "exit" && typed.Reason() == "nonzero_exit" {
		return discoveryError(stream+"_list_nonzero", stream, typed.ToolID(), typed.ExitCode(), typed.Diagnostic())
	}
	return err
}

func discoveryError(reason, stream, toolID string, exitCode int, diagnostic string) error {
	pointer := "/moduleGraph"
	if stream == "package" {
		pointer = "/retainedInputs"
	}
	return newProcessDiagnosticError("build_input_discovery_failed", "retain", reason, pointer, toolID, exitCode, diagnostic)
}

func readNormalizedBoundary(scratch *Scratch, name string, required bool, limit int64, reason, pointer string) (normalizedFile, error) {
	root := scratch.state.owner.rootHandle
	before, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && !required {
		return normalizedFile{}, nil
	}
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() > limit {
		return normalizedFile{}, normalizeError(reason, pointer)
	}
	file, err := root.Open(name)
	if err != nil {
		return normalizedFile{}, normalizeError(reason, pointer)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	after, statErr := root.Lstat(name)
	if readErr != nil || closeErr != nil || statErr != nil || int64(len(data)) > limit || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != int64(len(data)) {
		return normalizedFile{}, normalizeError(reason, pointer)
	}
	return normalizedFile{present: true, digest: provenance.SHA256(data), size: int64(len(data)), info: after}, nil
}

func validateNormalizedGraph(compilation buildinput.Compilation, facts normalizationFacts, requiredEntVersion string) error {
	graph, err := compilation.Graph()
	if err != nil {
		return normalizeError("module_graph_canonical_invalid", "/normalized/moduleGraph")
	}
	consumer, err := graph.ConsumerModule()
	if err != nil || consumer != facts.consumerModule {
		return normalizeError("consumer_module_mismatch", "/normalized/consumerModule")
	}
	tool, err := graph.ToolModule()
	if err != nil || tool != facts.toolModule {
		return normalizeError("tool_module_mismatch", "/normalized/toolModule")
	}
	modules, err := graph.Modules()
	if err != nil {
		return normalizeError("module_graph_canonical_invalid", "/normalized/moduleGraph")
	}
	entCount := 0
	for _, module := range modules {
		if module.Path == entModulePath {
			entCount++
			if requiredEntVersion != "" && module.Version != requiredEntVersion {
				return normalizeError("ent_module_mismatch", "/normalized/entModule")
			}
		}
	}
	if entCount != 1 {
		return normalizeError("ent_module_mismatch", "/normalized/entModule")
	}
	return nil
}

func projectNormalizationCompileError(err error, toolID string) error {
	var typed *buildinput.Error
	if !errors.As(err, &typed) {
		return normalizeError("module_graph_canonical_invalid", "/normalized/moduleGraph")
	}
	switch typed.Reason() {
	case "module_list_output_invalid":
		return normalizeError("resolved_modules_document_invalid", "/normalized/modules")
	case "normalized_module_file_invalid":
		return normalizeError("normalized_module_file_invalid", "/normalized/goMod")
	case "normalized_module_sum_invalid":
		return normalizeError("normalized_module_sum_invalid", "/normalized/goSum")
	case "module_graph_state_invalid", "module_content_variant_invalid", "remote_sum_invalid", "remote_go_mod_sum_invalid":
		return normalizeError("module_graph_canonical_invalid", "/normalized/moduleGraph")
	default:
		return newProcessError(typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer(), toolID, 0)
	}
}

func VerifyDrift(scratch *Scratch, normalization Normalization) error {
	if normalization.state == nil {
		return readbackError("normalization_state_invalid", "/normalization")
	}
	if normalization.state.scratch != scratch {
		return projectDriftError("resolved_module_drift", "/normalized/modules")
	}
	if _, err := normalizationFactsFor(scratch); err != nil {
		return err
	}
	if err := verifyNormalizedBoundary(scratch, "go.mod", normalization.state.goMod); err != nil {
		return projectDriftError("module_file_drift", "/normalized/goMod")
	}
	if err := verifyNormalizedBoundary(scratch, "go.sum", normalization.state.goSum); err != nil {
		return projectDriftError("module_sum_drift", "/normalized/goSum")
	}
	return nil
}

func cloneProcessTool(input ProcessTool) ProcessTool {
	result := input
	result.Args = append([]string(nil), input.Args...)
	result.InputScopes = append([]string(nil), input.InputScopes...)
	result.WriteScopes = append([]string(nil), input.WriteScopes...)
	result.Environment = append([]ProcessEnvironmentRule(nil), input.Environment...)
	result.Probe.Args = append([]string(nil), input.Probe.Args...)
	return result
}

func cloneProcessEnvironment(input []ProcessEnvironment) []ProcessEnvironment {
	return append([]ProcessEnvironment(nil), input...)
}

func verifyNormalizedBoundary(scratch *Scratch, name string, expected normalizedFile) error {
	actual, err := readNormalizedBoundary(scratch, name, expected.present, map[bool]int64{true: MaxModuleFileBytes, false: MaxModuleSumBytes}[name == "go.mod"], "", "")
	if err != nil || actual.present != expected.present || actual.size != expected.size || actual.digest != expected.digest || expected.present && !os.SameFile(actual.info, expected.info) {
		return errors.New("drift")
	}
	return nil
}

func projectDriftError(reason, pointer string) error {
	return newError("scratch_projection_invalid", "drift", reason, pointer)
}
