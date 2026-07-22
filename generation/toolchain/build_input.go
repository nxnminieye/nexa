package toolchain

import (
	"context"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/provenance"
)

type BuildInputKind string

const (
	BuildInputGo         BuildInputKind = "go"
	BuildInputEmbed      BuildInputKind = "embed"
	BuildInputModuleFile BuildInputKind = "module-file"
	BuildInputModuleSum  BuildInputKind = "module-sum"
)

type RetainedModuleRole string

const (
	RetainedModuleScratchMain RetainedModuleRole = "scratch-main"
	RetainedModuleRepository  RetainedModuleRole = "repository-module"
)

type BuildInputCompileSpec struct {
	RepositoryRoot, ScratchRoot string
	SchemaDir                   provenance.DomainSource
	SchemaImportPath            string
	BuildTags                   []string
	Tool                        Tool
	Environment                 []EnvVar
	ToolModule                  ModuleRequirement
	HelperDigest                provenance.Digest
}

type BuildInputCompilation struct {
	value buildinput.Compilation
}

type BuildInputManifest struct {
	value buildinput.ManifestSnapshot
	valid bool
}

type BuildInputManifestSnapshot struct {
	value buildinput.ManifestSnapshot
	valid bool
}

type RetainedLocalModule struct {
	value buildinput.RetainedLocalModule
}

type RetainedBuildInput struct {
	value buildinput.RetainedBuildInput
}

func CompileBuildInputManifest(ctx context.Context, runner Runner, spec BuildInputCompileSpec) (BuildInputCompilation, error) {
	if ctx == nil {
		return BuildInputCompilation{}, newError("build_input_invalid", "retain", "compile_spec_invalid", "/buildInputs", "", "", 0)
	}
	discovery := buildinput.DiscoveryInput{
		RepositoryRoot: spec.RepositoryRoot, ScratchRoot: spec.ScratchRoot,
		SchemaDir: spec.SchemaDir, SchemaImportPath: spec.SchemaImportPath,
		BuildTags:    spec.BuildTags,
		ToolModule:   buildinput.ModuleRequirement{Path: spec.ToolModule.Path, Version: spec.ToolModule.Version},
		HelperDigest: spec.HelperDigest,
	}
	if err := buildinput.Preflight(discovery); err != nil {
		return BuildInputCompilation{}, projectBuildInputError(err, "", 0)
	}
	tool, environment, tags, err := prepareReadback(spec, runner)
	if err != nil {
		return BuildInputCompilation{}, err
	}
	moduleArgs := []string{"list", "-mod=readonly", "-m", "-json", "all"}
	packageArgs := []string{"list", "-mod=readonly", "-deps", "-json"}
	if len(tags) > 0 {
		packageArgs = append(packageArgs, "-tags="+strings.Join(tags, ","))
	}
	packageArgs = append(packageArgs, spec.SchemaImportPath)

	moduleResult, err := runDiscovery(ctx, runner, spec, tool, environment, moduleArgs, "module", MaxModuleListOutputBytes)
	if err != nil {
		return BuildInputCompilation{}, err
	}
	packageResult, err := runDiscovery(ctx, runner, spec, tool, environment, packageArgs, "package", MaxPackageListOutputBytes)
	if err != nil {
		return BuildInputCompilation{}, err
	}
	if moduleResult.ExecutableVersion != packageResult.ExecutableVersion {
		return BuildInputCompilation{}, newError("build_input_invalid", "retain", "tool_version_mismatch", "/buildInputs/goExecutableVersion", "", packageResult.ToolID, 0)
	}
	discovery.BuildTags = tags
	discovery.GoExecutableVersion = moduleResult.ExecutableVersion
	discovery.ModuleList = append([]byte(nil), moduleResult.Stdout...)
	discovery.PackageList = append([]byte(nil), packageResult.Stdout...)
	compiled, err := buildinput.Compile(discovery)
	if err != nil {
		return BuildInputCompilation{}, projectBuildInputError(err, packageResult.ToolID, 0)
	}
	return BuildInputCompilation{value: compiled}, nil
}

