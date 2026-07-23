package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	servicecontract "github.com/nxnminieye/nexa/generation/service"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/generation/transaction"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
	sdkapi "github.com/nxnminieye/nexa/sdk/api"
)

const fixedOperationID = "op_0123456789abcdef0123456789abcdef"

func TestNewDefinesGenerationCapabilities(t *testing.T) {
	entTool := plugin.DelegatedToolSpec{ID: "consumer.ent", Version: "v1.9.2", Inputs: []string{"Ent schema"}, Writes: []string{"repository"}}
	crudTool := plugin.DelegatedToolSpec{ID: "consumer.crud", Version: "v1.9.2", Inputs: []string{"Ent graph"}, Writes: []string{"staging"}}
	rpcTool := plugin.DelegatedToolSpec{ID: "consumer.rpc", Version: "v1.9.2", Inputs: []string{"ProtocolIR"}, Writes: []string{"staging"}}
	apiTool := plugin.DelegatedToolSpec{ID: "consumer.api", Version: "v1.9.2", Inputs: []string{"APIIR"}, Writes: []string{"staging"}}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{&providerStub{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{
			{Role: generation.ToolRoleEntGenerate, Tool: entTool},
			{Role: generation.ToolRoleEntCRUD, Tool: crudTool},
			{Role: generation.ToolRoleRPCGo, Tool: rpcTool},
			{Role: generation.ToolRoleAPIGo, Tool: apiTool},
		}},
	}}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	spec := candidate.Spec()
	if spec.Descriptor.ID != "generation" || spec.Descriptor.Version != "v0.1.0" ||
		spec.Descriptor.ContractVersion != plugin.ContractVersion {
		t.Fatalf("descriptor = %#v", spec.Descriptor)
	}
	if !reflect.DeepEqual(spec.Descriptor.Provides, []plugin.Capability{
		{ID: "generation.crud", Version: "v1.0.0"},
		{ID: "generation.ent", Version: "v1.0.0"},
		{ID: "generation.rpc", Version: "v1.0.0"},
		{ID: "generation.api", Version: "v1.0.0"},
		{ID: "generation.service-manifest", Version: "v1.0.0"},
	}) {
		t.Fatalf("provides = %#v", spec.Descriptor.Provides)
	}
	if len(spec.Descriptor.Requires) != 0 || len(spec.Commands) != 12 {
		t.Fatalf("unexpected plugin spec: %#v", spec)
	}

	want := []struct {
		path       []string
		flags      []string
		required   []bool
		sideEffect plugin.SideEffect
		tools      []plugin.DelegatedToolSpec
	}{
		{
			path:       []string{"gen", "ent"},
			flags:      []string{"repo-root", "provider", "service"},
			required:   []bool{true, true, true},
			sideEffect: plugin.SideEffectRepositoryWrite,
			tools:      []plugin.DelegatedToolSpec{entTool},
		},
		{
			path:       []string{"generation", "crud", "plan"},
			flags:      []string{"repo-root", "provider", "service", "overwrite-logic"},
			required:   []bool{true, true, true, false},
			sideEffect: plugin.SideEffectRepositoryRead,
			tools:      []plugin.DelegatedToolSpec{crudTool, rpcTool},
		},
		{
			path:       []string{"generation", "crud", "check"},
			flags:      []string{"repo-root", "provider", "service", "overwrite-logic"},
			required:   []bool{true, true, true, false},
			sideEffect: plugin.SideEffectRepositoryRead,
			tools:      []plugin.DelegatedToolSpec{crudTool, rpcTool},
		},
		{
			path:       []string{"generation", "crud", "write"},
			flags:      []string{"repo-root", "provider", "service", "overwrite-logic", "plan-digest", "lock-digest"},
			required:   []bool{true, true, true, false, true, false},
			sideEffect: plugin.SideEffectRepositoryWrite,
			tools:      []plugin.DelegatedToolSpec{crudTool, rpcTool},
		},
		{
			path:       []string{"generation", "rpc", "plan"},
			flags:      []string{"repo-root", "provider", "service"},
			required:   []bool{true, true, true},
			sideEffect: plugin.SideEffectRepositoryRead,
			tools:      []plugin.DelegatedToolSpec{rpcTool},
		},
		{path: []string{"generation", "rpc", "check"}, flags: []string{"repo-root", "provider", "service"}, required: []bool{true, true, true}, sideEffect: plugin.SideEffectRepositoryRead, tools: []plugin.DelegatedToolSpec{rpcTool}},
		{path: []string{"generation", "rpc", "write"}, flags: []string{"repo-root", "provider", "service", "plan-digest"}, required: []bool{true, true, true, true}, sideEffect: plugin.SideEffectRepositoryWrite, tools: []plugin.DelegatedToolSpec{rpcTool}},
		{path: []string{"generation", "api", "plan"}, flags: []string{"repo-root", "provider", "core-service"}, required: []bool{true, true, true}, sideEffect: plugin.SideEffectRepositoryRead, tools: []plugin.DelegatedToolSpec{apiTool}},
		{path: []string{"generation", "api", "check"}, flags: []string{"repo-root", "provider", "core-service"}, required: []bool{true, true, true}, sideEffect: plugin.SideEffectRepositoryRead, tools: []plugin.DelegatedToolSpec{apiTool}},
		{path: []string{"generation", "api", "write"}, flags: []string{"repo-root", "provider", "core-service", "plan-digest"}, required: []bool{true, true, true, true}, sideEffect: plugin.SideEffectRepositoryWrite, tools: []plugin.DelegatedToolSpec{apiTool}},
		{path: []string{"generation", "service-manifest", "check"}, flags: []string{"repo-root", "provider", "service"}, required: []bool{true, true, true}, sideEffect: plugin.SideEffectRepositoryRead},
		{path: []string{"generation", "service-manifest", "write"}, flags: []string{"repo-root", "provider", "service", "plan-digest"}, required: []bool{true, true, true, true}, sideEffect: plugin.SideEffectRepositoryWrite},
	}
	for index, command := range spec.Commands {
		if !reflect.DeepEqual(command.Path, want[index].path) || command.SideEffect != want[index].sideEffect {
			t.Fatalf("command %d = %#v", index, command)
		}
		var input, output map[string]any
		if err := json.Unmarshal(command.InputSchema, &input); err != nil || input["type"] != "object" {
			t.Fatalf("command %d input schema = %s, error = %v", index, command.InputSchema, err)
		}
		if err := json.Unmarshal(command.OutputSchema, &output); err != nil || output["type"] != "object" {
			t.Fatalf("command %d output schema = %s, error = %v", index, command.OutputSchema, err)
		}
		if !reflect.DeepEqual(command.DelegatedTools, want[index].tools) {
			t.Fatalf("command %d delegated tools = %#v", index, command.DelegatedTools)
		}
		if len(command.Flags) != len(want[index].flags) {
			t.Fatalf("command %d flags = %#v", index, command.Flags)
		}
		for flagIndex, flag := range command.Flags {
			wantType := plugin.FlagString
			if flag.Name == "overwrite-logic" {
				wantType = plugin.FlagBool
			}
			if flag.Name != want[index].flags[flagIndex] || flag.Type != wantType || flag.Required != want[index].required[flagIndex] {
				t.Fatalf("command %d flag %d = %#v", index, flagIndex, flag)
			}
		}
	}
}

