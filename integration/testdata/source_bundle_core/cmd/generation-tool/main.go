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

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/nexactl/host"
	"github.com/nxnminieye/nexa/nexactl/plugin"
	generation "github.com/nxnminieye/nexa/plugins/nexactl/generation"
)

const providerID = "consumer"

type provider struct{ tools tools }

type tools struct {
	ent  toolchain.Tool
	crud toolchain.Tool
	rpc  toolchain.Tool
	api  toolchain.Tool
}

func (p provider) Descriptor() generation.ProviderDescriptor {
	return generation.ProviderDescriptor{ID: providerID, Version: "v1.0.0", Tools: []generation.ProviderTool{
		{Role: generation.ToolRoleEntGenerate, Tool: delegated(p.tools.ent)},
		{Role: generation.ToolRoleEntCRUD, Tool: delegated(p.tools.crud)},
		{Role: generation.ToolRoleRPCGo, Tool: delegated(p.tools.rpc)},
		{Role: generation.ToolRoleAPIGo, Tool: delegated(p.tools.api)},
	}}
}

func (p provider) Resolve(context.Context, string) (generation.Project, error) {
	return generation.Project{CatalogPath: "services.yaml", CoreServiceID: "core", Services: []generation.ServiceProject{
		{
			ServiceID: "core", EntSchemaDir: "backend/core/ent/schema", ProtoEntry: "backend/core/desc/core.proto", APIEntry: "backend/core/desc/core.api",
			EntGenerateTool: p.tools.ent, RPCGoTool: p.tools.rpc, APIGoTool: p.tools.api,
		},
		{ServiceID: "account", EntSchemaDir: "backend/account/ent/schema", ProtoEntry: "backend/account/desc/account.proto", LogicRoot: "backend/account/internal/logic", EntGenerateTool: p.tools.ent, EntCRUDTool: p.tools.crud, RPCGoTool: p.tools.rpc},
	}}, nil
}

func delegated(value toolchain.Tool) plugin.DelegatedToolSpec {
	return plugin.DelegatedToolSpec{ID: value.ID, Version: value.Version, Inputs: []string{"consumer facts"}, Writes: []string{"generated artifacts"}}
}

type view struct {
	PlanDigest string `json:"planDigest"`
	Status     string `json:"status"`
	Sources    []struct {
		Ref    string `json:"ref"`
		Digest string `json:"digest"`
	} `json:"sources"`
	Artifacts []struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
	} `json:"artifacts"`
	Changes []struct {
		Kind        string `json:"kind"`
		ID          string `json:"id"`
		Path        string `json:"path"`
		Digest      string `json:"digest"`
		ControlRole string `json:"controlRole"`
	} `json:"changes"`
	Conflicts []struct {
		ID          string `json:"id"`
		Path        string `json:"path"`
		Reason      string `json:"reason"`
		ControlRole string `json:"controlRole"`
	} `json:"conflicts"`
	ControlSources []struct {
		AfterDigest string `json:"afterDigest"`
	} `json:"controlSources"`
}

type generationReport struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Commands   []commandReport `json:"commands"`
}

type commandReport struct {
	Sequence        int            `json:"sequence"`
	CommandID       string         `json:"commandId"`
	PlanDigest      string         `json:"planDigest"`
	Status          string         `json:"status"`
	ArtifactDigests []digestReport `json:"artifactDigests"`
	InputDigests    []digestReport `json:"inputDigests"`
}

type digestReport struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

func main() {
	repository := flag.String("repo-root", "", "consumer repository root")
	helper := flag.String("helper", "", "RPC/API generation helper")
	flag.Parse()
	if *repository == "" || *helper == "" {
		panic("repo-root and helper are required")
	}
	selected := projectTools(*helper, filepath.Join(*repository, ".framework"))
	buildPlugin, err := generation.New(generation.Options{
		Providers: []generation.ProjectProvider{provider{tools: selected}}, Runner: toolchain.NewExecRunner(), Environment: hostEnvironment(filepath.Join(*repository, ".framework")),
	})
	if err != nil {
		panic(err)
	}
	cli, err := host.New(host.Options{Name: "nexactl-generation", Version: "v0.1.0"}, buildPlugin)
	if err != nil {
		panic(err)
	}
	run := func(arguments []string) []byte { return executeResult(cli, arguments) }
	report := generationReport{}
	generateEnt(run, "core", []string{"--repo-root", *repository, "--provider", providerID, "--service", "core"})
	generateEnt(run, "account", []string{"--repo-root", *repository, "--provider", providerID, "--service", "account"})
	apply(cli, &report, []string{"generation", "crud"}, []string{"--repo-root", *repository, "--provider", providerID, "--service", "account"}, true)
	apply(cli, &report, []string{"generation", "rpc"}, []string{"--repo-root", *repository, "--provider", providerID, "--service", "core"}, false)
	apply(cli, &report, []string{"generation", "rpc"}, []string{"--repo-root", *repository, "--provider", providerID, "--service", "account"}, false)
	apply(cli, &report, []string{"generation", "api"}, []string{"--repo-root", *repository, "--provider", providerID, "--core-service", "core"}, false)
	applyManifest(cli, &report, []string{"--repo-root", *repository, "--provider", providerID, "--service", "core"})
	applyManifest(cli, &report, []string{"--repo-root", *repository, "--provider", providerID, "--service", "account"})
	encoded, err := encodeReport(report)
	if err != nil {
		panic(err)
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		panic(err)
	}
}