func prepareReadback(spec BuildInputCompileSpec, runner Runner) (Tool, []EnvVar, []string, error) {
	if runner == nil || !validToolID(spec.Tool.ID) || spec.Tool.Version == "" || strings.ContainsRune(spec.Tool.Version, '\x00') || spec.Tool.Executable == "" || strings.ContainsRune(spec.Tool.Executable, '\x00') || len(spec.Tool.Args) != 0 {
		return Tool{}, nil, nil, newError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", "", 0)
	}
	tool := cloneTool(spec.Tool)
	ruleNames := make(map[string]struct{}, len(tool.Environment)+1)
	rules := make(map[string]EnvironmentRule, len(tool.Environment)+1)
	for _, rule := range tool.Environment {
		if !validEnvironmentName(rule.Name) || (rule.Source != EnvironmentHost && rule.Source != EnvironmentScratch && rule.Source != EnvironmentFixed) ||
			(rule.Source != EnvironmentFixed && rule.FixedValue != "") || strings.ContainsRune(rule.FixedValue, '\x00') {
			return Tool{}, nil, nil, newError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", "", 0)
		}
		if rule.Name == "CGO_ENABLED" {
			return Tool{}, nil, nil, newError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", "", 0)
		}
		if _, duplicate := ruleNames[rule.Name]; duplicate {
			return Tool{}, nil, nil, newError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", "", 0)
		}
		ruleNames[rule.Name] = struct{}{}
		rules[rule.Name] = rule
	}
	cgoRule := EnvironmentRule{Name: "CGO_ENABLED", Source: EnvironmentFixed, FixedValue: "0"}
	tool.Environment = append(tool.Environment, cgoRule)
	rules[cgoRule.Name] = cgoRule
	environment := cloneEnvironment(spec.Environment)
	seenEnvironment := make(map[string]struct{}, len(environment)+1)
	for _, value := range environment {
		if !validEnvironmentName(value.Name) || value.Name == "CGO_ENABLED" || strings.ContainsRune(value.Value, '\x00') {
			return Tool{}, nil, nil, newError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "", "", 0)
		}
		if _, duplicate := seenEnvironment[value.Name]; duplicate {
			return Tool{}, nil, nil, newError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "", "", 0)
		}
		rule, declared := rules[value.Name]
		if !declared || rule.Source == EnvironmentFixed && value.Value != rule.FixedValue || rule.Source != EnvironmentFixed && value.Value == "" {
			return Tool{}, nil, nil, newError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "", "", 0)
		}
		seenEnvironment[value.Name] = struct{}{}
	}
	for name := range rules {
		if name == "CGO_ENABLED" {
			continue
		}
		if _, exists := seenEnvironment[name]; !exists {
			return Tool{}, nil, nil, newError("build_input_invalid", "retain", "environment_invalid", "/buildInputs/environment", "", "", 0)
		}
	}
	environment = append(environment, EnvVar{Name: "CGO_ENABLED", Value: "0"})
	sort.Slice(environment, func(i, j int) bool { return environment[i].Name < environment[j].Name })
	tags := append([]string(nil), spec.BuildTags...)
	sort.Strings(tags)
	return tool, environment, tags, nil
}

func validToolID(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if index == 0 && !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9') {
			return false
		}
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if index == 0 && !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_') {
			return false
		}
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_') {
			return false
		}
	}
	return true
}

func runDiscovery(ctx context.Context, runner Runner, spec BuildInputCompileSpec, tool Tool, environment []EnvVar, args []string, stream string, limit int) (Result, error) {
	request := Request{
		RepositoryRoot: spec.RepositoryRoot, StagingRoot: spec.ScratchRoot, WorkDir: spec.ScratchRoot,
		Tool: cloneTool(tool), Args: append([]string(nil), args...), Environment: cloneEnvironment(environment),
	}
	result, runErr := runner.Run(ctx, request)
	reasonNonzero := stream + "_list_nonzero"
	pointer := "/moduleGraph"
	if stream == "package" {
		pointer = "/retainedInputs"
	}
	if runErr != nil {
		toolID, exitCode, diagnostic := "", 0, ""
		var owned *Error
		if errors.As(runErr, &owned) && owned.ToolID() == tool.ID && owned.ExitCode() != 0 {
			toolID, exitCode, diagnostic = owned.ToolID(), owned.ExitCode(), owned.Diagnostic()
		}
		if result.ToolID == tool.ID && result.Version == tool.Version && result.ExitCode != 0 {
			toolID, exitCode, diagnostic = result.ToolID, result.ExitCode, sanitizeDiagnostic(result.Diagnostic, request)
		}
		return Result{}, newDiagnosticError("build_input_discovery_failed", "retain", reasonNonzero, pointer, "", toolID, exitCode, diagnostic)
	}
	if result.ToolID != tool.ID || result.Version != tool.Version {
		return Result{}, newError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", "", 0)
	}
	if result.ExitCode != 0 {
		return Result{}, newDiagnosticError("build_input_discovery_failed", "retain", reasonNonzero, pointer, "", result.ToolID, result.ExitCode, sanitizeDiagnostic(result.Diagnostic, request))
	}
	if result.ExecutableVersion == "" {
		return Result{}, newError("build_input_invalid", "retain", "tool_invalid", "/buildInputs/tool", "", result.ToolID, 0)
	}
	if tool.Probe.ExpectedVersion != "" && result.ExecutableVersion != tool.Probe.ExpectedVersion {
		return Result{}, newError("build_input_invalid", "retain", "tool_version_mismatch", "/buildInputs/goExecutableVersion", "", result.ToolID, 0)
	}
	if len(result.Stdout) > limit {
		return Result{}, newError("build_input_discovery_failed", "retain", stream+"_list_output_limit_exceeded", pointer, "", result.ToolID, 0)
	}
	result.Stdout = append([]byte(nil), result.Stdout...)
	return result, nil
}

