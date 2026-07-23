package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/nxnminieye/nexa/cli/protocol"
	api "github.com/nxnminieye/nexa/generation/api"
	service "github.com/nxnminieye/nexa/generation/service"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	generation "github.com/nxnminieye/nexa/plugins/nexactl/generation"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

const (
	providerID    = "consumer"
	helperVersion = "nexa-generation-helper v1.0.0"
)

type consumerProvider struct {
	tools projectTools
	empty bool
}

type projectTools struct {
	entGenerate toolchain.Tool
	entCRUD     toolchain.Tool
	rpc         toolchain.Tool
	api         toolchain.Tool
}

func (p consumerProvider) Descriptor() generation.ProviderDescriptor {
	id := providerID
	if p.empty {
		id = "consumer-empty"
	}
	values := []generation.ProviderTool{{Role: generation.ToolRoleAPIGo, Tool: delegated(p.tools.api)}}
	if !p.empty {
		values = append(values,
			generation.ProviderTool{Role: generation.ToolRoleEntGenerate, Tool: delegated(p.tools.entGenerate)},
			generation.ProviderTool{Role: generation.ToolRoleEntCRUD, Tool: delegated(p.tools.entCRUD)},
			generation.ProviderTool{Role: generation.ToolRoleRPCGo, Tool: delegated(p.tools.rpc)},
		)
	}
	return generation.ProviderDescriptor{ID: id, Version: "v1.0.0", Tools: values}
}

func (p consumerProvider) Resolve(context.Context, string) (generation.Project, error) {
	if p.empty {
		return generation.Project{
			CoreServiceID: "core",
			Services:      []generation.ServiceProject{{ServiceID: "core", APIEntry: "backend/core/desc/core.api", APIGoTool: p.tools.api}},
		}, nil
	}
	return generation.Project{
		CatalogPath: "services.yaml", CoreServiceID: "core",
		Services: []generation.ServiceProject{
			{ServiceID: "core", APIEntry: "backend/core/desc/core.api", APIGoTool: p.tools.api},
			{
				ServiceID: "account", EntSchemaDir: "backend/account/ent/schema", ProtoEntry: "backend/account/desc/account.proto",
				LogicRoot: "backend/account/internal/logic", EntGenerateTool: p.tools.entGenerate, EntCRUDTool: p.tools.entCRUD, RPCGoTool: p.tools.rpc,
			},
		},
	}, nil
}

func delegated(tool toolchain.Tool) plugin.DelegatedToolSpec {
	return plugin.DelegatedToolSpec{ID: tool.ID, Version: tool.Version, Inputs: []string{"typed generation facts"}, Writes: []string{"declared generation scope"}}
}

type commandResult struct {
	envelope protocol.Envelope
	exit     int
	result   []byte
}

type generationView struct {
	PlanDigest     string `json:"planDigest"`
	Status         string `json:"status"`
	ControlSources []struct {
		AfterDigest string `json:"afterDigest"`
	} `json:"controlSources"`
}

func main() {
	repository := flag.String("repo-root", "", "consumer repository root")
	helper := flag.String("helper", "", "consumer generation helper")
	flag.Parse()
	if *repository == "" || *helper == "" {
		panic("repo-root and helper are required")
	}
	tools := consumerTools(*helper)
	verifyNoProvider(*repository)
	verifyFullGeneration(*repository, tools)
	verifyExplicitEmptyCatalog(*repository, tools)
	fmt.Println("generation-consumer-ok")
}

func verifyNoProvider(repository string) {
	buildPlugin, err := generation.New(generation.Options{})
	if err != nil {
		panic(err)
	}
	cli := newHost(buildPlugin)
	args := []string{"generation", "crud", "plan", "--repo-root", repository, "--provider", providerID, "--service", "account"}
	first, second := invoke(cli, args...), invoke(cli, args...)
	for _, result := range []commandResult{first, second} {
		if result.exit == 0 || result.envelope.OK || result.envelope.Error == nil || result.envelope.Error.Code != "capability_unavailable" || result.envelope.Error.Domain != "nexactl.generation" || result.envelope.Error.Category != protocol.CategoryUnavailable {
			panic("zero-provider command did not project stable unavailable semantics")
		}
	}
	if first.envelope.Error.Code != second.envelope.Error.Code || first.envelope.Error.Category != second.envelope.Error.Category || !bytes.Equal(first.envelope.Error.Details, second.envelope.Error.Details) {
		panic("zero-provider error semantics drifted")
	}
}

