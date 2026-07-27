package generation

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"

	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/replacetree"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"golang.org/x/mod/semver"
)

var (
	providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[-.][a-z0-9]+)*$`)
	serviceIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

type generationResult struct {
	APIVersion     string            `json:"apiVersion"`
	Kind           string            `json:"kind"`
	Status         string            `json:"status"`
	Service        string            `json:"service"`
	GeneratedScope string            `json:"generatedScope"`
	UserLogic      []userLogicResult `json:"userLogic"`
}

type userLogicResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

func newCommandRunner(options Options) (*commandRunner, error) {
	providers := make(map[string]ProjectProvider, len(options.Providers))
	providerTools := make(map[string]map[ToolRole]map[string]string, len(options.Providers))
	tools := map[ToolRole][]plugin.DelegatedToolSpec{ToolRoleRPCGo: {}, ToolRoleAPIGo: {}}
	for index, candidate := range options.Providers {
		pointer := "/providers/" + strconv.Itoa(index)
		if nilProvider(candidate) {
			return nil, inputError("provider_invalid", "provider", "provider_nil", pointer, "")
		}
		descriptor := candidate.Descriptor()
		if !providerIDPattern.MatchString(descriptor.ID) {
			return nil, inputError("provider_invalid", "provider", "provider_id_invalid", pointer+"/id", "")
		}
		if !semver.IsValid(descriptor.Version) || semver.Canonical(descriptor.Version) != descriptor.Version {
			return nil, inputError("provider_invalid", "provider", "provider_version_invalid", pointer+"/version", "")
		}
		if _, duplicate := providers[descriptor.ID]; duplicate {
			return nil, inputError("provider_invalid", "provider", "provider_duplicate", pointer+"/id", "")
		}
		roles := make(map[ToolRole]map[string]string)
		for toolIndex, declared := range descriptor.Tools {
			toolPointer := pointer + "/tools/" + strconv.Itoa(toolIndex)
			if declared.Role != ToolRoleRPCGo && declared.Role != ToolRoleAPIGo {
				return nil, inputError("provider_invalid", "provider", "provider_tool_role_invalid", toolPointer+"/role", "")
			}
			if roles[declared.Role] == nil {
				roles[declared.Role] = make(map[string]string)
			}
			if _, duplicate := roles[declared.Role][declared.Tool.ID]; duplicate {
				return nil, inputError("provider_invalid", "provider", "provider_tool_duplicate", toolPointer+"/tool/id", "")
			}
			roles[declared.Role][declared.Tool.ID] = declared.Tool.Version
			tools[declared.Role] = append(tools[declared.Role], cloneDelegatedTools([]plugin.DelegatedToolSpec{declared.Tool})[0])
		}
		providers[descriptor.ID] = candidate
		providerTools[descriptor.ID] = roles
	}
	for role := range tools {
		sort.Slice(tools[role], func(i, j int) bool {
			if tools[role][i].ID != tools[role][j].ID {
				return tools[role][i].ID < tools[role][j].ID
			}
			return tools[role][i].Version < tools[role][j].Version
		})
	}
	runner := options.Runner
	if runner == nil {
		runner = toolchain.NewExecRunner()
	}
	return &commandRunner{
		providers: providers, providerTools: providerTools, runner: runner,
		hostEnvironment: append([]toolchain.EnvVar(nil), options.Environment...), delegatedTools: tools,
	}, nil
}

func nilProvider(value ProjectProvider) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

func (r *commandRunner) generateRPC(ctx context.Context, invocation plugin.Invocation) (any, error) {
	repository, providerID, service, err := r.resolve(ctx, invocation)
	if err != nil {
		return nil, err
	}
	if service.RPC == nil || service.RPC.Facts.ServiceID() != service.ServiceID {
		return nil, inputError("fact_source_invalid", "provider", "rpc_facts_invalid", "/project/services/rpc", "")
	}
	stdin, err := genprotocol.CanonicalJSON(service.RPC.Facts)
	if err != nil || len(stdin) > toolchain.MaxStdinBytes {
		return nil, inputError("fact_source_invalid", "provider", "rpc_facts_invalid", "/project/services/rpc/facts", "")
	}
	return r.generate(ctx, repository, providerID, service.ServiceID, ToolRoleRPCGo, service.RPC.Tool, service.RPC.GeneratedScope, service.RPC.ExtensionScopes, service.RPC.UserLogic, stdin, invocation)
}

func (r *commandRunner) generateAPI(ctx context.Context, invocation plugin.Invocation) (any, error) {
	repository, providerID, service, err := r.resolve(ctx, invocation)
	if err != nil {
		return nil, err
	}
	if service.API == nil || service.API.Facts.APIVersion() != httpapi.APIVersion {
		return nil, inputError("fact_source_invalid", "provider", "api_facts_invalid", "/project/services/api", "")
	}
	stdin, err := httpapi.CanonicalJSON(service.API.Facts)
	if err != nil || len(stdin) > toolchain.MaxStdinBytes {
		return nil, inputError("fact_source_invalid", "provider", "api_facts_invalid", "/project/services/api/facts", "")
	}
	return r.generate(ctx, repository, providerID, service.ServiceID, ToolRoleAPIGo, service.API.Tool, service.API.GeneratedScope, service.API.ExtensionScopes, service.API.UserLogic, stdin, invocation)
}

func (r *commandRunner) generate(ctx context.Context, repository, providerID, serviceID string, role ToolRole, tool toolchain.Tool, generated string, extensions []string, userLogic []UserLogicFile, stdin []byte, invocation plugin.Invocation) (any, error) {
	if err := r.requireProviderTool(providerID, role, tool); err != nil {
		return nil, err
	}
	if len(tool.InputScopes) != 1 || tool.InputScopes[0] != "repository" || len(tool.WriteScopes) != 1 || tool.WriteScopes[0] != "repository" || tool.ID == "" || tool.Version == "" || tool.Executable == "" || tool.Probe.ExpectedVersion == "" {
		return nil, inputError("provider_invalid", "provider", "direct_tool_invalid", "/project/services/tool", tool.ID)
	}
	environment, err := r.environment(tool)
	if err != nil {
		return nil, err
	}
	logicTargets := make([]replacetree.UserLogicFile, len(userLogic))
	for index, value := range userLogic {
		logicTargets[index] = replacetree.UserLogicFile{Path: value.Path, Content: append([]byte(nil), value.Content...)}
	}
	prepared, err := replacetree.Prepare(repository, generated, extensions, logicTargets)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	result, err := toolchain.RunDirect(ctx, r.runner, toolchain.Request{
		RepositoryRoot: repository, StagingRoot: repository, WorkDir: repository,
		Tool: tool, Args: []string{"generate", "--service", serviceID, "--generated-scope", prepared.GeneratedScope()}, Environment: environment, Stdin: append([]byte(nil), stdin...),
	})
	if err != nil {
		return nil, projectOwnerError(err)
	}
	if result.ToolID != tool.ID || result.Version != tool.Version || result.ExecutableVersion != tool.Probe.ExpectedVersion {
		return nil, inputError("tool_result_invalid", "result", "tool_identity_mismatch", "/result", tool.ID)
	}
	if result.ExitCode != 0 {
		return nil, delegatedToolFailure(tool.ID, result.ExitCode, result.Diagnostic)
	}
	overwrite, ok := invocation.Flags["overwrite-logic"].(bool)
	if !ok {
		return nil, inputError("request_invalid", "input", "overwrite_logic_invalid", "/flags/overwrite-logic", "")
	}
	logic, err := prepared.WriteUserLogic(overwrite)
	if err != nil {
		return nil, projectOwnerError(err)
	}
	resultLogic := make([]userLogicResult, len(logic))
	for index, value := range logic {
		resultLogic[index] = userLogicResult{Path: value.Path, Action: string(value.Action)}
	}
	return generationResult{APIVersion: "nexa.dev/generation-result/v2", Kind: "GenerationResult", Status: "generated", Service: serviceID, GeneratedScope: prepared.GeneratedScope(), UserLogic: resultLogic}, nil
}

func (r *commandRunner) resolve(ctx context.Context, invocation plugin.Invocation) (string, string, ServiceProject, error) {
	if ctx == nil {
		return "", "", ServiceProject{}, inputError("request_invalid", "input", "context_nil", "/context", "")
	}
	repositoryValue, repoOK := invocation.Flags["repo-root"].(string)
	providerID, providerOK := invocation.Flags["provider"].(string)
	serviceID, serviceOK := invocation.Flags["service"].(string)
	if !repoOK || !providerOK || !serviceOK || !filepath.IsAbs(repositoryValue) || !serviceIDPattern.MatchString(serviceID) {
		return "", "", ServiceProject{}, inputError("request_invalid", "input", "selector_invalid", "/flags", "")
	}
	repository, err := filepath.EvalSymlinks(filepath.Clean(repositoryValue))
	if err != nil {
		return "", "", ServiceProject{}, inputError("repository_invalid", "repository", "repository_unavailable", "/repo-root", "")
	}
	info, err := os.Stat(repository)
	if err != nil || !info.IsDir() {
		return "", "", ServiceProject{}, inputError("repository_invalid", "repository", "repository_unavailable", "/repo-root", "")
	}
	provider, ok := r.providers[providerID]
	if !ok {
		return "", "", ServiceProject{}, unavailableProviderError()
	}
	project, err := provider.Resolve(ctx, repository)
	if err != nil {
		return "", "", ServiceProject{}, projectOwnerError(err)
	}
	var selected *ServiceProject
	for index := range project.Services {
		candidate := &project.Services[index]
		if candidate.ServiceID != serviceID {
			continue
		}
		if selected != nil {
			return "", "", ServiceProject{}, inputError("provider_invalid", "provider", "service_duplicate", "/project/services", serviceID)
		}
		selected = candidate
	}
	if selected == nil {
		return "", "", ServiceProject{}, inputError("provider_invalid", "provider", "service_missing", "/service", serviceID)
	}
	return repository, providerID, *selected, nil
}

func (r *commandRunner) requireProviderTool(providerID string, role ToolRole, tool toolchain.Tool) error {
	version, ok := r.providerTools[providerID][role][tool.ID]
	if !ok {
		return inputError("provider_invalid", "provider", "provider_tool_undeclared", "/project/services/tool", tool.ID)
	}
	if version != tool.Version {
		return inputError("provider_invalid", "provider", "provider_tool_identity_mismatch", "/project/services/tool", tool.ID)
	}
	return nil
}

func (r *commandRunner) environment(tool toolchain.Tool) ([]toolchain.EnvVar, error) {
	host := make(map[string]string, len(r.hostEnvironment))
	for _, value := range r.hostEnvironment {
		if value.Name == "" {
			return nil, inputError("environment_invalid", "environment", "host_environment_invalid", "/environment", "")
		}
		if _, duplicate := host[value.Name]; duplicate {
			return nil, inputError("environment_invalid", "environment", "host_environment_duplicate", "/environment", value.Name)
		}
		host[value.Name] = value.Value
	}
	seen := make(map[string]struct{}, len(tool.Environment))
	result := make([]toolchain.EnvVar, 0, len(tool.Environment))
	for _, rule := range tool.Environment {
		if rule.Name == "" {
			return nil, inputError("environment_invalid", "environment", "tool_environment_invalid", "/project/services/tool/environment", "")
		}
		if _, duplicate := seen[rule.Name]; duplicate {
			return nil, inputError("environment_invalid", "environment", "tool_environment_duplicate", "/project/services/tool/environment", rule.Name)
		}
		seen[rule.Name] = struct{}{}
		switch rule.Source {
		case toolchain.EnvironmentHost:
			value, ok := host[rule.Name]
			if !ok {
				return nil, inputError("environment_invalid", "environment", "host_environment_missing", "/environment", rule.Name)
			}
			result = append(result, toolchain.EnvVar{Name: rule.Name, Value: value})
		case toolchain.EnvironmentFixed:
			result = append(result, toolchain.EnvVar{Name: rule.Name, Value: rule.FixedValue})
		default:
			return nil, inputError("environment_invalid", "environment", "scratch_environment_forbidden", "/project/services/tool/environment", rule.Name)
		}
	}
	return result, nil
}
