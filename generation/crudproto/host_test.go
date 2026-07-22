package crudproto_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/internal/entipc"
)

func TestInvokeEntGraphHostRejectsInvalidDestinationBeforeExecution(t *testing.T) {
	_, err := crudproto.InvokeEntGraphHost(context.Background(), crudproto.EntGraphHostSpec{})
	typed, ok := err.(*crudproto.Error)
	if !ok || typed.Code() != "crud_host_invalid" || typed.Stage() != "request" || typed.Reason() != "destination_state_invalid" || typed.Pointer() != "/destination" {
		t.Fatalf("host error = %#v", err)
	}
}

func TestEntGraphHostRequestRoundTripCarriesMultiTenantBuildSpec(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	frameworkRoot, err := filepath.EvalSymlinks(filepath.Clean(filepath.Join(filepath.Dir(filename), "../..")))
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Dir(frameworkRoot)
	consumerRoot, err := os.MkdirTemp(repositoryRoot, ".crudproto-host-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(consumerRoot) })
	for _, path := range []string{filepath.Join(consumerRoot, "ent", "schema")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	module := fmt.Sprintf("module example.com/crudproto-host-test\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.8.0\n\nreplace github.com/nxnminieye/nexa v0.8.0 => %s\n", filepath.ToSlash(frameworkRoot))
	writeHostFixture(t, filepath.Join(consumerRoot, "go.mod"), []byte(module))
	writeHostFixture(t, filepath.Join(consumerRoot, "ent", "schema", "schema.go"), []byte("package schema\n"))
	writeHostFixture(t, filepath.Join(consumerRoot, "main.go"), []byte(hostRequestCaptureProgram))

	executionRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stagingRoot := filepath.Join(executionRoot, "staging")
	scratchRoot := filepath.Join(executionRoot, "scratch")
	for _, path := range []string{stagingRoot, scratchRoot} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	capturePath := filepath.Join(executionRoot, "request.json")
	schemaDir, err := filepath.Rel(repositoryRoot, filepath.Join(consumerRoot, "ent", "schema"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", "-mod=mod", ".", repositoryRoot, filepath.ToSlash(schemaDir), stagingRoot, scratchRoot, capturePath)
	command.Dir = consumerRoot
	command.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=go1.25.0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("capture host request: %v\n%s", err, output)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := entipc.ParseRequest(entipc.HelperRequestSource(), captured)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := entipc.CanonicalRequest(parsed)
	if err != nil || !bytes.Equal(canonical, captured) {
		t.Fatalf("canonical host request changed: %v", err)
	}
	buildSpec, err := parsed.BuildSpec()
	if err != nil {
		t.Fatal(err)
	}
	if !buildSpec.MultiTenant.Enabled || buildSpec.ServiceID != "accounts" || buildSpec.ProtoArtifactPath != "api/accounts.crud.generated.proto" || buildSpec.LockPath != "api/accounts.crud-protocol.lock.json" {
		t.Fatalf("host BuildSpec = %#v", buildSpec)
	}
}

func writeHostFixture(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

const hostRequestCaptureProgram = `package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/provenance"
)

type captureRunner struct{ path string }

func (r captureRunner) Run(_ context.Context, request toolchain.Request) (toolchain.Result, error) {
	if err := os.WriteFile(r.path, request.Stdin, 0o644); err != nil {
		return toolchain.Result{}, err
	}
	return toolchain.Result{}, errors.New("request captured")
}

func main() {
	if len(os.Args) != 6 {
		panic("arguments")
	}
	repositoryRoot, schemaValue, stagingRoot, scratchRoot, capturePath := os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5]
	schemaDir, err := provenance.ParseDomainSource(schemaValue)
	if err != nil {
		panic(err)
	}
	destination, err := crudproto.ProjectProtoDestination("accounts", "api/accounts.proto")
	if err != nil {
		panic(err)
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		panic(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		panic(err)
	}
	goVersion, err := exec.Command(goExecutable, "version").Output()
	if err != nil {
		panic(err)
	}
	goRoot, err := exec.Command(goExecutable, "env", "GOROOT").Output()
	if err != nil {
		panic(err)
	}
	moduleCache, err := exec.Command(goExecutable, "env", "GOMODCACHE").Output()
	if err != nil {
		panic(err)
	}
	home, temporary, goPath, goCache := filepath.Join(stagingRoot, "home"), filepath.Join(stagingRoot, "tmp"), filepath.Join(stagingRoot, "gopath"), filepath.Join(stagingRoot, "gocache")
	for _, path := range []string{home, temporary, goPath, goCache} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			panic(err)
		}
	}
	tool := toolchain.Tool{
		ID: "go", Version: "go-test", Executable: goExecutable,
		InputScopes: []string{"repository", "scratch"}, WriteScopes: []string{"scratch"},
		Environment: []toolchain.EnvironmentRule{
			{Name: "PATH", Source: toolchain.EnvironmentHost}, {Name: "GOROOT", Source: toolchain.EnvironmentHost},
			{Name: "GOMODCACHE", Source: toolchain.EnvironmentHost}, {Name: "GOPROXY", Source: toolchain.EnvironmentHost},
			{Name: "GOSUMDB", Source: toolchain.EnvironmentHost}, {Name: "HOME", Source: toolchain.EnvironmentScratch},
			{Name: "TMPDIR", Source: toolchain.EnvironmentScratch}, {Name: "GOPATH", Source: toolchain.EnvironmentScratch},
			{Name: "GOCACHE", Source: toolchain.EnvironmentScratch}, {Name: "GOWORK", Source: toolchain.EnvironmentFixed, FixedValue: "off"},
			{Name: "GOENV", Source: toolchain.EnvironmentFixed, FixedValue: "off"}, {Name: "GOTOOLCHAIN", Source: toolchain.EnvironmentFixed, FixedValue: "local"},
			{Name: "GOFLAGS", Source: toolchain.EnvironmentFixed, FixedValue: ""}, {Name: "CGO_ENABLED", Source: toolchain.EnvironmentFixed, FixedValue: "0"},
		},
		Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: strings.TrimSpace(string(goVersion))},
	}
	environment := []toolchain.EnvVar{
		{Name: "PATH", Value: os.Getenv("PATH")}, {Name: "GOROOT", Value: strings.TrimSpace(string(goRoot))},
		{Name: "GOMODCACHE", Value: strings.TrimSpace(string(moduleCache))}, {Name: "GOPROXY", Value: "off"},
		{Name: "GOSUMDB", Value: "off"}, {Name: "HOME", Value: home}, {Name: "TMPDIR", Value: temporary},
		{Name: "GOPATH", Value: goPath}, {Name: "GOCACHE", Value: goCache}, {Name: "GOWORK", Value: "off"},
		{Name: "GOENV", Value: "off"}, {Name: "GOTOOLCHAIN", Value: "local"}, {Name: "GOFLAGS", Value: ""},
		{Name: "CGO_ENABLED", Value: "0"},
	}
	_, invokeErr := crudproto.InvokeEntGraphHost(context.Background(), crudproto.EntGraphHostSpec{
		RepositoryRoot: repositoryRoot, StagingRoot: stagingRoot, ScratchParent: scratchRoot,
		SchemaDir: schemaDir, ProtoPackage: "acme.accounts.v1", GoPackage: "example.com/acme/accounts/v1;accountsv1",
		Destination: destination, Tool: tool, Environment: environment, Runner: captureRunner{path: capturePath},
		MultiTenant: crudproto.MultiTenantConfig{Enabled: true},
	})
	if invokeErr == nil {
		panic("capture runner unexpectedly succeeded")
	}
	if _, err := os.Stat(capturePath); err != nil {
		if typed, ok := invokeErr.(*crudproto.Error); ok {
			fmt.Fprintf(os.Stderr, "host error: code=%s stage=%s reason=%s pointer=%s source=%s tool=%s exit=%d diagnostic=%s\n", typed.Code(), typed.Stage(), typed.Reason(), typed.Pointer(), typed.Source(), typed.ToolID(), typed.ExitCode(), typed.Diagnostic())
		}
		panic(invokeErr)
	}
}
`