func TestGenerationCommandOutputSchemasUseCurrentTransactionVersions(t *testing.T) {
	candidate, err := generation.New(generation.Options{})
	if err != nil {
		t.Fatal(err)
	}
	planCommands, resultCommands := 0, 0
	for _, command := range candidate.Spec().Commands {
		if len(command.Path) == 0 {
			continue
		}
		action := command.Path[len(command.Path)-1]
		if action != "plan" && action != "check" && action != "write" {
			continue
		}
		expectedVersion, expectedKind := transaction.PlanAPIVersion, "GenerationPlan"
		if action == "plan" {
			planCommands++
		} else {
			resultCommands++
			expectedVersion, expectedKind = transaction.ResultAPIVersion, "GenerationResult"
		}
		var schema struct {
			Properties map[string]struct {
				Const string `json:"const"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(command.OutputSchema, &schema); err != nil {
			t.Fatalf("%v output schema = %s: %v", command.Path, command.OutputSchema, err)
		}
		if got := schema.Properties["apiVersion"].Const; got != expectedVersion {
			t.Fatalf("%v apiVersion = %q, want %q", command.Path, got, expectedVersion)
		}
		if got := schema.Properties["kind"].Const; got != expectedKind {
			t.Fatalf("%v kind = %q, want %q", command.Path, got, expectedKind)
		}
	}
	if planCommands == 0 || resultCommands == 0 {
		t.Fatalf("generation transaction commands = plan:%d result:%d", planCommands, resultCommands)
	}
}

func TestNewDefensivelyCopiesProviderTools(t *testing.T) {
	tools := []generation.ProviderTool{{Role: generation.ToolRoleEntGenerate, Tool: plugin.DelegatedToolSpec{ID: "consumer.goctl", Version: "v1.9.2", Inputs: []string{"facts"}, Writes: []string{"staging"}}}}
	provider := &providerStub{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: tools}}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	tools[0].Tool.ID, tools[0].Tool.Inputs[0], tools[0].Tool.Writes[0] = "changed", "changed", "changed"
	got := candidate.Spec().Commands[0].DelegatedTools
	if len(got) != 1 || got[0].ID != "consumer.goctl" || got[0].Inputs[0] != "facts" || got[0].Writes[0] != "staging" {
		t.Fatalf("delegated tools = %#v", got)
	}
}

func TestServiceProjectMultiTenantAndLogicRootShape(t *testing.T) {
	project := generation.ServiceProject{
		ServiceID:   "accounts",
		LogicRoot:   "backend/accounts/internal/logic",
		MultiTenant: generation.MultiTenantConfig{Enabled: true},
	}
	if !project.MultiTenant.Enabled || project.LogicRoot != "backend/accounts/internal/logic" {
		t.Fatalf("service project = %#v", project)
	}
}

func TestCRUDCommandReplacesCRUDProtoWithoutAlias(t *testing.T) {
	candidate, err := generation.New(generation.Options{})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := map[string]bool{}
	for _, capability := range candidate.Spec().Descriptor.Provides {
		capabilities[capability.ID] = true
	}
	if !capabilities["generation.crud"] || capabilities["generation.crud-proto"] {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	actions := map[string]bool{}
	for _, command := range candidate.Spec().Commands {
		path := strings.Join(command.Path, " ")
		if strings.HasPrefix(path, "generation crud-proto ") {
			t.Fatalf("legacy command remains: %q", path)
		}
		if strings.HasPrefix(path, "generation crud ") {
			actions[command.Path[2]] = true
		}
	}
	if !reflect.DeepEqual(actions, map[string]bool{"plan": true, "check": true, "write": true}) {
		t.Fatalf("crud actions = %#v", actions)
	}
}

func TestCRUDCommandsExposeOverwriteLogicOnPlanCheckWrite(t *testing.T) {
	candidate, err := generation.New(generation.Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, command := range candidate.Spec().Commands {
		if len(command.Path) != 3 || command.Path[0] != "generation" || command.Path[1] != "crud" {
			continue
		}
		found++
		var overwrite *plugin.FlagSpec
		for index := range command.Flags {
			if command.Flags[index].Name == "overwrite-logic" {
				overwrite = &command.Flags[index]
				break
			}
		}
		if overwrite == nil || overwrite.Type != plugin.FlagBool || overwrite.Required || string(overwrite.Default) != "false" {
			t.Fatalf("%v overwrite flag = %#v", command.Path, overwrite)
		}
	}
	if found != 3 {
		t.Fatalf("crud command count = %d", found)
	}
}

func TestZeroProviderInvocationReturnsStableUnavailableEnvelope(t *testing.T) {
	candidate, err := generation.New(generation.Options{})
	if err != nil {
		t.Fatal(err)
	}
	composed, err := host.New(host.Options{
		Version: "v0.0.0-test",
		OperationIDs: protocol.OperationIDGeneratorFunc(func() (string, error) {
			return fixedOperationID, nil
		}),
	}, candidate)
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := composed.Execute(context.Background(), []string{
		"generation", "crud", "plan",
		"--repo-root", t.TempDir(),
		"--provider", "consumer",
		"--service", "accounts",
		"--json",
	}, &stdout, &stderr)
	if exit != 6 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", exit, stderr.String(), stdout.String())
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.OperationID != fixedOperationID || envelope.Error == nil {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope.Error.Code != "capability_unavailable" || envelope.Error.Domain != "nexactl.generation" ||
		envelope.Error.Category != protocol.CategoryUnavailable || len(envelope.Error.Details) == 0 {
		t.Fatalf("error = %#v", envelope.Error)
	}
	var details struct {
		Stage   string `json:"stage"`
		Reason  string `json:"reason"`
		Pointer string `json:"pointer,omitempty"`
		Source  string `json:"source,omitempty"`
	}
	if err := json.Unmarshal(envelope.Error.Details, &details); err != nil {
		t.Fatal(err)
	}
	if details.Stage != "provider" || details.Reason != "provider_missing" || details.Pointer != "/provider" || details.Source != "" {
		t.Fatalf("details = %#v", details)
	}
}

func TestCommandResolvesOnlyTheSelectedProviderOnce(t *testing.T) {
	first := &providerStub{descriptor: generation.ProviderDescriptor{ID: "first", Version: "v1.0.0"}}
	second := &providerStub{descriptor: generation.ProviderDescriptor{ID: "second", Version: "v1.0.0"}}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{first, second}})
	if err != nil {
		t.Fatal(err)
	}
	envelope, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "crud", "plan",
		"--repo-root", t.TempDir(), "--provider", "second", "--service", "accounts", "--json",
	)
	if exit != 3 || stderr != "" || envelope.Error == nil || envelope.Error.Code != "fact_source_missing" {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	if first.resolves != 0 || second.resolves != 1 {
		t.Fatalf("resolve calls = first:%d second:%d", first.resolves, second.resolves)
	}
}

func TestRPCPlanCompilesProtoAndInvokesSelectedTool(t *testing.T) {
	repository := t.TempDir()
	protoSource := []byte("syntax = \"proto3\"; package acme.account.v1; option go_package = \"example.com/acme/account/v1;accountv1\"; message GetRequest {} message GetResponse {} service Account { rpc Get(GetRequest) returns (GetResponse); }\n")
	if err := os.WriteFile(filepath.Join(repository, "account.proto"), protoSource, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{err: errors.New("private runner failure")}
	rpcTool := toolchain.Tool{ID: "consumer.rpc-go", Version: "v1.0.0", Executable: "goctl", Probe: toolchain.ExecutableProbe{ExpectedVersion: "goctl 1.9.2"}}
	provider := &providerStub{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{declaredProviderTool(generation.ToolRoleRPCGo, rpcTool)}},
		project: generation.Project{Services: []generation.ServiceProject{{
			ServiceID: "account", ProtoEntry: "account.proto",
			RPCGoTool: rpcTool,
		}}},
	}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	envelope, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "rpc", "plan", "--repo-root", repository, "--provider", "consumer", "--service", "account", "--json",
	)
	if exit != 3 || stderr != "" || envelope.Error == nil || envelope.Error.Code != "rpc_go_invalid" {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	var details struct {
		ToolID      string `json:"toolId"`
		ToolVersion string `json:"toolVersion"`
		ExitCode    int    `json:"exitCode"`
	}
	if err := json.Unmarshal(envelope.Error.Details, &details); err != nil || details.ToolID != "consumer.rpc-go" || details.ToolVersion != "v1.0.0" || details.ExitCode != 0 {
		t.Fatalf("details = %#v, error = %v", details, err)
	}
	if len(runner.requests) != 1 || !reflect.DeepEqual(runner.requests[0].Args, []string{"generate", "--service", "account"}) {
		t.Fatalf("requests = %#v", runner.requests)
	}
	var input struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		ServiceID  string `json:"serviceId"`
	}
	if err := json.Unmarshal(runner.requests[0].Stdin, &input); err != nil || input.APIVersion != "nexa.dev/protocol-ir/v2" || input.Kind != "ProtocolIR" || input.ServiceID != "account" {
		t.Fatalf("input = %#v, error = %v", input, err)
	}
}

func TestRPCPlanRejectsToolOutsideDeclaredCommandRole(t *testing.T) {
	declared := toolchain.Tool{ID: "consumer.declared-rpc", Version: "v1.0.0"}
	selected := toolchain.Tool{ID: "consumer.private-rpc", Version: "v1.0.0"}
	runner := &recordingRunner{}
	provider := &providerStub{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{declaredProviderTool(generation.ToolRoleRPCGo, declared)}},
		project:    generation.Project{Services: []generation.ServiceProject{{ServiceID: "account", ProtoEntry: "account.proto", RPCGoTool: selected}}},
	}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	envelope, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "rpc", "plan", "--repo-root", t.TempDir(), "--provider", "consumer", "--service", "account", "--json",
	)
	if exit != 3 || stderr != "" || envelope.Error == nil || envelope.Error.Code != "provider_invalid" || envelope.Error.Category != protocol.CategoryInput {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	var details struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(envelope.Error.Details, &details); err != nil || details.Reason != "provider_tool_undeclared" {
		t.Fatalf("details = %#v, error = %v", details, err)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("runner requests = %d", len(runner.requests))
	}
}

func TestAPIPlanBuildsCompositionAndInvokesSelectedTool(t *testing.T) {
	repository := t.TempDir()
	files := map[string]string{
		"backend/go.mod": `module example.com/consumer/backend

go 1.25.0
`,
		"services.json": `{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":[{"id":"core","root":"backend/core","dependsOn":["account"],"capabilityBindings":[]},{"id":"account","root":"backend/account","dependsOn":[],"capabilityBindings":[{"id":"nexa.dev/generation-api-proxy","apiVersion":"nexa.dev/generation-api-proxy/v1"}]}]}`,
		"backend/core/desc/core.api": `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type HealthRequest {}
type HealthResponse { OK bool }
@server (nexaOperationId: "health.get" nexaAuthMode: "none")
service core-api { @handler health get /health (HealthRequest) returns (HealthResponse) }
`,
		"backend/account/desc/account.proto": `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message GetAccountRequest { string id = 1; }
message GetAccountResponse { string name = 1; }
service AccountService {
  rpc Get(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.get" method: GET path: "/accounts/{id}"
      auth: { mode: NONE }
      request_fields: { http_field: "id" rpc_field: "id" }
      response_fields: { rpc_field: "name" http_field: "name" }
    };
  }
}
`,
	}
	for name, content := range files {
		filename := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &recordingRunner{err: errors.New("private runner failure")}
	runner.inspect = func(request toolchain.Request) {
		logicPath := filepath.Join(request.StagingRoot, "backend/core/internal/logic/rpcproxy/account-get.generated.go")
		parsed, err := parser.ParseFile(token.NewFileSet(), logicPath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse staged nested-module logic: %v", err)
		}
		imports := make([]string, 0, len(parsed.Imports))
		for _, item := range parsed.Imports {
			value, err := strconv.Unquote(item.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			imports = append(imports, value)
		}
		if !reflect.DeepEqual(imports, []string{"context", "example.com/consumer/backend/core/internal/serviceclients/account"}) {
			t.Fatalf("nested-module imports = %#v", imports)
		}
	}
	apiTool := toolchain.Tool{ID: "consumer.api-go", Version: "v1.0.0", Executable: "goctl", Probe: toolchain.ExecutableProbe{ExpectedVersion: "goctl 1.9.2"}}
	provider := &providerStub{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{declaredProviderTool(generation.ToolRoleAPIGo, apiTool)}},
		project: generation.Project{CatalogPath: "services.json", CoreServiceID: "core", Services: []generation.ServiceProject{
			{ServiceID: "core", APIEntry: "backend/core/desc/core.api", APIGoTool: apiTool},
			{ServiceID: "account", ProtoEntry: "backend/account/desc/account.proto"},
		}},
	}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	envelope, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "api", "plan", "--repo-root", repository, "--provider", "consumer", "--core-service", "core", "--json",
	)
	if exit != 3 || stderr != "" || envelope.Error == nil || envelope.Error.Code != "api_go_invalid" {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	var details struct {
		ToolID      string `json:"toolId"`
		ToolVersion string `json:"toolVersion"`
	}
	if err := json.Unmarshal(envelope.Error.Details, &details); err != nil || details.ToolID != "consumer.api-go" || details.ToolVersion != "v1.0.0" {
		t.Fatalf("details = %#v, error = %v", details, err)
	}
	if len(runner.requests) != 1 || !reflect.DeepEqual(runner.requests[0].Args, []string{"generate", "--core-service", "core"}) {
		t.Fatalf("requests = %#v, error = %#v", runner.requests, envelope.Error)
	}
	var input struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal(runner.requests[0].Stdin, &input); err != nil || input.APIVersion != "nexa.dev/http-api-ir/v1" {
		t.Fatalf("input = %#v, error = %v", input, err)
	}
}

func TestAPIPlanProjectsRuntimeContractCapabilityError(t *testing.T) {
	repository := t.TempDir()
	files := map[string]string{
		"go.mod":                     "module example.com/consumer\n\ngo 1.25.0\n",
		"services.json":              `{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":[{"id":"core","root":"backend/core","dependsOn":[],"capabilityBindings":[]}]}`,
		"backend/core/desc/core.api": "syntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-api/v1\")\ntype BoundaryRequest {}\n@server (nexaOperationId: \"boundary.call\" nexaAuthMode: \"none\")\nservice core-api { @handler boundary get /" + strings.Repeat("a", sdkapi.RuntimeContractLimits().RawBytes) + " (BoundaryRequest) }\n",
	}
	for name, content := range files {
		filename := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &recordingRunner{}
	apiTool := toolchain.Tool{ID: "consumer.api-go", Version: "v1.0.0", Executable: "goctl", Probe: toolchain.ExecutableProbe{ExpectedVersion: "goctl 1.9.2"}}
	provider := &providerStub{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{declaredProviderTool(generation.ToolRoleAPIGo, apiTool)}},
		project: generation.Project{CatalogPath: "services.json", CoreServiceID: "core", Services: []generation.ServiceProject{{
			ServiceID: "core", APIEntry: "backend/core/desc/core.api",
			APIGoTool: apiTool,
		}}},
	}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	envelope, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "api", "plan", "--repo-root", repository, "--provider", "consumer", "--core-service", "core", "--json",
	)
	if exit != 3 || stderr != "" || envelope.Error == nil || envelope.Error.Code != "runtime_contract_unrepresentable" || envelope.Error.Category != protocol.CategoryInput {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	var details struct {
		Reason  string `json:"reason"`
		Pointer string `json:"pointer"`
	}
	if err := json.Unmarshal(envelope.Error.Details, &details); err != nil || details.Reason != "runtime_contract_raw_limit_exceeded" || details.Pointer != "/manifest" {
		t.Fatalf("details = %#v, error = %v", details, err)
	}
	if len(runner.requests) != 0 {
		t.Fatalf("runner requests = %d", len(runner.requests))
	}
	if matches, err := filepath.Glob(filepath.Join(repository, ".nexa/generation/.staging-*")); err != nil || len(matches) != 0 {
		t.Fatalf("failed plan retained candidate staging: %v, %v", matches, err)
	}
}

func TestServiceManifestCheckAndWritePublishSelectedServiceProjection(t *testing.T) {
	repository := t.TempDir()
	files := map[string]string{
		"go.mod": `module example.com/consumer

go 1.25.0
`,
		"services.json": `{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":[{"id":"account","root":"backend/account","dependsOn":[],"capabilityBindings":[]}]}`,
		"backend/account/desc/account.proto": `syntax = "proto3";
package acme.account.v1;
option go_package = "example.com/acme/account/v1;accountv1";
message GetRequest {}
message GetResponse {}
service Account { rpc Get(GetRequest) returns (GetResponse); }
`,
	}
	for name, content := range files {
		filename := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	provider := &providerStub{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0"},
		project: generation.Project{CatalogPath: "services.json", Services: []generation.ServiceProject{{
			ServiceID: "account", ProtoEntry: "backend/account/desc/account.proto",
		}}},
	}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	checked, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "check", "--repo-root", repository, "--provider", "consumer", "--service", "account", "--json",
	)
	if exit != 0 || stderr != "" || !checked.OK {
		t.Fatalf("check exit=%d stderr=%q envelope=%#v details=%s", exit, stderr, checked, checked.Error.Details)
	}
	result, err := json.Marshal(checked.Result)
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		PlanDigest string `json:"planDigest"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(result, &projection); err != nil || projection.PlanDigest == "" || projection.Status == "current" {
		t.Fatalf("projection = %#v, error = %v", projection, err)
	}
	written, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "write", "--repo-root", repository, "--provider", "consumer", "--service", "account", "--plan-digest", projection.PlanDigest, "--json",
	)
	if exit != 0 || stderr != "" || !written.OK {
		t.Fatalf("write exit=%d stderr=%q envelope=%#v", exit, stderr, written)
	}
	artifactPath := filepath.Join(repository, "backend/account/generated/service-manifest.json")
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := servicecontract.Parse("backend/account/generated/service-manifest.json", data)
	if err != nil || manifest.ServiceID() != "account" || manifest.ServiceKind() != "rpc" || manifest.ModulePath() != "example.com/consumer" {
		t.Fatalf("manifest = %#v, error = %v", manifest, err)
	}
	moduleSourceFound := false
	for _, source := range manifest.ContractSources() {
		if source.Ref.String() == "repo:go.mod" {
			moduleSourceFound = true
		}
	}
	if !moduleSourceFound {
		t.Fatalf("contract sources = %#v", manifest.ContractSources())
	}
	firstContractDigest := manifest.ContractDigest()
	current, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "check", "--repo-root", repository, "--provider", "consumer", "--service", "account", "--json",
	)
	if exit != 0 || stderr != "" || !current.OK {
		t.Fatalf("current exit=%d stderr=%q envelope=%#v", exit, stderr, current)
	}
	changedProto := files["backend/account/desc/account.proto"] + "message ChangedContract { string id = 1; }\n"
	if err := os.WriteFile(filepath.Join(repository, "backend/account/desc/account.proto"), []byte(changedProto), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "write", "--repo-root", repository, "--provider", "consumer", "--service", "account", "--plan-digest", projection.PlanDigest, "--json",
	)
	if exit == 0 || stderr != "" || stale.Error == nil || stale.Error.Category != protocol.CategoryDrift {
		t.Fatalf("stale write exit=%d stderr=%q envelope=%#v", exit, stderr, stale)
	}
	updated, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "check", "--repo-root", repository, "--provider", "consumer", "--service", "account", "--json",
	)
	if exit != 0 || stderr != "" || !updated.OK {
		t.Fatalf("updated check exit=%d stderr=%q envelope=%#v", exit, stderr, updated)
	}
	updatedJSON, err := json.Marshal(updated.Result)
	if err != nil {
		t.Fatal(err)
	}
	var updatedProjection struct {
		PlanDigest string `json:"planDigest"`
	}
	if err := json.Unmarshal(updatedJSON, &updatedProjection); err != nil || updatedProjection.PlanDigest == "" || updatedProjection.PlanDigest == projection.PlanDigest {
		t.Fatalf("updated projection = %#v, error = %v", updatedProjection, err)
	}
	updatedWrite, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "write", "--repo-root", repository, "--provider", "consumer", "--service", "account", "--plan-digest", updatedProjection.PlanDigest, "--json",
	)
	if exit != 0 || stderr != "" || !updatedWrite.OK {
		t.Fatalf("updated write exit=%d stderr=%q envelope=%#v", exit, stderr, updatedWrite)
	}
	updatedData, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	updatedManifest, err := servicecontract.Parse("backend/account/generated/service-manifest.json", updatedData)
	if err != nil || updatedManifest.ContractDigest() == firstContractDigest {
		t.Fatalf("updated manifest = %#v, error = %v", updatedManifest, err)
	}
}

func TestCoreServiceManifestTracksComposedRPCContractSources(t *testing.T) {
	repository := t.TempDir()
	protoSource := `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message GetAccountRequest { string id = 1; }
message GetAccountResponse { string name = 1; }
service AccountService {
  rpc Get(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.get" method: GET path: "/accounts/{id}"
      auth: { mode: NONE }
      request_fields: { http_field: "id" rpc_field: "id" }
      response_fields: { rpc_field: "name" http_field: "name" }
    };
  }
}
`
	files := map[string]string{
		"go.mod":        "module example.com/consumer\n\ngo 1.25.0\n",
		"services.json": `{"apiVersion":"nexa.dev/service-catalog/v1","kind":"ServiceCatalog","services":[{"id":"core","root":"backend/core","dependsOn":["account"],"capabilityBindings":[]},{"id":"account","root":"backend/account","dependsOn":[],"capabilityBindings":[{"id":"nexa.dev/generation-api-proxy","apiVersion":"nexa.dev/generation-api-proxy/v1"}]}]}`,
		"backend/core/desc/core.api": `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type HealthRequest {}
type HealthResponse { OK bool }
@server (nexaOperationId: "health.get" nexaAuthMode: "none")
service core-api { @handler health get /health (HealthRequest) returns (HealthResponse) }
`,
		"backend/account/desc/account.proto": protoSource,
	}
	for name, content := range files {
		filename := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	provider := &providerStub{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0"},
		project: generation.Project{CatalogPath: "services.json", CoreServiceID: "core", Services: []generation.ServiceProject{
			{ServiceID: "core", APIEntry: "backend/core/desc/core.api"},
			{ServiceID: "account", ProtoEntry: "backend/account/desc/account.proto"},
		}},
	}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	checked, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "check", "--repo-root", repository, "--provider", "consumer", "--service", "core", "--json",
	)
	if exit != 0 || stderr != "" || !checked.OK {
		t.Fatalf("check exit=%d stderr=%q envelope=%#v", exit, stderr, checked)
	}
	result, err := json.Marshal(checked.Result)
	if err != nil {
		t.Fatal(err)
	}
	var initial struct {
		PlanDigest string `json:"planDigest"`
	}
	if err := json.Unmarshal(result, &initial); err != nil || initial.PlanDigest == "" {
		t.Fatalf("initial = %#v, error = %v", initial, err)
	}
	written, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "write", "--repo-root", repository, "--provider", "consumer", "--service", "core", "--plan-digest", initial.PlanDigest, "--json",
	)
	if exit != 0 || stderr != "" || !written.OK {
		t.Fatalf("write exit=%d stderr=%q envelope=%#v", exit, stderr, written)
	}
	data, err := os.ReadFile(filepath.Join(repository, "backend/core/generated/service-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := servicecontract.Parse("backend/core/generated/service-manifest.json", data)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, source := range manifest.ContractSources() {
		switch {
		case source.Ref.String() == "repo:go.mod":
			found["module"] = true
		case source.Ref.Path() == "services.json" && source.Ref.Fragment() == "service:core":
			found["core"] = true
		case source.Ref.Path() == "services.json" && strings.Contains(source.Ref.Fragment(), "binding:nexa.dev/generation-api-proxy"):
			found["binding"] = true
		case source.Ref.Path() == "backend/account/desc/account.proto" && strings.HasPrefix(source.Ref.Fragment(), "method:"):
			found["method"] = true
		case source.Ref.Path() == "backend/core/desc/core.api":
			found["api"] = true
		}
	}
	if !reflect.DeepEqual(found, map[string]bool{"module": true, "core": true, "binding": true, "method": true, "api": true}) {
		t.Fatalf("contract source classes = %#v; sources=%#v", found, manifest.ContractSources())
	}
	changed := strings.Replace(protoSource, `path: "/accounts/{id}"`, `path: "/v2/accounts/{id}"`, 1)
	if err := os.WriteFile(filepath.Join(repository, "backend/account/desc/account.proto"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	drifted, stderr, exit := executeGenerationPlugin(t, candidate,
		"generation", "service-manifest", "check", "--repo-root", repository, "--provider", "consumer", "--service", "core", "--json",
	)
	if exit != 0 || stderr != "" || !drifted.OK {
		t.Fatalf("drift check exit=%d stderr=%q envelope=%#v", exit, stderr, drifted)
	}
	driftJSON, err := json.Marshal(drifted.Result)
	if err != nil {
		t.Fatal(err)
	}
	var drift struct {
		PlanDigest string `json:"planDigest"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(driftJSON, &drift); err != nil || drift.PlanDigest == initial.PlanDigest || drift.Status != "changes-required" {
		t.Fatalf("drift = %#v, error = %v", drift, err)
	}
}

func TestNewRejectsInvalidProviderComposition(t *testing.T) {
	valid := &providerStub{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0"}}
	var typedNil *providerStub
	tests := []struct {
		name      string
		providers []generation.ProjectProvider
		reason    string
	}{
		{name: "nil", providers: []generation.ProjectProvider{nil}, reason: "provider_nil"},
		{name: "typed nil", providers: []generation.ProjectProvider{typedNil}, reason: "provider_nil"},
		{name: "invalid id", providers: []generation.ProjectProvider{&providerStub{descriptor: generation.ProviderDescriptor{ID: "Consumer", Version: "v1.0.0"}}}, reason: "provider_id_invalid"},
		{name: "invalid version", providers: []generation.ProjectProvider{&providerStub{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "1.0.0"}}}, reason: "provider_version_invalid"},
		{name: "duplicate", providers: []generation.ProjectProvider{valid, valid}, reason: "provider_duplicate"},
		{name: "invalid tool role", providers: []generation.ProjectProvider{
			&providerStub{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{
				{Role: "dynamic", Tool: plugin.DelegatedToolSpec{ID: "goctl", Version: "v1"}},
			}}},
		}, reason: "provider_tool_role_invalid"},
		{name: "duplicate tool in provider", providers: []generation.ProjectProvider{
			&providerStub{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{
				{Role: generation.ToolRoleRPCGo, Tool: plugin.DelegatedToolSpec{ID: "goctl", Version: "v1"}},
				{Role: generation.ToolRoleRPCGo, Tool: plugin.DelegatedToolSpec{ID: "goctl", Version: "v1"}},
			}}},
		}, reason: "provider_tool_duplicate"},
		{name: "duplicate tool across providers", providers: []generation.ProjectProvider{
			&providerStub{descriptor: generation.ProviderDescriptor{ID: "first", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleRPCGo, Tool: plugin.DelegatedToolSpec{ID: "goctl", Version: "v1"}}}}},
			&providerStub{descriptor: generation.ProviderDescriptor{ID: "second", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleRPCGo, Tool: plugin.DelegatedToolSpec{ID: "goctl", Version: "v1"}}}}},
		}, reason: "provider_tool_duplicate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := generation.New(generation.Options{Providers: test.providers})
			if err == nil {
				t.Fatal("New() error = nil")
			}
			projected := protocol.Project(err)
			if projected.Code != "provider_invalid" || projected.Domain != "nexactl.generation" || projected.Category != protocol.CategoryInput {
				t.Fatalf("error = %#v", projected)
			}
			var details struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(projected.Details, &details); err != nil || details.Reason != test.reason {
				t.Fatalf("details = %#v, error = %v", details, err)
			}
		})
	}
}

