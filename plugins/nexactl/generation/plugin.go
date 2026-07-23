package generation

import (
	"context"
	"encoding/json"

	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

const (
	pluginVersion     = "v0.1.0"
	capabilityVersion = "v1.0.0"
)

// ProviderDescriptor identifies one consumer-owned project provider and its delegated tools.
type ProviderDescriptor struct {
	ID      string
	Version string
	Tools   []ProviderTool
}

// ToolRole closes the generation command family served by one delegated tool.
type ToolRole string

const (
	ToolRoleEntGenerate ToolRole = "ent-generate"
	ToolRoleEntCRUD     ToolRole = "ent-crud"
	ToolRoleRPCGo       ToolRole = "rpc-go"
	ToolRoleAPIGo       ToolRole = "api-go"
)

// ProviderTool binds one inspectable delegated tool to exactly one command role.
type ProviderTool struct {
	Role ToolRole
	Tool plugin.DelegatedToolSpec
}

type MultiTenantConfig struct {
	Enabled bool
}

// ServiceProject closes the consumer-owned inputs for one generation service.
type ServiceProject struct {
	ServiceID, EntSchemaDir, ProtoEntry, APIEntry      string
	LogicRoot                                          string
	MultiTenant                                        MultiTenantConfig
	EntGenerateTool, EntCRUDTool, RPCGoTool, APIGoTool toolchain.Tool
}

// Project contains the service projections resolved by a consumer provider.
type Project struct {
	CatalogPath, CoreServiceID string
	Services                   []ServiceProject
}

// ProjectProvider resolves typed project relations from a consumer repository.
type ProjectProvider interface {
	Descriptor() ProviderDescriptor
	Resolve(context.Context, string) (Project, error)
}

// Options configures the official generation Build Plugin.
type Options struct {
	Providers   []ProjectProvider
	Runner      toolchain.Runner
	Environment []toolchain.EnvVar
}

type commandRunner struct {
	providers       map[string]ProjectProvider
	providerTools   map[string]map[ToolRole]map[string]string
	runner          toolchain.Runner
	hostEnvironment []toolchain.EnvVar
	delegatedTools  map[ToolRole][]plugin.DelegatedToolSpec
}

// New constructs the official generation Build Plugin.
func New(options Options) (plugin.Plugin, error) {
	runner, err := newCommandRunner(options)
	if err != nil {
		return nil, err
	}
	crudTools := append(cloneDelegatedTools(runner.delegatedTools[ToolRoleEntCRUD]), runner.delegatedTools[ToolRoleRPCGo]...)
	return plugin.NewStatic(plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              "generation",
			Version:         pluginVersion,
			ContractVersion: plugin.ContractVersion,
			Provides: []plugin.Capability{
				{ID: "generation.crud", Version: capabilityVersion},
				{ID: "generation.ent", Version: capabilityVersion},
				{ID: "generation.rpc", Version: capabilityVersion},
				{ID: "generation.api", Version: capabilityVersion},
				{ID: "generation.service-manifest", Version: capabilityVersion},
			},
		},
		Commands: []plugin.CommandSpec{
			entGenerateCommand(runner.delegatedTools[ToolRoleEntGenerate], runner.generateEnt),
			generationCommand("crud", "plan", serviceFlags(), false, true, crudTools, runner.plan),
			generationCommand("crud", "check", serviceFlags(), false, true, crudTools, runner.check),
			generationCommand("crud", "write", serviceFlags(), true, true, crudTools, runner.write),
			generationCommand("rpc", "plan", serviceFlags(), false, true, runner.delegatedTools[ToolRoleRPCGo], runner.rpcPlan),
			generationCommand("rpc", "check", serviceFlags(), false, true, runner.delegatedTools[ToolRoleRPCGo], runner.rpcCheck),
			generationCommand("rpc", "write", serviceFlags(), true, true, runner.delegatedTools[ToolRoleRPCGo], runner.rpcWrite),
			generationCommand("api", "plan", coreServiceFlags(), false, true, runner.delegatedTools[ToolRoleAPIGo], runner.apiPlan),
			generationCommand("api", "check", coreServiceFlags(), false, true, runner.delegatedTools[ToolRoleAPIGo], runner.apiCheck),
			generationCommand("api", "write", coreServiceFlags(), true, true, runner.delegatedTools[ToolRoleAPIGo], runner.apiWrite),
			generationCommand("service-manifest", "check", serviceFlags(), false, false, nil, runner.serviceManifestCheck),
			generationCommand("service-manifest", "write", serviceFlags(), true, false, nil, runner.serviceManifestWrite),
		},
	})
}

