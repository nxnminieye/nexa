package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	modmodule "golang.org/x/mod/module"
)

func TestTask4ExternalConsumerGeneratesCRUDTransaction(t *testing.T) {
	base := canonicalIntegrationDirectory(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	consumer := filepath.Join(repository, "consumer")
	framework := filepath.Join(repository, "framework")
	staging := filepath.Join(base, "staging")
	scratch := filepath.Join(base, "scratch")
	for _, directory := range []string{consumer, filepath.Join(consumer, "no_crud_schema"), filepath.Join(repository, "api"), staging, scratch} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatal(err)
	}
	copyTask4Tree(t, filepath.Join(repositoryRoot(t), "fixtures/generation/ent-consumer/schema"), filepath.Join(consumer, "schema"))
	writeConsumerFile(t, filepath.Join(consumer, "no_crud_schema", "audit.go"), task4NoCRUDSchemaSource)
	writeConsumerFile(t, filepath.Join(repository, "api", "accounts.proto"), task4AuthoredProtoSource)
	writeConsumerFile(t, filepath.Join(consumer, "main.go"), task4ExternalConsumerSource)
	writeConsumerFile(t, filepath.Join(consumer, "go.mod"), "module example.com/task4-consumer\n\ngo 1.25.0\n\nrequire (\n\tentgo.io/ent v0.14.5\n\tgithub.com/bufbuild/protocompile v0.14.1\n\tgithub.com/nxnminieye/nexa v0.8.0\n)\n\nreplace github.com/nxnminieye/nexa v0.8.0 => "+filepath.ToSlash(framework)+"\n")

	environment := overriddenEnvironment(
		os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local",
		"GOMODCACHE="+rootModuleCache(t),
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")), "GOSUMDB=off",
	)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir, tidy.Env = consumer, environment
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy Task4 consumer: %v\n%s", err, output)
	}
	command := exec.Command("go", "run", "-mod=readonly", ".", repository, staging, scratch)
	command.Dir, command.Env = consumer, environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Task4 external consumer: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "crud-cli-ok" {
		t.Fatalf("Task4 external output = %q", output)
	}
}

func copyTask4Tree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
}

const task4NoCRUDSchemaSource = `package no_crud_schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

type Audit struct{ ent.Schema }

func (Audit) Annotations() []schema.Annotation {
	return []schema.Annotation{nexaent.Schema(nexaent.SchemaMeta{
		Label: nexaent.LocalizedText{Key: "audit.label", ZhCN: "Audit", EnUS: "Audit"},
		Description: nexaent.LocalizedText{Key: "audit.description", ZhCN: "Audit", EnUS: "Audit"},
		Identity: nexaent.IdentityEntID, Scope: nexaent.ScopeGlobal,
	})}
}

func (Audit) Fields() []ent.Field {
	return []ent.Field{field.String("actor").Annotations(nexaent.Field(nexaent.FieldMeta{
		Label: nexaent.LocalizedText{Key: "audit.actor", ZhCN: "Actor", EnUS: "Actor"},
		Description: nexaent.LocalizedText{Key: "audit.actor.description", ZhCN: "Actor", EnUS: "Actor"},
		UIHint: nexaent.UIHintText, Visibility: nexaent.VisibilityPublic,
	}))}
}
`

const task4AuthoredProtoSource = `syntax = "proto3";
package acme.accounts.v1;
option go_package = "example.com/task4/accounts;accountsv1";
`

