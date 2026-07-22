package integration_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nxnminieye/nexa/internal/adoption"
)

func TestAdoptionLocalConsumer(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), ".."))
	for _, fixture := range []string{"neutral", "backend-only"} {
		t.Run(fixture, func(t *testing.T) {
			result, err := adoption.RunConsumer(context.Background(), adoption.ConsumerRequest{
				FixtureRoot:   filepath.Join(repositoryRoot, "integration", "testdata", "adoption", fixture),
				FrameworkRoot: repositoryRoot,
				WorkRoot:      filepath.Join(t.TempDir(), "consumer"),
				Checks:        []adoption.Check{adoption.CheckTest, adoption.CheckBuild, adoption.CheckVet},
			})
			if err != nil {
				t.Fatalf("RunConsumer() error = %v; result=%#v", err, result)
			}
			if len(result.Commands) != 4 {
				t.Fatalf("commands = %#v", result.Commands)
			}
		})
	}
}
