package toolchain

import (
	"context"

	"github.com/nxnminieye/nexa/generation/internal/entexec"
)

type NormalizationResult struct {
	value entexec.Normalization
	valid bool
}

type ModuleNormalizer interface {
	Normalize(context.Context, *ScratchModule, Tool, []EnvVar) (NormalizationResult, error)
	VerifyDrift(*ScratchModule, NormalizationResult) error
}

func NormalizeScratchModule(ctx context.Context, scratch *ScratchModule, tool Tool, environment []EnvVar) (NormalizationResult, error) {
	return normalizeScratchModule(ctx, scratch, tool, environment, entexec.Normalize)
}

// NormalizeScratchModuleForEntGeneration retains the consumer-selected Ent module version.
func NormalizeScratchModuleForEntGeneration(ctx context.Context, scratch *ScratchModule, tool Tool, environment []EnvVar) (NormalizationResult, error) {
	return normalizeScratchModule(ctx, scratch, tool, environment, entexec.NormalizeConsumerEnt)
}

func normalizeScratchModule(
	ctx context.Context,
	scratch *ScratchModule,
	tool Tool,
	environment []EnvVar,
	normalize func(context.Context, entexec.NormalizeSpec) (entexec.Normalization, error),
) (NormalizationResult, error) {
	var internalScratch *entexec.Scratch
	if scratch != nil {
		internalScratch = scratch.scratch
		if internalScratch == nil {
			internalScratch = &entexec.Scratch{}
		}
	}
	rules := make([]entexec.ProcessEnvironmentRule, len(tool.Environment))
	for index, rule := range tool.Environment {
		rules[index] = entexec.ProcessEnvironmentRule{Name: rule.Name, Source: string(rule.Source), FixedValue: rule.FixedValue}
	}
	values := make([]entexec.ProcessEnvironment, len(environment))
	for index, value := range environment {
		values[index] = entexec.ProcessEnvironment{Name: value.Name, Value: value.Value}
	}
	value, err := normalize(ctx, entexec.NormalizeSpec{
		Scratch: internalScratch,
		Tool: entexec.ProcessTool{
			ID: tool.ID, Version: tool.Version, Executable: tool.Executable,
			Args: append([]string(nil), tool.Args...), InputScopes: append([]string(nil), tool.InputScopes...), WriteScopes: append([]string(nil), tool.WriteScopes...),
			Environment: rules,
			Probe:       entexec.ProcessProbe{Args: append([]string(nil), tool.Probe.Args...), ExpectedVersion: tool.Probe.ExpectedVersion},
		},
		Environment: values,
	})
	if err != nil {
		return NormalizationResult{}, projectEntExecError(err)
	}
	return NormalizationResult{value: value, valid: true}, nil
}

func VerifyScratchModuleDrift(scratch *ScratchModule, result NormalizationResult) error {
	if !result.valid {
		return newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	var internalScratch *entexec.Scratch
	if scratch != nil {
		internalScratch = scratch.scratch
		if internalScratch == nil {
			internalScratch = &entexec.Scratch{}
		}
	}
	return projectEntExecError(entexec.VerifyDrift(internalScratch, result.value))
}

func (r NormalizationResult) ModuleGraph() (ModuleGraph, error) {
	if !r.valid {
		return ModuleGraph{}, newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	value, err := r.value.Graph()
	return ModuleGraph{snapshot: value}, projectEntExecError(err)
}

func (r NormalizationResult) BuildInputs() (BuildInputManifest, error) {
	if !r.valid {
		return BuildInputManifest{}, newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	value, err := r.value.Manifest()
	return BuildInputManifest{value: value, valid: err == nil}, projectEntExecError(err)
}

func (r NormalizationResult) ExecutableVersion() (string, error) {
	if !r.valid {
		return "", newError("module_graph_readback_invalid", "readback", "normalization_state_invalid", "/normalization", "", "", 0)
	}
	value, err := r.value.ExecutableVersion()
	return value, projectEntExecError(err)
}
