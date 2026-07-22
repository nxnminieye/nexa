package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	modmodule "golang.org/x/mod/module"
)

func TestTask5ExternalConsumerDelegatesEntGeneration(t *testing.T) {
	base := canonicalIntegrationDirectory(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	consumer := filepath.Join(repository, "consumer")
	framework := filepath.Join(repository, "framework")
	entModule := filepath.Join(repository, "ent")
	if err := os.MkdirAll(filepath.Join(consumer, "cmd", "verify"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatal(err)
	}
	entSource := filepath.Join(rootModuleCache(t), "entgo.io", "ent@v0.14.5")
	if err := materializeHermeticModuleDirectory(entSource, entModule, modmodule.Version{Path: "entgo.io/ent", Version: "v0.15.0"}); err != nil {
		t.Fatal(err)
	}
	copyTask4Tree(t, filepath.Join(repositoryRoot(t), "fixtures/generation/ent-consumer/schema"), filepath.Join(consumer, "ent", "schema"))
	writeConsumerFile(t, filepath.Join(consumer, "cmd", "verify", "main.go"), task5ExternalConsumerSource)
	writeConsumerFile(t, filepath.Join(consumer, "go.mod"), "module example.com/task5-consumer\n\ngo 1.25.0\n\nrequire (\n\tentgo.io/ent v0.15.0\n\tgithub.com/nxnminieye/nexa v0.8.0\n)\n\nreplace (\n\tentgo.io/ent v0.15.0 => "+filepath.ToSlash(entModule)+"\n\tgithub.com/nxnminieye/nexa v0.8.0 => "+filepath.ToSlash(framework)+"\n)\n")

	environment := overriddenEnvironment(
		os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local",
		"GOMODCACHE="+rootModuleCache(t),
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")), "GOSUMDB=off",
	)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir, tidy.Env = consumer, environment
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy Task5 consumer: %v\n%s", err, output)
	}
	command := exec.Command("go", "run", "-mod=readonly", "./cmd/verify", repository)
	command.Dir, command.Env = consumer, environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Task5 external consumer: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "ent-generation-ok" {
		t.Fatalf("Task5 external output = %q", output)
	}
	generatedTidy := exec.Command("go", "mod", "tidy")
	generatedTidy.Dir, generatedTidy.Env = consumer, environment
	if output, err := generatedTidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy Task5 generated dependencies: %v\n%s", err, output)
	}
	compile := exec.Command("go", "test", "-mod=readonly", "./...")
	compile.Dir, compile.Env = consumer, environment
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile Task5 generated Go: %v\n%s", err, output)
	}
}

