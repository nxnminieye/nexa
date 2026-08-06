package generation_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

func TestEntProtoGenerationMaterializesGeneratedProto(t *testing.T) {
	repository := entProtoFixtureRoot(t)
	generatedScope := "test-generated/ent-proto"
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repository, filepath.FromSlash("test-generated"))) })
	provider := testProvider{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0"}, project: generation.Project{Services: []generation.ServiceProject{{
		ServiceID: "account",
		EntProto: &generation.EntProtoProject{
			SchemaDir: "schema", ProtoPackage: "account.v1", GoPackage: "example.com/acme/account/v1;accountv1",
			GeneratedScope: generatedScope, GeneratedFile: "account.proto",
		},
	}}}}
	command := generationCommand(t, provider, nil, "ent-proto")
	if _, err := command.Run(context.Background(), entProtoInvocation(repository)); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(repository, filepath.FromSlash(generatedScope), "account.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `// @nexa $contract: "nexa.dev/source-comment/v1"`) || !strings.Contains(string(generated), "message Account {") {
		t.Fatalf("generated Proto is incomplete:\n%s", generated)
	}
}

func TestEntProtoCheckLeavesRepositoryUnchanged(t *testing.T) {
	repository := entProtoFixtureRoot(t)
	generatedScope := "test-generated/ent-proto-check"
	marker := filepath.Join(repository, filepath.FromSlash(generatedScope), "marker.txt")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repository, filepath.FromSlash("test-generated"))) })
	provider := testProvider{descriptor: generation.ProviderDescriptor{ID: "consumer", Version: "v1.0.0"}, project: generation.Project{Services: []generation.ServiceProject{{
		ServiceID: "account",
		EntProto: &generation.EntProtoProject{
			SchemaDir: "schema", ProtoPackage: "account.v1", GoPackage: "example.com/acme/account/v1;accountv1",
			GeneratedScope: generatedScope, GeneratedFile: "account.proto",
		},
	}}}}
	candidate, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{provider}})
	if err != nil {
		t.Fatal(err)
	}
	var check plugin.CommandSpec
	for _, command := range candidate.Spec().Commands {
		if strings.Join(command.Path, " ") == "generation ent-proto check" {
			check = command
			break
		}
	}
	if check.Run == nil {
		t.Fatal("generation ent-proto check command is missing")
	}
	if _, err := check.Run(context.Background(), entProtoInvocation(repository)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "unchanged" {
		t.Fatalf("check changed repository: %q, %v", data, err)
	}
}

func entProtoFixtureRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "generation", "ent-consumer"))
}

func entProtoInvocation(repository string) plugin.Invocation {
	return plugin.Invocation{Flags: map[string]any{"repo-root": repository, "provider": "consumer", "service": "account"}}
}
