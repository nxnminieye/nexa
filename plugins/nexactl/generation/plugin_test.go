package generation_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/composition"
	genfrontend "github.com/nxnminieye/nexa/generation/frontend"
	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
	"github.com/nxnminieye/nexa/provenance"
)

func TestNewExposesDirectRPCAPIAndFrontendGeneration(t *testing.T) {
	provider := testProvider{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{
		{Role: generation.ToolRoleRPCGo, Tool: delegated("consumer.rpc")},
		{Role: generation.ToolRoleAPIGo, Tool: delegated("consumer.api")},
		{Role: generation.ToolRoleFrontendRender, Tool: frontendDelegated("consumer.frontend")},
	}}}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}, Runner: &testRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	spec := candidate.Spec()
	if !reflect.DeepEqual(spec.Descriptor.Provides, []plugin.Capability{{ID: "generation.rpc", Version: "v1.0.0"}, {ID: "generation.api", Version: "v1.0.0"}, {ID: "generation.frontend", Version: "v1.0.0"}}) {
		t.Fatalf("capabilities = %#v", spec.Descriptor.Provides)
	}
	paths := make([]string, len(spec.Commands))
	for index, command := range spec.Commands {
		paths[index] = strings.Join(command.Path, " ")
		wantFlags := 4
		if command.Path[1] == "frontend" {
			wantFlags = 3
		}
		if command.SideEffect != plugin.SideEffectRepositoryWrite || len(command.Flags) != wantFlags || len(command.DelegatedTools) != 1 {
			t.Fatalf("command = %#v", command)
		}
	}
	if !reflect.DeepEqual(paths, []string{"generation rpc generate", "generation api generate", "generation frontend generate"}) {
		t.Fatalf("commands = %#v", paths)
	}
}

func TestFrontendGenerationReplacesStaleTreeAndPreservesExtensions(t *testing.T) {
	repository := t.TempDir()
	mustWrite(t, filepath.Join(repository, "generated/frontend/stale.ts"), []byte("stale"))
	extension := []byte("export const manual = true;\n")
	mustWrite(t, filepath.Join(repository, "extensions/frontend/manual.ts"), extension)
	document := frontendDocument(t, repository)
	tool := directTool("consumer.frontend")
	lockDigest := provenance.SHA256([]byte("frontend source lock"))
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleFrontendRender, Tool: frontendDelegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", Frontend: &generation.FrontendProject{
			Facts: document, Tool: tool, GeneratedScope: "generated/frontend", ExtensionScopes: []string{"extensions/frontend"}, FrontendSourceLockDigest: lockDigest,
		}}}},
	}
	runner := &testRunner{}
	command := generationCommand(t, provider, runner, "frontend")
	result, err := command.Run(context.Background(), frontendInvocation(repository))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(repository, "generated/frontend"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty frontend tree = %#v, %v", entries, err)
	}
	if data, err := os.ReadFile(filepath.Join(repository, "extensions/frontend/manual.ts")); err != nil || !reflect.DeepEqual(data, extension) {
		t.Fatalf("extension changed: %q %v", data, err)
	}
	wantArgs := []string{"render", "--service", "sample", "--generated-scope", "generated/frontend"}
	if !reflect.DeepEqual(runner.request.Args, wantArgs) {
		t.Fatalf("delegated args = %#v, want %#v", runner.request.Args, wantArgs)
	}
	wantStdin, err := genfrontend.CanonicalRenderRequest(genfrontend.RenderRequest{
		FrontendIR: document, RepositoryRoot: runner.request.RepositoryRoot, GeneratedScope: "generated/frontend",
		ExtensionScopes: []string{"extensions/frontend"}, FrontendSourceLockDigest: lockDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.request.Stdin, wantStdin) {
		t.Fatal("delegated frontend stdin is not canonical FrontendRenderRequest")
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), `"userLogic":[]`) {
		t.Fatalf("frontend result = %s, %v", encoded, err)
	}
}

func TestFrontendGenerationRejectsNonEmptyOutputForEmptyPageSet(t *testing.T) {
	repository := t.TempDir()
	document := frontendDocument(t, repository)
	tool := directTool("consumer.frontend")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleFrontendRender, Tool: frontendDelegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", Frontend: &generation.FrontendProject{
			Facts: document, Tool: tool, GeneratedScope: "generated/frontend", FrontendSourceLockDigest: provenance.SHA256([]byte("lock")),
		}}}},
	}
	runner := &testRunner{writePath: "generated/frontend/unexpected.ts", writeData: []byte("unexpected")}
	command := generationCommand(t, provider, runner, "frontend")
	if _, err := command.Run(context.Background(), frontendInvocation(repository)); err == nil {
		t.Fatal("non-empty output accepted for empty page set")
	}
	if data, err := os.ReadFile(filepath.Join(repository, "generated/frontend/unexpected.ts")); err != nil || string(data) != "unexpected" {
		t.Fatalf("partial output was not preserved: %q, %v", data, err)
	}
}