const task4ExternalConsumerSource = `package main

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

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/generation/artifact"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

type provider struct{
	tool toolchain.Tool
	schema string
}

func (p provider) Descriptor() generation.ProviderDescriptor {
	return generation.ProviderDescriptor{ID:"consumer", Version:"v1.0.0", Tools:[]generation.ProviderTool{{
		Role:generation.ToolRoleEntCRUD,
		Tool:plugin.DelegatedToolSpec{ID:p.tool.ID,Version:p.tool.Version,Inputs:[]string{"Ent graph"},Writes:[]string{"staging"}},
	}}}
}

func (p provider) Resolve(context.Context, string) (generation.Project, error) {
	return generation.Project{Services: []generation.ServiceProject{
		{ServiceID:"accounts", EntSchemaDir:p.schema, ProtoEntry:"api/accounts.proto", EntCRUDTool:p.tool},
	}}, nil
}

type planView struct {
	PlanDigest string ` + "`json:\"planDigest\"`" + `
	Status string ` + "`json:\"status\"`" + `
	Artifacts []json.RawMessage ` + "`json:\"artifacts\"`" + `
	ControlSources []struct{ AfterDigest string ` + "`json:\"afterDigest\"`" + ` } ` + "`json:\"controlSources\"`" + `
	Changes []struct{ Kind, ID, Path string } ` + "`json:\"changes\"`" + `
	Conflicts []json.RawMessage ` + "`json:\"conflicts\"`" + `
	NextManifest struct{ Artifacts []json.RawMessage ` + "`json:\"artifacts\"`" + ` } ` + "`json:\"nextManifest\"`" + `
}

func main() {
	if len(os.Args) != 4 { panic("arguments") }
	repository, staging, scratch := os.Args[1], os.Args[2], os.Args[3]
	_ = staging
	_ = scratch
	tool, environment := goTool()
	consumerProvider := &provider{tool:tool, schema:"consumer/schema"}
	buildPlugin, err := generation.New(generation.Options{Providers:[]generation.ProjectProvider{consumerProvider}, Runner:toolchain.NewExecRunner(), Environment:environment})
	if err != nil { panic(err) }
	cli, err := host.New(host.Options{Version:"v0.0.0-test"}, buildPlugin); if err != nil { panic(err) }
	execute := func(args ...string) protocol.Envelope {
		var stdout, stderr bytes.Buffer
		exit := cli.Execute(context.Background(), append(args, "--json"), &stdout, &stderr)
		var envelope protocol.Envelope
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil { panic(err) }
		if stderr.Len() != 0 { panic("stderr noise: "+stderr.String()) }
		if envelope.OK && exit != 0 || !envelope.OK && exit == 0 { panic("exit mismatch") }
		return envelope
	}
	decode := func(envelope protocol.Envelope) planView {
		encoded, _ := json.Marshal(envelope.Result)
		var result planView
		if err := json.Unmarshal(encoded, &result); err != nil { panic(err) }
		return result
	}
	base := []string{"generation","crud-proto"}
	args := func(command, service string) []string { return append(append([]string{}, base...), command, "--repo-root", repository, "--provider", "consumer", "--service", service) }
	planEnvelope := execute(args("plan", "accounts")...)
	if !planEnvelope.OK { panic(fmt.Sprintf("plan failed: %#v", planEnvelope.Error)) }
	plan := decode(planEnvelope)
	if plan.PlanDigest == "" || len(plan.Artifacts) != 1 || len(plan.ControlSources) != 1 || plan.ControlSources[0].AfterDigest == "" { panic("plan projection") }
	assertMissing(repository, "api/accounts.crud.generated.proto", "api/accounts.crud-protocol.lock.json", ".nexa/generation/crud-proto.accounts.manifest.json")
	wrongPlan := append(args("write", "accounts"), "--plan-digest", "sha256:"+strings.Repeat("a",64), "--lock-digest", plan.ControlSources[0].AfterDigest)
	assertFailure(execute(wrongPlan...), "transaction_write_failed", protocol.CategoryDrift)
	wrongLock := append(args("write", "accounts"), "--plan-digest", plan.PlanDigest, "--lock-digest", "sha256:"+strings.Repeat("b",64))
	assertFailure(execute(wrongLock...), "lock_digest_mismatch", protocol.CategoryDrift)
	assertMissing(repository, "api/accounts.crud.generated.proto", "api/accounts.crud-protocol.lock.json", ".nexa/generation/crud-proto.accounts.manifest.json")
	write := append(args("write", "accounts"), "--plan-digest", plan.PlanDigest, "--lock-digest", plan.ControlSources[0].AfterDigest)
	if envelope := execute(write...); !envelope.OK { panic("write failed") }
	check := decode(execute(args("check", "accounts")...))
	if check.Status != "clean" { panic("check is not clean") }
	protoBytes, err := os.ReadFile(filepath.Join(repository, "api/accounts.crud.generated.proto")); if err != nil { panic(err) }
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{"api/accounts.crud.generated.proto": string(protoBytes)})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	compiled, err := compiler.Compile(context.Background(), "api/accounts.crud.generated.proto")
	if err != nil || len(compiled) != 1 { panic("generated Proto does not compile") }
	for _, path := range []string{"api/accounts.crud-protocol.lock.json", ".nexa/generation/crud-proto.accounts.manifest.json"} {
		if _, err := os.Stat(filepath.Join(repository, path)); err != nil { panic(err) }
	}
	consumerProvider.schema = "consumer/no_crud_schema"
	removal := decode(execute(args("plan", "accounts")...))
	if len(removal.Artifacts) != 0 || len(removal.ControlSources) != 0 || len(removal.Changes) != 1 || removal.Changes[0].Kind != "delete" || removal.Changes[0].ID != "crud-proto.accounts" || len(removal.Conflicts) != 0 || len(removal.NextManifest.Artifacts) != 0 { panic("annotation removal plan") }
	remove := append(args("write", "accounts"), "--plan-digest", removal.PlanDigest)
	if envelope := execute(remove...); !envelope.OK { panic("annotation removal write failed") }
	if clean := decode(execute(args("check", "accounts")...)); clean.Status != "clean" { panic("annotation removal check is not clean") }
	assertMissing(repository, "api/accounts.crud.generated.proto")
	if _, err := os.Stat(filepath.Join(repository, "api/accounts.crud-protocol.lock.json")); err != nil { panic("compatibility lock history missing") }
	manifestPath := ".nexa/generation/crud-proto.accounts.manifest.json"
	manifestBytes, err := os.ReadFile(filepath.Join(repository, manifestPath)); if err != nil { panic(err) }
	manifest, err := artifact.Parse(manifestPath, manifestBytes); if err != nil || len(manifest.Artifacts()) != 0 { panic("empty service manifest missing") }
	fmt.Println("crud-cli-ok")
}

func goTool() (toolchain.Tool, []toolchain.EnvVar) {
	executable, err := exec.LookPath("go"); if err != nil { panic(err) }
	executable, err = filepath.EvalSymlinks(executable); if err != nil { panic(err) }
	version, err := exec.Command(executable, "version").Output(); if err != nil { panic(err) }
	tool := toolchain.Tool{ID:"go", Version:"v1.25.0", Executable:executable, InputScopes:[]string{"repository","scratch"}, WriteScopes:[]string{"scratch"},
		Environment: []toolchain.EnvironmentRule{
			{Name:"PATH",Source:toolchain.EnvironmentHost},{Name:"GOROOT",Source:toolchain.EnvironmentHost},{Name:"GOMODCACHE",Source:toolchain.EnvironmentHost},{Name:"GOPROXY",Source:toolchain.EnvironmentHost},{Name:"GOSUMDB",Source:toolchain.EnvironmentHost},
			{Name:"HOME",Source:toolchain.EnvironmentScratch},{Name:"TMPDIR",Source:toolchain.EnvironmentScratch},{Name:"GOPATH",Source:toolchain.EnvironmentScratch},{Name:"GOCACHE",Source:toolchain.EnvironmentScratch},
			{Name:"GOWORK",Source:toolchain.EnvironmentFixed,FixedValue:"off"},{Name:"GOENV",Source:toolchain.EnvironmentFixed,FixedValue:"off"},{Name:"GOTOOLCHAIN",Source:toolchain.EnvironmentFixed,FixedValue:"local"},{Name:"GOFLAGS",Source:toolchain.EnvironmentFixed},{Name:"CGO_ENABLED",Source:toolchain.EnvironmentFixed,FixedValue:"0"},
		}, Probe:toolchain.ExecutableProbe{Args:[]string{"version"},ExpectedVersion:strings.TrimSpace(string(version))}}
	environment := []toolchain.EnvVar{{Name:"PATH",Value:os.Getenv("PATH")},{Name:"GOROOT",Value:runtime.GOROOT()},{Name:"GOMODCACHE",Value:os.Getenv("GOMODCACHE")},{Name:"GOPROXY",Value:os.Getenv("GOPROXY")},{Name:"GOSUMDB",Value:os.Getenv("GOSUMDB")}}
	return tool, environment
}

func assertMissing(repository string, paths ...string) {
	for _, path := range paths { if _, err := os.Stat(filepath.Join(repository,path)); !os.IsNotExist(err) { panic("unexpected output: "+path) } }
}

func assertFailure(envelope protocol.Envelope, code string, category protocol.Category) {
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != code || envelope.Error.Category != category { panic("unexpected failure") }
}
`
