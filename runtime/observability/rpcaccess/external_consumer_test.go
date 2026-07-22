package rpcaccess_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaseConsumerBuildsAndRunsWithoutOTelDependency(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("repository root: %v", err)
	}
	consumer := t.TempDir()
	goMod := "module example.com/rpcconsumer\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\nreplace github.com/nxnminieye/nexa => " + repositoryRoot + "\n"
	mainSource := `package main

import (
	"context"
	"io"
	"log/slog"

	"github.com/nxnminieye/nexa/runtime/observability/rpcaccess"
	"google.golang.org/grpc"
)

func main() {
	interceptor, err := rpcaccess.UnaryServerInterceptor(rpcaccess.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Extractor: rpcaccess.ExtractorFunc(func(context.Context, string) (rpcaccess.RequestContext, error) {
			return rpcaccess.RequestContext{}, nil
		}),
	})
	if err != nil {
		panic(err)
	}
	response, err := interceptor(context.Background(), "request", &grpc.UnaryServerInfo{FullMethod: "/sample.Service/Get"}, func(context.Context, any) (any, error) {
		return "response", nil
	})
	if err != nil || response != "response" {
		panic("unexpected interceptor result")
	}
}
`
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "main.go"), []byte(mainSource), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	runGo(t, consumer, "run", "-mod=mod", ".")
	dependencies := runGo(t, consumer, "list", "-mod=mod", "-deps", ".")
	for _, dependency := range strings.Fields(dependencies) {
		if dependency == "go.opentelemetry.io/otel" || strings.HasPrefix(dependency, "go.opentelemetry.io/otel/") {
			t.Fatalf("base consumer linked optional dependency %q", dependency)
		}
	}
}

func runGo(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOPROXY=off", "GOSUMDB=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