func apply(cli *host.Host, report *generationReport, base, flags []string, lock bool) {
	plan := execute(cli, report, appendCommand(base, "plan", flags))
	if len(plan.Conflicts) != 0 {
		encoded, _ := json.Marshal(plan)
		panic("generation plan has conflicts: " + strings.Join(base, " ") + ": " + string(encoded))
	}
	if len(plan.Changes) != 0 {
		writeFlags := append(append([]string(nil), flags...), "--plan-digest", plan.PlanDigest)
		if lock && len(plan.ControlSources) == 1 && plan.ControlSources[0].AfterDigest != "" {
			writeFlags = append(writeFlags, "--lock-digest", plan.ControlSources[0].AfterDigest)
		}
		execute(cli, report, appendCommand(base, "write", writeFlags))
	}
	checked := execute(cli, report, appendCommand(base, "check", flags))
	if checked.Status != "clean" {
		encoded, _ := json.Marshal(checked)
		panic("generation check is not clean: " + strings.Join(base, " ") + ": " + string(encoded))
	}
}

func applyManifest(cli *host.Host, report *generationReport, flags []string) {
	base := []string{"generation", "service-manifest"}
	checked := execute(cli, report, appendCommand(base, "check", flags))
	if checked.Status == "changes-required" {
		writeFlags := append(append([]string(nil), flags...), "--plan-digest", checked.PlanDigest)
		execute(cli, report, appendCommand(base, "write", writeFlags))
		checked = execute(cli, report, appendCommand(base, "check", flags))
	}
	if checked.Status != "clean" {
		encoded, _ := json.Marshal(checked)
		panic("service manifest check is not clean: " + string(encoded))
	}
}

func appendCommand(base []string, action string, flags []string) []string {
	arguments := append([]string(nil), base...)
	arguments = append(arguments, action)
	return append(arguments, flags...)
}

func generateEnt(execute func([]string) []byte, expectedService string, flags []string) {
	encoded := execute(append([]string{"gen", "ent"}, flags...))
	var result struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil || result.Status != "generated" || result.Service != expectedService {
		panic(fmt.Sprintf("Ent generation returned invalid result for expected %s: %v %s", expectedService, err, encoded))
	}
}

func execute(cli *host.Host, report *generationReport, arguments []string) view {
	encoded := executeResult(cli, arguments)
	var result view
	if err := json.Unmarshal(encoded, &result); err != nil || result.PlanDigest == "" {
		panic(fmt.Sprintf("command %v returned invalid result: %v %s", arguments, err, encoded))
	}
	command, err := reportCommand(len(report.Commands)+1, arguments, result)
	if err != nil {
		panic(fmt.Sprintf("command %v returned inconsistent report evidence: %v", arguments, err))
	}
	report.Commands = append(report.Commands, command)
	return result
}

func reportCommand(sequence int, arguments []string, result view) (commandReport, error) {
	status := result.Status
	if status == "" {
		switch {
		case len(result.Conflicts) != 0:
			status = "conflicts"
		case len(result.Changes) != 0:
			status = "changes-required"
		default:
			status = "clean"
		}
	}
	command := commandReport{Sequence: sequence, CommandID: commandIdentity(arguments), PlanDigest: result.PlanDigest, Status: status}
	artifactDigests := make(map[string]string, len(result.Artifacts)+len(result.Changes))
	appendArtifact := func(id, digest string) error {
		if id == "" || digest == "" {
			return nil
		}
		if previous, exists := artifactDigests[id]; exists {
			if previous != digest {
				return fmt.Errorf("artifact %q has conflicting digests %q and %q", id, previous, digest)
			}
			return nil
		}
		artifactDigests[id] = digest
		command.ArtifactDigests = append(command.ArtifactDigests, digestReport{ID: id, Digest: digest})
		return nil
	}
	for _, item := range result.Artifacts {
		if err := appendArtifact(item.ID, item.Digest); err != nil {
			return commandReport{}, err
		}
	}
	for _, item := range result.Changes {
		if item.ControlRole != "" {
			continue
		}
		if err := appendArtifact(item.ID, item.Digest); err != nil {
			return commandReport{}, err
		}
	}
	for _, item := range result.Sources {
		if item.Ref != "" && item.Digest != "" {
			command.InputDigests = append(command.InputDigests, digestReport{ID: item.Ref, Digest: item.Digest})
		}
	}
	return command, nil
}