func verifyExplicitEmptyCatalog(repository string, tools projectTools) {
	root, err := os.MkdirTemp("", "nexa-empty-generation-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	copyAuthored(repository, root, "go.mod")
	copyAuthored(repository, root, "backend/core/desc/core.api")
	buildPlugin, err := generation.New(generation.Options{Providers: []generation.ProjectProvider{consumerProvider{tools: tools, empty: true}}, Runner: toolchain.NewExecRunner()})
	if err != nil {
		panic(err)
	}
	cli := newHost(buildPlugin)
	base := []string{"generation", "api"}
	flags := []string{"--repo-root", root, "--provider", "consumer-empty", "--core-service", "core"}
	plan := requireOK(invoke(cli, commandArgs(base, "plan", flags)...))
	view := decodeView(plan.result)
	requireOK(invoke(cli, commandArgs(base, "write", append(flags, "--plan-digest", view.PlanDigest))...))
	first := requireClean(invoke(cli, commandArgs(base, "check", flags)...))
	manifestPath := filepath.Join(root, "backend/core/generated/api-manifest.json")
	manifestBytes := read(manifestPath)
	manifest, err := api.Parse("backend/core/generated/api-manifest.json", manifestBytes)
	if err != nil || len(manifest.Operations()) != 1 || manifest.Operations()[0].ID() != "health.get" {
		panic("explicit empty catalog did not produce the native-only API projection")
	}
	second := requireClean(invoke(cli, commandArgs(base, "check", flags)...))
	if !bytes.Equal(first.result, second.result) || !bytes.Equal(manifestBytes, read(manifestPath)) {
		panic("explicit empty catalog projection drifted")
	}
}

func verifyFullGeneration(repository string, tools projectTools) {
	buildPlugin, err := generation.New(generation.Options{
		Providers: []generation.ProjectProvider{consumerProvider{tools: tools}}, Runner: toolchain.NewExecRunner(), Environment: goEnvironment(),
	})
	if err != nil {
		panic(err)
	}
	cli := newHost(buildPlugin)
	requireOK(invoke(cli, "gen", "ent", "--repo-root", repository, "--provider", providerID, "--service", "account"))
	tidyGeneratedModule(repository)

	crudFlags := []string{"--repo-root", repository, "--provider", providerID, "--service", "account"}
	crudPlan := requireOK(invoke(cli, append([]string{"generation", "crud", "plan"}, crudFlags...)...))
	crud := decodeView(crudPlan.result)
	if len(crud.ControlSources) != 1 || crud.ControlSources[0].AfterDigest == "" {
		panic("CRUD plan omitted its compatibility decision")
	}
	requireOK(invoke(cli, append([]string{"generation", "crud", "write"}, append(crudFlags, "--plan-digest", crud.PlanDigest, "--lock-digest", crud.ControlSources[0].AfterDigest)...)...))

	rpcFlags := []string{"--repo-root", repository, "--provider", providerID, "--service", "account"}
	planWrite(cli, []string{"generation", "rpc"}, rpcFlags)
	apiFlags := []string{"--repo-root", repository, "--provider", providerID, "--core-service", "core"}
	planWrite(cli, []string{"generation", "api"}, apiFlags)

	manifestFlags := []string{"--repo-root", repository, "--provider", providerID, "--service", "account"}
	manifestBefore := requireOK(invoke(cli, append([]string{"generation", "service-manifest", "check"}, manifestFlags...)...))
	manifestPlan := decodeView(manifestBefore.result)
	requireOK(invoke(cli, append([]string{"generation", "service-manifest", "write"}, append(manifestFlags, "--plan-digest", manifestPlan.PlanDigest)...)...))

	verifyGeneratedProto(repository)
	verifyGeneratedAPI(repository, []string{"account.get", "health.get"})
	verifyServiceManifest(repository)

	paths := []string{
		"backend/account/ent/client.go",
		"backend/account/desc/account.crud.generated.proto",
		"backend/account/generated/account.proto",
		"backend/account/internal/pb/account.pb.go",
		"backend/core/desc/generated/account.generated.api",
		"backend/core/generated/api-manifest.json",
		"backend/core/generated/runtime-contract.json",
		"backend/core/internal/serviceclients/account/client.generated.go",
		"backend/account/generated/service-manifest.json",
	}
	before := snapshot(repository, paths)
	runChecks := func() map[string]commandResult {
		return map[string]commandResult{
			"crud":    requireClean(invoke(cli, append([]string{"generation", "crud", "check"}, crudFlags...)...)),
			"rpc":     requireClean(invoke(cli, append([]string{"generation", "rpc", "check"}, rpcFlags...)...)),
			"api":     requireClean(invoke(cli, append([]string{"generation", "api", "check"}, apiFlags...)...)),
			"service": requireClean(invoke(cli, append([]string{"generation", "service-manifest", "check"}, manifestFlags...)...)),
		}
	}
	first := runChecks()
	second := runChecks()
	for id, result := range first {
		if !bytes.Equal(result.result, second[id].result) {
			panic("generation check result drifted: " + id)
		}
	}
	if after := snapshot(repository, paths); !equalSnapshot(before, after) {
		panic("generated artifact bytes drifted across repeated checks")
	}
}

func tidyGeneratedModule(repository string) {
	command := exec.Command("go", "mod", "tidy")
	command.Dir = repository
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		panic(fmt.Sprintf("go mod tidy failed: %v\n%s", err, output))
	}
}

