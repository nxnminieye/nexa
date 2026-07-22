package crudproto

import (
	"context"
	_ "embed"
	"errors"
	"path/filepath"

	"github.com/nxnminieye/nexa/generation/internal/entipc"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

//go:embed enthelper_main.go.txt
var entHelperMain []byte

type ExistingLockInput struct {
	Lock   Lock
	Source provenance.Source
}

type PublishedArtifactInput struct {
	ID             string
	Digest         provenance.Digest
	ManifestSource provenance.Source
}

type MultiTenantConfig struct {
	Enabled bool
}

type EntGraphHostSpec struct {
	RepositoryRoot, StagingRoot, ScratchParent string
	SchemaDir                                  provenance.DomainSource
	BuildTags                                  []string
	ProtoPackage, GoPackage                    string
	Destination                                ProtoDestination
	Tool                                       toolchain.Tool
	Environment                                []toolchain.EnvVar
	Runner                                     toolchain.Runner
	ExistingLock                               *ExistingLockInput
	PublishedArtifact                          *PublishedArtifactInput
	MultiTenant                                MultiTenantConfig
}

func InvokeEntGraphHost(ctx context.Context, spec EntGraphHostSpec) (plan EntGraphPlan, err error) {
	if ctx == nil {
		return EntGraphPlan{}, newHostError("request", "context_invalid", "/context", "")
	}
	if spec.Destination.state == nil {
		return EntGraphPlan{}, newHostError("request", "destination_state_invalid", "/destination", "")
	}
	if spec.Runner == nil {
		return EntGraphPlan{}, newHostError("request", "runner_invalid", "/runner", "")
	}
	framework, err := toolchain.CurrentFrameworkModuleIdentity()
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	location, err := toolchain.LocateModule(toolchain.ModuleLocateSpec{RepositoryRoot: spec.RepositoryRoot, SchemaDir: spec.SchemaDir})
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	scratch, err := toolchain.ProjectScratchModule(toolchain.ScratchModuleSpec{
		RepositoryRoot: spec.RepositoryRoot, StagingRoot: spec.StagingRoot, ScratchParent: spec.ScratchParent,
		Location: location, BuildTags: spec.BuildTags, Framework: framework,
		Helper: toolchain.HelperSource{Path: "cmd/enthelper/main.go", Bytes: entHelperMain, Digest: provenance.SHA256(entHelperMain)},
	})
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	defer func() {
		if cleanupErr := scratch.Cleanup(); cleanupErr != nil {
			plan = EntGraphPlan{}
			err = projectHostOperationError(cleanupErr)
		}
	}()

	normalized, err := toolchain.NormalizeScratchModule(ctx, scratch, spec.Tool, spec.Environment)
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	inspection, err := toolchain.InspectEntityInputs(ctx, toolchain.EntityInputInspectionSpec{
		RepositoryRoot: spec.RepositoryRoot, SchemaDir: spec.SchemaDir, BuildTags: spec.BuildTags,
	})
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	request, executableVersion, err := hostRequest(spec, scratch, inspection)
	if err != nil {
		return EntGraphPlan{}, err
	}
	stdin, err := entipc.CanonicalRequest(request)
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	helperEnvironment, err := helperProcessEnvironment(scratch, spec.Environment)
	if err != nil {
		return EntGraphPlan{}, err
	}
	result, err := spec.Runner.Run(ctx, toolchain.Request{
		RepositoryRoot: spec.RepositoryRoot, StagingRoot: spec.StagingRoot, Scratch: scratch,
		Tool: spec.Tool, Args: []string{"run", "-mod=readonly", "./cmd/enthelper"}, Environment: helperEnvironment, Stdin: stdin,
	})
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	if result.ToolID != spec.Tool.ID || result.Version != spec.Tool.Version || result.ExecutableVersion != executableVersion || result.ExitCode != 0 {
		return EntGraphPlan{}, newHostError("result", "tool_result_invalid", "/result", "")
	}
	resultSource, _ := provenance.ParseDomainSource("stdout/ent-graph-result.json")
	snapshot, err := entipc.ParseResult(resultSource, request, result.Stdout)
	if err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	if err := toolchain.VerifyScratchModuleDrift(scratch, normalized); err != nil {
		return EntGraphPlan{}, projectHostOperationError(err)
	}
	if failure, ok := snapshot.DomainFailure(); ok {
		return EntGraphPlan{}, &Error{owner: failure.Owner(), code: failure.Code(), stage: "helper", reason: failure.Reason(), pointer: failure.Pointer(), source: failure.Source()}
	}
	verified, ok := snapshot.Plan()
	if !ok {
		return EntGraphPlan{}, newHostError("result", "result_branch_invalid", "/result", resultSource.String())
	}
	return verifiedEntGraphPlanFromSnapshot(verified)
}

func helperProcessEnvironment(scratch *toolchain.ScratchModule, environment []toolchain.EnvVar) ([]toolchain.EnvVar, error) {
	root, err := scratch.Root()
	if err != nil {
		return nil, projectHostOperationError(err)
	}
	result := append([]toolchain.EnvVar(nil), environment...)
	for index := range result {
		if result[index].Name == "TMPDIR" {
			result[index].Value = filepath.Join(root, "cmd")
		}
	}
	return result, nil
}

func hostRequest(spec EntGraphHostSpec, scratch *toolchain.ScratchModule, inspection toolchain.EntityInputInspection) (entipc.Request, string, error) {
	graphDigest, err := inspection.ModuleGraphDigest()
	if err != nil {
		return entipc.Request{}, "", projectHostOperationError(err)
	}
	moduleSources, err := inspection.ModuleSources()
	if err != nil {
		return entipc.Request{}, "", projectHostOperationError(err)
	}
	inputDigest, err := inspection.BuildInputDigest()
	if err != nil {
		return entipc.Request{}, "", projectHostOperationError(err)
	}
	executableVersion, err := inspection.ExecutableVersion()
	if err != nil {
		return entipc.Request{}, "", projectHostOperationError(err)
	}
	schemaDir, err := scratch.SchemaDir()
	if err != nil {
		return entipc.Request{}, "", projectHostOperationError(err)
	}
	buildTags, err := scratch.NormalizedBuildTags()
	if err != nil {
		return entipc.Request{}, "", projectHostOperationError(err)
	}
	requestSpec := entipc.RequestSpec{
		RepositoryRoot: spec.RepositoryRoot, SchemaDir: schemaDir, BuildTags: buildTags,
		ModuleGraphDigest: graphDigest, BuildInputDigest: inputDigest, ModuleSources: moduleSources,
		ServiceID: spec.Destination.ServiceID(), ProtoPackage: spec.ProtoPackage, GoPackage: spec.GoPackage,
		ProtoDestination: entipc.ProtoDestination{EntryPath: spec.Destination.EntryPath(), ArtifactPath: spec.Destination.ArtifactPath(), LockPath: spec.Destination.LockPath()},
		Tool:             entipc.ToolIdentity{ID: spec.Tool.ID, Version: spec.Tool.Version, ExecutableVersion: executableVersion},
		MultiTenant:      entipc.MultiTenantConfig{Enabled: spec.MultiTenant.Enabled},
	}
	if spec.ExistingLock != nil {
		requestSpec.ExistingLock = &entipc.ExistingLockInput{Source: spec.ExistingLock.Source, Value: spec.ExistingLock.Lock.state}
	}
	if spec.PublishedArtifact != nil {
		requestSpec.PublishedArtifact = &entipc.PublishedArtifact{ID: spec.PublishedArtifact.ID, Digest: spec.PublishedArtifact.Digest, ManifestSource: spec.PublishedArtifact.ManifestSource}
	}
	request, err := entipc.NewRequest(requestSpec)
	return request, executableVersion, projectHostOperationError(err)
}

func projectHostOperationError(err error) error {
	if err == nil {
		return nil
	}
	var toolErr *toolchain.Error
	if errors.As(err, &toolErr) {
		return &Error{code: toolErr.Code(), stage: toolErr.Stage(), reason: toolErr.Reason(), pointer: toolErr.Pointer(), source: toolErr.Source(), toolID: toolErr.ToolID(), exitCode: toolErr.ExitCode(), diagnostic: toolErr.Diagnostic(), cause: err}
	}
	var ipcErr *entipc.Error
	if errors.As(err, &ipcErr) {
		return &Error{code: ipcErr.Code(), stage: ipcErr.Stage(), reason: ipcErr.Reason(), pointer: ipcErr.Pointer(), source: ipcErr.Source(), cause: err}
	}
	return newHostCauseError("host", "operation_failed", "", "", err)
}