func TestFrontendGenerationValidatesRequestBeforeReplacingTree(t *testing.T) {
	repository := t.TempDir()
	stale := filepath.Join(repository, "generated/frontend/stale.ts")
	mustWrite(t, stale, []byte("stale"))
	document := frontendDocument(t, repository)
	tool := directTool("consumer.frontend")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleFrontendRender, Tool: frontendDelegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", Frontend: &generation.FrontendProject{
			Facts: document, Tool: tool, GeneratedScope: "generated/frontend/../frontend", FrontendSourceLockDigest: provenance.SHA256([]byte("lock")),
		}}}},
	}
	command := generationCommand(t, provider, &testRunner{}, "frontend")
	if _, err := command.Run(context.Background(), frontendInvocation(repository)); err == nil {
		t.Fatal("non-canonical generated scope accepted")
	}
	if data, err := os.ReadFile(stale); err != nil || string(data) != "stale" {
		t.Fatalf("tree changed before request validation: %q, %v", data, err)
	}
}

func TestFrontendProviderRequiresExactDelegatedMetadata(t *testing.T) {
	_, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{testProvider{descriptor: generation.ProviderDescriptor{
		ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleFrontendRender, Tool: delegated("consumer.frontend")}},
	}}}})
	if err == nil {
		t.Fatal("frontend delegated tool with generic metadata accepted")
	}
}

func TestAPIProviderRequiresExactDelegatedMetadata(t *testing.T) {
	provider := testProvider{descriptor: generation.ProviderDescriptor{
		ID:      "consumer",
		Version: "v1.0.0",
		Tools: []generation.ProviderTool{{Role: generation.ToolRoleAPIGo, Tool: plugin.DelegatedToolSpec{
			ID: "consumer.api", Version: "v1.0.0", Inputs: []string{"typed-facts", "repository"}, Writes: []string{"repository"},
		}}},
	}}
	_, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}})
	if err == nil {
		t.Fatal("API delegated tool with serialized typed-facts metadata accepted")
	}
}

