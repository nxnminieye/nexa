package generation

import (
	"bytes"
	"context"
	"errors"
	goparser "go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	protooptions "github.com/bufbuild/protocompile/options"
	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	cliprotocol "github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/generation/apigo"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/rpcgo"
	servicecontract "github.com/nxnminieye/nexa/generation/service"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

const maxControlFileBytes = 16 << 20

var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-.][a-z0-9]+)*$`)

type commandBuild struct {
	repositoryRoot string
	plan           transaction.Plan
	lockDigest     provenance.Digest
	lockChanged    bool
}

type entGenerationResult struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Service    string `json:"service"`
}

func (r *commandRunner) generateEnt(ctx context.Context, invocation plugin.Invocation) (result any, resultErr error) {
	repositoryRoot, service, err := r.resolveService(ctx, invocation)
	if err != nil {
		return nil, err
	}
	if err := r.requireProviderTool(invocation.Flags["provider"].(string), ToolRoleEntGenerate, service.EntGenerateTool, "/project/services/entGenerateTool"); err != nil {
		return nil, err
	}
	schema, err := provenance.ParseDomainSource(service.EntSchemaDir)
	if err != nil {
		return nil, inputError("fact_source_invalid", "provider", "schema_source_invalid", "/project/services/entSchemaDir", "")
	}
	invocationRoot, stagingRoot, scratchParent, environment, err := r.invocationEnvironment(service.EntGenerateTool)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(invocationRoot)

	framework, err := toolchain.CurrentFrameworkModuleIdentity()
	if err != nil {
		return nil, projectOwnerError(err)
	}
	location, err := toolchain.LocateModule(toolchain.ModuleLocateSpec{RepositoryRoot: repositoryRoot, SchemaDir: schema})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	helper := []byte("package main\n\nfunc main() {}\n")
	scratch, err := toolchain.ProjectScratchModule(toolchain.ScratchModuleSpec{
		RepositoryRoot: repositoryRoot,
		StagingRoot:    stagingRoot,
		ScratchParent:  scratchParent,
		Location:       location,
		BuildTags:      nil,
		Framework:      framework,
		Helper: toolchain.HelperSource{
			Path:   "cmd/enthelper/main.go",
			Bytes:  helper,
			Digest: provenance.SHA256(helper),
		},
	})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	defer func() {
		if cleanupErr := scratch.Cleanup(); cleanupErr != nil {
			result = nil
			resultErr = projectOwnerError(cleanupErr)
		}
	}()

	if !exactScopes(service.EntGenerateTool.WriteScopes, "repository", "scratch") {
		return nil, inputError("fact_source_invalid", "provider", "ent_generate_tool_scope_invalid", "/project/services/entGenerateTool/writeScopes", "")
	}
	normalizationTool := service.EntGenerateTool
	normalizationTool.Args = append([]string(nil), service.EntGenerateTool.Args...)
	normalizationTool.InputScopes = append([]string(nil), service.EntGenerateTool.InputScopes...)
	normalizationTool.WriteScopes = []string{"scratch"}
	normalizationTool.Environment = append([]toolchain.EnvironmentRule(nil), service.EntGenerateTool.Environment...)
	normalizationTool.Probe.Args = append([]string(nil), service.EntGenerateTool.Probe.Args...)
	normalized, err := toolchain.NormalizeScratchModuleForEntGeneration(ctx, scratch, normalizationTool, environment)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	executableVersion, err := normalized.ExecutableVersion()
	if err != nil {
		return nil, projectOwnerError(err)
	}
	moduleRoot, err := location.ModuleDir()
	if err != nil {
		return nil, projectOwnerError(err)
	}
	scratchRoot, err := scratch.Root()
	if err != nil {
		return nil, projectOwnerError(err)
	}
	args, err := officialEntGenerateArgs(moduleRoot, scratchRoot, filepath.Join(repositoryRoot, filepath.FromSlash(schema.String())))
	if err != nil {
		return nil, err
	}
	processResult, err := r.runner.Run(ctx, toolchain.Request{
		RepositoryRoot: repositoryRoot,
		StagingRoot:    stagingRoot,
		Scratch:        scratch,
		Tool:           service.EntGenerateTool,
		Args:           args,
		Environment:    environment,
	})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	if processResult.ToolID != service.EntGenerateTool.ID ||
		processResult.Version != service.EntGenerateTool.Version ||
		processResult.ExecutableVersion != executableVersion || processResult.ExitCode != 0 {
		return nil, generationError("tool_result_invalid", cliprotocol.CategoryInternal, "Ent generation failed", "result", "tool_result_invalid", "/result", "")
	}
	return entGenerationResult{
		APIVersion: "nexa.dev/ent-generation-result/v1",
		Kind:       "EntGenerationResult",
		Status:     "generated",
		Service:    service.ServiceID,
	}, nil
}

func officialEntGenerateArgs(moduleRoot, scratchRoot, schemaRoot string) ([]string, error) {
	relativeSchema, err := filepath.Rel(moduleRoot, schemaRoot)
	if err != nil || relativeSchema == ".." || strings.HasPrefix(relativeSchema, ".."+string(filepath.Separator)) {
		return nil, inputError("fact_source_invalid", "provider", "schema_source_invalid", "/project/services/entSchemaDir", "")
	}
	schemaArgument := filepath.ToSlash(relativeSchema)
	if schemaArgument != "." {
		schemaArgument = "./" + schemaArgument
	}
	return []string{
		"-C", moduleRoot,
		"run", "-modfile=" + filepath.Join(scratchRoot, "go.mod"), "-mod=mod",
		"entgo.io/ent/cmd/ent", "generate", schemaArgument,
	}, nil
}

func exactScopes(actual []string, expected ...string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

// invocationRunner binds scratch-valued environment entries to the staging
// directory that the delegated RPC/API planner actually passes to the runner.
type invocationRunner struct {
	next toolchain.Runner
}

func (runner invocationRunner) Run(ctx context.Context, request toolchain.Request) (toolchain.Result, error) {
	if runner.next == nil {
		return toolchain.Result{}, errors.New("delegated runner is unavailable")
	}
	staging, err := filepath.EvalSymlinks(request.StagingRoot)
	if err != nil || staging == "" {
		return toolchain.Result{}, errors.New("delegated staging is unavailable")
	}
	request.Environment = append([]toolchain.EnvVar(nil), request.Environment...)
	for ruleIndex, rule := range request.Tool.Environment {
		if rule.Source != toolchain.EnvironmentScratch {
			continue
		}
		found := false
		for valueIndex := range request.Environment {
			if request.Environment[valueIndex].Name != rule.Name {
				continue
			}
			directory := filepath.Join(staging, ".nexa-env", strconv.Itoa(ruleIndex)+"-"+strings.ToLower(rule.Name))
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return toolchain.Result{}, errors.New("delegated scratch environment is unavailable")
			}
			canonical, err := filepath.EvalSymlinks(directory)
			if err != nil {
				return toolchain.Result{}, errors.New("delegated scratch environment is unavailable")
			}
			request.Environment[valueIndex].Value = canonical
			found = true
			break
		}
		if !found {
			return toolchain.Result{}, errors.New("delegated scratch environment is missing")
		}
	}
	return runner.next.Run(ctx, request)
}

func newCommandRunner(options Options) (*commandRunner, error) {
	providers := make(map[string]ProjectProvider, len(options.Providers))
	providerTools := make(map[string]map[ToolRole]map[string]string, len(options.Providers))
	toolProviders := make(map[string]int)
	tools := map[ToolRole][]plugin.DelegatedToolSpec{
		ToolRoleEntGenerate: {}, ToolRoleEntCRUD: {}, ToolRoleRPCGo: {}, ToolRoleAPIGo: {},
	}
	for index, candidate := range options.Providers {
		if nilProvider(candidate) {
			return nil, inputError("provider_invalid", "provider", "provider_nil", indexedPointer("/providers", index), "")
		}
		descriptor := candidate.Descriptor()
		if !providerIDPattern.MatchString(descriptor.ID) {
			return nil, inputError("provider_invalid", "provider", "provider_id_invalid", indexedPointer("/providers", index)+"/id", "")
		}
		if !validProviderVersion(descriptor.Version) {
			return nil, inputError("provider_invalid", "provider", "provider_version_invalid", indexedPointer("/providers", index)+"/version", "")
		}
		if _, duplicate := providers[descriptor.ID]; duplicate {
			return nil, inputError("provider_invalid", "provider", "provider_duplicate", indexedPointer("/providers", index)+"/id", "")
		}
		providerToolSet := make(map[ToolRole]map[string]string)
		for toolIndex, declared := range descriptor.Tools {
			if !validToolRole(declared.Role) {
				return nil, inputError("provider_invalid", "provider", "provider_tool_role_invalid", indexedPointer("/providers", index)+"/tools/"+strconv.Itoa(toolIndex)+"/role", "")
			}
			tool := declared.Tool
			key := string(declared.Role) + "\x00" + tool.ID
			if _, duplicate := toolProviders[key]; duplicate {
				return nil, inputError("provider_invalid", "provider", "provider_tool_duplicate", indexedPointer("/providers", index)+"/tools/"+strconv.Itoa(toolIndex)+"/tool/id", "")
			}
			toolProviders[key] = index
			if providerToolSet[declared.Role] == nil {
				providerToolSet[declared.Role] = make(map[string]string)
			}
			providerToolSet[declared.Role][tool.ID] = tool.Version
			tools[declared.Role] = append(tools[declared.Role], cloneDelegatedTool(tool))
		}
		providers[descriptor.ID] = candidate
		providerTools[descriptor.ID] = providerToolSet
	}
	for role := range tools {
		sort.Slice(tools[role], func(left, right int) bool {
			if tools[role][left].ID != tools[role][right].ID {
				return tools[role][left].ID < tools[role][right].ID
			}
			return tools[role][left].Version < tools[role][right].Version
		})
	}
	runner := options.Runner
	if runner == nil {
		runner = toolchain.NewExecRunner()
	}
	return &commandRunner{
		providers:       providers,
		providerTools:   providerTools,
		runner:          runner,
		hostEnvironment: append([]toolchain.EnvVar(nil), options.Environment...),
		delegatedTools:  tools,
	}, nil
}

func validToolRole(role ToolRole) bool {
	switch role {
	case ToolRoleEntGenerate, ToolRoleEntCRUD, ToolRoleRPCGo, ToolRoleAPIGo:
		return true
	default:
		return false
	}
}

func (r *commandRunner) requireProviderTool(providerID string, role ToolRole, tool toolchain.Tool, pointer string) error {
	roles := r.providerTools[providerID]
	declared := roles[role]
	version, ok := declared[tool.ID]
	if !ok {
		return inputError("provider_invalid", "provider", "provider_tool_undeclared", pointer, tool.ID)
	}
	if version != tool.Version {
		return inputError("provider_invalid", "provider", "provider_tool_identity_mismatch", pointer, tool.ID)
	}
	return nil
}

func (r *commandRunner) rpcPlan(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionPlan(ctx, invocation, r.buildRPC)
}
func (r *commandRunner) rpcCheck(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionCheck(ctx, invocation, r.buildRPC)
}
func (r *commandRunner) rpcWrite(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionWrite(ctx, invocation, r.buildRPC)
}
func (r *commandRunner) apiPlan(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionPlan(ctx, invocation, r.buildAPI)
}
func (r *commandRunner) apiCheck(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionCheck(ctx, invocation, r.buildAPI)
}
func (r *commandRunner) apiWrite(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionWrite(ctx, invocation, r.buildAPI)
}
func (r *commandRunner) serviceManifestCheck(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionCheck(ctx, invocation, r.buildServiceManifest)
}
func (r *commandRunner) serviceManifestWrite(ctx context.Context, invocation plugin.Invocation) (any, error) {
	return r.transactionWrite(ctx, invocation, r.buildServiceManifest)
}

type transactionBuilder func(context.Context, plugin.Invocation) (commandBuild, func(), error)

func (r *commandRunner) transactionPlan(ctx context.Context, invocation plugin.Invocation, build transactionBuilder) (any, error) {
	built, cleanup, err := build(ctx, invocation)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	defer built.plan.Close()
	return jsonDocument(built.plan.CanonicalJSON()), nil
}

func (r *commandRunner) transactionCheck(ctx context.Context, invocation plugin.Invocation, build transactionBuilder) (any, error) {
	built, cleanup, err := build(ctx, invocation)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	defer built.plan.Close()
	repository, err := os.OpenRoot(built.repositoryRoot)
	if err != nil {
		return nil, inputError("repository_invalid", "repository", "repository_open_failed", "/repo-root", "")
	}
	defer repository.Close()
	result, err := transaction.Check(built.plan, repository)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	return jsonDocument(result.CanonicalJSON()), nil
}

func (r *commandRunner) transactionWrite(ctx context.Context, invocation plugin.Invocation, build transactionBuilder) (any, error) {
	built, cleanup, err := build(ctx, invocation)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	defer built.plan.Close()
	accepted, err := provenance.ParseDigest(invocation.Flags["plan-digest"].(string))
	if err != nil {
		return nil, driftError("transaction_write_failed", "write", "plan_digest_mismatch", "/plan-digest", "")
	}
	result, err := transaction.Write(ctx, built.plan, built.repositoryRoot, transaction.WriteOptions{PlanDigest: accepted})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	return jsonDocument(result.CanonicalJSON()), nil
}

func (r *commandRunner) buildRPC(ctx context.Context, invocation plugin.Invocation) (commandBuild, func(), error) {
	repositoryRoot, selected, err := r.resolveService(ctx, invocation)
	if err != nil {
		return commandBuild{}, nil, err
	}
	if err := r.requireProviderTool(invocation.Flags["provider"].(string), ToolRoleRPCGo, selected.RPCGoTool, "/project/services/rpcGoTool"); err != nil {
		return commandBuild{}, nil, err
	}
	repository, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return commandBuild{}, nil, inputError("repository_invalid", "repository", "repository_open_failed", "/repo-root", "")
	}
	defer repository.Close()
	document, err := genprotocol.Compile(ctx, genprotocol.CompileOptions{
		ServiceID: selected.ServiceID, EntryFiles: []string{selected.ProtoEntry}, Resolver: rootProtocolResolver{root: repository},
	})
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	invocationRoot, toolStagingRoot, _, environment, err := r.invocationEnvironment(selected.RPCGoTool)
	if err != nil {
		return commandBuild{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(invocationRoot) }
	manifestPath := ".nexa/generation/rpc-go." + selected.ServiceID + ".manifest.json"
	previous, err := loadArtifactManifest(repository, manifestPath)
	if err != nil {
		return commandBuild{}, cleanup, err
	}
	plan, err := transaction.Build(ctx, repositoryRoot, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		inputs, planErr := rpcgo.Plan(ctx, document, rpcgo.Options{
			ServiceID: selected.ServiceID, RepositoryRoot: repositoryRoot, StagingRoot: toolStagingRoot, Emit: emit,
			Tool: selected.RPCGoTool, Runner: invocationRunner{next: r.runner}, Environment: environment,
		})
		if planErr != nil {
			return transaction.PlanRequest{}, projectOwnerError(planErr)
		}
		staleProbes := ownershipProbes(inputs)
		if previous != nil {
			staleProbes = append(staleProbes, rpcgo.StaleOwnershipProbes(*previous)...)
		}
		return transaction.PlanRequest{
			Generator: artifact.GeneratorSpec{ID: "rpc-go", Version: capabilityVersion}, Sources: document.Sources(),
			Expected: inputs, StaleOwnershipProbes: staleProbes, Previous: previous, ManifestPath: manifestPath,
			RevalidateSources: func(revalidateCtx context.Context) ([]provenance.Source, error) {
				currentRoot, openErr := os.OpenRoot(repositoryRoot)
				if openErr != nil {
					return nil, openErr
				}
				defer currentRoot.Close()
				current, compileErr := genprotocol.Compile(revalidateCtx, genprotocol.CompileOptions{
					ServiceID: selected.ServiceID, EntryFiles: []string{selected.ProtoEntry}, Resolver: rootProtocolResolver{root: currentRoot},
				})
				if compileErr != nil {
					return nil, compileErr
				}
				return current.Sources(), nil
			},
		}, nil
	})
	if err != nil {
		return commandBuild{}, cleanup, projectOwnerError(err)
	}
	return commandBuild{repositoryRoot: repositoryRoot, plan: plan}, cleanup, nil
}

func (r *commandRunner) buildAPI(ctx context.Context, invocation plugin.Invocation) (commandBuild, func(), error) {
	repositoryRoot, project, selected, err := r.resolveCoreService(ctx, invocation)
	if err != nil {
		return commandBuild{}, nil, err
	}
	if err := r.requireProviderTool(invocation.Flags["provider"].(string), ToolRoleAPIGo, selected.APIGoTool, "/project/services/apiGoTool"); err != nil {
		return commandBuild{}, nil, err
	}
	repository, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return commandBuild{}, nil, inputError("repository_invalid", "repository", "repository_open_failed", "/repo-root", "")
	}
	defer repository.Close()
	catalog := servicecatalog.Empty()
	if project.CatalogPath != "" {
		catalog, err = servicecatalog.Load(repository, project.CatalogPath)
		if err != nil {
			return commandBuild{}, nil, projectOwnerError(err)
		}
	}
	protocols := make([]genprotocol.Document, 0, len(project.Services))
	for _, serviceProject := range project.Services {
		if serviceProject.ProtoEntry == "" {
			continue
		}
		document, compileErr := genprotocol.Compile(ctx, genprotocol.CompileOptions{
			ServiceID: serviceProject.ServiceID, EntryFiles: []string{serviceProject.ProtoEntry}, Resolver: rootProtocolResolver{root: repository},
		})
		if compileErr != nil {
			return commandBuild{}, nil, projectOwnerError(compileErr)
		}
		protocols = append(protocols, document)
	}
	native, err := httpapi.Load(ctx, httpapi.LoadOptions{RepositoryRoot: repositoryRoot, EntryFile: selected.APIEntry})
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	module, repositoryCoreRoot, moduleCoreRoot, err := loadAPIModuleFacts(repositoryRoot, selected.APIEntry)
	if err != nil {
		return commandBuild{}, nil, err
	}
	composed, err := composition.Build(catalog, protocols, native, composition.BuildOptions{CoreServiceID: selected.ServiceID, ConsumerModulePath: module.modulePath})
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	generated, err := composition.GeneratedAPI(composed)
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	merged, err := httpapi.Merge(native, generated)
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	rendered, err := composition.Render(composed, composition.RenderOptions{CoreRoot: moduleCoreRoot})
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	rendered, err = rebaseRenderedArtifacts(rendered, module, repositoryCoreRoot)
	if err != nil {
		return commandBuild{}, nil, err
	}
	sources, err := apiOwnerSources(catalog, protocols, native, merged, rendered, module.source)
	if err != nil {
		return commandBuild{}, nil, err
	}
	invocationRoot, toolStagingRoot, _, environment, err := r.invocationEnvironment(selected.APIGoTool)
	if err != nil {
		return commandBuild{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(invocationRoot) }
	manifestPath := ".nexa/generation/api-go." + selected.ServiceID + ".manifest.json"
	previous, err := loadArtifactManifest(repository, manifestPath)
	if err != nil {
		return commandBuild{}, cleanup, err
	}
	plan, err := transaction.Build(ctx, repositoryRoot, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		inputs, planErr := apigo.Plan(ctx, merged, rendered, apigo.Options{
			CoreServiceID: selected.ServiceID, RepositoryRoot: repositoryRoot, StagingRoot: toolStagingRoot, Emit: emit,
			Tool: selected.APIGoTool, Runner: invocationRunner{next: r.runner}, Environment: environment, Sources: sources,
		})
		if planErr != nil {
			return transaction.PlanRequest{}, projectOwnerError(planErr)
		}
		staleProbes := ownershipProbes(inputs)
		if previous != nil {
			staleProbes = append(staleProbes, apigo.StaleOwnershipProbes(*previous)...)
		}
		return transaction.PlanRequest{
			Generator: artifact.GeneratorSpec{ID: "api-go", Version: capabilityVersion}, Sources: sources,
			Expected: inputs, StaleOwnershipProbes: staleProbes, Previous: previous, ManifestPath: manifestPath,
			RevalidateSources: func(revalidateCtx context.Context) ([]provenance.Source, error) {
				return reloadAPIOwnerSources(revalidateCtx, repositoryRoot, project, selected)
			},
		}, nil
	})
	if err != nil {
		return commandBuild{}, cleanup, projectOwnerError(err)
	}
	return commandBuild{repositoryRoot: repositoryRoot, plan: plan}, cleanup, nil
}

func reloadAPIOwnerSources(ctx context.Context, repositoryRoot string, project Project, selected ServiceProject) ([]provenance.Source, error) {
	repository, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return nil, err
	}
	defer repository.Close()
	catalog := servicecatalog.Empty()
	if project.CatalogPath != "" {
		catalog, err = servicecatalog.Load(repository, project.CatalogPath)
		if err != nil {
			return nil, err
		}
	}
	protocols := make([]genprotocol.Document, 0, len(project.Services))
	for _, serviceProject := range project.Services {
		if serviceProject.ProtoEntry == "" {
			continue
		}
		document, compileErr := genprotocol.Compile(ctx, genprotocol.CompileOptions{
			ServiceID: serviceProject.ServiceID, EntryFiles: []string{serviceProject.ProtoEntry}, Resolver: rootProtocolResolver{root: repository},
		})
		if compileErr != nil {
			return nil, compileErr
		}
		protocols = append(protocols, document)
	}
	native, err := httpapi.Load(ctx, httpapi.LoadOptions{RepositoryRoot: repositoryRoot, EntryFile: selected.APIEntry})
	if err != nil {
		return nil, err
	}
	module, repositoryCoreRoot, moduleCoreRoot, err := loadAPIModuleFacts(repositoryRoot, selected.APIEntry)
	if err != nil {
		return nil, err
	}
	composed, err := composition.Build(catalog, protocols, native, composition.BuildOptions{CoreServiceID: selected.ServiceID, ConsumerModulePath: module.modulePath})
	if err != nil {
		return nil, err
	}
	generated, err := composition.GeneratedAPI(composed)
	if err != nil {
		return nil, err
	}
	merged, err := httpapi.Merge(native, generated)
	if err != nil {
		return nil, err
	}
	rendered, err := composition.Render(composed, composition.RenderOptions{CoreRoot: moduleCoreRoot})
	if err != nil {
		return nil, err
	}
	rendered, err = rebaseRenderedArtifacts(rendered, module, repositoryCoreRoot)
	if err != nil {
		return nil, err
	}
	return apiOwnerSources(catalog, protocols, native, merged, rendered, module.source)
}

func (r *commandRunner) buildServiceManifest(ctx context.Context, invocation plugin.Invocation) (commandBuild, func(), error) {
	repositoryRoot, project, selected, err := r.resolveProjectService(ctx, invocation)
	if err != nil {
		return commandBuild{}, nil, err
	}
	repository, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return commandBuild{}, nil, inputError("repository_invalid", "repository", "repository_open_failed", "/repo-root", "")
	}
	defer repository.Close()
	catalog, err := servicecatalog.Load(repository, project.CatalogPath)
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	discovered, ok := catalog.Lookup(selected.ServiceID)
	if !ok {
		return commandBuild{}, nil, inputError("fact_source_missing", "catalog", "service_missing", "/service", selected.ServiceID)
	}
	kind, err := serviceProjectKind(selected)
	if err != nil {
		return commandBuild{}, nil, err
	}
	module, err := loadNearestModuleFacts(repositoryRoot, discovered.Root())
	if err != nil {
		return commandBuild{}, nil, err
	}
	var sources []provenance.Source
	if selected.APIEntry != "" {
		sources, err = coreServiceSources(ctx, repositoryRoot, repository, project, catalog, discovered, selected, module)
	} else {
		sources, err = selectedServiceSources(ctx, repositoryRoot, repository, discovered, selected, module.source)
	}
	if err != nil {
		return commandBuild{}, nil, err
	}
	contractDigest, err := servicecontract.ComputeContractDigest(sources)
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	manifest, err := servicecontract.New(servicecontract.Spec{
		ServiceID: selected.ServiceID, ServiceKind: kind, ModulePath: module.modulePath, ContractSources: sources, ContractDigest: contractDigest,
	})
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	content, err := servicecontract.CanonicalJSON(manifest)
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	refs := make([]provenance.SourceRef, len(sources))
	for index, source := range sources {
		refs[index] = source.Ref
	}
	manifestPath := ".nexa/generation/service-manifest." + selected.ServiceID + ".manifest.json"
	artifactID := "service-manifest." + selected.ServiceID
	artifactPath := path.Join(discovered.Root(), "generated/service-manifest.json")
	previous, err := loadArtifactManifest(repository, manifestPath)
	if err != nil {
		return commandBuild{}, nil, err
	}
	inputs := []transaction.ArtifactInput{{
		ID: artifactID, Path: artifactPath,
		Owner: "nexa.dev/generator/service-manifest/v1", Digest: provenance.SHA256(content), Sources: refs, StalePolicy: artifact.StaleRetain,
		Probe: serviceManifestOwnershipProbe{serviceID: selected.ServiceID, artifactID: artifactID, path: artifactPath},
	}}
	plan, err := transaction.Build(ctx, repositoryRoot, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		if emitErr := emit(artifactPath, content); emitErr != nil {
			return transaction.PlanRequest{}, emitErr
		}
		return transaction.PlanRequest{
			Generator: artifact.GeneratorSpec{ID: "service-manifest", Version: capabilityVersion}, Sources: sources,
			Expected: inputs, Previous: previous, ManifestPath: manifestPath,
			RevalidateSources: func(revalidateCtx context.Context) ([]provenance.Source, error) {
				currentRoot, openErr := os.OpenRoot(repositoryRoot)
				if openErr != nil {
					return nil, openErr
				}
				defer currentRoot.Close()
				currentCatalog, loadErr := servicecatalog.Load(currentRoot, project.CatalogPath)
				if loadErr != nil {
					return nil, loadErr
				}
				currentDiscovered, ok := currentCatalog.Lookup(selected.ServiceID)
				if !ok {
					return nil, errors.New("service source is missing")
				}
				currentModule, moduleErr := loadNearestModuleFacts(repositoryRoot, currentDiscovered.Root())
				if moduleErr != nil {
					return nil, moduleErr
				}
				if selected.APIEntry != "" {
					return coreServiceSources(revalidateCtx, repositoryRoot, currentRoot, project, currentCatalog, currentDiscovered, selected, currentModule)
				}
				return selectedServiceSources(revalidateCtx, repositoryRoot, currentRoot, currentDiscovered, selected, currentModule.source)
			},
		}, nil
	})
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	return commandBuild{repositoryRoot: repositoryRoot, plan: plan}, nil, nil
}

type serviceManifestOwnershipProbe struct {
	serviceID, artifactID, path string
}

func (p serviceManifestOwnershipProbe) Inspect(name string, content []byte, expected transaction.Ownership) (bool, error) {
	if name != p.path || expected.GeneratorID != "service-manifest" || expected.ArtifactID != p.artifactID {
		return false, nil
	}
	manifest, err := servicecontract.Parse(name, content)
	if err != nil || manifest.ServiceID() != p.serviceID {
		return false, nil
	}
	inputDigest, err := artifact.ComputeInputDigest(
		artifact.GeneratorSpec{ID: "service-manifest", Version: capabilityVersion},
		manifest.ContractSources(),
	)
	return err == nil && inputDigest == expected.InputDigest, nil
}

func serviceProjectKind(selected ServiceProject) (string, error) {
	switch {
	case selected.APIEntry != "":
		return "core", nil
	case selected.ProtoEntry != "":
		return "rpc", nil
	case selected.EntSchemaDir != "":
		return "data", nil
	default:
		return "", inputError("provider_invalid", "provider", "service_kind_unresolved", "/project/services", selected.ServiceID)
	}
}

func selectedServiceSources(ctx context.Context, repositoryRoot string, repository *os.Root, discovered servicecatalog.Service, selected ServiceProject, moduleSource provenance.Source) ([]provenance.Source, error) {
	values := []provenance.Source{discovered.Source(), moduleSource}
	for _, binding := range discovered.CapabilityBindings() {
		values = append(values, binding.Source())
	}
	if selected.ProtoEntry != "" {
		document, err := genprotocol.Compile(ctx, genprotocol.CompileOptions{
			ServiceID: selected.ServiceID, EntryFiles: []string{selected.ProtoEntry}, Resolver: rootProtocolResolver{root: repository},
		})
		if err != nil {
			return nil, projectOwnerError(err)
		}
		values = append(values, document.Sources()...)
	}
	if selected.APIEntry != "" {
		document, err := httpapi.Load(ctx, httpapi.LoadOptions{RepositoryRoot: repositoryRoot, EntryFile: selected.APIEntry})
		if err != nil {
			return nil, projectOwnerError(err)
		}
		values = append(values, document.Sources()...)
	}
	return normalizeServiceSources(values)
}

func coreServiceSources(ctx context.Context, repositoryRoot string, repository *os.Root, project Project, catalog servicecatalog.Catalog, discovered servicecatalog.Service, selected ServiceProject, module nearestModuleFacts) ([]provenance.Source, error) {
	if project.CoreServiceID != selected.ServiceID {
		return nil, inputError("fact_source_invalid", "provider", "core_service_mismatch", "/project/coreServiceId", project.CoreServiceID)
	}
	protocols := make([]genprotocol.Document, 0, len(project.Services))
	var selectedProtocolSources []provenance.Source
	for _, serviceProject := range project.Services {
		if serviceProject.ProtoEntry == "" {
			continue
		}
		document, err := genprotocol.Compile(ctx, genprotocol.CompileOptions{
			ServiceID: serviceProject.ServiceID, EntryFiles: []string{serviceProject.ProtoEntry}, Resolver: rootProtocolResolver{root: repository},
		})
		if err != nil {
			return nil, projectOwnerError(err)
		}
		protocols = append(protocols, document)
		if serviceProject.ServiceID == selected.ServiceID {
			selectedProtocolSources = document.Sources()
		}
	}
	native, err := httpapi.Load(ctx, httpapi.LoadOptions{RepositoryRoot: repositoryRoot, EntryFile: selected.APIEntry})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	apiModule, repositoryCoreRoot, moduleCoreRoot, err := loadAPIModuleFacts(repositoryRoot, selected.APIEntry)
	if err != nil {
		return nil, err
	}
	if apiModule.modulePath != module.modulePath || apiModule.repositoryRoot != module.repositoryRoot || apiModule.source != module.source {
		return nil, inputError("fact_source_invalid", "module", "service_module_mismatch", "/project/services/apiEntry", selected.APIEntry)
	}
	composed, err := composition.Build(catalog, protocols, native, composition.BuildOptions{
		CoreServiceID: selected.ServiceID, ConsumerModulePath: module.modulePath,
	})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	generated, err := composition.GeneratedAPI(composed)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	merged, err := httpapi.Merge(native, generated)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	rendered, err := composition.Render(composed, composition.RenderOptions{CoreRoot: moduleCoreRoot})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	rendered, err = rebaseRenderedArtifacts(rendered, module, repositoryCoreRoot)
	if err != nil {
		return nil, err
	}
	values, err := apiOwnerSources(catalog, protocols, native, merged, rendered, module.source)
	if err != nil {
		return nil, err
	}
	values = append(values, selectedProtocolSources...)
	values = append(values, discovered.Source(), module.source)
	for _, binding := range discovered.CapabilityBindings() {
		values = append(values, binding.Source())
	}
	return normalizeServiceSources(values)
}

func normalizeServiceSources(values []provenance.Source) ([]provenance.Source, error) {
	byRef := make(map[string]provenance.Source, len(values))
	for _, source := range values {
		key := source.Ref.String()
		if current, exists := byRef[key]; exists && current.Digest != source.Digest {
			return nil, inputError("fact_source_invalid", "provenance", "source_digest_conflict", "/sources", key)
		}
		byRef[key] = source
	}
	keys := make([]string, 0, len(byRef))
	for key := range byRef {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]provenance.Source, len(keys))
	for index, key := range keys {
		result[index] = byRef[key]
	}
	return result, nil
}

func (r *commandRunner) resolveCoreService(ctx context.Context, invocation plugin.Invocation) (string, Project, ServiceProject, error) {
	repositoryRoot, err := canonicalRepositoryRoot(invocation.Flags["repo-root"].(string))
	if err != nil {
		return "", Project{}, ServiceProject{}, err
	}
	provider, ok := r.providers[invocation.Flags["provider"].(string)]
	if !ok {
		return "", Project{}, ServiceProject{}, unavailableProviderError()
	}
	project, err := provider.Resolve(ctx, repositoryRoot)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", Project{}, ServiceProject{}, projectOwnerError(err)
		}
		return "", Project{}, ServiceProject{}, inputError("provider_invalid", "provider", "provider_resolution_failed", "/provider", "")
	}
	selectedID := invocation.Flags["core-service"].(string)
	if project.CoreServiceID == "" {
		return "", Project{}, ServiceProject{}, inputError("fact_source_missing", "provider", "core_service_missing", "/project/coreServiceId", "")
	}
	if project.CoreServiceID != selectedID {
		return "", Project{}, ServiceProject{}, inputError("fact_source_invalid", "provider", "core_service_mismatch", "/core-service", "")
	}
	selected, err := selectService(project, selectedID)
	if err != nil {
		return "", Project{}, ServiceProject{}, err
	}
	if selected.APIEntry == "" {
		return "", Project{}, ServiceProject{}, inputError("fact_source_missing", "provider", "api_entry_missing", "/project/services/apiEntry", "")
	}
	return repositoryRoot, project, selected, nil
}

func loadAPIModuleFacts(repositoryRoot, apiEntry string) (nearestModuleFacts, string, string, error) {
	entryDir := path.Dir(apiEntry)
	if entryDir == "." {
		return nearestModuleFacts{}, "", "", inputError("fact_source_invalid", "provider", "api_entry_invalid", "/project/services/apiEntry", apiEntry)
	}
	module, err := loadNearestModuleFacts(repositoryRoot, entryDir)
	if err != nil {
		return nearestModuleFacts{}, "", "", err
	}
	repositoryCoreRoot := path.Dir(apiEntry)
	if path.Base(repositoryCoreRoot) == "desc" {
		repositoryCoreRoot = path.Dir(repositoryCoreRoot)
	}
	if repositoryCoreRoot == "." {
		return nearestModuleFacts{}, "", "", inputError("fact_source_invalid", "provider", "api_entry_invalid", "/project/services/apiEntry", apiEntry)
	}
	moduleCoreRoot := repositoryCoreRoot
	if module.repositoryRoot != "." {
		prefix := module.repositoryRoot + "/"
		if !strings.HasPrefix(repositoryCoreRoot, prefix) {
			return nearestModuleFacts{}, "", "", inputError("fact_source_invalid", "module", "api_entry_outside_module", "/project/services/apiEntry", apiEntry)
		}
		moduleCoreRoot = strings.TrimPrefix(repositoryCoreRoot, prefix)
	}
	return module, repositoryCoreRoot, moduleCoreRoot, nil
}

func loadNearestModulePath(repositoryRoot, sourceDir string) (string, error) {
	facts, err := loadNearestModuleFacts(repositoryRoot, sourceDir)
	return facts.modulePath, err
}

type nearestModuleFacts struct {
	modulePath     string
	repositoryRoot string
	source         provenance.Source
}

func loadNearestModuleFacts(repositoryRoot, sourceDir string) (nearestModuleFacts, error) {
	domain, err := provenance.ParseDomainSource(sourceDir)
	if err != nil {
		return nearestModuleFacts{}, inputError("fact_source_invalid", "provider", "service_root_invalid", "/project/services", sourceDir)
	}
	location, err := toolchain.LocateModule(toolchain.ModuleLocateSpec{RepositoryRoot: repositoryRoot, SchemaDir: domain})
	if err != nil {
		return nearestModuleFacts{}, projectOwnerError(err)
	}
	moduleDir, err := location.ModuleDir()
	if err != nil {
		return nearestModuleFacts{}, projectOwnerError(err)
	}
	data, err := os.ReadFile(filepath.Join(moduleDir, "go.mod"))
	if err != nil {
		return nearestModuleFacts{}, inputError("fact_source_missing", "module", "module_file_missing", "", "go.mod")
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path == "" {
		return nearestModuleFacts{}, inputError("fact_source_invalid", "module", "module_path_invalid", "/module", "go.mod")
	}
	relativeDir, err := filepath.Rel(repositoryRoot, moduleDir)
	if err != nil || relativeDir == ".." || strings.HasPrefix(relativeDir, ".."+string(filepath.Separator)) {
		return nearestModuleFacts{}, inputError("fact_source_invalid", "module", "module_path_invalid", "/module", "go.mod")
	}
	goModPath := path.Join(filepath.ToSlash(relativeDir), "go.mod")
	ref, err := provenance.RepositoryRef(goModPath, "")
	if err != nil {
		return nearestModuleFacts{}, inputError("fact_source_invalid", "module", "module_path_invalid", "/module", goModPath)
	}
	return nearestModuleFacts{
		modulePath: parsed.Module.Mod.Path, repositoryRoot: filepath.ToSlash(relativeDir),
		source: provenance.Source{Ref: ref, Digest: provenance.SHA256(data)},
	}, nil
}

func rebaseRenderedArtifacts(values []composition.RenderedArtifact, module nearestModuleFacts, repositoryCoreRoot string) ([]composition.RenderedArtifact, error) {
	result := make([]composition.RenderedArtifact, len(values))
	for index, value := range values {
		item := composition.RenderedArtifact{
			ID: value.ID, Path: value.Path, Owner: value.Owner,
			Content: append([]byte(nil), value.Content...), Sources: append([]provenance.SourceRef(nil), value.Sources...),
		}
		if module.repositoryRoot != "." {
			item.Path = path.Join(module.repositoryRoot, item.Path)
		}
		if !strings.HasPrefix(item.Path, repositoryCoreRoot+"/") {
			return nil, inputError("fact_source_invalid", "composition", "artifact_outside_core", "/artifacts", item.Path)
		}
		if path.Ext(item.Path) == ".go" {
			usesModule, err := goArtifactUsesModule(item.Content, module.modulePath)
			if err != nil {
				return nil, inputError("fact_source_invalid", "composition", "artifact_go_invalid", "/artifacts", item.Path)
			}
			if usesModule {
				item.Sources = append(item.Sources, module.source.Ref)
				sort.Slice(item.Sources, func(left, right int) bool { return item.Sources[left].String() < item.Sources[right].String() })
			}
		}
		result[index] = item
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Path < result[right].Path })
	return result, nil
}

func goArtifactUsesModule(content []byte, modulePath string) (bool, error) {
	file, err := goparser.ParseFile(token.NewFileSet(), "artifact.go", content, goparser.ImportsOnly)
	if err != nil {
		return false, err
	}
	for _, imported := range file.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return false, err
		}
		if value == modulePath || strings.HasPrefix(value, modulePath+"/") {
			return true, nil
		}
	}
	return false, nil
}

func apiOwnerSources(catalog servicecatalog.Catalog, protocols []genprotocol.Document, native, merged httpapi.Document, rendered []composition.RenderedArtifact, moduleSource provenance.Source) ([]provenance.Source, error) {
	available := make(map[string]provenance.Source)
	available[moduleSource.Ref.String()] = moduleSource
	for _, source := range catalog.Sources() {
		available[source.Ref.String()] = source
	}
	for _, document := range protocols {
		for _, source := range document.Sources() {
			available[source.Ref.String()] = source
		}
	}
	for _, source := range native.Sources() {
		available[source.Ref.String()] = source
	}
	wanted := make(map[string]struct{})
	for _, source := range merged.Sources() {
		wanted[source.Ref.String()] = struct{}{}
		available[source.Ref.String()] = source
	}
	for _, item := range rendered {
		for _, ref := range item.Sources {
			wanted[ref.String()] = struct{}{}
		}
	}
	keys := make([]string, 0, len(wanted))
	for key := range wanted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]provenance.Source, len(keys))
	for index, key := range keys {
		source, ok := available[key]
		if !ok {
			return nil, inputError("fact_source_missing", "provenance", "source_ref_unresolved", "/sources", key)
		}
		result[index] = source
	}
	return result, nil
}

type rootProtocolResolver struct{ root *os.Root }

func (r rootProtocolResolver) Open(ctx context.Context, source string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.root.Open(source)
}

func ownershipProbes(inputs []transaction.ArtifactInput) []transaction.OwnershipProbe {
	result := make([]transaction.OwnershipProbe, 0, len(inputs))
	for _, input := range inputs {
		if input.Probe != nil {
			result = append(result, input.Probe)
		}
	}
	return result
}

func loadArtifactManifest(root *os.Root, source string) (*artifact.Manifest, error) {
	data, exists, err := readOptionalRegular(root, source)
	if err != nil || !exists {
		return nil, err
	}
	manifest, err := artifact.Parse(source, data)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	return &manifest, nil
}

func (r *commandRunner) plan(ctx context.Context, invocation plugin.Invocation) (any, error) {
	built, cleanup, err := r.build(ctx, invocation)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	defer built.plan.Close()
	return jsonDocument(built.plan.CanonicalJSON()), nil
}

func (r *commandRunner) check(ctx context.Context, invocation plugin.Invocation) (any, error) {
	built, cleanup, err := r.build(ctx, invocation)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	defer built.plan.Close()
	repository, err := os.OpenRoot(built.repositoryRoot)
	if err != nil {
		return nil, inputError("repository_invalid", "repository", "repository_open_failed", "/repo-root", "")
	}
	defer repository.Close()
	result, err := transaction.Check(built.plan, repository)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	return jsonDocument(result.CanonicalJSON()), nil
}

func (r *commandRunner) write(ctx context.Context, invocation plugin.Invocation) (any, error) {
	built, cleanup, err := r.build(ctx, invocation)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return nil, err
	}
	defer built.plan.Close()
	if built.lockChanged {
		accepted, parseErr := provenance.ParseDigest(invocation.Flags["lock-digest"].(string))
		if parseErr != nil || accepted != built.lockDigest {
			return nil, driftError("lock_digest_mismatch", "write", "lock_digest_mismatch", "/lock-digest", "")
		}
	}
	accepted, parseErr := provenance.ParseDigest(invocation.Flags["plan-digest"].(string))
	if parseErr != nil {
		return nil, driftError("transaction_write_failed", "write", "plan_digest_mismatch", "/plan-digest", "")
	}
	result, err := transaction.Write(ctx, built.plan, built.repositoryRoot, transaction.WriteOptions{PlanDigest: accepted})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	return jsonDocument(result.CanonicalJSON()), nil
}

func (r *commandRunner) build(ctx context.Context, invocation plugin.Invocation) (commandBuild, func(), error) {
	repositoryPath, service, err := r.resolveService(ctx, invocation)
	if err != nil {
		return commandBuild{}, nil, err
	}
	if err := r.requireProviderTool(invocation.Flags["provider"].(string), ToolRoleEntCRUD, service.EntCRUDTool, "/project/services/entCRUDTool"); err != nil {
		return commandBuild{}, nil, err
	}
	schema, err := provenance.ParseDomainSource(service.EntSchemaDir)
	if err != nil {
		return commandBuild{}, nil, inputError("fact_source_invalid", "provider", "schema_source_invalid", "/project/services/entSchemaDir", "")
	}
	destination, err := crudproto.ProjectProtoDestination(service.ServiceID, service.ProtoEntry)
	if err != nil {
		return commandBuild{}, nil, projectOwnerError(err)
	}
	repository, err := os.OpenRoot(repositoryPath)
	if err != nil {
		return commandBuild{}, nil, inputError("repository_invalid", "repository", "repository_open_failed", "/repo-root", "")
	}
	defer repository.Close()
	packageFacts, err := loadProtoPackageFacts(repository, service.ProtoEntry)
	if err != nil {
		return commandBuild{}, nil, err
	}
	existingLock, err := loadExistingLock(repository, destination)
	if err != nil {
		return commandBuild{}, nil, err
	}
	previous, published, err := loadPreviousManifest(repository, destination)
	if err != nil {
		return commandBuild{}, nil, err
	}
	invocationRoot, toolStagingRoot, scratchParent, environment, err := r.invocationEnvironment(service.EntCRUDTool)
	if err != nil {
		return commandBuild{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(invocationRoot) }
	var request transaction.PlanRequest
	var hostPlan crudproto.EntGraphPlan
	plan, err := transaction.Build(ctx, repositoryPath, func(_ string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
		var hostErr error
		hostPlan, hostErr = crudproto.InvokeEntGraphHost(ctx, crudproto.EntGraphHostSpec{
			RepositoryRoot: repositoryPath, StagingRoot: toolStagingRoot, ScratchParent: scratchParent,
			SchemaDir: schema, BuildTags: nil, ProtoPackage: packageFacts.protoPackage, GoPackage: packageFacts.goPackage,
			Destination: destination, Tool: service.EntCRUDTool, Environment: environment, Runner: r.runner,
			ExistingLock: existingLock, PublishedArtifact: published,
			MultiTenant: crudproto.MultiTenantConfig{Enabled: service.MultiTenant.Enabled},
		})
		if hostErr != nil {
			return transaction.PlanRequest{}, projectOwnerError(hostErr)
		}
		entitySnapshot, snapshotErr := hostPlan.EntitySnapshot()
		if snapshotErr != nil {
			return transaction.PlanRequest{}, projectOwnerError(snapshotErr)
		}
		hasTenantMixin := false
		for _, item := range entitySnapshot.Entities() {
			for _, field := range item.Fields() {
				if field.IsTenantField() {
					hasTenantMixin = true
					break
				}
			}
		}
		if hostErr = validateCRUDMultiTenant(service, hasTenantMixin); hostErr != nil {
			return transaction.PlanRequest{}, hostErr
		}
		request, hostErr = projectTransactionRequest(hostPlan, previous, destination.ManifestPath(), emit)
		if hostErr != nil {
			return transaction.PlanRequest{}, projectOwnerError(hostErr)
		}
		moduleGraphDigest, digestErr := hostPlan.ModuleGraphDigest()
		if digestErr != nil {
			return transaction.PlanRequest{}, projectOwnerError(digestErr)
		}
		buildInputDigest, digestErr := hostPlan.BuildInputDigest()
		if digestErr != nil {
			return transaction.PlanRequest{}, projectOwnerError(digestErr)
		}
		projectedSources := entitySnapshot.ProjectedSources()
		request.RevalidateSources = func(revalidateCtx context.Context) ([]provenance.Source, error) {
			inspection, inspectErr := toolchain.InspectEntityInputs(revalidateCtx, toolchain.EntityInputInspectionSpec{
				RepositoryRoot: repositoryPath, SchemaDir: schema, BuildTags: nil,
				ExpectedModuleGraphDigest: toolchain.OptionalDigest{Value: moduleGraphDigest, Present: true},
				ExpectedBuildInputDigest:  toolchain.OptionalDigest{Value: buildInputDigest, Present: true},
			})
			if inspectErr != nil {
				return nil, inspectErr
			}
			current, inspectErr := inspection.ModuleSources()
			if inspectErr != nil {
				return nil, inspectErr
			}
			current = append(current, projectedSources...)
			currentRoot, openErr := os.OpenRoot(repositoryPath)
			if openErr != nil {
				return nil, openErr
			}
			defer currentRoot.Close()
			currentLock, lockErr := loadExistingLock(currentRoot, destination)
			if lockErr != nil {
				return nil, lockErr
			}
			if currentLock != nil {
				current = append(current, currentLock.Source)
			}
			_, currentPublished, manifestErr := loadPreviousManifest(currentRoot, destination)
			if manifestErr != nil {
				return nil, manifestErr
			}
			if currentPublished != nil {
				current = append(current, currentPublished.ManifestSource)
			}
			return current, nil
		}
		return request, nil
	})
	if err != nil {
		return commandBuild{}, cleanup, projectBuildError(err)
	}
	built, err := finalizeCRUDCommandBuild(repositoryPath, plan, hostPlan, len(request.ControlSources) != 0)
	if err != nil {
		return commandBuild{}, cleanup, err
	}
	return built, cleanup, nil
}

func projectBuildError(err error) error {
	if err == nil {
		return err
	}
	var generationErr *cliprotocol.Error
	if errors.As(err, &generationErr) && cliprotocol.Project(generationErr).Domain == errorDomain {
		return &safeCauseError{projected: generationErr, cause: err}
	}
	return projectOwnerError(err)
}

func validateCRUDMultiTenant(service ServiceProject, hasTenantMixin bool) error {
	if hasTenantMixin && !service.MultiTenant.Enabled {
		return inputError("fact_source_invalid", "provider", "multi_tenant_disabled", "/project/services/multiTenant/enabled", "")
	}
	return nil
}

func finalizeCRUDCommandBuild(repositoryPath string, plan transaction.Plan, hostPlan crudproto.EntGraphPlan, lockChanged bool) (commandBuild, error) {
	built := commandBuild{repositoryRoot: repositoryPath, plan: plan, lockChanged: lockChanged}
	if built.lockChanged {
		proposal, proposalErr := hostPlan.LockProposal()
		if proposalErr != nil {
			return commandBuild{}, projectOwnerError(errors.Join(proposalErr, plan.Close()))
		}
		built.lockDigest, proposalErr = proposal.Digest()
		if proposalErr != nil {
			return commandBuild{}, projectOwnerError(errors.Join(proposalErr, plan.Close()))
		}
	}
	return built, nil
}

func (r *commandRunner) resolveService(ctx context.Context, invocation plugin.Invocation) (string, ServiceProject, error) {
	repositoryPath, _, service, err := r.resolveProjectService(ctx, invocation)
	return repositoryPath, service, err
}

func (r *commandRunner) resolveProjectService(ctx context.Context, invocation plugin.Invocation) (string, Project, ServiceProject, error) {
	repositoryPath, err := canonicalRepositoryRoot(invocation.Flags["repo-root"].(string))
	if err != nil {
		return "", Project{}, ServiceProject{}, err
	}
	providerID := invocation.Flags["provider"].(string)
	provider, ok := r.providers[providerID]
	if !ok {
		return "", Project{}, ServiceProject{}, unavailableProviderError()
	}
	project, err := provider.Resolve(ctx, repositoryPath)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", Project{}, ServiceProject{}, projectOwnerError(err)
		}
		return "", Project{}, ServiceProject{}, inputError("provider_invalid", "provider", "provider_resolution_failed", "/provider", "")
	}
	service, err := selectService(project, invocation.Flags["service"].(string))
	if err != nil {
		return "", Project{}, ServiceProject{}, err
	}
	return repositoryPath, project, service, nil
}

type transactionProjection interface {
	TransactionInputs(func(string, []byte) error) ([]transaction.ArtifactInput, []transaction.ControlSourceMutation, error)
	StaleOwnershipProbes() ([]transaction.OwnershipProbe, error)
	Sources() ([]provenance.Source, error)
}

func projectTransactionRequest(projection transactionProjection, previous *artifact.Manifest, manifestPath string, emit func(string, []byte) error) (transaction.PlanRequest, error) {
	expected, controls, err := projection.TransactionInputs(emit)
	if err != nil {
		return transaction.PlanRequest{}, err
	}
	staleProbes, err := projection.StaleOwnershipProbes()
	if err != nil {
		return transaction.PlanRequest{}, err
	}
	sources, err := projection.Sources()
	if err != nil {
		return transaction.PlanRequest{}, err
	}
	return transaction.PlanRequest{
		Generator:            artifact.GeneratorSpec{ID: "crud-proto", Version: capabilityVersion},
		Sources:              sources,
		Expected:             expected,
		ControlSources:       controls,
		StaleOwnershipProbes: staleProbes,
		Previous:             previous,
		ManifestPath:         manifestPath,
	}, nil
}

func canonicalRepositoryRoot(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", inputError("repository_invalid", "repository", "repository_path_invalid", "/repo-root", "")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(absolute))
	if err != nil {
		return "", inputError("repository_invalid", "repository", "repository_path_invalid", "/repo-root", "")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", inputError("repository_invalid", "repository", "repository_path_invalid", "/repo-root", "")
	}
	return canonical, nil
}

func selectService(project Project, selected string) (ServiceProject, error) {
	seen := make(map[string]struct{}, len(project.Services))
	var result ServiceProject
	found := false
	for index, service := range project.Services {
		if !providerIDPattern.MatchString(service.ServiceID) {
			return ServiceProject{}, inputError("fact_source_invalid", "provider", "service_id_invalid", indexedPointer("/project/services", index)+"/serviceId", "")
		}
		if _, duplicate := seen[service.ServiceID]; duplicate {
			return ServiceProject{}, inputError("fact_source_invalid", "provider", "service_duplicate", indexedPointer("/project/services", index)+"/serviceId", "")
		}
		seen[service.ServiceID] = struct{}{}
		if service.ServiceID == selected {
			result = service
			found = true
		}
	}
	if !found {
		return ServiceProject{}, inputError("fact_source_missing", "provider", "service_missing", "/service", "")
	}
	return result, nil
}

type protoPackageFacts struct {
	protoPackage string
	goPackage    string
}

func loadProtoPackageFacts(root *os.Root, source string) (protoPackageFacts, error) {
	data, exists, err := readOptionalRegular(root, source)
	if err != nil {
		return protoPackageFacts{}, err
	}
	if !exists {
		return protoPackageFacts{}, inputError("fact_source_missing", "proto", "proto_entry_missing", "/project/services/protoEntry", source)
	}
	handler := reporter.NewHandler(nil)
	file, err := parser.Parse(source, bytes.NewReader(data), handler)
	if err != nil {
		return protoPackageFacts{}, inputError("fact_source_invalid", "proto", "proto_entry_invalid", "/project/services/protoEntry", source)
	}
	parsed, err := parser.ResultFromAST(file, true, reporter.NewHandler(nil))
	if err != nil {
		return protoPackageFacts{}, inputError("fact_source_invalid", "proto", "proto_entry_invalid", "/project/services/protoEntry", source)
	}
	if _, err := protooptions.InterpretUnlinkedOptions(parsed); err != nil {
		return protoPackageFacts{}, inputError("fact_source_invalid", "proto", "proto_entry_invalid", "/project/services/protoEntry/options", source)
	}
	descriptor := parsed.FileDescriptorProto()
	if descriptor.GetPackage() == "" {
		return protoPackageFacts{}, inputError("fact_source_missing", "proto", "proto_package_missing", "/project/services/protoEntry/package", source)
	}
	if descriptor.GetOptions().GetGoPackage() == "" {
		return protoPackageFacts{}, inputError("fact_source_missing", "proto", "proto_go_package_missing", "/project/services/protoEntry/options/go_package", source)
	}
	return protoPackageFacts{protoPackage: descriptor.GetPackage(), goPackage: descriptor.GetOptions().GetGoPackage()}, nil
}

func (r *commandRunner) invocationEnvironment(tool toolchain.Tool) (string, string, string, []toolchain.EnvVar, error) {
	root, err := os.MkdirTemp("", "nexactl-generation-")
	if err != nil {
		return "", "", "", nil, inputError("execution_environment_invalid", "environment", "invocation_root_failed", "", "")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		_ = os.RemoveAll(root)
		return "", "", "", nil, inputError("execution_environment_invalid", "environment", "invocation_root_failed", "", "")
	}
	staging := filepath.Join(canonical, "staging")
	scratch := filepath.Join(canonical, "scratch")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		_ = os.RemoveAll(canonical)
		return "", "", "", nil, inputError("execution_environment_invalid", "environment", "invocation_root_failed", "", "")
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		_ = os.RemoveAll(canonical)
		return "", "", "", nil, inputError("execution_environment_invalid", "environment", "invocation_root_failed", "", "")
	}
	host := make(map[string]string, len(r.hostEnvironment))
	for _, value := range r.hostEnvironment {
		if _, duplicate := host[value.Name]; duplicate {
			_ = os.RemoveAll(canonical)
			return "", "", "", nil, inputError("execution_environment_invalid", "environment", "host_environment_duplicate", "/hostEnvironment", "")
		}
		host[value.Name] = value.Value
	}
	environment := make([]toolchain.EnvVar, 0, len(tool.Environment))
	for index, rule := range tool.Environment {
		value := rule.FixedValue
		switch rule.Source {
		case toolchain.EnvironmentHost:
			value = host[rule.Name]
			if value == "" {
				_ = os.RemoveAll(canonical)
				return "", "", "", nil, inputError("execution_environment_invalid", "environment", "host_environment_missing", indexedPointer("/tool/environment", index), "")
			}
		case toolchain.EnvironmentScratch:
			value = filepath.Join(staging, strings.ToLower(rule.Name))
			if err := os.MkdirAll(value, 0o700); err != nil {
				_ = os.RemoveAll(canonical)
				return "", "", "", nil, inputError("execution_environment_invalid", "environment", "scratch_environment_failed", indexedPointer("/tool/environment", index), "")
			}
		case toolchain.EnvironmentFixed:
		default:
			_ = os.RemoveAll(canonical)
			return "", "", "", nil, inputError("execution_environment_invalid", "environment", "environment_source_invalid", indexedPointer("/tool/environment", index), "")
		}
		environment = append(environment, toolchain.EnvVar{Name: rule.Name, Value: value})
	}
	return canonical, staging, scratch, environment, nil
}

func loadExistingLock(root *os.Root, destination crudproto.ProtoDestination) (*crudproto.ExistingLockInput, error) {
	data, exists, err := readOptionalRegular(root, destination.LockPath())
	if err != nil || !exists {
		return nil, err
	}
	source, parseErr := provenance.ParseDomainSource(destination.LockPath())
	if parseErr != nil {
		return nil, inputError("fact_source_invalid", "lock", "lock_source_invalid", "", destination.LockPath())
	}
	lock, parseErr := crudproto.ParseLock(source, data)
	if parseErr != nil {
		return nil, projectOwnerError(parseErr)
	}
	ref, _ := provenance.RepositoryRef(destination.LockPath(), "")
	return &crudproto.ExistingLockInput{Lock: lock, Source: provenance.Source{Ref: ref, Digest: provenance.SHA256(data)}}, nil
}

func loadPreviousManifest(root *os.Root, destination crudproto.ProtoDestination) (*artifact.Manifest, *crudproto.PublishedArtifactInput, error) {
	data, exists, err := readOptionalRegular(root, destination.ManifestPath())
	if err != nil || !exists {
		return nil, nil, err
	}
	manifest, parseErr := artifact.Parse(destination.ManifestPath(), data)
	if parseErr != nil {
		return nil, nil, projectOwnerError(parseErr)
	}
	ref, _ := provenance.RepositoryRef(destination.ManifestPath(), "")
	source := provenance.Source{Ref: ref, Digest: provenance.SHA256(data)}
	var published *crudproto.PublishedArtifactInput
	for _, item := range manifest.Artifacts() {
		if item.ID() == destination.ArtifactID() {
			published = &crudproto.PublishedArtifactInput{ID: item.ID(), Digest: item.Digest(), ManifestSource: source}
			break
		}
	}
	return &manifest, published, nil
}

func readOptionalRegular(root *os.Root, path string) ([]byte, bool, error) {
	return readOptionalRegularFrom(root, path)
}

type rootedRegularReader interface {
	Lstat(string) (os.FileInfo, error)
	Open(string) (*os.File, error)
}

func readOptionalRegularFrom(root rootedRegularReader, path string) ([]byte, bool, error) {
	before, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil || !safeControlFile(before) {
		return nil, false, inputError("fact_source_invalid", "repository", "control_source_unsafe", "", path)
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, false, inputError("fact_source_invalid", "repository", "control_source_read_failed", "", path)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !safeControlFile(opened) || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, false, inputError("fact_source_invalid", "repository", "control_source_changed", "", path)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxControlFileBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > maxControlFileBytes {
		return nil, false, inputError("fact_source_invalid", "repository", "control_source_read_failed", "", path)
	}
	after, err := root.Lstat(path)
	if err != nil || !safeControlFile(after) || !os.SameFile(opened, after) || before.Size() != opened.Size() || opened.Size() != after.Size() || int64(len(data)) != opened.Size() {
		return nil, false, inputError("fact_source_invalid", "repository", "control_source_changed", "", path)
	}
	return data, true, nil
}

func safeControlFile(info os.FileInfo) bool {
	return info != nil && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && info.Size() >= 0 && info.Size() <= maxControlFileBytes
}

func nilProvider(value ProjectProvider) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func validProviderVersion(value string) bool {
	if !semver.IsValid(value) {
		return false
	}
	core := strings.TrimPrefix(value, "v")
	if separator := strings.IndexAny(core, "-+"); separator >= 0 {
		core = core[:separator]
	}
	return len(strings.Split(core, ".")) == 3
}

func indexedPointer(base string, index int) string {
	return base + "/" + strconv.Itoa(index)
}

func jsonDocument(value []byte) any {
	return jsonRawMessage(append([]byte(nil), value...))
}

type jsonRawMessage []byte

func (m jsonRawMessage) MarshalJSON() ([]byte, error) {
	return append([]byte(nil), m...), nil
}
