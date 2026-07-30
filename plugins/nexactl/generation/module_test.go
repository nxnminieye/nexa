package generation_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

func TestDirectAPIGenerationConsumesTypedFacts(t *testing.T) {
	repository := t.TempDir()
	apiDocument(t, repository)
	tool := directTool("consumer.api")
	provider := testProvider{
		descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0", Tools: []generation.ProviderTool{{Role: generation.ToolRoleAPIGo, Tool: delegated(tool.ID)}}},
		project: generation.Project{Services: []generation.ServiceProject{{ServiceID: "sample", API: &generation.APIProject{
			EntryFile: "sample.api", Tool: tool, GeneratedScope: "generated/api", ExtensionScopes: []string{"extensions/api"},
		}}}},
	}
	runner := &testRunner{writePath: "generated/api/routes.go", writeData: []byte("package apigenerated\n")}
	command := generationCommand(t, provider, runner, "api")
	if _, err := command.Run(context.Background(), invocation(repository)); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(repository, "generated/api/routes.go")); err != nil || string(data) != "package apigenerated\n" {
		t.Fatalf("generated API = %q, %v", data, err)
	}
}
