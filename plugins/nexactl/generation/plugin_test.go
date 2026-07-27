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

	"github.com/nxnminieye/nexa/generation/httpapi"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

func TestNewExposesOnlyDirectRPCAndAPIGeneration(t *testing.T) {
	provider := testProvider{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{
		{Role: generation.ToolRoleRPCGo, Tool: delegated("consumer.rpc")},
		{Role: generation.ToolRoleAPIGo, Tool: delegated("consumer.api")},
	}}}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}, Runner: &testRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	spec := candidate.Spec()
	if !reflect.DeepEqual(spec.Descriptor.Provides, []plugin.Capability{{ID: "generation.rpc", Version: "v1.0.0"}, {ID: "generation.api", Version: "v1.0.0"}}) {
		t.Fatalf("capabilities = %#v", spec.Descriptor.Provides)
	}
	paths := make([]string, len(spec.Commands))
	for index, command := range spec.Commands {
		paths[index] = strings.Join(command.Path, " ")
		if command.SideEffect != plugin.SideEffectRepositoryWrite || len(command.Flags) != 3 || len(command.DelegatedTools) != 1 {
			t.Fatalf("command = %#v", command)
		}
	}
	if !reflect.DeepEqual(paths, []string{"generation rpc generate", "generation api generate"}) {
		t.Fatalf("commands = %#v", paths)
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
	if got.Status != "generated" || got.GeneratedScope != "generated/rpc" {
		t.Fatalf("result = %#v", got)
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
}

func (r *testRunner) Run(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
	if r.writePath != "" {
		if err := os.WriteFile(filepath.Join(request.RepositoryRoot, filepath.FromSlash(r.writePath)), r.writeData, 0o644); err != nil {
			return toolchain.Result{}, err
		}
	}
	return toolchain.Result{ToolID: request.Tool.ID, Version: request.Tool.Version, ExecutableVersion: request.Tool.Probe.ExpectedVersion, ExitCode: r.exitCode}, nil
}

func delegated(id string) plugin.DelegatedToolSpec {
	return plugin.DelegatedToolSpec{ID: id, Version: "v1.0.0", Inputs: []string{"typed-facts", "repository"}, Writes: []string{"repository"}}
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
	mustWrite(t, filepath.Join(repository, "sample.api"), []byte("syntax = \"v1\"\ninfo (nexaContractVersion: \"nexa.dev/http-api/v1\")\ntype Request {}\ntype Response { OK bool }\n@server (nexaOperationId: \"sample.get\" nexaAuthMode: \"none\")\nservice sample-api { @handler sample get /sample (Request) returns (Response) }\n"))
	document, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: repository, EntryFile: "sample.api"})
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
