package generation

import (
	"context"
	"encoding/json"

	genfrontend "github.com/nxnminieye/nexa/generation/frontend"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/provenance"
)

const (
	pluginVersion     = "v0.1.0"
	capabilityVersion = "v1.0.0"
	// APISourceInput identifies the source-based API-Go tool boundary.
	APISourceInput = "nexa.dev/api-source/v1"
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

// APIProject contains the exact consumer .api source entry and output boundaries.
type APIProject struct {
	EntryFile       string
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

// EntProtoProject declares the consumer-owned Ent source and Proto output boundary.
// The framework owns the Ent-to-Proto projection; the consumer owns these choices and paths.
type EntProtoProject struct {
	SchemaDir       string
	ProtoPackage    string
	GoPackage       string
	MultiTenant     bool
	GeneratedScope  string
	GeneratedFile   string
	ExtensionScopes []string
}

// ServiceProject closes the consumer-owned facts for one service.
type ServiceProject struct {
	ServiceID string
	EntProto  *EntProtoProject
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
				{ID: "generation.ent-proto", Version: capabilityVersion},
				{ID: "generation.rpc", Version: capabilityVersion},
				{ID: "generation.api", Version: capabilityVersion},
				{ID: "generation.frontend", Version: capabilityVersion},
			},
		},
		Commands: []plugin.CommandSpec{
			entProtoCommand(runner.generateEntProto),
			entProtoCheckCommand(runner.checkEntProto),
			directCommand("rpc", runner.delegatedTools[ToolRoleRPCGo], runner.generateRPC),
			sourceCheckCommand("rpc", runner.checkRPC),
			directCommand("api", runner.delegatedTools[ToolRoleAPIGo], runner.generateAPI),
			sourceCheckCommand("api", runner.checkAPI),
			frontendCommand(runner.delegatedTools[ToolRoleFrontendRender], runner.generateFrontend),
		},
	})
}

func sourceCheckCommand(owner string, run plugin.Handler) plugin.CommandSpec {
	return plugin.CommandSpec{
		Path:         []string{"generation", owner, "check"},
		Summary:      "check " + owner + " source",
		Flags:        frontendSelectorFlags(),
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["repo-root","provider","service"],"properties":{"repo-root":{"type":"string"},"provider":{"type":"string"},"service":{"type":"string"}}}`),
		OutputSchema: entProtoCheckResultSchema(),
		SideEffect:   plugin.SideEffectRepositoryRead,
		Run:          run,
	}
}

func entProtoCheckCommand(run plugin.Handler) plugin.CommandSpec {
	return plugin.CommandSpec{
		Path:         []string{"generation", "ent-proto", "check"},
		Summary:      "check Ent CRUD Proto source",
		Flags:        frontendSelectorFlags(),
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["repo-root","provider","service"],"properties":{"repo-root":{"type":"string"},"provider":{"type":"string"},"service":{"type":"string"}}}`),
		OutputSchema: entProtoCheckResultSchema(),
		SideEffect:   plugin.SideEffectRepositoryRead,
		Run:          run,
	}
}

func entProtoCommand(run plugin.Handler) plugin.CommandSpec {
	return plugin.CommandSpec{
		Path:         []string{"generation", "ent-proto", "generate"},
		Summary:      "directly generate Ent CRUD Proto source",
		Flags:        frontendSelectorFlags(),
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["repo-root","provider","service"],"properties":{"repo-root":{"type":"string"},"provider":{"type":"string"},"service":{"type":"string"}}}`),
		OutputSchema: generationResultSchema(),
		SideEffect:   plugin.SideEffectRepositoryWrite,
		Run:          run,
	}
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

func entProtoCheckResultSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["apiVersion","kind","status","service"],"properties":{"apiVersion":{"const":"nexa.dev/generation-check-result/v1"},"kind":{"const":"GenerationCheckResult"},"status":{"const":"valid"},"service":{"type":"string"}}}`)
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
