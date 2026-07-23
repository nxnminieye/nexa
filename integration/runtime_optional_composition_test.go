package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimePackagesOptionalComposition(t *testing.T) {
	runRuntimeOptionalCompositionProof(t)
}

func runRuntimeOptionalCompositionProof(t *testing.T) {
	t.Helper()
	temporary := t.TempDir()
	moduleRoot := filepath.Join(temporary, "consumer")
	commandRoot := filepath.Join(moduleRoot, "cmd", "minimum")
	if err := os.MkdirAll(commandRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConsumerFile(t, filepath.Join(moduleRoot, "go.mod"), "module example.com/minimum-runtime\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n")
	writeConsumerFile(t, filepath.Join(commandRoot, "main.go"), minimumRuntimeConsumerProgram)
	environment := prepareHermeticExternalModule(t, temporary, moduleRoot)
	t.Cleanup(func() {
		clean := exec.Command("go", "clean", "-modcache")
		clean.Env = environment
		_ = clean.Run()
	})

	binary := filepath.Join(temporary, "minimum-runtime")
	buildExternalProgram(t, moduleRoot, environment, binary, "./cmd/minimum")
	emptyRuntime := filepath.Join(temporary, "empty-runtime")
	if err := os.Mkdir(emptyRuntime, 0o755); err != nil {
		t.Fatal(err)
	}
	output := runExternalProgram(t, binary, emptyRuntime, environment)
	var state struct {
		Start  string `json:"start"`
		Health string `json:"health"`
		Stop   string `json:"stop"`
	}
	if err := json.Unmarshal(output, &state); err != nil {
		t.Fatalf("decode minimum runtime output: %v\n%s", err, output)
	}
	if state.Start != "started" || state.Health != "healthy" || state.Stop != "stopped" {
		t.Fatalf("minimum runtime state = %#v", state)
	}

	dependencies := runRuntimeConsumerCommand(t, moduleRoot, environment, "go", "list", "-mod=readonly", "-deps", "./cmd/minimum")
	symbols := runRuntimeConsumerCommand(t, moduleRoot, environment, "go", "tool", "nm", binary)
	for _, forbidden := range []string{
		"github.com/nxnminieye/nexa/runtime/crud",
		"github.com/nxnminieye/nexa/runtime/kafka",
		"github.com/nxnminieye/nexa/runtime/s3",
		"github.com/nxnminieye/nexa/runtime/observability/logging",
		"github.com/nxnminieye/nexa/runtime/observability/rpcaccess",
		"github.com/twmb/franz-go",
		"github.com/aws/aws-sdk-go-v2",
		"github.com/aws/smithy-go",
		"google.golang.org/grpc",
		"go.opentelemetry.io/otel",
		"github.com/zeromicro/go-zero",
	} {
		for _, dependency := range strings.Fields(string(dependencies)) {
			if dependency == forbidden || strings.HasPrefix(dependency, forbidden+"/") {
				t.Fatalf("minimum runtime linked optional dependency %q", dependency)
			}
		}
		if strings.Contains(string(symbols), forbidden) {
			t.Fatalf("minimum runtime binary contains optional symbols from %q", forbidden)
		}
	}
}

const minimumRuntimeConsumerProgram = `package main

import (
	"encoding/json"
	"os"

	"github.com/nxnminieye/nexa/nexactl/host"
)

func main() {
	composed, err := host.New(host.Options{Version: "v0.0.0-minimum"})
	if err != nil { panic(err) }
	inspection := composed.Inspect()
	if len(inspection.Plugins) != 0 || len(inspection.Capabilities) != 0 { panic("unexpected optional composition") }
	json.NewEncoder(os.Stdout).Encode(map[string]string{
		"start": "started", "health": "healthy", "stop": "stopped",
	})
}
`