func commandIdentity(arguments []string) string {
	command := make([]string, 0, len(arguments))
	selectors := map[string]string{}
	for index := 0; index < len(arguments); index++ {
		if !strings.HasPrefix(arguments[index], "--") {
			command = append(command, arguments[index])
			continue
		}
		name := strings.TrimPrefix(arguments[index], "--")
		if index+1 < len(arguments) && !strings.HasPrefix(arguments[index+1], "--") {
			if name == "provider" || name == "core-service" || name == "service" {
				selectors[name] = arguments[index+1]
			}
			index++
		}
	}
	identity := strings.Join(command, ".")
	query := make([]string, 0, 3)
	for _, name := range []string{"provider", "core-service", "service"} {
		if value := selectors[name]; value != "" {
			query = append(query, name+"="+value)
		}
	}
	if len(query) != 0 {
		identity += "?" + strings.Join(query, "&")
	}
	return identity
}

func encodeReport(report generationReport) ([]byte, error) {
	report.APIVersion = "nexa.dev/core-generation-report/v1"
	report.Kind = "CoreGenerationReport"
	sort.SliceStable(report.Commands, func(left, right int) bool {
		return report.Commands[left].Sequence < report.Commands[right].Sequence
	})
	for index := range report.Commands {
		command := &report.Commands[index]
		if command.ArtifactDigests == nil {
			command.ArtifactDigests = []digestReport{}
		}
		if command.InputDigests == nil {
			command.InputDigests = []digestReport{}
		}
		sort.SliceStable(command.ArtifactDigests, func(left, right int) bool {
			if command.ArtifactDigests[left].ID != command.ArtifactDigests[right].ID {
				return command.ArtifactDigests[left].ID < command.ArtifactDigests[right].ID
			}
			return command.ArtifactDigests[left].Digest < command.ArtifactDigests[right].Digest
		})
		sort.SliceStable(command.InputDigests, func(left, right int) bool {
			if command.InputDigests[left].ID != command.InputDigests[right].ID {
				return command.InputDigests[left].ID < command.InputDigests[right].ID
			}
			return command.InputDigests[left].Digest < command.InputDigests[right].Digest
		})
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func executeResult(cli *host.Host, arguments []string) []byte {
	var stdout, stderr bytes.Buffer
	exit := cli.Execute(context.Background(), append(arguments, "--json"), &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		panic(fmt.Sprintf("command %v failed: exit=%d stderr=%s stdout=%s", arguments, exit, stderr.String(), stdout.String()))
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || !envelope.OK || envelope.Error != nil {
		panic(fmt.Sprintf("command %v returned invalid envelope: %v %#v", arguments, err, envelope.Error))
	}
	encoded, err := json.Marshal(envelope.Result)
	if err != nil {
		panic(err)
	}
	return encoded
}

func projectTools(helper, frameworkRoot string) tools {
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
	crud := toolchain.Tool{
		ID: "go", Version: "v1.25.0", Executable: goExecutable, InputScopes: []string{"repository", "scratch"}, WriteScopes: []string{"scratch"},
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
	ent := crud
	ent.WriteScopes = []string{"repository", "scratch"}
	helperTool := toolchain.Tool{
		Version: "v1.0.0", Executable: helper, InputScopes: []string{"staging"}, WriteScopes: []string{"staging"},
		Environment: append([]toolchain.EnvironmentRule(nil), crud.Environment...),
		Probe:       toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "nexa-core-generation-helper v1.0.0"},
	}
	helperTool.Environment = append(helperTool.Environment, toolchain.EnvironmentRule{Name: "NEXA_FRAMEWORK_ROOT", Source: toolchain.EnvironmentHost})
	rpc, api := helperTool, helperTool
	rpc.ID, rpc.Args = "consumer.rpc-go", []string{"rpc"}
	api.ID, api.Args = "consumer.api-go", []string{"api"}
	return tools{ent: ent, crud: crud, rpc: rpc, api: api}
}

func hostEnvironment(frameworkRoot string) []toolchain.EnvVar {
	canonicalFramework, err := filepath.EvalSymlinks(frameworkRoot)
	if err != nil {
		panic(err)
	}
	return []toolchain.EnvVar{
		{Name: "PATH", Value: os.Getenv("PATH")}, {Name: "GOROOT", Value: runtime.GOROOT()},
		{Name: "GOMODCACHE", Value: os.Getenv("GOMODCACHE")}, {Name: "GOPROXY", Value: os.Getenv("GOPROXY")}, {Name: "GOSUMDB", Value: os.Getenv("GOSUMDB")},
		{Name: "NEXA_FRAMEWORK_ROOT", Value: canonicalFramework},
	}
}