func (c BuildInputCompilation) ModuleGraph() (ModuleGraph, error) {
	value, err := c.value.Graph()
	return ModuleGraph{snapshot: value}, projectBuildInputError(err, "", 0)
}

func (c BuildInputCompilation) Manifest() (BuildInputManifest, error) {
	value, err := c.value.Manifest()
	return BuildInputManifest{value: value, valid: err == nil}, projectBuildInputError(err, "", 0)
}

func (c BuildInputCompilation) ExecutableVersion() (string, error) {
	value, err := c.value.ExecutableVersion()
	return value, projectBuildInputError(err, "", 0)
}

func (m BuildInputManifest) BuildTags() ([]string, error) {
	if !m.valid {
		return nil, manifestReadbackError(false)
	}
	value, err := m.value.BuildTags()
	return value, projectBuildInputError(err, "", 0)
}

func (m BuildInputManifest) SchemaImportPath() (string, error) {
	if !m.valid {
		return "", manifestReadbackError(false)
	}
	value, err := m.value.SchemaImportPath()
	return value, projectBuildInputError(err, "", 0)
}

func (m BuildInputManifest) GoExecutableVersion() (string, error) {
	if !m.valid {
		return "", manifestReadbackError(false)
	}
	value, err := m.value.GoExecutableVersion()
	return value, projectBuildInputError(err, "", 0)
}

func (m BuildInputManifest) ModuleGraphDigest() (provenance.Digest, error) {
	if !m.valid {
		return provenance.Digest{}, manifestReadbackError(false)
	}
	value, err := m.value.ModuleGraphDigest()
	return value, projectBuildInputError(err, "", 0)
}

func (m BuildInputManifest) LocalModules() ([]RetainedLocalModule, error) {
	if !m.valid {
		return nil, manifestReadbackError(false)
	}
	value, err := m.value.LocalModules()
	if err != nil {
		return nil, projectBuildInputError(err, "", 0)
	}
	result := make([]RetainedLocalModule, len(value))
	for index, item := range value {
		result[index] = RetainedLocalModule{value: item}
	}
	return result, nil
}

func (m BuildInputManifest) Inputs() ([]RetainedBuildInput, error) {
	if !m.valid {
		return nil, manifestReadbackError(false)
	}
	value, err := m.value.Inputs()
	if err != nil {
		return nil, projectBuildInputError(err, "", 0)
	}
	result := make([]RetainedBuildInput, len(value))
	for index, item := range value {
		result[index] = RetainedBuildInput{value: item}
	}
	return result, nil
}

func (m BuildInputManifest) CanonicalJSON() ([]byte, error) {
	if !m.valid {
		return nil, manifestReadbackError(false)
	}
	value, err := buildinput.CanonicalSnapshot(m.value)
	return value, projectBuildInputError(err, "", 0)
}

func (m BuildInputManifest) Digest() (provenance.Digest, error) {
	if !m.valid {
		return provenance.Digest{}, manifestReadbackError(false)
	}
	value, err := buildinput.ManifestDigest(m.value)
	return value, projectBuildInputError(err, "", 0)
}

func VerifyBuildInputManifest(manifest BuildInputManifest) error {
	if !manifest.valid {
		return manifestReadbackError(false)
	}
	_, err := buildinput.CanonicalSnapshot(manifest.value)
	return projectBuildInputError(err, "", 0)
}

func ParseBuildInputManifestSnapshot(source provenance.DomainSource, data []byte) (BuildInputManifestSnapshot, error) {
	value, err := buildinput.ParseManifest(source, append([]byte(nil), data...))
	if err != nil {
		return BuildInputManifestSnapshot{}, projectBuildInputError(err, "", 0)
	}
	return BuildInputManifestSnapshot{value: value, valid: true}, nil
}

func (s BuildInputManifestSnapshot) BuildTags() ([]string, error) {
	if !s.valid {
		return nil, manifestReadbackError(true)
	}
	value, err := s.value.BuildTags()
	return value, projectBuildInputError(err, "", 0)
}

func (s BuildInputManifestSnapshot) SchemaImportPath() (string, error) {
	if !s.valid {
		return "", manifestReadbackError(true)
	}
	value, err := s.value.SchemaImportPath()
	return value, projectBuildInputError(err, "", 0)
}

