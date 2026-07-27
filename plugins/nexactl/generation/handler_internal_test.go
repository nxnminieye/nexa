package generation

import (
	"context"
	"testing"

	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/plugin"
)

func TestDirectEnvironmentRejectsScratchValues(t *testing.T) {
	runner := &commandRunner{}
	_, err := runner.environment(toolchain.Tool{Environment: []toolchain.EnvironmentRule{{Name: "CACHE", Source: toolchain.EnvironmentScratch}}})
	if err == nil {
		t.Fatal("scratch environment accepted")
	}
}

func TestResolveRejectsMissingProviderBeforeProjectAccess(t *testing.T) {
	runner := &commandRunner{providers: map[string]ProjectProvider{}}
	_, _, _, err := runner.resolve(context.Background(), invocationForInternalTest(t.TempDir()))
	if err == nil {
		t.Fatal("missing provider accepted")
	}
}

func invocationForInternalTest(repository string) pluginInvocation {
	return plugin.Invocation{Flags: map[string]any{"repo-root": repository, "provider": "missing", "service": "sample"}}
}

type pluginInvocation = plugin.Invocation