func entGenerateCommand(tools []plugin.DelegatedToolSpec, run plugin.Handler) plugin.CommandSpec {
	return plugin.CommandSpec{
		Path:           []string{"gen", "ent"},
		Summary:        "generate Ent Go code",
		Flags:          serviceFlags(),
		InputSchema:    inputSchema("service", false, false, false),
		OutputSchema:   json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","status","service"],"properties":{"apiVersion":{"const":"nexa.dev/ent-generation-result/v1"},"kind":{"const":"EntGenerationResult"},"status":{"const":"generated"},"service":{"type":"string"}}}`),
		SideEffect:     plugin.SideEffectRepositoryWrite,
		DelegatedTools: cloneDelegatedTools(tools),
		Run:            run,
	}
}

func generationCommand(owner, action string, flags []plugin.FlagSpec, write, delegated bool, tools []plugin.DelegatedToolSpec, run plugin.Handler) plugin.CommandSpec {
	crud := owner == "crud"
	if crud {
		flags = append(flags, plugin.FlagSpec{Name: "overwrite-logic", Type: plugin.FlagBool, Summary: "overwrite existing CRUD logic", Default: json.RawMessage(`false`)})
	}
	if write {
		flags = append(flags, plugin.FlagSpec{Name: "plan-digest", Type: plugin.FlagString, Summary: "accepted generation plan digest", Required: true})
		if crud {
			flags = append(flags, plugin.FlagSpec{Name: "lock-digest", Type: plugin.FlagString, Summary: "accepted compatibility lock digest"})
		}
	}
	sideEffect := plugin.SideEffectRepositoryRead
	if write {
		sideEffect = plugin.SideEffectRepositoryWrite
	}
	selector := "service"
	if owner == "api" {
		selector = "core-service"
	}
	command := plugin.CommandSpec{
		Path:         []string{"generation", owner, action},
		Summary:      action + " " + owner + " generation",
		Flags:        flags,
		InputSchema:  inputSchema(selector, write, crud, crud),
		OutputSchema: outputSchema(action == "plan"),
		SideEffect:   sideEffect,
		Run:          run,
	}
	if delegated {
		command.DelegatedTools = cloneDelegatedTools(tools)
	}
	return command
}

func serviceFlags() []plugin.FlagSpec {
	return selectorFlags("service")
}

func coreServiceFlags() []plugin.FlagSpec {
	return selectorFlags("core-service")
}

func selectorFlags(selector string) []plugin.FlagSpec {
	return []plugin.FlagSpec{
		{Name: "repo-root", Type: plugin.FlagString, Summary: "repository root", Required: true},
		{Name: "provider", Type: plugin.FlagString, Summary: "project provider id", Required: true},
		{Name: selector, Type: plugin.FlagString, Summary: "selected " + selector + " id", Required: true},
	}
}

func inputSchema(selector string, write, lock, overwrite bool) json.RawMessage {
	required := `["repo-root","provider","` + selector + `"`
	properties := `"repo-root":{"type":"string"},"provider":{"type":"string"},"` + selector + `":{"type":"string"}`
	if overwrite {
		properties += `,"overwrite-logic":{"type":"boolean","default":false}`
	}
	if write {
		required += `,"plan-digest"`
		properties += `,"plan-digest":{"type":"string"}`
		if lock {
			properties += `,"lock-digest":{"type":"string"}`
		}
	}
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":` + required + `],"properties":{` + properties + `}}`)
}

func outputSchema(plan bool) json.RawMessage {
	if plan {
		return json.RawMessage(`{"type":"object","required":["apiVersion","kind","planDigest"],"properties":{"apiVersion":{"const":"nexa.dev/generation-plan/v2"},"kind":{"const":"GenerationPlan"},"planDigest":{"type":"string"}}}`)
	}
	return json.RawMessage(`{"type":"object","required":["apiVersion","kind","planDigest","status"],"properties":{"apiVersion":{"const":"nexa.dev/generation-result/v1"},"kind":{"const":"GenerationResult"},"planDigest":{"type":"string"},"status":{"type":"string"}}}`)
}

func cloneDelegatedTools(values []plugin.DelegatedToolSpec) []plugin.DelegatedToolSpec {
	result := make([]plugin.DelegatedToolSpec, len(values))
	for index, value := range values {
		result[index] = cloneDelegatedTool(value)
	}
	return result
}

func cloneDelegatedTool(value plugin.DelegatedToolSpec) plugin.DelegatedToolSpec {
	value.Inputs = append([]string(nil), value.Inputs...)
	value.Writes = append([]string(nil), value.Writes...)
	return value
}
