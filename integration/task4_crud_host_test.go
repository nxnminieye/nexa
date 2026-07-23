package integration_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	modmodule "golang.org/x/mod/module"
)

func TestTask4TenantOnlyMigrationKeepsAccountsServiceLayout(t *testing.T) {
	schema, err := parser.ParseFile(token.NewFileSet(), "audit.go", task4NoCRUDSchemaSource, parser.PackageClauseOnly)
	if err != nil || schema.Name.Name != "schema" {
		t.Fatalf("tenant-only schema package = %#v, %v", schema, err)
	}
	program, err := parser.ParseFile(token.NewFileSet(), "main.go", task4ExternalConsumerSource, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	var entGeneration []token.Pos
	var lastWrite, lastBlocked token.Pos
	ast.Inspect(program, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if ok {
			for _, expression := range assignment.Lhs {
				selector, ok := expression.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				identifier, ownerOK := selector.X.(*ast.Ident)
				if ownerOK && identifier.Name == "consumerProvider" && selector.Sel.Name == "schema" {
					t.Fatal("tenant-only migration changed the provider schema root")
				}
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == "execute" && len(call.Args) >= 2 {
			first, firstOK := call.Args[0].(*ast.BasicLit)
			second, secondOK := call.Args[1].(*ast.BasicLit)
			if firstOK && secondOK && first.Value == `"gen"` && second.Value == `"ent"` {
				entGeneration = append(entGeneration, call.Pos())
			}
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			if owner, ownerOK := selector.X.(*ast.Ident); ownerOK && owner.Name == "os" && selector.Sel.Name == "WriteFile" {
				lastWrite = call.Pos()
			}
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == "assertFailure" {
			lastBlocked = call.Pos()
		}
		return true
	})
	if len(entGeneration) != 2 || lastWrite == token.NoPos || lastBlocked == token.NoPos || entGeneration[1] <= lastWrite || entGeneration[1] >= lastBlocked {
		t.Fatalf("tenant-only regeneration ordering: ent=%v write=%v blocked=%v", entGeneration, lastWrite, lastBlocked)
	}
}

func TestTask4ExternalConsumerGeneratesCRUDTransaction(t *testing.T) {
	base := canonicalIntegrationDirectory(t, t.TempDir())
	repository := filepath.Join(base, "repository")
	consumer := filepath.Join(repository, "consumer")
	framework := filepath.Join(consumer, ".framework")
	staging := filepath.Join(base, "staging")
	scratch := filepath.Join(base, "scratch")
	accountRoot := filepath.Join(consumer, "backend", "accounts")
	noCRUDRoot := filepath.Join(consumer, "backend", "no-crud")
	for _, directory := range []string{consumer, filepath.Join(accountRoot, "ent", "schema"), filepath.Join(noCRUDRoot, "ent", "schema"), filepath.Join(accountRoot, "desc"), staging, scratch} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatal(err)
	}
	copyTask4Tree(t, filepath.Join(repositoryRoot(t), "fixtures/generation/ent-consumer/schema"), filepath.Join(accountRoot, "ent", "schema"))
	writeConsumerFile(t, filepath.Join(noCRUDRoot, "ent", "schema", "audit.go"), task4NoCRUDSchemaSource)
	writeConsumerFile(t, filepath.Join(accountRoot, "desc", "accounts.proto"), task4AuthoredProtoSource)
	writeConsumerFile(t, filepath.Join(consumer, "main.go"), task4ExternalConsumerSource)
	copyTask4Tree(t, filepath.Join(repositoryRoot(t), "fixtures", "consumers", "generation", "cmd", "generation-helper"), filepath.Join(consumer, "cmd", "generation-helper"))
	writeConsumerFile(t, filepath.Join(consumer, "go.mod"), "module example.com/task4-consumer\n\ngo 1.25.0\n\nrequire (\n\tentgo.io/ent v0.14.5\n\tgithub.com/bufbuild/protocompile v0.14.1\n\tgithub.com/nxnminieye/nexa v0.8.0\n)\n\nreplace github.com/nxnminieye/nexa v0.8.0 => "+filepath.ToSlash(framework)+"\n")

	environment := overriddenEnvironment(
		os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local",
		"GOMODCACHE="+rootModuleCache(t),
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")), "GOSUMDB=off",
	)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir, tidy.Env = consumer, environment
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy Task4 consumer dependencies: %v\n%s", err, output)
	}
	helper := filepath.Join(base, "generation-helper")
	buildHelper := exec.Command("go", "build", "-mod=readonly", "-o", helper, "./cmd/generation-helper")
	buildHelper.Dir, buildHelper.Env = consumer, environment
	if output, err := buildHelper.CombinedOutput(); err != nil {
		t.Fatalf("build Task4 generation helper: %v\n%s", err, output)
	}
	command := exec.Command("go", "run", "-mod=readonly", ".", consumer, staging, scratch, helper)
	command.Dir, command.Env = consumer, environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Task4 external consumer: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "crud-cli-ok" {
		t.Fatalf("Task4 external output = %q", output)
	}
	for _, arguments := range [][]string{{"mod", "tidy"}, {"test", "./..."}} {
		verify := exec.Command("go", arguments...)
		verify.Dir, verify.Env = consumer, environment
		if output, err := verify.CombinedOutput(); err != nil {
			t.Fatalf("verify Task4 consumer with go %v: %v\n%s", arguments, err, output)
		}
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

const task4NoCRUDSchemaSource = `package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/nexaent/mixin"
)

type Audit struct{ ent.Schema }

func (Audit) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Tenant{}} }

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
option go_package = "example.com/task4-consumer/backend/accounts/internal/pb;accountspb";
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
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	"github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

type provider struct{
	entTool toolchain.Tool
	crudTool toolchain.Tool
	rpcTool toolchain.Tool
	schema string
	logic string
}

func (p provider) Descriptor() generation.ProviderDescriptor {
	delegated := func(role generation.ToolRole, tool toolchain.Tool) generation.ProviderTool { return generation.ProviderTool{Role:role,Tool:plugin.DelegatedToolSpec{ID:tool.ID,Version:tool.Version,Inputs:[]string{"generation facts"},Writes:[]string{"staging"}}} }
	return generation.ProviderDescriptor{ID:"consumer", Version:"v1.0.0", Tools:[]generation.ProviderTool{
		delegated(generation.ToolRoleEntGenerate,p.entTool),delegated(generation.ToolRoleEntCRUD,p.crudTool),delegated(generation.ToolRoleRPCGo,p.rpcTool),
	}}
}

func (p provider) Resolve(context.Context, string) (generation.Project, error) {
	return generation.Project{Services: []generation.ServiceProject{
		{ServiceID:"accounts", EntSchemaDir:p.schema, ProtoEntry:"backend/accounts/desc/accounts.proto", LogicRoot:p.logic, EntGenerateTool:p.entTool, EntCRUDTool:p.crudTool, RPCGoTool:p.rpcTool, MultiTenant:generation.MultiTenantConfig{Enabled:true}},
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
	if len(os.Args) != 5 { panic("arguments") }
	repository, staging, scratch, helper := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	_ = staging
	_ = scratch
	tool, environment := goTool()
	crudTool := tool
	crudTool.WriteScopes = []string{"scratch"}
	consumerProvider := &provider{entTool:tool, crudTool:crudTool, rpcTool:rpcTool(helper,crudTool), schema:"backend/accounts/ent/schema", logic:"backend/accounts/internal/logic"}
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
	if generated := execute("gen","ent","--repo-root",repository,"--provider","consumer","--service","accounts"); !generated.OK { panic(fmt.Sprintf("Ent generation failed: %#v", generated.Error)) }
	serviceContext := filepath.Join(repository,"backend/accounts/internal/svc/servicecontext.go")
	if err := os.MkdirAll(filepath.Dir(serviceContext),0o755); err != nil { panic(err) }
	if err := os.WriteFile(serviceContext,[]byte("package svc\n\nimport \"example.com/task4-consumer/backend/accounts/ent\"\n\ntype ServiceContext struct { DB *ent.Client }\n"),0o644); err != nil { panic(err) }
	tidy := exec.Command(tool.Executable,"mod","tidy"); tidy.Dir=repository
	if output, err := tidy.CombinedOutput(); err != nil { panic(fmt.Sprintf("tidy generated consumer: %v: %s",err,output)) }
	base := []string{"generation","crud"}
	args := func(command, service string) []string { return append(append([]string{}, base...), command, "--repo-root", repository, "--provider", "consumer", "--service", service) }
	planEnvelope := execute(args("plan", "accounts")...)
	if !planEnvelope.OK { encoded,_:=json.Marshal(planEnvelope.Error); panic(fmt.Sprintf("plan failed: %s", encoded)) }
	plan := decode(planEnvelope)
	if plan.PlanDigest == "" || len(plan.Artifacts) < 2 || len(plan.ControlSources) != 1 || plan.ControlSources[0].AfterDigest == "" { panic("plan projection") }
	manualLogic := []string{}
	for _, change := range plan.Changes { if strings.Contains(change.Path,"/internal/logic/") && strings.HasSuffix(change.Path,"logic.go") { manualLogic=append(manualLogic,change.Path) } }
	if len(manualLogic)==0 { panic("initial plan omitted manual Logic") }
	assertMissing(repository, "backend/accounts/desc/accounts.crud.generated.proto", "backend/accounts/desc/accounts.crud-protocol.lock.json", ".nexa/generation/crud-proto.accounts.manifest.json")
	wrongPlan := append(args("write", "accounts"), "--plan-digest", "sha256:"+strings.Repeat("a",64), "--lock-digest", plan.ControlSources[0].AfterDigest)
	assertFailure(execute(wrongPlan...), "transaction_write_failed", protocol.CategoryDrift)
	wrongLock := append(args("write", "accounts"), "--plan-digest", plan.PlanDigest, "--lock-digest", "sha256:"+strings.Repeat("b",64))
	assertFailure(execute(wrongLock...), "lock_digest_mismatch", protocol.CategoryDrift)
	assertMissing(repository, "backend/accounts/desc/accounts.crud.generated.proto", "backend/accounts/desc/accounts.crud-protocol.lock.json", ".nexa/generation/crud-proto.accounts.manifest.json")
	write := append(args("write", "accounts"), "--plan-digest", plan.PlanDigest, "--lock-digest", plan.ControlSources[0].AfterDigest)
	if envelope := execute(write...); !envelope.OK { encoded,_:=json.Marshal(envelope.Error); panic(fmt.Sprintf("write failed: %s", encoded)) }
	check := decode(execute(args("check", "accounts")...))
	if check.Status != "clean" { panic("check is not clean") }
	protoBytes, err := os.ReadFile(filepath.Join(repository, "backend/accounts/desc/accounts.crud.generated.proto")); if err != nil { panic(err) }
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{"backend/accounts/desc/accounts.crud.generated.proto": string(protoBytes),"nexa/protocol/v1/options.proto":string(genprotocol.OptionsProto())})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	compiled, err := compiler.Compile(context.Background(), "backend/accounts/desc/accounts.crud.generated.proto")
	if err != nil || len(compiled) != 1 { panic("generated Proto does not compile") }
	for _, path := range []string{"backend/accounts/desc/accounts.crud-protocol.lock.json", ".nexa/generation/crud-proto.accounts.manifest.json"} {
		if _, err := os.Stat(filepath.Join(repository, path)); err != nil { panic(err) }
	}
	accountSchema:=filepath.Join(repository,"backend/accounts/ent/schema")
	tenantOnlySchema,err:=os.ReadFile(filepath.Join(repository,"backend/no-crud/ent/schema/audit.go")); if err!=nil { panic(err) }
	entries,err:=os.ReadDir(accountSchema); if err!=nil { panic(err) }
	for _,entry:=range entries { if !entry.IsDir() && strings.HasSuffix(entry.Name(),".go") { if err:=os.Remove(filepath.Join(accountSchema,entry.Name())); err!=nil { panic(err) } } }
	if err:=os.WriteFile(filepath.Join(accountSchema,"audit.go"),tenantOnlySchema,0o644); err!=nil { panic(err) }
	if regenerated:=execute("gen","ent","--repo-root",repository,"--provider","consumer","--service","accounts"); !regenerated.OK { panic(fmt.Sprintf("tenant-only Ent regeneration failed: %#v",regenerated.Error)) }
	assertFailure(execute(args("plan", "accounts")...), "crud_logic_removal_blocked", protocol.CategoryConflict)
	if _, err := os.Stat(filepath.Join(repository, "backend/accounts/desc/accounts.crud.generated.proto")); err != nil { panic("blocked removal changed CRUD Proto") }
	if _, err := os.Stat(filepath.Join(repository, "backend/accounts/internal/pb/accounts.crud.generated.pb.go")); err != nil { panic("blocked removal changed generated PB") }
	if _, err := os.Stat(filepath.Join(repository, "backend/accounts/desc/accounts.crud-protocol.lock.json")); err != nil { panic("compatibility lock history missing") }
	manifestPath := ".nexa/generation/crud-proto.accounts.manifest.json"
	manifestBytes, err := os.ReadFile(filepath.Join(repository, manifestPath)); if err != nil { panic(err) }
	manifest, err := artifact.Parse(manifestPath, manifestBytes); if err != nil || len(manifest.Artifacts()) < 2 { panic("blocked removal changed service manifest") }
	for _, name := range manualLogic { if err:=os.Remove(filepath.Join(repository,filepath.FromSlash(name))); err!=nil { panic(err) } }
	removalEnvelope := execute(args("plan", "accounts")...); if !removalEnvelope.OK { encoded,_:=json.Marshal(removalEnvelope.Error); panic(fmt.Sprintf("removal plan failed: %s",encoded)) }
	removal := decode(removalEnvelope); if removal.PlanDigest=="" { panic("removal plan projection") }
	if envelope:=execute(append(args("write","accounts"),"--plan-digest",removal.PlanDigest)...); !envelope.OK { encoded,_:=json.Marshal(envelope.Error); panic(fmt.Sprintf("removal write failed: %s",encoded)) }
	if clean:=decode(execute(args("check","accounts")...)); clean.Status!="clean" { panic("removal check is not clean") }
	assertMissing(repository,"backend/accounts/desc/accounts.crud.generated.proto","backend/accounts/internal/pb/accounts.crud.generated.pb.go")
	manifestBytes,err=os.ReadFile(filepath.Join(repository,manifestPath)); if err!=nil { panic(err) }
	manifest,err=artifact.Parse(manifestPath,manifestBytes); if err!=nil { panic(err) }
	artifacts:=manifest.Artifacts(); if len(artifacts)!=1 || artifacts[0].ID()!="crud-logic.accounts.tenant-helper" { panic("tenant-only manifest did not retain exactly the tenant helper") }
	if _,err:=os.Stat(filepath.Join(repository,"backend/accounts/internal/logic/crudtenant/tenant.generated.go")); err!=nil { panic("tenant helper was removed") }
	fmt.Println("crud-cli-ok")
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

func rpcTool(executable string, base toolchain.Tool) toolchain.Tool {
	base.ID="generation-helper"; base.Version="v1.0.0"; base.Executable=executable; base.Probe=toolchain.ExecutableProbe{Args:[]string{"version"},ExpectedVersion:"nexa-generation-helper v1.0.0"}; return base
}

func assertMissing(repository string, paths ...string) {
	for _, path := range paths { if _, err := os.Stat(filepath.Join(repository,path)); !os.IsNotExist(err) { panic("unexpected output: "+path) } }
}

func assertFailure(envelope protocol.Envelope, code string, category protocol.Category) {
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != code || envelope.Error.Category != category { panic("unexpected failure") }
}
`