func (s BuildInputManifestSnapshot) GoExecutableVersion() (string, error) {
	if !s.valid {
		return "", manifestReadbackError(true)
	}
	value, err := s.value.GoExecutableVersion()
	return value, projectBuildInputError(err, "", 0)
}

func (s BuildInputManifestSnapshot) ModuleGraphDigest() (provenance.Digest, error) {
	if !s.valid {
		return provenance.Digest{}, manifestReadbackError(true)
	}
	value, err := s.value.ModuleGraphDigest()
	return value, projectBuildInputError(err, "", 0)
}

func (s BuildInputManifestSnapshot) LocalModules() ([]RetainedLocalModule, error) {
	if !s.valid {
		return nil, manifestReadbackError(true)
	}
	value, err := s.value.LocalModules()
	if err != nil {
		return nil, projectBuildInputError(err, "", 0)
	}
	result := make([]RetainedLocalModule, len(value))
	for index, item := range value {
		result[index] = RetainedLocalModule{value: item}
	}
	return result, nil
}

func (s BuildInputManifestSnapshot) Inputs() ([]RetainedBuildInput, error) {
	if !s.valid {
		return nil, manifestReadbackError(true)
	}
	value, err := s.value.Inputs()
	if err != nil {
		return nil, projectBuildInputError(err, "", 0)
	}
	result := make([]RetainedBuildInput, len(value))
	for index, item := range value {
		result[index] = RetainedBuildInput{value: item}
	}
	return result, nil
}

func (s BuildInputManifestSnapshot) CanonicalJSON() ([]byte, error) {
	if !s.valid {
		return nil, manifestReadbackError(true)
	}
	value, err := buildinput.CanonicalSnapshot(s.value)
	return value, projectBuildInputError(err, "", 0)
}

func (s BuildInputManifestSnapshot) Digest() (provenance.Digest, error) {
	if !s.valid {
		return provenance.Digest{}, manifestReadbackError(true)
	}
	value, err := buildinput.ManifestDigest(s.value)
	return value, projectBuildInputError(err, "", 0)
}

func BuildInputManifestSchema() []byte { return buildinput.ManifestSchema() }

func manifestReadbackError(snapshot bool) *Error {
	reason := "manifest_state_invalid"
	if snapshot {
		reason = "manifest_snapshot_state_invalid"
	}
	return newError("build_input_readback_invalid", "retain-readback", reason, "/buildInputs", "", "", 0)
}

func (m RetainedLocalModule) Role() (RetainedModuleRole, error) {
	if m.value.Role == "" {
		return "", newError("build_input_readback_invalid", "retain-readback", "retained_module_state_invalid", "/retainedModules/0", "", "", 0)
	}
	return RetainedModuleRole(m.value.Role), nil
}

func (m RetainedLocalModule) Module() (ModuleIdentity, error) {
	if m.value.Role == "" {
		return ModuleIdentity{}, newError("build_input_readback_invalid", "retain-readback", "retained_module_state_invalid", "/retainedModules/0", "", "", 0)
	}
	return moduleIdentityFromInternal(m.value.Module), nil
}

func (m RetainedLocalModule) RepositoryPath() (string, bool, error) {
	if m.value.Role == "" {
		return "", false, newError("build_input_readback_invalid", "retain-readback", "retained_module_state_invalid", "/retainedModules/0", "", "", 0)
	}
	return m.value.RepositoryPath, m.value.HasRepositoryPath, nil
}

func (i RetainedBuildInput) Module() (RetainedLocalModule, error) {
	if i.value.Kind == "" {
		return RetainedLocalModule{}, newError("build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", "", 0)
	}
	return RetainedLocalModule{value: i.value.Module}, nil
}

func (i RetainedBuildInput) Path() (string, error) {
	if i.value.Kind == "" {
		return "", newError("build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", "", 0)
	}
	return i.value.Path, nil
}

func (i RetainedBuildInput) Kind() (BuildInputKind, error) {
	if i.value.Kind == "" {
		return "", newError("build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", "", 0)
	}
	return BuildInputKind(i.value.Kind), nil
}

func (i RetainedBuildInput) Size() (int64, error) {
	if i.value.Kind == "" {
		return 0, newError("build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", "", 0)
	}
	return i.value.Size, nil
}

func (i RetainedBuildInput) Digest() (provenance.Digest, error) {
	if i.value.Kind == "" {
		return provenance.Digest{}, newError("build_input_readback_invalid", "retain-readback", "retained_input_state_invalid", "/retainedInputs/0", "", "", 0)
	}
	return i.value.Digest, nil
}