func TestCommandRejectsMissingAndDuplicateServices(t *testing.T) {
	tests := []struct {
		name     string
		project  generation.Project
		code     string
		reason   string
		category protocol.Category
	}{
		{name: "missing", project: generation.Project{}, code: "fact_source_missing", reason: "service_missing", category: protocol.CategoryInput},
		{name: "duplicate", project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "accounts"}, {ServiceID: "accounts"}}}, code: "fact_source_invalid", reason: "service_duplicate", category: protocol.CategoryInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{&providerStub{
				descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0"},
				project:    test.project,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			envelope, stderr, exit := executeGenerationPlugin(t, candidate,
				"generation", "crud", "plan",
				"--repo-root", t.TempDir(), "--provider", "consumer", "--service", "accounts", "--json",
			)
			if exit != 3 || stderr != "" || envelope.OK || envelope.Error == nil || envelope.Error.Code != test.code || envelope.Error.Category != test.category {
				t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
			}
			var details struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(envelope.Error.Details, &details); err != nil || details.Reason != test.reason {
				t.Fatalf("details = %#v, error = %v", details, err)
			}
		})
	}
}

type providerStub struct {
	descriptor generation.ProviderDescriptor
	project    generation.Project
	resolves   int
}

func declaredProviderTool(role generation.ToolRole, tool toolchain.Tool) generation.ProviderTool {
	return generation.ProviderTool{
		Role: role,
		Tool: plugin.DelegatedToolSpec{ID: tool.ID, Version: tool.Version, Inputs: []string{"typed generation facts"}, Writes: []string{"staging"}},
	}
}