const task5ExternalConsumerSource = `package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

type provider struct{ tool toolchain.Tool }

func (p provider) Descriptor() generation.ProviderDescriptor {
	return generation.ProviderDescriptor{ID:"consumer", Version:"v1.0.0", Tools:[]generation.ProviderTool{{
		Role:generation.ToolRoleEntGenerate,
		Tool:plugin.DelegatedToolSpec{ID:p.tool.ID,Version:p.tool.Version,Inputs:[]string{"Ent schema"},Writes:[]string{"repository"}},
	}}}
}

func (p provider) Resolve(context.Context, string) (generation.Project, error) {
	return generation.Project{Services: []generation.ServiceProject{{
		ServiceID:"accounts",
		EntSchemaDir:"consumer/ent/schema",
		EntGenerateTool:p.tool,
	}}}, nil
}

type resultView struct {
	APIVersion string ` + "`json:\"apiVersion\"`" + `
	Kind string ` + "`json:\"kind\"`" + `
	Status string ` + "`json:\"status\"`" + `
	Service string ` + "`json:\"service\"`" + `
}

type errorDetails struct {
	Stage string ` + "`json:\"stage\"`" + `
	Reason string ` + "`json:\"reason\"`" + `
	Pointer string ` + "`json:\"pointer\"`" + `
	Source string ` + "`json:\"source\"`" + `
}

type failingRunner struct{ delegate toolchain.Runner }

func (r failingRunner) Run(ctx context.Context, request toolchain.Request) (toolchain.Result, error) {
	request.Args = []string{"nexa-invalid-go-command"}
	return r.delegate.Run(ctx, request)
}

type cancelingRunner struct {
	delegate toolchain.Runner
	cancel context.CancelFunc
}

func (r cancelingRunner) Run(ctx context.Context, request toolchain.Request) (toolchain.Result, error) {
	r.cancel()
	return r.delegate.Run(ctx, request)
}

func main() {
	if len(os.Args) != 2 { panic("arguments") }
	repository := os.Args[1]
	tool, environment := goTool()
	cli := compose(tool, environment, toolchain.NewExecRunner())

	canceledContext, cancel := context.WithCancel(context.Background())
	canceledCLI := compose(tool, environment, cancelingRunner{delegate:toolchain.NewExecRunner(), cancel:cancel})
	canceled, canceledExit := execute(canceledCLI, canceledContext, repository)
	assertFailure(canceled, canceledExit, "operation_canceled", protocol.CategoryCanceled, 130, "wait", "context_canceled", "/context")

	failedCLI := compose(tool, environment, failingRunner{delegate:toolchain.NewExecRunner()})
	failed, failedExit := execute(failedCLI, context.Background(), repository)
	assertFailure(failed, failedExit, "tool_failed", protocol.CategoryInput, 3, "exit", "nonzero_exit", "")

	envelope, exit := execute(cli, context.Background(), repository)
	if exit != 0 || !envelope.OK || envelope.Error != nil { panic("failure envelope") }
	encoded, err := json.Marshal(envelope.Result); if err != nil { panic(err) }
	var result resultView
	if err := json.Unmarshal(encoded, &result); err != nil { panic(err) }
	if result.APIVersion != "nexa.dev/ent-generation-result/v1" || result.Kind != "EntGenerationResult" || result.Status != "generated" || result.Service != "accounts" { panic("result contract") }
	for _, path := range []string{
		"api/accounts.crud.generated.proto",
		"api/accounts.crud-protocol.lock.json",
		".nexa/generation/crud-proto.accounts.manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(repository, path)); !os.IsNotExist(err) { panic("unexpected CRUD artifact: "+path) }
	}
	if _, err := os.Stat(filepath.Join(repository, "consumer", "ent", "client.go")); err != nil { panic(err) }
	fmt.Println("ent-generation-ok")
}

func compose(tool toolchain.Tool, environment []toolchain.EnvVar, runner toolchain.Runner) *host.Host {
	buildPlugin, err := generation.New(generation.Options{
		Providers:[]generation.ProjectProvider{provider{tool:tool}},
		Runner:runner,
		Environment:environment,
	})
	if err != nil { panic(err) }
	cli, err := host.New(host.Options{Version:"v0.0.0-test"}, buildPlugin)
	if err != nil { panic(err) }
	return cli
}

func execute(cli *host.Host, ctx context.Context, repository string) (protocol.Envelope, int) {
	var stdout, stderr bytes.Buffer
	exit := cli.Execute(ctx, []string{
		"gen", "ent", "--repo-root", repository,
		"--provider", "consumer", "--service", "accounts", "--json",
	}, &stdout, &stderr)
	if stderr.Len() != 0 { panic(fmt.Sprintf("command exit=%d stderr=%s stdout=%s", exit, stderr.String(), stdout.String())) }
	var envelope protocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil { panic(err) }
	return envelope, exit
}

func assertFailure(envelope protocol.Envelope, exit int, code string, category protocol.Category, wantExit int, stage, reason, pointer string) {
	if exit != wantExit || envelope.OK || envelope.Error == nil || envelope.Error.Code != code || envelope.Error.Domain != "nexactl.generation" || envelope.Error.Category != category {
		if envelope.Error == nil { panic(fmt.Sprintf("unexpected failure: exit=%d without error", exit)) }
		panic(fmt.Sprintf("unexpected failure: exit=%d code=%s domain=%s category=%s", exit, envelope.Error.Code, envelope.Error.Domain, envelope.Error.Category))
	}
	var details errorDetails
	if err := json.Unmarshal(envelope.Error.Details, &details); err != nil { panic(err) }
	if details.Stage != stage || details.Reason != reason || details.Pointer != pointer || details.Source != "" { panic(fmt.Sprintf("unexpected details: %#v", details)) }
}

func goTool() (toolchain.Tool, []toolchain.EnvVar) {
	executable, err := exec.LookPath("go"); if err != nil { panic(err) }
	executable, err = filepath.EvalSymlinks(executable); if err != nil { panic(err) }
	version, err := exec.Command(executable, "version").Output(); if err != nil { panic(err) }
	tool := toolchain.Tool{ID:"go", Version:"v1.25.0", Executable:executable, InputScopes:[]string{"repository","scratch"}, WriteScopes:[]string{"repository","scratch"},
		Environment: []toolchain.EnvironmentRule{
			{Name:"PATH",Source:toolchain.EnvironmentHost},{Name:"GOROOT",Source:toolchain.EnvironmentHost},{Name:"GOMODCACHE",Source:toolchain.EnvironmentHost},{Name:"GOPROXY",Source:toolchain.EnvironmentHost},{Name:"GOSUMDB",Source:toolchain.EnvironmentHost},
			{Name:"HOME",Source:toolchain.EnvironmentScratch},{Name:"TMPDIR",Source:toolchain.EnvironmentScratch},{Name:"GOPATH",Source:toolchain.EnvironmentScratch},{Name:"GOCACHE",Source:toolchain.EnvironmentScratch},
			{Name:"GOWORK",Source:toolchain.EnvironmentFixed,FixedValue:"off"},{Name:"GOENV",Source:toolchain.EnvironmentFixed,FixedValue:"off"},{Name:"GOTOOLCHAIN",Source:toolchain.EnvironmentFixed,FixedValue:"local"},{Name:"GOFLAGS",Source:toolchain.EnvironmentFixed},{Name:"CGO_ENABLED",Source:toolchain.EnvironmentFixed,FixedValue:"0"},
		}, Probe:toolchain.ExecutableProbe{Args:[]string{"version"},ExpectedVersion:strings.TrimSpace(string(version))}}
	environment := []toolchain.EnvVar{{Name:"PATH",Value:os.Getenv("PATH")},{Name:"GOROOT",Value:runtime.GOROOT()},{Name:"GOMODCACHE",Value:os.Getenv("GOMODCACHE")},{Name:"GOPROXY",Value:os.Getenv("GOPROXY")},{Name:"GOSUMDB",Value:os.Getenv("GOSUMDB")}}
	return tool, environment
}
`
