package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	generationplugin "github.com/nxnminieye/nexa/plugins/nexactl/generation"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

func TestDG2TypedHTTPProjectionExternalConsumer(t *testing.T) {
	root := canonicalIntegrationDirectory(t, t.TempDir())
	consumer := filepath.Join(root, "consumer")
	for _, directory := range []string{"backend/account/desc", "backend/core/desc", "generated", "extensions"} {
		if err := os.MkdirAll(filepath.Join(consumer, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeDG2File(t, consumer, "backend/account/desc/account.proto", dg2ProtocolSource)
	writeDG2File(t, consumer, "backend/core/desc/core.api", dg2NativeAPI)
	writeDG2File(t, consumer, "generated/stale.go", "package stale\n")
	const extension = "package extensions\n\nconst Manual = true\n"
	writeDG2File(t, consumer, "extensions/manual.go", extension)

	protocolDocument, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID: "account", EntryFiles: []string{"backend/account/desc/account.proto"},
		Resolver: dg2ProtocolResolver{consumer: consumer, framework: repositoryRoot(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := servicecatalog.Parse("services.yaml", []byte(fmt.Sprintf(dg2Catalog, composition.CapabilityID, composition.CapabilityVersion)))
	if err != nil {
		t.Fatal(err)
	}
	native, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: consumer, EntryFile: "backend/core/desc/core.api"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := composition.Build(catalog, []protocol.Document{protocolDocument}, native, composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/dg2-consumer"})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	rendered := map[string]bool{}
	for _, artifact := range artifacts {
		switch artifact.ID {
		case "api.account":
			if parsed, parseErr := goctlparser.Parse("account.generated.api", artifact.Content); parseErr != nil || parsed.Validate() != nil {
				t.Fatalf("parse rendered API: %v", parseErr)
			}
			rendered[artifact.ID] = true
		case "client.account":
			rendered[artifact.ID] = true
		}
	}
	if !rendered["api.account"] || !rendered["client.account"] {
		t.Fatalf("composition.Render artifacts = %#v", rendered)
	}

	environment := overriddenEnvironment(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "GOCACHE="+filepath.Join(root, "gocache"), "TMPDIR="+filepath.Join(root, "tmp"))
	for _, directory := range []string{filepath.Join(root, "gocache"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	renderer := filepath.Join(root, "renderer")
	runGenerationConsumerCommand(t, repositoryRoot(t), environment, "go", "build", "-mod=readonly", "-o", renderer, "./integration/testdata/dg2_typed_http_projection/renderer")
	assertDG2RendererRejectsInvalidSource(t, renderer, environment, root)
	provider := dg2GenerationProvider{renderer: renderer}
	buildPlugin, err := generationplugin.New(generationplugin.Options{Providers: []generationplugin.ProjectProvider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	cli, err := host.New(host.Options{Name: "dg2-consumer", Version: "v1.0.0"}, buildPlugin)
	if err != nil {
		t.Fatal(err)
	}
	runDG2Generation(t, cli, consumer)
	if _, err := os.Stat(filepath.Join(consumer, "generated", "stale.go")); !os.IsNotExist(err) {
		t.Fatalf("stale generated file remains: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(consumer, "extensions", "manual.go")); err != nil || string(content) != extension {
		t.Fatalf("extension changed: %q %v", content, err)
	}
	if parsed, err := goctlparser.Parse(filepath.Join(consumer, "generated", "account.generated.api"), nil); err != nil || parsed.Validate() != nil {
		t.Fatalf("parse delegated API: %v", err)
	}
	writeDG2File(t, consumer, "go.mod", "module example.com/dg2-consumer\n\ngo 1.25.0\n")
	writeDG2File(t, consumer, "clienttest/client_test.go", "package clienttest\n\nimport (\n  \"testing\"\n  generated \"example.com/dg2-consumer/generated\"\n)\n\nfunc TestAPISourceIdentity(t *testing.T) {\n  if generated.APISourceEntry != \"backend/core/desc/core.api\" {\n    t.Fatalf(\"API source entry = %q\", generated.APISourceEntry)\n  }\n}\n")
	runGenerationConsumerCommand(t, consumer, environment, "go", "test", "./...")
	runGenerationConsumerCommand(t, consumer, environment, "git", "init", "-q")
	runGenerationConsumerCommand(t, consumer, environment, "git", "config", "user.name", "Nexa Test")
	runGenerationConsumerCommand(t, consumer, environment, "git", "config", "user.email", "nexa-test@example.invalid")
	runGenerationConsumerCommand(t, consumer, environment, "git", "add", ".")
	runGenerationConsumerCommand(t, consumer, environment, "git", "commit", "-qm", "expected DG2 projection")
	runDG2Generation(t, cli, consumer)
	runGenerationConsumerCommand(t, consumer, environment, "git", "diff", "--exit-code")
}

type dg2GenerationProvider struct {
	renderer string
}

func (provider dg2GenerationProvider) Descriptor() generationplugin.ProviderDescriptor {
	return generationplugin.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generationplugin.ProviderTool{{Role: generationplugin.ToolRoleAPIGo, Tool: plugin.DelegatedToolSpec{ID: "consumer.api", Version: "v1.0.0", Inputs: []string{generationplugin.APISourceInput, "repository"}, Writes: []string{"repository"}}}}}
}

func (provider dg2GenerationProvider) Resolve(context.Context, string) (generationplugin.Project, error) {
	tool := toolchain.Tool{ID: "consumer.api", Version: "v1.0.0", Executable: provider.renderer, Args: []string{"api"}, InputScopes: []string{"repository"}, WriteScopes: []string{"repository"}, Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "dg2-api-source-renderer v1.0.0"}}
	return generationplugin.Project{Services: []generationplugin.ServiceProject{{ServiceID: "core", API: &generationplugin.APIProject{EntryFile: "backend/core/desc/core.api", Tool: tool, GeneratedScope: "generated", ExtensionScopes: []string{"extensions"}}}}}, nil
}

func assertDG2RendererRejectsInvalidSource(t *testing.T, renderer string, environment []string, root string) {
	t.Helper()
	negativeRoot := filepath.Join(root, "negative-render")
	if err := os.MkdirAll(filepath.Join(negativeRoot, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDG2File(t, negativeRoot, "invalid.api", "this is not an api document\n")
	command := exec.Command(renderer, "api", "generate", "--service", "core", "--entry-file", "invalid.api", "--generated-scope", "generated")
	command.Dir = negativeRoot
	command.Env = environment
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("renderer accepted invalid .api source: %s", output)
	}
}

func runDG2Generation(t *testing.T, cli *host.Host, consumer string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := []string{"generation", "api", "generate", "--repo-root", consumer, "--provider", "consumer", "--service", "core", "--json"}
	if exit := cli.Execute(context.Background(), args, &stdout, &stderr); exit != 0 || stderr.Len() != 0 {
		t.Fatalf("DG2 generation: exit=%d stdout=%s stderr=%s", exit, stdout.Bytes(), stderr.Bytes())
	}
}

type dg2ProtocolResolver struct{ consumer, framework string }

func (resolver dg2ProtocolResolver) Open(_ context.Context, name string) (io.ReadCloser, error) {
	if name == "nexa/protocol/v1/options.proto" {
		return os.Open(filepath.Join(resolver.framework, "generation", "protocol", "nexa", "protocol", "v1", "options.proto"))
	}
	return os.Open(filepath.Join(resolver.consumer, filepath.FromSlash(name)))
}

func writeDG2File(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const dg2Catalog = `apiVersion: nexa.dev/service-catalog/v1
kind: ServiceCatalog
services:
  - id: core
    root: backend/core
    capabilityBindings: []
  - id: account
    root: backend/account
    capabilityBindings:
      - id: %s
        apiVersion: %s
`

const dg2NativeAPI = `syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-api/v1")
type HealthRequest {}
type HealthResponse { Ready bool }
@server (nexaOperationId: "core.health" nexaAuthMode: "none")
service core-api { @handler health get /health (HealthRequest) returns (HealthResponse) }
`

const dg2ProtocolSource = `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
enum State { STATE_UNSPECIFIED = 0; STATE_ACTIVE = 1; }
message Settings { string locale = 1; }
message Member { string id = 1; repeated string role_codes = 2; Settings settings = 3; }
message ReplaceRequest { repeated string role_codes = 1; repeated State states = 2; Settings settings = 3; repeated Member items = 4; }
message ReplaceResponse { int64 total = 1; repeated Member items = 2; }
service AccountService { rpc Replace(ReplaceRequest) returns (ReplaceResponse) {
  option (nexa.protocol.v1.http_proxy) = { operation_id: "account.replace" method: POST path: "/accounts/replace" auth: { mode: NONE }
    request_fields: { http_field: "roleCodes" rpc_field: "role_codes" }
    request_fields: { http_field: "states" rpc_field: "states" }
    request_fields: { http_field: "settings" rpc_field: "settings" }
    request_fields: { http_field: "items" rpc_field: "items" }
    response_fields: { rpc_field: "total" http_field: "total" }
    response_fields: { rpc_field: "items" http_field: "items" }
  };
} }
`
