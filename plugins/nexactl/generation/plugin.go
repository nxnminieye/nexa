package generation

import (
	"context"
	"encoding/json"

	genfrontend "github.com/nxnminieye/nexa/generation/frontend"
	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/provenance"
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

// ToolRole closes the direct generation command families.
type ToolRole string

const (
	ToolRoleRPCGo          ToolRole = "rpc-go"
	ToolRoleAPIGo          ToolRole = "api-go"
	ToolRoleFrontendRender ToolRole = "frontend-render"
)

// ProviderTool binds one inspectable delegated tool to exactly one command role.
type ProviderTool struct {
	Role ToolRole
	Tool plugin.DelegatedToolSpec
}

// UserLogicFile declares one exact create-once file outside generated scopes.
type UserLogicFile struct {
	Path    string
	Content []byte
}

// RPCProject contains the typed RPC facts and explicit consumer-owned path boundaries.
type RPCProject struct {
	Facts           genprotocol.Document
	Tool            toolchain.Tool
	GeneratedScope  string
	ExtensionScopes []string
	UserLogic       []UserLogicFile
}

// APIProject contains the typed API facts and explicit consumer-owned path boundaries.
type APIProject struct {
	Facts           httpapi.Document
	Tool            toolchain.Tool
	GeneratedScope  string
	ExtensionScopes []string
	UserLogic       []UserLogicFile
}

// FrontendProject contains the canonical FrontendIR and consumer-owned source boundaries.
type FrontendProject struct {
	Facts                    genfrontend.Document
	Tool                     toolchain.Tool
	GeneratedScope           string
	ExtensionScopes          []string
	FrontendSourceLockDigest provenance.Digest
}

// ServiceProject closes the consumer-owned facts for one service.
type ServiceProject struct {
	ServiceID string
	RPC       *RPCProject
	API       *APIProject
	Frontend  *FrontendProject
}

// Project contains the services resolved by a consumer provider.
type Project struct {
	Services []ServiceProject
}

// ProjectProvider resolves typed project facts from a consumer repository.
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
	return plugin.NewStatic(plugin.Spec{
		Descriptor: plugin.Descriptor{
			ID:              "generation",
			Version:         pluginVersion,
			ContractVersion: plugin.ContractVersion,
			Provides: []plugin.Capability{
				{ID: "generation.rpc", Version: capabilityVersion},
				{ID: "generation.api", Version: capabilityVersion},
				{ID: "generation.frontend", Version: capabilityVersion},
			},
		},
		Commands: []plugin.CommandSpec{
			directCommand("rpc", runner.delegatedTools[ToolRoleRPCGo], runner.generateRPC),
			directCommand("api", runner.delegatedTools[ToolRoleAPIGo], runner.generateAPI),
			frontendCommand(runner.delegatedTools[ToolRoleFrontendRender], runner.generateFrontend),
		},
	})
}

func directCommand(owner string, tools []plugin.DelegatedToolSpec, run plugin.Handler) plugin.CommandSpec {
	return plugin.CommandSpec{
		Path:           []string{"generation", owner, "generate"},
		Summary:        "directly generate " + owner + " source",
		Flags:          selectorFlags(),
		InputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false,"required":["repo-root","provider","service"],"properties":{"repo-root":{"type":"string"},"provider":{"type":"string"},"service":{"type":"string"},"overwrite-logic":{"type":"boolean","default":false}}}`),
		OutputSchema:   generationResultSchema(),
		SideEffect:     plugin.SideEffectRepositoryWrite,
		DelegatedTools: cloneDelegatedTools(tools),
		Run:            run,
	}
}

func frontendCommand(tools []plugin.DelegatedToolSpec, run plugin.Handler) plugin.CommandSpec {
	return plugin.CommandSpec{
		Path:           []string{"generation", "frontend", "generate"},
		Summary:        "directly generate frontend source",
		Flags:          frontendSelectorFlags(),
		InputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false,"required":["repo-root","provider","service"],"properties":{"repo-root":{"type":"string"},"provider":{"type":"string"},"service":{"type":"string"}}}`),
		OutputSchema:   generationResultSchema(),
		SideEffect:     plugin.SideEffectRepositoryWrite,
		DelegatedTools: cloneDelegatedTools(tools),
		Run:            run,
	}
}

func generationResultSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","status","service","generatedScope","userLogic"],"properties":{"apiVersion":{"const":"nexa.dev/generation-result/v2"},"kind":{"const":"GenerationResult"},"status":{"const":"generated"},"service":{"type":"string"},"generatedScope":{"type":"string"},"userLogic":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["path","action"],"properties":{"path":{"type":"string"},"action":{"enum":["created","skipped","overwritten"]}}}}}}`)
}

func selectorFlags() []plugin.FlagSpec {
	return []plugin.FlagSpec{
		{Name: "repo-root", Type: plugin.FlagString, Summary: "repository root", Required: true},
		{Name: "provider", Type: plugin.FlagString, Summary: "project provider id", Required: true},
		{Name: "service", Type: plugin.FlagString, Summary: "selected service id", Required: true},
		{Name: "overwrite-logic", Type: plugin.FlagBool, Summary: "overwrite declared user-logic files", Default: json.RawMessage(`false`)},
	}
}

func frontendSelectorFlags() []plugin.FlagSpec {
	return []plugin.FlagSpec{
		{Name: "repo-root", Type: plugin.FlagString, Summary: "repository root", Required: true},
		{Name: "provider", Type: plugin.FlagString, Summary: "project provider id", Required: true},
		{Name: "service", Type: plugin.FlagString, Summary: "selected service id", Required: true},
	}
}

func cloneDelegatedTools(values []plugin.DelegatedToolSpec) []plugin.DelegatedToolSpec {
	result := make([]plugin.DelegatedToolSpec, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Inputs = append([]string(nil), value.Inputs...)
		result[index].Writes = append([]string(nil), value.Writes...)
	}
	return result
}
