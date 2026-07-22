package source_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestTrueUnlinkedHostBinaryKeepsBuiltinsWithoutSourceComposition(t *testing.T) {
	repositoryRoot := trueUnlinkedRepositoryRoot(t)
	consumerRoot := t.TempDir()
	writeTrueUnlinkedConsumer(t, consumerRoot, repositoryRoot)
	environment := trueUnlinkedEnvironment()

	binary := filepath.Join(consumerRoot, "bin", "nexactl-unlinked")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-mod=readonly", "-o", binary, ".")
	build.Dir = consumerRoot
	build.Env = environment
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build true-unlinked binary: %v\n%s", err, output)
	}

	inspect := runTrueUnlinkedBinary(t, binary, "inspect", "--json")
	var composition struct {
		APIVersion string `json:"apiVersion"`
		Binary     struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"binary"`
		Plugins []struct {
			ID string `json:"id"`
		} `json:"plugins"`
		Capabilities []struct {
			ID               string `json:"id"`
			ProviderPluginID string `json:"providerPluginId"`
		} `json:"capabilities"`
		Commands []struct {
			Path          []string `json:"path"`
			OwnerPluginID string   `json:"ownerPluginId"`
		} `json:"commands"`
	}
	decodeTrueUnlinkedResult(t, inspect, &composition)
	if composition.APIVersion != "nexa.dev/cli-inspection/v1" ||
		composition.Binary.Name != "nexactl-unlinked" ||
		composition.Binary.Version != "v0.0.0-test" {
		t.Fatalf("inspection identity = %#v", composition)
	}
	if len(composition.Plugins) != 0 || len(composition.Capabilities) != 0 {
		t.Fatalf("unlinked composition contains plugins or capabilities: %#v", composition)
	}
	if len(composition.Commands) != 2 {
		t.Fatalf("unlinked commands = %#v", composition.Commands)
	}
	for _, command := range composition.Commands {
		if command.OwnerPluginID == "source" ||
			(len(command.Path) != 0 && command.Path[0] == "source") {
			t.Fatalf("unlinked composition contains source command: %#v", command)
		}
	}

	version := runTrueUnlinkedBinary(t, binary, "version", "--json")
	var binaryVersion struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	decodeTrueUnlinkedResult(t, version, &binaryVersion)
	if binaryVersion.Name != "nexactl-unlinked" || binaryVersion.Version != "v0.0.0-test" {
		t.Fatalf("version = %#v", binaryVersion)
	}
}

type trueUnlinkedEnvelope struct {
	APIVersion string          `json:"apiVersion"`
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result"`
}

func runTrueUnlinkedBinary(t *testing.T, binary string, args ...string) trueUnlinkedEnvelope {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary, args...)
	command.Env = trueUnlinkedEnvironment()
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run %v: %v\nstdout=%s\nstderr=%s", args, err, stdout.Bytes(), stderr.Bytes())
	}
	if stderr.Len() != 0 {
		t.Fatalf("run %v stderr = %q", args, stderr.String())
	}
	var envelope trueUnlinkedEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %v output %q: %v", args, stdout.String(), err)
	}
	if envelope.APIVersion != "nexa.dev/cli-envelope/v1" || !envelope.OK {
		t.Fatalf("run %v envelope = %#v", args, envelope)
	}
	return envelope
}

func decodeTrueUnlinkedResult(t *testing.T, envelope trueUnlinkedEnvelope, target any) {
	t.Helper()
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		t.Fatalf("decode result %q: %v", envelope.Result, err)
	}
}

func writeTrueUnlinkedConsumer(t *testing.T, consumerRoot, repositoryRoot string) {
	t.Helper()
	module := fmt.Sprintf(`module example.test/nexactl-unlinked

go 1.25.0

require github.com/nxnminieye/nexa v0.0.0

require (
	github.com/gowebpki/jcs v1.0.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/mod v0.31.0 // indirect
)

replace github.com/nxnminieye/nexa => %s
`, filepath.ToSlash(repositoryRoot))
	main := `package main

import (
	"context"
	"os"

	"github.com/nxnminieye/nexa/nexactl/host"
)

func main() {
	composed, err := host.New(host.Options{Name: "nexactl-unlinked", Version: "v0.0.0-test"})
	if err != nil {
		os.Exit(70)
	}
	os.Exit(composed.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
`
	for path, content := range map[string][]byte{
		"go.mod":  []byte(module),
		"main.go": []byte(main),
	} {
		if err := os.WriteFile(filepath.Join(consumerRoot, path), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sums, err := os.ReadFile(filepath.Join(repositoryRoot, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumerRoot, "go.sum"), sums, 0o600); err != nil {
		t.Fatal(err)
	}
}

func trueUnlinkedRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func trueUnlinkedEnvironment() []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	values["GOENV"] = "off"
	values["GOFLAGS"] = "-mod=readonly"
	values["GOPROXY"] = "off"
	values["GOSUMDB"] = "off"
	values["GOTOOLCHAIN"] = "local"
	values["GOWORK"] = "off"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}