func TestDirectRPCGenerationReplacesStaleTreeAndPreservesExtensions(t *testing.T) {
	repository := t.TempDir()
	mustWrite(t, filepath.Join(repository, "generated/rpc/stale.go"), []byte("stale"))
	extension := []byte("package extension\n\nconst Value = \"manual\"\n")
	mustWrite(t, filepath.Join(repository, "extensions/rpc/manual.go"), extension)
	document := rpcDocument(t)
	tool := directTool("consumer.rpc")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleRPCGo, Tool: delegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", RPC: &generation.RPCProject{
			Facts: document, Tool: tool, GeneratedScope: "generated/rpc", ExtensionScopes: []string{"extensions/rpc"},
		}}}},
	}
	runner := &testRunner{writePath: "generated/rpc/sample.go", writeData: []byte("package rpcgenerated\n")}
	command := generationCommand(t, provider, runner, "rpc")
	result, err := command.Run(context.Background(), invocation(repository))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repository, "generated/rpc/stale.go")); !os.IsNotExist(err) {
		t.Fatalf("stale file remains: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(repository, "extensions/rpc/manual.go")); err != nil || !reflect.DeepEqual(data, extension) {
		t.Fatalf("extension changed: %q %v", data, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		APIVersion     string `json:"apiVersion"`
		Kind           string `json:"kind"`
		Status         string `json:"status"`
		Service        string `json:"service"`
		GeneratedScope string `json:"generatedScope"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.APIVersion != "nexa.dev/generation-result/v2" || got.Status != "generated" || got.GeneratedScope != "generated/rpc" {
		t.Fatalf("result = %#v", got)
	}
	wantArgs := []string{"generate", "--service", "sample", "--generated-scope", "generated/rpc"}
	if !reflect.DeepEqual(runner.request.Args, wantArgs) {
		t.Fatalf("delegated args = %#v, want %#v", runner.request.Args, wantArgs)
	}
	wantStdin, err := genprotocol.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.request.Stdin, wantStdin) {
		t.Fatal("delegated RPC stdin is not canonical ProtocolIR")
	}
}

func TestDirectAPIGenerationPassesPreparedScopeAndCanonicalFacts(t *testing.T) {
	repository := t.TempDir()
	tool := directTool("consumer.api")
	apiDocument(t, repository)
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleAPIGo, Tool: delegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", API: &generation.APIProject{
			EntryFile: "sample.api", Tool: tool, GeneratedScope: "generated/api", ExtensionScopes: []string{"extensions/api"},
		}}}},
	}
	runner := &testRunner{writePath: "generated/api/sample.go", writeData: []byte("package apigenerated\n")}
	command := generationCommand(t, provider, runner, "api")
	if _, err := command.Run(context.Background(), invocation(repository)); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"generate", "--service", "sample", "--entry-file", "sample.api", "--generated-scope", "generated/api"}
	if !reflect.DeepEqual(runner.request.Args, wantArgs) {
		t.Fatalf("delegated args = %#v, want %#v", runner.request.Args, wantArgs)
	}
	if len(runner.request.Stdin) != 0 {
		t.Fatal("delegated API received serialized HTTP API facts")
	}
}

func TestDirectAPIGenerationRejectsInvalidSourceBeforeReplacingTree(t *testing.T) {
	repository := t.TempDir()
	stale := filepath.Join(repository, "generated/api/stale.go")
	mustWrite(t, stale, []byte("stale"))
	apiDocument(t, repository)
	mustWrite(t, filepath.Join(repository, "sample.api"), []byte("syntax = \"v1\"\ntype Broken {\n"))
	tool := directTool("consumer.api")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleAPIGo, Tool: delegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", API: &generation.APIProject{
			EntryFile: "sample.api", Tool: tool, GeneratedScope: "generated/api",
		}}}},
	}
	command := generationCommand(t, provider, &testRunner{}, "api")
	if _, err := command.Run(context.Background(), invocation(repository)); err == nil {
		t.Fatal("invalid API source accepted")
	}
	if data, err := os.ReadFile(stale); err != nil || string(data) != "stale" {
		t.Fatalf("tree changed before API source validation: %q, %v", data, err)
	}
}

func TestDirectAPIGenerationRejectsSourceSymlinkBeforeReplacingTree(t *testing.T) {
	repository := t.TempDir()
	stale := filepath.Join(repository, "generated/api/stale.go")
	mustWrite(t, stale, []byte("stale"))
	target := filepath.Join(t.TempDir(), "sample.api")
	apiDocument(t, filepath.Dir(target))
	if err := os.Symlink(target, filepath.Join(repository, "sample.api")); err != nil {
		t.Fatal(err)
	}
	tool := directTool("consumer.api")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleAPIGo, Tool: delegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", API: &generation.APIProject{
			EntryFile: "sample.api", Tool: tool, GeneratedScope: "generated/api",
		}}}},
	}
	command := generationCommand(t, provider, &testRunner{}, "api")
	if _, err := command.Run(context.Background(), invocation(repository)); err == nil {
		t.Fatal("API source symlink accepted")
	}
	if data, err := os.ReadFile(stale); err != nil || string(data) != "stale" {
		t.Fatalf("tree changed before API source symlink validation: %q, %v", data, err)
	}
}

func TestDirectGenerationSkipsExistingLogicAndOverwritesOnlyDeclaredTarget(t *testing.T) {
	repository := t.TempDir()
	tool := directTool("consumer.rpc")
	logic := []byte("package logic\n\nconst Value = 1\n")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleRPCGo, Tool: delegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", RPC: &generation.RPCProject{
			Facts: rpcDocument(t), Tool: tool, GeneratedScope: "generated/rpc", UserLogic: []generation.UserLogicFile{{Path: "logic/sample.go", Content: logic}},
		}}}},
	}
	path := filepath.Join(repository, "logic/sample.go")
	other := filepath.Join(repository, "logic/other.go")
	mustWrite(t, path, []byte("package logic\n\nconst Value = 2\n"))
	mustWrite(t, other, []byte("package logic\n\nconst Other = 3\n"))
	runner := &testRunner{writePath: "generated/rpc/sample.go", writeData: []byte("package rpcgenerated\n")}
	command := generationCommand(t, provider, runner, "rpc")
	result, err := command.Run(context.Background(), invocation(repository))
	if err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "package logic\n\nconst Value = 2\n" {
		t.Fatalf("default generation changed logic: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(other); readErr != nil || string(data) != "package logic\n\nconst Other = 3\n" {
		t.Fatalf("default generation changed unrelated logic: %q, %v", data, readErr)
	}
	encoded, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(encoded), `"action":"skipped"`) {
		t.Fatalf("skip result = %s, %v", encoded, err)
	}
	overwrite := invocation(repository)
	overwrite.Flags["overwrite-logic"] = true
	if _, err := command.Run(context.Background(), overwrite); err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != string(logic) {
		t.Fatalf("explicit overwrite content: %q, %v", data, readErr)
	}
	if data, readErr := os.ReadFile(other); readErr != nil || string(data) != "package logic\n\nconst Other = 3\n" {
		t.Fatalf("explicit overwrite changed unrelated logic: %q, %v", data, readErr)
	}
}

func TestDirectGenerationFailureKeepsPartialChanges(t *testing.T) {
	repository := t.TempDir()
	mustWrite(t, filepath.Join(repository, "generated/rpc/stale.go"), []byte("stale"))
	tool := directTool("consumer.rpc")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleRPCGo, Tool: delegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", RPC: &generation.RPCProject{
			Facts: rpcDocument(t), Tool: tool, GeneratedScope: "generated/rpc",
		}}}},
	}
	runner := &testRunner{writePath: "generated/rpc/partial.go", writeData: []byte("package partial\n"), exitCode: 9}
	command := generationCommand(t, provider, runner, "rpc")
	if _, err := command.Run(context.Background(), invocation(repository)); err == nil {
		t.Fatal("generation succeeded")
	}
	if data, err := os.ReadFile(filepath.Join(repository, "generated/rpc/partial.go")); err != nil || string(data) != "package partial\n" {
		t.Fatalf("partial change was rolled back: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(repository, "generated/rpc/stale.go")); !os.IsNotExist(err) {
		t.Fatalf("stale tree was restored: %v", err)
	}
}

func generationCommand(t *testing.T, provider testProvider, runner toolchain.Runner, family string) plugin.CommandSpec {
	t.Helper()
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range candidate.Spec().Commands {
		if command.Path[1] == family {
			return command
		}
	}
	t.Fatalf("missing command %s", family)
	return plugin.CommandSpec{}
}

func invocation(repository string) plugin.Invocation {
	return plugin.Invocation{Flags: map[string]any{"repo-root": repository, "provider": "consumer", "service": "sample", "overwrite-logic": false}}
}

func frontendInvocation(repository string) plugin.Invocation {
	return plugin.Invocation{Flags: map[string]any{"repo-root": repository, "provider": "consumer", "service": "sample"}}
}

type testProvider struct {
	descriptor generation.ProviderDescriptor
	project    generation.Project
}

func (p testProvider) Descriptor() generation.ProviderDescriptor { return p.descriptor }
func (p testProvider) Resolve(context.Context, string) (generation.Project, error) {
	return p.project, nil
}

type testRunner struct {
	writePath string
	writeData []byte
	exitCode  int
	request   toolchain.Request
}

func (r *testRunner) Run(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
	r.request = request
	if r.writePath != "" {
		if err := os.WriteFile(filepath.Join(request.RepositoryRoot, filepath.FromSlash(r.writePath)), r.writeData, 0o644); err != nil {
			return toolchain.Result{}, err
		}
	}
	return toolchain.Result{ToolID: request.Tool.ID, Version: request.Tool.Version, ExecutableVersion: request.Tool.Probe.ExpectedVersion, ExitCode: r.exitCode}, nil
}

func delegated(id string) plugin.DelegatedToolSpec {
	inputs := []string{"typed-facts", "repository"}
	if strings.HasSuffix(id, ".api") {
		inputs = []string{generation.APISourceInput, "repository"}
	}
	return plugin.DelegatedToolSpec{ID: id, Version: "v1.0.0", Inputs: inputs, Writes: []string{"repository"}}
}

func frontendDelegated(id string) plugin.DelegatedToolSpec {
	return plugin.DelegatedToolSpec{ID: id, Version: "v1.0.0", Inputs: []string{"nexa.dev/frontend-renderer/v1", "frontend-ir", "repository"}, Writes: []string{"repository"}}
}

func directTool(id string) toolchain.Tool {
	return toolchain.Tool{ID: id, Version: "v1.0.0", Executable: "/consumer/tool", InputScopes: []string{"repository"}, WriteScopes: []string{"repository"}, Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "tool-v1"}}
}

type protoResolver map[string]string

func (r protoResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r[path])), nil
}

func rpcDocument(t *testing.T) genprotocol.Document {
	t.Helper()
	document, err := genprotocol.Compile(context.Background(), genprotocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{"sample.proto"}, Resolver: protoResolver{"sample.proto": `syntax = "proto3"; package sample.v1; message Request {} message Response {} service Sample { rpc Get(Request) returns (Response); }`}})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func apiDocument(t *testing.T, repository string) httpapi.Document {
	t.Helper()
	mustWrite(t, filepath.Join(repository, "sample.api"), []byte("// @nexa $contract: \"nexa.dev/source-comment/v1\"\nsyntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-convention/v1\")\ntype Request {}\ntype Response { OK bool }\nservice sample-api {\n  // @nexa auth: \"none\"\n  @handler Sample\n  get /sample (Request) returns (Response)\n}\n"))
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: repository, EntryFile: "sample.api"})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func frontendDocument(t *testing.T, repository string) genfrontend.Document {
	t.Helper()
	closure, err := composition.FrontendClosure(apiDocument(t, repository))
	if err != nil {
		t.Fatal(err)
	}
	document, err := genfrontend.Build(closure, nil)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