func planWrite(cli *host.Host, base, flags []string) {
	plan := requireOK(invoke(cli, commandArgs(base, "plan", flags)...))
	view := decodeView(plan.result)
	requireOK(invoke(cli, commandArgs(base, "write", append(flags, "--plan-digest", view.PlanDigest))...))
}

func commandArgs(base []string, action string, flags []string) []string {
	result := append([]string{}, base...)
	result = append(result, action)
	return append(result, flags...)
}

func newHost(buildPlugin plugin.Plugin) *host.Host {
	cli, err := host.New(host.Options{Version: "v0.0.0-consumer"}, buildPlugin)
	if err != nil {
		panic(err)
	}
	return cli
}

func invoke(cli *host.Host, args ...string) commandResult {
	var stdout, stderr bytes.Buffer
	exit := cli.Execute(context.Background(), append(args, "--json"), &stdout, &stderr)
	if stderr.Len() != 0 {
		panic("unexpected stderr: " + stderr.String())
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		panic(err)
	}
	result, err := json.Marshal(envelope.Result)
	if err != nil {
		panic(err)
	}
	return commandResult{envelope: envelope, exit: exit, result: result}
}

func requireOK(result commandResult) commandResult {
	if result.exit != 0 || !result.envelope.OK || result.envelope.Error != nil {
		panic(fmt.Sprintf("generation command failed: exit=%d error=%#v", result.exit, result.envelope.Error))
	}
	return result
}

func requireClean(result commandResult) commandResult {
	result = requireOK(result)
	if decodeView(result.result).Status != "clean" {
		panic("generation check is not clean")
	}
	return result
}

func decodeView(data []byte) generationView {
	var view generationView
	if err := json.Unmarshal(data, &view); err != nil || view.PlanDigest == "" {
		panic("invalid generation projection")
	}
	return view
}

func verifyGeneratedProto(repository string) {
	files := map[string]string{}
	for _, name := range []string{"backend/account/desc/account.crud.generated.proto", "backend/account/generated/account.proto"} {
		files[name] = string(read(filepath.Join(repository, filepath.FromSlash(name))))
	}
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(files)}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	compiled, err := compiler.Compile(context.Background(), "backend/account/desc/account.crud.generated.proto", "backend/account/generated/account.proto")
	if err != nil || len(compiled) != 2 {
		panic(fmt.Sprintf("generated Proto does not compile: %v", err))
	}
	hasAccount, hasAudit := false, false
	messages := compiled[0].Messages()
	for index := 0; index < messages.Len(); index++ {
		name := string(messages.Get(index).Name())
		hasAccount = hasAccount || strings.Contains(name, "Account")
		hasAudit = hasAudit || strings.Contains(name, "Audit")
	}
	if !hasAccount || hasAudit {
		panic("typed CRUD annotation selection was not reflected in the compiled descriptor")
	}
}

func verifyGeneratedAPI(repository string, expected []string) {
	for _, name := range []string{"backend/core/desc/generated/account.generated.api", "backend/core/desc/generated/core.generated.api"} {
		parsed, err := goctlparser.Parse(filepath.Join(repository, filepath.FromSlash(name)), nil)
		if err != nil || parsed.Validate() != nil {
			panic(fmt.Sprintf("generated API does not parse: %s: %v", name, err))
		}
	}
	data := read(filepath.Join(repository, "backend/core/generated/api-manifest.json"))
	manifest, err := api.Parse("backend/core/generated/api-manifest.json", data)
	if err != nil {
		panic(err)
	}
	operations := manifest.Operations()
	got := make([]string, len(operations))
	for index, operation := range operations {
		got[index] = operation.ID()
	}
	sort.Strings(got)
	sort.Strings(expected)
	if strings.Join(got, "\x00") != strings.Join(expected, "\x00") {
		panic(fmt.Sprintf("API manifest operations = %#v", got))
	}
}

