package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"

	"github.com/bufbuild/protocompile"
	cliprotocol "github.com/nxnminieye/nexa/cli/protocol"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	generation "github.com/nxnminieye/nexa/plugins/nexactl/generation"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

const providerID = "consumer"

type consumerProvider struct{ helper string }

const (
	rpcLogicPath = "backend/account/internal/logic/account.go"
	apiLogicPath = "backend/core/internal/logic/health.go"
	rpcLogic     = "package logic\n\nconst AccountLogic = true\n"
	apiLogic     = "package logic\n\nconst HealthLogic = true\n"
)

func (provider consumerProvider) Descriptor() generation.ProviderDescriptor {
	return generation.ProviderDescriptor{ID: providerID, Version: "v1.0.0", Tools: []generation.ProviderTool{
		{Role: generation.ToolRoleRPCGo, Tool: delegated("consumer.rpc")},
		{Role: generation.ToolRoleAPIGo, Tool: delegated("consumer.api")},
	}}
}

func (provider consumerProvider) Resolve(ctx context.Context, repository string) (generation.Project, error) {
	rpc, err := genprotocol.Compile(ctx, genprotocol.CompileOptions{
		ServiceID: "account", EntryFiles: []string{"backend/account/desc/account.proto"}, Resolver: repositoryResolver(repository),
	})
	if err != nil {
		return generation.Project{}, err
	}
	return generation.Project{Services: []generation.ServiceProject{
		{ServiceID: "account", RPC: &generation.RPCProject{Facts: rpc, Tool: directTool("consumer.rpc", "rpc", provider.helper), GeneratedScope: "backend/account/generated", ExtensionScopes: []string{"backend/account/extensions"}, UserLogic: []generation.UserLogicFile{{Path: rpcLogicPath, Content: []byte(rpcLogic)}}}},
		{ServiceID: "core", API: &generation.APIProject{EntryFile: "backend/core/desc/core.api", Tool: directTool("consumer.api", "api", provider.helper), GeneratedScope: "backend/core/generated", ExtensionScopes: []string{"backend/core/extensions"}, UserLogic: []generation.UserLogicFile{{Path: apiLogicPath, Content: []byte(apiLogic)}}}},
	}}, nil
}

type repositoryResolver string

func (r repositoryResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(string(r), filepath.FromSlash(path)))
}

func delegated(id string) plugin.DelegatedToolSpec {
	inputs := []string{"typed facts", "repository"}
	if id == "consumer.api" {
		inputs = []string{generation.APISourceInput, "repository"}
	}
	return plugin.DelegatedToolSpec{ID: id, Version: "v1.0.0", Inputs: inputs, Writes: []string{"repository"}}
}

func directTool(id, family, helper string) toolchain.Tool {
	return toolchain.Tool{ID: id, Version: "v1.0.0", Executable: helper, Args: []string{family}, InputScopes: []string{"repository"}, WriteScopes: []string{"repository"}, Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "consumer-generation-helper v1.0.0"}}
}

func main() {
	repository := flag.String("repo-root", "", "consumer repository root")
	helper := flag.String("helper", "", "direct generation helper")
	overwrite := flag.Bool("overwrite-logic", false, "overwrite declared logic files")
	expectAction := flag.String("expect-action", "skipped", "expected user-logic result action")
	flag.Parse()
	if *repository == "" || *helper == "" {
		panic("repo-root and helper are required")
	}
	buildPlugin, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{consumerProvider{helper: *helper}}})
	if err != nil {
		panic(err)
	}
	cli, err := host.New(host.Options{Version: "v1.0.0"}, buildPlugin)
	if err != nil {
		panic(err)
	}
	rpcArgs := []string{"generation", "rpc", "generate", "--repo-root", *repository, "--provider", providerID, "--service", "account"}
	apiArgs := []string{"generation", "api", "generate", "--repo-root", *repository, "--provider", providerID, "--service", "core"}
	if *overwrite {
		rpcArgs = append(rpcArgs, "--overwrite-logic")
		apiArgs = append(apiArgs, "--overwrite-logic")
	}
	rpcArgs = append(rpcArgs, "--json")
	apiArgs = append(apiArgs, "--json")
	requireOK(cli, rpcArgs, *expectAction)
	requireOK(cli, apiArgs, *expectAction)
	verifyGenerated(*repository)
	fmt.Println("generation-consumer-ok")
}

func requireOK(cli *host.Host, args []string, want string) {
	var stdout, stderr bytes.Buffer
	exit := cli.Execute(context.Background(), args, &stdout, &stderr)
	var envelope cliprotocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || exit != 0 || !envelope.OK || stderr.Len() != 0 {
		panic(fmt.Sprintf("command %v failed: exit=%d envelope=%#v stderr=%q decode=%v", args, exit, envelope, stderr.String(), err))
	}
	resultBytes, err := json.Marshal(envelope.Result)
	if err != nil {
		panic(fmt.Sprintf("command %v result encode: %v", args, err))
	}
	var result struct {
		UserLogic []struct {
			Path   string `json:"path"`
			Action string `json:"action"`
		} `json:"userLogic"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil || len(result.UserLogic) != 1 {
		panic(fmt.Sprintf("command %v missing user logic result: %v %s", args, err, resultBytes))
	}
	if result.UserLogic[0].Action != want {
		panic(fmt.Sprintf("command %v user logic action = %q, want %q", args, result.UserLogic[0].Action, want))
	}
}

func verifyGenerated(repository string) {
	protoPath := filepath.Join(repository, "backend/account/generated/account.generated.proto")
	compiler := protocompile.Compiler{Resolver: &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{"account.generated.proto": string(read(protoPath))})}}
	if _, err := compiler.Compile(context.Background(), "account.generated.proto"); err != nil {
		panic(err)
	}
	apiPath := filepath.Join(repository, "backend/core/generated/core.generated.api")
	parsedAPI, err := goctlparser.Parse(apiPath, read(apiPath))
	if err != nil || parsedAPI.Validate() != nil {
		panic(fmt.Sprintf("generated API invalid: %v", err))
	}
	for _, name := range []string{"backend/account/generated/account.generated.go", "backend/core/generated/core.generated.go"} {
		if _, err := parser.ParseFile(token.NewFileSet(), name, read(filepath.Join(repository, filepath.FromSlash(name))), parser.AllErrors); err != nil {
			panic(err)
		}
	}
	for name, want := range map[string]string{
		"backend/account/extensions/manual.go": "package extensions\n\nconst ManualRPC = true\n",
		"backend/core/extensions/manual.go":    "package extensions\n\nconst ManualAPI = true\n",
	} {
		if string(read(filepath.Join(repository, filepath.FromSlash(name)))) != want {
			panic("extension changed: " + name)
		}
	}
}

func read(name string) []byte {
	data, err := os.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}