type recordingRunner struct {
	requests []toolchain.Request
	err      error
	inspect  func(toolchain.Request)
}

func (r *recordingRunner) Run(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
	copy := request
	copy.Args = append([]string(nil), request.Args...)
	copy.Stdin = append([]byte(nil), request.Stdin...)
	r.requests = append(r.requests, copy)
	if r.inspect != nil {
		r.inspect(request)
	}
	return toolchain.Result{}, r.err
}

func (p *providerStub) Descriptor() generation.ProviderDescriptor { return p.descriptor }
func (p *providerStub) Resolve(context.Context, string) (generation.Project, error) {
	p.resolves++
	return p.project, nil
}

func executeGenerationPlugin(t *testing.T, candidate plugin.Plugin, args ...string) (protocol.Envelope, string, int) {
	t.Helper()
	composed, err := host.New(host.Options{
		Version: "v0.0.0-test",
		OperationIDs: protocol.OperationIDGeneratorFunc(func() (string, error) {
			return fixedOperationID, nil
		}),
	}, candidate)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	exit := composed.Execute(context.Background(), args, &stdout, &stderr)
	var envelope protocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope, stderr.String(), exit
}

func assertJSONSemanticEqual(t *testing.T, actual, expected []byte) {
	t.Helper()
	var actualValue, expectedValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		t.Fatalf("decode actual schema: %v", err)
	}
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		t.Fatalf("decode expected schema: %v", err)
	}
	if !reflect.DeepEqual(actualValue, expectedValue) {
		t.Fatalf("schema = %s, want semantic equality with %s", actual, expected)
	}
}