func verifyServiceManifest(repository string) {
	name := "backend/account/generated/service-manifest.json"
	manifest, err := service.Parse(name, read(filepath.Join(repository, filepath.FromSlash(name))))
	if err != nil || manifest.ServiceID() != "account" || manifest.ServiceKind() != "rpc" || manifest.ModulePath() != "example.com/nexa-generation-consumer" || len(manifest.ContractSources()) == 0 {
		panic(fmt.Sprintf("service manifest is invalid: %#v, %v", manifest, err))
	}
}

func consumerTools(helper string) projectTools {
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		panic(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		panic(err)
	}
	version, err := exec.Command(goExecutable, "version").Output()
	if err != nil {
		panic(err)
	}
	goBase := toolchain.Tool{
		ID: "go", Version: "v1.25.0", Executable: goExecutable, InputScopes: []string{"repository", "scratch"},
		Environment: []toolchain.EnvironmentRule{
			{Name: "PATH", Source: toolchain.EnvironmentHost}, {Name: "GOROOT", Source: toolchain.EnvironmentHost},
			{Name: "GOMODCACHE", Source: toolchain.EnvironmentHost}, {Name: "GOPROXY", Source: toolchain.EnvironmentHost}, {Name: "GOSUMDB", Source: toolchain.EnvironmentHost},
			{Name: "HOME", Source: toolchain.EnvironmentScratch}, {Name: "TMPDIR", Source: toolchain.EnvironmentScratch},
			{Name: "GOPATH", Source: toolchain.EnvironmentScratch}, {Name: "GOCACHE", Source: toolchain.EnvironmentScratch},
			{Name: "GOWORK", Source: toolchain.EnvironmentFixed, FixedValue: "off"}, {Name: "GOENV", Source: toolchain.EnvironmentFixed, FixedValue: "off"},
			{Name: "GOTOOLCHAIN", Source: toolchain.EnvironmentFixed, FixedValue: "local"}, {Name: "GOFLAGS", Source: toolchain.EnvironmentFixed},
			{Name: "CGO_ENABLED", Source: toolchain.EnvironmentFixed, FixedValue: "0"},
		},
		Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: strings.TrimSpace(string(version))},
	}
	entGenerate, entCRUD := goBase, goBase
	entGenerate.WriteScopes = []string{"repository", "scratch"}
	entCRUD.WriteScopes = []string{"scratch"}
	helperBase := toolchain.Tool{
		Version: "v1.0.0", Executable: helper, InputScopes: []string{"staging"}, WriteScopes: []string{"staging"},
		Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: helperVersion},
	}
	rpc, apiTool := helperBase, helperBase
	rpc.ID, rpc.Args = "consumer.rpc-go", []string{"rpc"}
	apiTool.ID, apiTool.Args = "consumer.api-go", []string{"api"}
	return projectTools{entGenerate: entGenerate, entCRUD: entCRUD, rpc: rpc, api: apiTool}
}

func goEnvironment() []toolchain.EnvVar {
	return []toolchain.EnvVar{
		{Name: "PATH", Value: os.Getenv("PATH")}, {Name: "GOROOT", Value: runtime.GOROOT()},
		{Name: "GOMODCACHE", Value: os.Getenv("GOMODCACHE")}, {Name: "GOPROXY", Value: os.Getenv("GOPROXY")}, {Name: "GOSUMDB", Value: os.Getenv("GOSUMDB")},
	}
}

func copyAuthored(sourceRoot, destinationRoot, name string) {
	content := read(filepath.Join(sourceRoot, filepath.FromSlash(name)))
	filename := filepath.Join(destinationRoot, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filename, content, 0o644); err != nil {
		panic(err)
	}
}

func read(name string) []byte {
	content, err := os.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return content
}

func snapshot(repository string, paths []string) map[string][]byte {
	result := make(map[string][]byte, len(paths))
	for _, name := range paths {
		result[name] = read(filepath.Join(repository, filepath.FromSlash(name)))
	}
	return result
}

func equalSnapshot(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, content := range left {
		if !bytes.Equal(content, right[name]) {
			return false
		}
	}
	return true
}
