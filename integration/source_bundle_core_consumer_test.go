package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nxnminieye/nexa/generation/apigo"
	"github.com/nxnminieye/nexa/generation/composition"
	generationhttpapi "github.com/nxnminieye/nexa/generation/httpapi"
	generationprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/toolchain"
	"github.com/nxnminieye/nexa/project/servicecatalog"
	"github.com/nxnminieye/nexa/provenance"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
	modmodule "golang.org/x/mod/module"
)

type coreSourceRef struct {
	Provider       string `json:"provider"`
	Version        string `json:"version"`
	ManifestDigest string `json:"manifestDigest"`
	TreeDigest     string `json:"treeDigest"`
}

type coreModuleSnapshot struct {
	goMod []byte
	goSum []byte
}

func TestSourceBundleCoreMaterializesGeneratesCompilesAndRuns(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	makeTreeWritableOnCleanup(t, temporary)
	consumer := filepath.Join(temporary, "consumer")
	frontendConsumer := filepath.Join(temporary, "frontend-consumer")
	fixture := filepath.Join(repositoryRoot(t), "fixtures", "consumers", "framework-minimum")
	if err := os.CopyFS(consumer, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy Core consumer: %v", err)
	}
	coreTemplate := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core")
	if err := os.CopyFS(consumer, os.DirFS(coreTemplate)); err != nil {
		t.Fatalf("copy Core consumer template: %v", err)
	}
	restoreCoreCommand := deferCoreCommandUntilGenerated(t, consumer)
	sourceModule := filepath.Join(temporary, "source-tool-module")
	generationModule := filepath.Join(temporary, "generation-tool-module")
	if err := os.MkdirAll(sourceModule, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(generationModule, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(consumer, "cmd", "source-tool", "main.go"), filepath.Join(sourceModule, "main.go"))
	copyFile(t, filepath.Join(consumer, "cmd", "generation-tool", "main.go"), filepath.Join(generationModule, "main.go"))
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(filepath.Join(generationModule, "cmd", "generation-helper"), os.DirFS(helperSource)); err != nil {
		t.Fatalf("copy consumer generation helper: %v", err)
	}
	for _, tool := range []string{"source-tool", "generation-tool", "generation-helper"} {
		if err := os.RemoveAll(filepath.Join(consumer, "cmd", tool)); err != nil {
			t.Fatal(err)
		}
	}

	framework := filepath.Join(temporary, "framework")
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatalf("materialize framework module: %v", err)
	}
	writeToolModule(t, sourceModule, "example.com/nexa-source-tool", framework)
	moduleFile := readModuleFile(t, filepath.Join(consumer, "go.mod"))
	if err := moduleFile.DropReplace(nexaModulePath, ""); err != nil {
		t.Fatal(err)
	}
	if err := moduleFile.AddReplace(nexaModulePath, "", filepath.ToSlash(framework), ""); err != nil {
		t.Fatal(err)
	}
	for _, requirement := range []modmodule.Version{
		{Path: "entgo.io/ent", Version: "v0.14.5"},
		{Path: "golang.org/x/crypto", Version: "v0.48.0"},
		{Path: "golang.org/x/net", Version: "v0.50.0"},
		{Path: "golang.org/x/term", Version: "v0.40.0"},
	} {
		if err := moduleFile.AddRequire(requirement.Path, requirement.Version); err != nil {
			t.Fatal(err)
		}
	}
	if err := moduleFile.AddReplace("golang.org/x/tools", "v0.41.0", "golang.org/x/tools", "v0.39.0"); err != nil {
		t.Fatal(err)
	}
	formatted, err := moduleFile.Format()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), formatted, 0o644); err != nil {
		t.Fatal(err)
	}
	downloadModuleGraph(t, consumer)

	environment := isolatedExternalGoEnvironment(t, filepath.Join(temporary, "go-environment"))
	goPath := filepath.Join(temporary, "go-environment", "gopath")
	tempPath := filepath.Join(temporary, "go-environment", "tmp")
	toolPath := filepath.Join(temporary, "go-environment", "bin")
	for _, directory := range []string{goPath, tempPath, toolPath} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(goExecutable, filepath.Join(toolPath, "go")); err != nil {
		t.Fatal(err)
	}
	environment = overriddenEnvironment(environment,
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")),
		"GOPATH="+goPath,
		"TMPDIR="+tempPath,
		"PATH="+toolPath,
		"CGO_ENABLED=0",
		"GOTELEMETRY=off",
		"NEXA_SOURCE_CACHE="+filepath.Join(temporary, "source-cache"),
	)
	makeTreeWritableOnCleanup(t, filepath.Join(temporary, "go-environment", "gomodcache"))
	if executableInEnvironment(environment, "node") {
		t.Fatal("Core backend consumer unexpectedly has Node available")
	}
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "download")
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "download",
		"github.com/google/uuid@v1.6.0",
		"github.com/santhosh-tekuri/jsonschema/v6@v6.0.2",
		"golang.org/x/mod@v0.32.0",
		"golang.org/x/net@v0.50.0",
		"golang.org/x/sync@v0.19.0",
		"golang.org/x/sys@v0.41.0",
		"golang.org/x/term@v0.40.0",
		"golang.org/x/text@v0.34.0",
		"golang.org/x/tools@v0.39.0",
		"gopkg.in/yaml.v3@v3.0.1",
	)

	sourceTool := filepath.Join(temporary, "source-tool")
	runFrameworkConsumer(t, sourceModule, environment, "go", "build", "-mod=mod", "-o", sourceTool, ".")
	var sourceRef coreSourceRef
	if err := json.Unmarshal(runFrameworkConsumer(t, sourceModule, environment, sourceTool, "ref"), &sourceRef); err != nil {
		t.Fatalf("decode Core source ref: %v", err)
	}
	selection := []string{
		"--repo-root", temporary, "--provider", sourceRef.Provider, "--version", sourceRef.Version,
		"--profile", "backend", "--target", "consumer", "--manifest-digest", sourceRef.ManifestDigest,
		"--tree-digest", sourceRef.TreeDigest, "--json",
	}
	runFrameworkConsumer(t, sourceModule, environment, sourceTool, append([]string{"source", "materialize"}, selection...)...)
	if _, err := os.Stat(filepath.Join(consumer, "frontend")); !os.IsNotExist(err) {
		t.Fatalf("Core backend materialization unexpectedly selected frontend: %v", err)
	}
	frontendSelection := []string{
		"--repo-root", temporary, "--provider", sourceRef.Provider, "--version", sourceRef.Version,
		"--profile", "frontend", "--target", "frontend-consumer", "--manifest-digest", sourceRef.ManifestDigest,
		"--tree-digest", sourceRef.TreeDigest, "--json",
	}
	runFrameworkConsumer(t, sourceModule, environment, sourceTool, append([]string{"source", "materialize"}, frontendSelection...)...)
	if _, err := os.Stat(filepath.Join(frontendConsumer, "backend")); !os.IsNotExist(err) {
		t.Fatalf("Core frontend materialization unexpectedly selected backend: %v", err)
	}
	consumerFramework := filepath.Join(consumer, ".framework")
	if err := os.Rename(framework, consumerFramework); err != nil {
		t.Fatalf("move framework module into consumer: %v", err)
	}
	framework = consumerFramework
	configureCoreConsumerModule(t, consumer, framework)
	writeToolModule(t, generationModule, "example.com/nexa-generation-tool", framework)
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=mod", "./backend/core/coreapp", "./backend/core/ent/schema")
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=readonly", "./backend/core/coreapp", "./backend/core/ent/schema")

	helper := filepath.Join(temporary, "generation-helper")
	generationTool := filepath.Join(temporary, "generation-tool")
	runFrameworkConsumer(t, generationModule, environment, "go", "build", "-mod=mod", "-o", helper, "./cmd/generation-helper")
	runFrameworkConsumer(t, generationModule, environment, "go", "build", "-mod=mod", "-o", generationTool, ".")
	assertPackageGraphExcludes(t, generationModule, environment, ".",
		nexaModulePath+"/plugins/nexactl/source", nexaModulePath+"/plugins/service/core", nexaModulePath+"/sourceplugin")

	moduleSnapshot := readCoreModuleSnapshot(t, consumer)
	initialReport := runAndValidateGenerationReport(t, "primary-initial", consumer, environment, generationTool, helper, coreGenerationWritesAll)
	restoreCoreCommand()
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "tidy")
	writeCoreTenantIDContractTest(t, consumer)
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=readonly", "./cmd/core")
	runAndValidateGenerationReport(t, "primary-clean", consumer, environment, generationTool, helper, coreGenerationWritesManifestOnly)
	first := generatedSnapshot(t, consumer)
	if len(first) == 0 {
		t.Fatal("Core generation produced no artifacts")
	}
	fresh, freshReport := materializeFreshCoreGeneratedSnapshot(t, sourceRef, environment, moduleSnapshot)
	if difference := snapshotDifference(first, fresh); difference != "" {
		t.Fatalf("fresh Core materialize -> generate artifact bytes differ from primary snapshot: %s", difference)
	}
	if !bytes.Equal(initialReport, freshReport) {
		t.Fatalf("fresh Core generation report differs from primary report: %s", generationReportDifference(t, initialReport, freshReport))
	}
	if _, err := os.Stat(filepath.Join(consumer, "backend", "account", "ent", "client.go")); err != nil {
		t.Fatalf("Core Ent generation did not produce the account client: %v", err)
	}
	if _, err := os.Stat(filepath.Join(consumer, "backend", "core", "ent", "client.go")); err != nil {
		t.Fatalf("Core Ent generation did not produce the Core client: %v", err)
	}
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=readonly", "./backend/account/...", "./backend/core/...")
	primaryBinary := filepath.Join(temporary, "core-runtime-primary")
	primaryBinarySHA := buildCoreBinary(t, consumer, environment, primaryBinary)
	runCoreBinaryBehavior(t, consumer, environment, primaryBinary, primaryBinarySHA, false, "/auth/providers", "")
	runCoreBinaryBehavior(t, consumer, environment, primaryBinary, primaryBinarySHA, true, "/auth/providers", "")

	for _, target := range []string{"consumer", "frontend-consumer"} {
		managed := []string{"source", "detach", "--repo-root", temporary, "--provider", sourceRef.Provider, "--target", target, "--json"}
		runFrameworkConsumer(t, sourceModule, environment, sourceTool, managed...)
	}
	if err := os.Remove(sourceTool); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sourceModule); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(temporary, "source-cache")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"plugins/nexactl/source", "plugins/service/core", "sourceplugin"} {
		if err := os.RemoveAll(filepath.Join(framework, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}

	assertDetachedSourceState(t, temporary)
	validateDetachedMigrationFacts(t, consumer)
	validateDetachedFrontendFacts(t, frontendConsumer)
	assertPackageGraphExcludes(t, generationModule, environment, ".",
		nexaModulePath+"/plugins/nexactl/source", nexaModulePath+"/plugins/service/core", nexaModulePath+"/sourceplugin")
	runAndValidateGenerationReport(t, "detached-first", consumer, environment, generationTool, helper, coreGenerationWritesNone)
	second := generatedSnapshot(t, consumer)
	if difference := snapshotDifference(first, second); difference != "" {
		t.Fatalf("generated artifact bytes changed after source detach and provider/cache removal: %s", difference)
	}
	runAndValidateGenerationReport(t, "detached-second", consumer, environment, generationTool, helper, coreGenerationWritesNone)
	third := generatedSnapshot(t, consumer)
	if difference := snapshotDifference(second, third); difference != "" {
		t.Fatalf("generated artifact bytes drifted on repeated detached generation: %s", difference)
	}
	assertDetachedNativeFactMutation(t, consumer, environment, generationTool, helper)
	if err := os.Remove(generationTool); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(generationModule); err != nil {
		t.Fatal(err)
	}
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=readonly", "./backend/account/...", "./backend/core/...")
	assertPackageGraphExcludes(t, consumer, environment, "./cmd/core",
		nexaModulePath+"/generation", nexaModulePath+"/plugins/nexactl/generation", nexaModulePath+"/plugins/nexactl/source", nexaModulePath+"/plugins/service/core", nexaModulePath+"/sourceplugin")
	detachedBinary := filepath.Join(temporary, "core-runtime-detached")
	detachedBinarySHA := buildCoreBinary(t, consumer, environment, detachedBinary)
	runCoreBinaryBehavior(t, consumer, environment, detachedBinary, detachedBinarySHA, false, "/auth/providers-detached", "/auth/providers")
	runCoreBinaryBehavior(t, consumer, environment, detachedBinary, detachedBinarySHA, true, "/auth/providers-detached", "/auth/providers")
}

type coreGenerationReport struct {
	APIVersion string                        `json:"apiVersion"`
	Kind       string                        `json:"kind"`
	Commands   []coreGenerationCommandReport `json:"commands"`
}

type coreGenerationCommandReport struct {
	Sequence        int                          `json:"sequence"`
	CommandID       string                       `json:"commandId"`
	PlanDigest      string                       `json:"planDigest"`
	Status          string                       `json:"status"`
	ArtifactDigests []coreGenerationDigestReport `json:"artifactDigests"`
	InputDigests    []coreGenerationDigestReport `json:"inputDigests"`
}

type coreGenerationDigestReport struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type coreGenerationWriteMode uint8

const (
	coreGenerationWritesNone coreGenerationWriteMode = iota
	coreGenerationWritesAll
	coreGenerationWritesManifestOnly
)

func TestValidateGenerationReportCommandRequiresPlanEvidence(t *testing.T) {
	validDigest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	validPlan := coreGenerationCommandReport{
		Sequence: 1, CommandID: "generation.rpc.plan?provider=consumer&service=core", PlanDigest: validDigest, Status: "changes-required",
		ArtifactDigests: []coreGenerationDigestReport{{ID: "transport", Digest: validDigest}},
		InputDigests:    []coreGenerationDigestReport{{ID: "source", Digest: validDigest}},
	}
	tests := []struct {
		name    string
		command coreGenerationCommandReport
		want    string
	}{
		{name: "valid plan", command: validPlan},
		{name: "missing artifacts", command: withGenerationDigests(validPlan, nil, validPlan.InputDigests), want: "artifactDigests"},
		{name: "missing inputs", command: withGenerationDigests(validPlan, validPlan.ArtifactDigests, nil), want: "inputDigests"},
		{name: "duplicate artifact ID", command: withGenerationDigests(validPlan, append(validPlan.ArtifactDigests, validPlan.ArtifactDigests[0]), validPlan.InputDigests), want: "duplicate artifactDigests ID"},
		{name: "duplicate input ID", command: withGenerationDigests(validPlan, validPlan.ArtifactDigests, append(validPlan.InputDigests, validPlan.InputDigests[0])), want: "duplicate inputDigests ID"},
		{name: "invalid artifact digest", command: withGenerationDigests(validPlan, []coreGenerationDigestReport{{ID: "transport", Digest: "invalid"}}, validPlan.InputDigests), want: "invalid"},
		{
			name: "service manifest check may omit plan evidence",
			command: coreGenerationCommandReport{
				Sequence: 1, CommandID: "generation.service-manifest.check?provider=consumer&service=core", PlanDigest: validDigest, Status: "clean",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateGenerationReportCommand("test", 0, "/consumer", test.command)
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validation error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func withGenerationDigests(command coreGenerationCommandReport, artifacts, inputs []coreGenerationDigestReport) coreGenerationCommandReport {
	command.ArtifactDigests = artifacts
	command.InputDigests = inputs
	return command
}

func runAndValidateGenerationReport(t *testing.T, label, consumer string, environment []string, generationTool, helper string, writeMode coreGenerationWriteMode) []byte {
	t.Helper()
	encoded := runFrameworkConsumer(t, consumer, environment, generationTool, "--repo-root", consumer, "--helper", helper)
	var report coreGenerationReport
	if err := json.Unmarshal(encoded, &report); err != nil {
		t.Fatalf("decode %s generation report: %v\n%s", label, err, encoded)
	}
	if report.APIVersion != "nexa.dev/core-generation-report/v1" || report.Kind != "CoreGenerationReport" {
		t.Fatalf("%s generation report identity = %q %q", label, report.APIVersion, report.Kind)
	}
	expected := expectedCoreGenerationCommands(writeMode)
	actual := make(map[string]int, len(expected))
	artifactDigestCount, inputDigestCount := 0, 0
	for index, command := range report.Commands {
		if err := validateGenerationReportCommand(label, index, consumer, command); err != nil {
			t.Fatal(err)
		}
		actual[command.CommandID]++
		artifactDigestCount += len(command.ArtifactDigests)
		inputDigestCount += len(command.InputDigests)
	}
	if len(report.Commands) == 0 || artifactDigestCount == 0 || inputDigestCount == 0 {
		t.Fatalf("%s generation report lacks command/digest evidence: commands=%d artifacts=%d inputs=%d", label, len(report.Commands), artifactDigestCount, inputDigestCount)
	}
	if len(actual) != len(expected) {
		t.Fatalf("%s generation command identity count = %#v, want %#v", label, actual, expected)
	}
	for commandID, count := range expected {
		if actual[commandID] != count {
			t.Fatalf("%s generation command %q count = %d, want %d (all=%#v)", label, commandID, actual[commandID], count, actual)
		}
	}
	t.Logf("%s generation report: commands=%d digest=%x", label, len(report.Commands), sha256.Sum256(encoded))
	return encoded
}

func validateGenerationReportCommand(label string, index int, consumer string, command coreGenerationCommandReport) error {
	if command.Sequence != index+1 || command.CommandID == "" {
		return fmt.Errorf("%s generation report command[%d] identity = %#v", label, index, command)
	}
	base, _, _ := strings.Cut(command.CommandID, "?")
	isPlan := strings.HasSuffix(base, ".plan")
	if !isPlan && !strings.HasSuffix(base, ".write") && !strings.HasSuffix(base, ".check") {
		return fmt.Errorf("%s generation report command[%d] is not a plan/write/check command: %q", label, index, command.CommandID)
	}
	if strings.Contains(command.CommandID, consumer) || strings.Contains(command.CommandID, "repo-root") {
		return fmt.Errorf("%s generation report command[%d] leaked repository identity: %q", label, index, command.CommandID)
	}
	if _, err := provenance.ParseDigest(command.PlanDigest); err != nil {
		return fmt.Errorf("%s generation report command[%d] plan digest %q: %w", label, index, command.PlanDigest, err)
	}
	if command.Status != "clean" && command.Status != "changes-required" {
		return fmt.Errorf("%s generation report command[%d] status = %q", label, index, command.Status)
	}
	if isPlan && len(command.ArtifactDigests) == 0 {
		return fmt.Errorf("%s generation report command[%d] plan has empty artifactDigests", label, index)
	}
	if isPlan && len(command.InputDigests) == 0 {
		return fmt.Errorf("%s generation report command[%d] plan has empty inputDigests", label, index)
	}
	if err := validateGenerationDigestReports(label, index, "artifactDigests", command.ArtifactDigests); err != nil {
		return err
	}
	return validateGenerationDigestReports(label, index, "inputDigests", command.InputDigests)
}

func generationReportDifference(t *testing.T, primary, fresh []byte) string {
	t.Helper()
	var left, right coreGenerationReport
	if err := json.Unmarshal(primary, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fresh, &right); err != nil {
		t.Fatal(err)
	}
	if len(left.Commands) != len(right.Commands) {
		return fmt.Sprintf("command count primary=%d fresh=%d", len(left.Commands), len(right.Commands))
	}
	var differences []string
	for index := range left.Commands {
		before, after := left.Commands[index], right.Commands[index]
		identity := before.CommandID
		if before.CommandID != after.CommandID || before.Sequence != after.Sequence {
			differences = append(differences, fmt.Sprintf("command[%d] identity primary=%d:%s fresh=%d:%s", index, before.Sequence, before.CommandID, after.Sequence, after.CommandID))
			continue
		}
		if before.PlanDigest != after.PlanDigest {
			differences = append(differences, fmt.Sprintf("%s planDigest primary=%s fresh=%s", identity, before.PlanDigest, after.PlanDigest))
		}
		if before.Status != after.Status {
			differences = append(differences, fmt.Sprintf("%s status primary=%s fresh=%s", identity, before.Status, after.Status))
		}
		beforeArtifacts, _ := json.Marshal(before.ArtifactDigests)
		afterArtifacts, _ := json.Marshal(after.ArtifactDigests)
		if !bytes.Equal(beforeArtifacts, afterArtifacts) {
			differences = append(differences, identity+" artifactDigests differ")
		}
		beforeInputs, _ := json.Marshal(before.InputDigests)
		afterInputs, _ := json.Marshal(after.InputDigests)
		if !bytes.Equal(beforeInputs, afterInputs) {
			differences = append(differences, identity+" inputDigests "+generationDigestDifference(before.InputDigests, after.InputDigests))
		}
	}
	if len(differences) == 0 {
		return fmt.Sprintf("canonical bytes primary=%x fresh=%x", sha256.Sum256(primary), sha256.Sum256(fresh))
	}
	return strings.Join(differences, "; ")
}

func generationDigestDifference(primary, fresh []coreGenerationDigestReport) string {
	left := make(map[string]string, len(primary))
	right := make(map[string]string, len(fresh))
	for _, value := range primary {
		left[value.ID] = value.Digest
	}
	for _, value := range fresh {
		right[value.ID] = value.Digest
	}
	keys := make([]string, 0, len(left)+len(right))
	for key := range left {
		keys = append(keys, key)
	}
	for key := range right {
		if _, exists := left[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var differences []string
	for _, key := range keys {
		if left[key] != right[key] {
			differences = append(differences, fmt.Sprintf("%s primary=%s fresh=%s", key, left[key], right[key]))
		}
	}
	return strings.Join(differences, ", ")
}

func validateGenerationDigestReports(label string, index int, kind string, digests []coreGenerationDigestReport) error {
	identities := make(map[string]struct{}, len(digests))
	for _, digest := range digests {
		if digest.ID == "" {
			return fmt.Errorf("%s generation report command[%d] has empty %s identity", label, index, kind)
		}
		if _, exists := identities[digest.ID]; exists {
			return fmt.Errorf("%s generation report command[%d] has duplicate %s ID %q", label, index, kind, digest.ID)
		}
		identities[digest.ID] = struct{}{}
		if _, err := provenance.ParseDigest(digest.Digest); err != nil {
			return fmt.Errorf("%s generation report command[%d] %s %s=%q: %w", label, index, kind, digest.ID, digest.Digest, err)
		}
	}
	return nil
}

func expectedCoreGenerationCommands(writeMode coreGenerationWriteMode) map[string]int {
	result := map[string]int{}
	for _, identity := range []string{
		"generation.crud?provider=consumer&service=account",
		"generation.rpc?provider=consumer&service=core",
		"generation.rpc?provider=consumer&service=account",
		"generation.api?provider=consumer&core-service=core",
	} {
		result[strings.Replace(identity, "?", ".plan?", 1)] = 1
		result[strings.Replace(identity, "?", ".check?", 1)] = 1
		if writeMode == coreGenerationWritesAll {
			result[strings.Replace(identity, "?", ".write?", 1)] = 1
		}
	}
	for _, identity := range []string{
		"generation.service-manifest?provider=consumer&service=core",
		"generation.service-manifest?provider=consumer&service=account",
	} {
		checks := 1
		if writeMode == coreGenerationWritesAll || writeMode == coreGenerationWritesManifestOnly {
			checks = 2
			result[strings.Replace(identity, "?", ".write?", 1)] = 1
		}
		result[strings.Replace(identity, "?", ".check?", 1)] = checks
	}
	return result
}

func materializeFreshCoreGeneratedSnapshot(t *testing.T, expectedRef coreSourceRef, primaryEnvironment []string, expectedModule coreModuleSnapshot) (map[string][]byte, []byte) {
	t.Helper()
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	makeTreeWritableOnCleanup(t, temporary)
	consumer := filepath.Join(temporary, "fresh-consumer")
	fixture := filepath.Join(repositoryRoot(t), "fixtures", "consumers", "framework-minimum")
	if err := os.CopyFS(consumer, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy fresh Core consumer: %v", err)
	}
	coreTemplate := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core")
	if err := os.CopyFS(consumer, os.DirFS(coreTemplate)); err != nil {
		t.Fatalf("copy fresh Core consumer template: %v", err)
	}
	restoreCoreCommand := deferCoreCommandUntilGenerated(t, consumer)

	sourceModule := filepath.Join(temporary, "fresh-source-tool-module")
	generationModule := filepath.Join(temporary, "fresh-generation-tool-module")
	for _, directory := range []string{sourceModule, generationModule} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, filepath.Join(consumer, "cmd", "source-tool", "main.go"), filepath.Join(sourceModule, "main.go"))
	copyFile(t, filepath.Join(consumer, "cmd", "generation-tool", "main.go"), filepath.Join(generationModule, "main.go"))
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(filepath.Join(generationModule, "cmd", "generation-helper"), os.DirFS(helperSource)); err != nil {
		t.Fatalf("copy fresh consumer generation helper: %v", err)
	}
	for _, tool := range []string{"source-tool", "generation-tool", "generation-helper"} {
		if err := os.RemoveAll(filepath.Join(consumer, "cmd", tool)); err != nil {
			t.Fatal(err)
		}
	}

	framework := filepath.Join(temporary, "fresh-framework")
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatalf("materialize fresh framework module: %v", err)
	}
	restoreProviderManifest := reverseCoreProviderFileOrder(t, filepath.Join(framework, "plugins", "service", "core", "bundle.json"))
	writeToolModule(t, sourceModule, "example.com/nexa-fresh-source-tool", framework)
	configureCoreConsumerModule(t, consumer, framework)

	environment := freshCoreEnvironment(t, temporary, primaryEnvironment)
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "download")
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "download",
		"github.com/google/uuid@v1.6.0",
		"github.com/santhosh-tekuri/jsonschema/v6@v6.0.2",
		"golang.org/x/mod@v0.32.0",
		"golang.org/x/net@v0.50.0",
		"golang.org/x/sync@v0.19.0",
		"golang.org/x/sys@v0.41.0",
		"golang.org/x/term@v0.40.0",
		"golang.org/x/text@v0.34.0",
		"golang.org/x/tools@v0.39.0",
		"gopkg.in/yaml.v3@v3.0.1",
	)

	sourceTool := filepath.Join(temporary, "fresh-source-tool")
	runFrameworkConsumer(t, sourceModule, environment, "go", "build", "-mod=mod", "-o", sourceTool, ".")
	var actualRef coreSourceRef
	if err := json.Unmarshal(runFrameworkConsumer(t, sourceModule, environment, sourceTool, "ref"), &actualRef); err != nil {
		t.Fatalf("decode fresh Core source ref: %v", err)
	}
	if actualRef != expectedRef {
		t.Fatalf("provider ref changed after equivalent authored file-order reversal: got %#v want %#v", actualRef, expectedRef)
	}
	selection := []string{
		"--repo-root", temporary, "--provider", actualRef.Provider, "--version", actualRef.Version,
		"--profile", "backend", "--target", "fresh-consumer", "--manifest-digest", actualRef.ManifestDigest,
		"--tree-digest", actualRef.TreeDigest, "--json",
	}
	runFrameworkConsumer(t, sourceModule, environment, sourceTool, append([]string{"source", "materialize"}, selection...)...)
	if _, err := os.Stat(filepath.Join(consumer, "frontend")); !os.IsNotExist(err) {
		t.Fatalf("fresh Core backend materialization unexpectedly selected frontend: %v", err)
	}
	restoreProviderManifest()

	consumerFramework := filepath.Join(consumer, ".framework")
	if err := os.Rename(framework, consumerFramework); err != nil {
		t.Fatalf("move fresh framework module into consumer: %v", err)
	}
	framework = consumerFramework
	configureCoreConsumerModule(t, consumer, framework)
	downloadModuleGraph(t, consumer)
	writeToolModule(t, generationModule, "example.com/nexa-fresh-generation-tool", framework)
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=mod", "./backend/core/coreapp", "./backend/core/ent/schema")
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=readonly", "./backend/core/coreapp", "./backend/core/ent/schema")

	helper := filepath.Join(temporary, "fresh-generation-helper")
	generationTool := filepath.Join(temporary, "fresh-generation-tool")
	runFrameworkConsumer(t, generationModule, environment, "go", "build", "-mod=mod", "-o", helper, "./cmd/generation-helper")
	runFrameworkConsumer(t, generationModule, environment, "go", "build", "-mod=mod", "-o", generationTool, ".")
	assertCoreModuleSnapshot(t, expectedModule, readCoreModuleSnapshot(t, consumer))
	initialReport := runAndValidateGenerationReport(t, "fresh-initial", consumer, environment, generationTool, helper, coreGenerationWritesAll)
	restoreCoreCommand()
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "tidy")
	runAndValidateGenerationReport(t, "fresh-clean", consumer, environment, generationTool, helper, coreGenerationWritesManifestOnly)
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=readonly", "./backend/account/...", "./backend/core/...")

	snapshot := generatedSnapshot(t, consumer)
	if len(snapshot) == 0 {
		t.Fatal("fresh Core generation produced no artifacts")
	}
	return snapshot, initialReport
}

func configureCoreConsumerModule(t *testing.T, consumer, framework string) {
	t.Helper()
	moduleFile := readModuleFile(t, filepath.Join(consumer, "go.mod"))
	if err := moduleFile.DropReplace(nexaModulePath, ""); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.ToSlash(framework)
	if relative, err := filepath.Rel(consumer, framework); err == nil && relative == ".framework" {
		replacement = "./.framework"
	}
	if err := moduleFile.AddReplace(nexaModulePath, "", replacement, ""); err != nil {
		t.Fatal(err)
	}
	for _, requirement := range []modmodule.Version{
		{Path: "entgo.io/ent", Version: "v0.14.5"},
		{Path: "golang.org/x/crypto", Version: "v0.48.0"},
		{Path: "golang.org/x/net", Version: "v0.50.0"},
		{Path: "golang.org/x/term", Version: "v0.40.0"},
	} {
		if err := moduleFile.AddRequire(requirement.Path, requirement.Version); err != nil {
			t.Fatal(err)
		}
	}
	if err := moduleFile.AddReplace("golang.org/x/tools", "v0.41.0", "golang.org/x/tools", "v0.39.0"); err != nil {
		t.Fatal(err)
	}
	formatted, err := moduleFile.Format()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), formatted, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCoreModuleSnapshot(t *testing.T, consumer string) coreModuleSnapshot {
	t.Helper()
	result := coreModuleSnapshot{goMod: mustReadFile(t, filepath.Join(consumer, "go.mod"))}
	if value, err := os.ReadFile(filepath.Join(consumer, "go.sum")); err == nil {
		result.goSum = value
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return result
}

func assertCoreModuleSnapshot(t *testing.T, expected, actual coreModuleSnapshot) {
	t.Helper()
	if !bytes.Equal(actual.goMod, expected.goMod) {
		t.Fatalf("fresh Core go.mod differs before initial generation: got %x want %x", sha256.Sum256(actual.goMod), sha256.Sum256(expected.goMod))
	}
	if !bytes.Equal(actual.goSum, expected.goSum) {
		t.Fatalf("fresh Core go.sum differs before initial generation: got %x want %x", sha256.Sum256(actual.goSum), sha256.Sum256(expected.goSum))
	}
}

func reverseCoreProviderFileOrder(t *testing.T, manifestPath string) func() {
	t.Helper()
	encoded := mustReadFile(t, manifestPath)
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode fresh Core provider manifest: %v", err)
	}
	var files []json.RawMessage
	if err := json.Unmarshal(document["files"], &files); err != nil || len(files) < 2 {
		t.Fatalf("decode fresh Core provider files: count=%d err=%v", len(files), err)
	}
	for left, right := 0, len(files)-1; left < right; left, right = left+1, right-1 {
		files[left], files[right] = files[right], files[left]
	}
	reordered, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	document["files"] = reordered
	rewritten, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(rewritten, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	restored := false
	return func() {
		t.Helper()
		if restored {
			t.Fatal("Core provider manifest restored more than once")
		}
		if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		if actual := mustReadFile(t, manifestPath); !bytes.Equal(actual, encoded) {
			t.Fatal("Core provider manifest bytes changed while restoring authored order fixture")
		}
		restored = true
	}
}

func freshCoreEnvironment(t *testing.T, temporary string, primary []string) []string {
	t.Helper()
	root := filepath.Join(temporary, "fresh-go-environment")
	environment := isolatedExternalGoEnvironment(t, root)
	goPath := filepath.Join(root, "gopath")
	tempPath := filepath.Join(root, "tmp")
	toolPath := filepath.Join(root, "bin")
	for _, directory := range []string{goPath, tempPath, toolPath} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(goExecutable, filepath.Join(toolPath, "go")); err != nil {
		t.Fatal(err)
	}
	moduleCache := environmentValue(t, primary, "GOMODCACHE")
	buildCache := environmentValue(t, primary, "GOCACHE")
	return overriddenEnvironment(environment,
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")),
		"GOMODCACHE="+moduleCache,
		"GOCACHE="+buildCache,
		"GOPATH="+goPath,
		"TMPDIR="+tempPath,
		"PATH="+toolPath,
		"CGO_ENABLED=0",
		"NEXA_SOURCE_CACHE="+filepath.Join(temporary, "fresh-source-cache"),
	)
}

func environmentValue(t *testing.T, environment []string, name string) string {
	t.Helper()
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, name+"="); ok && value != "" {
			return value
		}
	}
	t.Fatalf("environment %s is unset", name)
	return ""
}

func TestSourceBundleCoreGenerationHelperBuildsRunnableRPCTransport(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	helperModule := filepath.Join(temporary, "helper")
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(helperModule, os.DirFS(helperSource)); err != nil {
		t.Fatalf("copy dedicated Core generation helper: %v", err)
	}
	writeCoreGenerationHelperModule(t, helperModule)
	helper := filepath.Join(temporary, "generation-helper")
	runFrameworkConsumer(t, helperModule, os.Environ(), "go", "build", "-o", helper, ".")

	staging := filepath.Join(temporary, "rpc-staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "go.mod"), []byte("module example.invalid/nexa/rpc/core\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := sourceBundleProtocolIR(t, "core")
	command := exec.Command(helper, "rpc", "generate", "--service", "core")
	command.Dir = staging
	command.Env = os.Environ()
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate Core RPC transport: %v\n%s", err, output)
	}
	var result struct {
		GoTestPassed bool `json:"goTestPassed"`
		Artifacts    []struct {
			Path string `json:"path"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode RPC helper inventory: %v\n%s", err, output)
	}
	if !result.GoTestPassed || len(result.Artifacts) == 0 {
		t.Fatalf("RPC helper result = %#v", result)
	}
	foundTransport := false
	for _, artifact := range result.Artifacts {
		foundTransport = foundTransport || artifact.Path == "backend/core/rpctransport/transport.generated.go"
	}
	if !foundTransport {
		t.Fatalf("RPC inventory omitted runnable transport: %#v", result.Artifacts)
	}

	probe := `package rpctransport_test

import (
	"context"
	"net"
	"testing"

	transport "example.invalid/nexa/rpc/core/backend/core/rpctransport"
)

type service struct { tenantID int64 }

func (service) Health(context.Context, transport.HealthRequest) (transport.HealthResponse, error) {
	return transport.HealthResponse{Ready: true}, nil
}
func (service) Register(_ context.Context, request transport.RegisterRequest) (transport.RegisterResponse, error) {
	return transport.RegisterResponse{AccountID: request.Username}, nil
}
func (service) Login(context.Context, transport.LoginRequest) (transport.LoginResponse, error) {
	return transport.LoginResponse{}, nil
}
func (service) Refresh(context.Context, transport.RefreshRequest) (transport.RefreshResponse, error) {
	return transport.RefreshResponse{}, nil
}
func (service) Revoke(context.Context, transport.RevokeRequest) (transport.RevokeResponse, error) {
	return transport.RevokeResponse{}, nil
}
func (value *service) CheckPermission(_ context.Context, request transport.CheckPermissionRequest) (transport.CheckPermissionResponse, error) {
	value.tenantID = request.TenantID
	return transport.CheckPermissionResponse{}, nil
}

func TestRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	implementation := &service{}
	server := transport.NewServer(implementation)
	go func() { _ = server.Serve(listener) }()
	client, err := transport.Dial(listener.Addr().String())
	if err != nil { t.Fatal(err) }
	defer client.Close()
	response, err := client.Health(context.Background(), transport.HealthRequest{})
	if err != nil || !response.Ready { t.Fatalf("health = %#v, %v", response, err) }
	registered, err := client.Register(context.Background(), transport.RegisterRequest{Username: "alice"})
	if err != nil || registered.AccountID != "alice" { t.Fatalf("register = %#v, %v", registered, err) }
	if _, err := client.CheckPermission(context.Background(), transport.CheckPermissionRequest{TenantID: 101, SubjectID: "member-1", Permission: "core.read"}); err != nil || implementation.tenantID != 101 { t.Fatalf("permission tenant = %d, %v", implementation.tenantID, err) }
}
`
	probePath := filepath.Join(staging, "backend", "core", "rpctransport", "transport_external_test.go")
	if err := os.WriteFile(probePath, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	runFrameworkConsumer(t, staging, os.Environ(), "go", "test", "./...")
}

func TestSourceBundleCoreGenerationHelperBuildsBearerProtectedHTTPTransport(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	helperModule := filepath.Join(temporary, "helper")
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(helperModule, os.DirFS(helperSource)); err != nil {
		t.Fatalf("copy dedicated Core generation helper: %v", err)
	}
	writeCoreGenerationHelperModule(t, helperModule)
	helper := filepath.Join(temporary, "generation-helper")
	runFrameworkConsumer(t, helperModule, os.Environ(), "go", "build", "-o", helper, ".")

	staging := filepath.Join(temporary, "api-staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "go.mod"), []byte("module example.com/nexa-generation-consumer\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		filepath.Join(staging, "backend", "core", "desc", "generated"),
		filepath.Join(staging, "backend", "core", "internal", "rpcproxy", "account"),
		filepath.Join(staging, "backend", "core", "internal", "rpcproxy", "generated"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeConsumerFile(t, filepath.Join(staging, "backend", "core", "desc", "generated", "core.generated.api"), "syntax = \"v1\"\n")
	writeConsumerFile(t, filepath.Join(staging, "backend", "core", "desc", "generated", "core.proxy.generated.api"), "syntax = \"v1\"\n")
	writeConsumerFile(t, filepath.Join(staging, "backend", "core", "internal", "rpcproxy", "account", "client.generated.go"), "// Code generated by focused test. DO NOT EDIT.\npackage account\n")
	writeConsumerFile(t, filepath.Join(staging, "backend", "core", "internal", "rpcproxy", "generated", "register.generated.go"), "// Code generated by focused test. DO NOT EDIT.\npackage generated\n")
	input := focusedCoreAPIIR()
	input = bytes.ReplaceAll(input, []byte("/auth/providers"), []byte("/auth/providers-detached"))
	command := exec.Command(helper, "api", "generate", "--core-service", "core")
	command.Dir = staging
	command.Env = os.Environ()
	command.Stdin = bytes.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate Core API transport: %v\n%s", err, output)
	}
	var result struct {
		GoTestPassed bool `json:"goTestPassed"`
		Artifacts    []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode API helper inventory: %v\n%s", err, output)
	}
	if !result.GoTestPassed || len(result.Artifacts) != 5 || result.Artifacts[0].ID != "api.aggregate.core" || result.Artifacts[1].ID != "api.core" || result.Artifacts[2].ID != "api.transport" || result.Artifacts[3].ID != "client.account" || result.Artifacts[4].ID != "register" {
		t.Fatalf("API helper result = %#v", result)
	}

	probe := `package apitransport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	transport "example.com/nexa-generation-consumer/backend/core/apitransport"
)

type backend struct { permissionCalls int }

func (value *backend) Health(context.Context, transport.HealthRequest) (transport.HealthResponse, error) {
	return transport.HealthResponse{Ready: true}, nil
}
func (value *backend) Register(_ context.Context, request transport.RegisterRequest) (transport.RegisterResponse, error) {
	return transport.RegisterResponse{AccountID: request.Username}, nil
}
func (value *backend) Login(context.Context, transport.LoginRequest) (transport.LoginResponse, error) {
	return transport.LoginResponse{SessionID: "session", AccessToken: "access-token", RefreshToken: "refresh"}, nil
}
func (value *backend) Refresh(context.Context, transport.RefreshRequest) (transport.RefreshResponse, error) {
	return transport.RefreshResponse{SessionID: "next", AccessToken: "next-access", RefreshToken: "next-refresh"}, nil
}
func (value *backend) Revoke(_ context.Context, request transport.RevokeRequest) (transport.RevokeResponse, error) {
	if request.Identity.TenantID != 101 || request.Identity.SubjectID != "member-1" { return transport.RevokeResponse{}, context.Canceled }
	return transport.RevokeResponse{Revoked: true}, nil
}
func (value *backend) CheckPermission(_ context.Context, request transport.CheckPermissionRequest) (transport.CheckPermissionResponse, error) {
	value.permissionCalls++
	if request.Identity.TenantID != 101 || request.Identity.SubjectID != "member-1" || request.Permission != "core.session.revoke" { return transport.CheckPermissionResponse{}, context.Canceled }
	return transport.CheckPermissionResponse{Allowed: true}, nil
}
func (value *backend) Providers(context.Context, transport.ListProvidersRequest) (transport.ListProvidersResponse, error) {
	return transport.ListProvidersResponse{}, nil
}
func (value *backend) GetAccount(_ context.Context, request transport.GetAccountRequest) (transport.GetAccountResponse, error) {
	return transport.GetAccountResponse{Name: request.ID}, nil
}

type authenticator struct { permissions []string }
func (*authenticator) Authenticate(_ context.Context, token string) (transport.Identity, error) {
	if token != "access-token" { return transport.Identity{}, context.Canceled }
	return transport.Identity{TenantID: 101, SubjectID: "member-1"}, nil
}
func (value *authenticator) Authorize(_ context.Context, identity transport.Identity, permission string) error {
	if identity.SubjectID != "member-1" { return context.Canceled }
	value.permissions = append(value.permissions, permission)
	return nil
}

func TestRoutesAndBearer(t *testing.T) {
	backend := &backend{}
	security := &authenticator{}
	handler := transport.NewHandler(backend, security)
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK { t.Fatalf("health status = %d", health.Code) }
	providers := httptest.NewRecorder()
	handler.ServeHTTP(providers, httptest.NewRequest(http.MethodGet, "/auth/providers-detached", nil))
	if providers.Code != http.StatusOK { t.Fatalf("detached providers status = %d", providers.Code) }
	oldProviders := httptest.NewRecorder()
	handler.ServeHTTP(oldProviders, httptest.NewRequest(http.MethodGet, "/auth/providers", nil))
	if oldProviders.Code != http.StatusNotFound { t.Fatalf("old providers status = %d", oldProviders.Code) }
	registered := httptest.NewRecorder()
	handler.ServeHTTP(registered, httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader("{\"tenant\":\"tenant-a\",\"username\":\"alice\",\"password\":\"secret\",\"email\":\"alice@example.test\",\"displayName\":\"Alice\"}")))
	if registered.Code != http.StatusCreated { t.Fatalf("register status = %d body=%s", registered.Code, registered.Body.String()) }

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/auth/permissions/core.session.revoke", nil))
	if missing.Code != http.StatusUnauthorized || backend.permissionCalls != 0 { t.Fatalf("missing bearer status=%d calls=%d", missing.Code, backend.permissionCalls) }

	request := httptest.NewRequest(http.MethodGet, "/auth/permissions/core.session.revoke", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, request)
	if allowed.Code != http.StatusOK || backend.permissionCalls != 1 || len(security.permissions) != 1 || security.permissions[0] != "core.authorization.check" { t.Fatalf("permission status=%d calls=%d security=%#v", allowed.Code, backend.permissionCalls, security.permissions) }

	revoke := httptest.NewRequest(http.MethodPost, "/auth/revoke", strings.NewReader("{\"sessionId\":\"session\"}"))
	revoke.Header.Set("Authorization", "Bearer access-token")
	revoked := httptest.NewRecorder()
	handler.ServeHTTP(revoked, revoke)
	if revoked.Code != http.StatusOK || len(security.permissions) != 2 || security.permissions[1] != "core.session.revoke" { t.Fatalf("revoke status=%d security=%#v", revoked.Code, security.permissions) }
}
`
	probePath := filepath.Join(staging, "backend", "core", "apitransport", "transport_external_test.go")
	if err := os.WriteFile(probePath, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	runFrameworkConsumer(t, staging, os.Environ(), "go", "test", "./...")
}

func TestSourceBundleCoreGenerationHelperBuildsRunnableAccountRPCTransport(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	helperModule := filepath.Join(temporary, "helper")
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(helperModule, os.DirFS(helperSource)); err != nil {
		t.Fatal(err)
	}
	writeCoreGenerationHelperModule(t, helperModule)
	helper := filepath.Join(temporary, "generation-helper")
	runFrameworkConsumer(t, helperModule, os.Environ(), "go", "build", "-o", helper, ".")
	staging := filepath.Join(temporary, "account-rpc-staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "go.mod"), []byte("module example.invalid/nexa/rpc/account\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := sourceBundleProtocolIR(t, "account")
	command := exec.Command(helper, "rpc", "generate", "--service", "account")
	command.Dir = staging
	command.Env = os.Environ()
	command.Stdin = bytes.NewReader(input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate account RPC transport: %v\n%s", err, output)
	}
	probe := `package rpctransport_test

import (
	"context"
	"net"
	"testing"

	transport "example.invalid/nexa/rpc/account/backend/account/rpctransport"
)

type service struct{}
func (service) Get(_ context.Context, request transport.GetRequest) (transport.GetResponse, error) { return transport.GetResponse{Name: request.ID}, nil }

func TestRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	server := transport.NewServer(service{})
	go func() { _ = server.Serve(listener) }()
	client, err := transport.Dial(listener.Addr().String())
	if err != nil { t.Fatal(err) }
	defer client.Close()
	response, err := client.Get(context.Background(), transport.GetRequest{ID: "account-1"})
	if err != nil || response.Name != "account-1" { t.Fatalf("get = %#v, %v", response, err) }
}
`
	probePath := filepath.Join(staging, "backend", "account", "rpctransport", "transport_external_test.go")
	if err := os.WriteFile(probePath, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	runFrameworkConsumer(t, staging, os.Environ(), "go", "test", "./...")
}

func TestSourceBundleCoreGenerationHelperRejectsProtocolAndCredentialDrift(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	helperModule := filepath.Join(temporary, "helper")
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(helperModule, os.DirFS(helperSource)); err != nil {
		t.Fatal(err)
	}
	writeCoreGenerationHelperModule(t, helperModule)
	helper := filepath.Join(temporary, "generation-helper")
	runFrameworkConsumer(t, helperModule, os.Environ(), "go", "build", "-o", helper, ".")

	coreIR := sourceBundleProtocolIR(t, "core")
	accountIR := sourceBundleProtocolIR(t, "account")
	for service, input := range map[string][]byte{"core": coreIR, "account": accountIR} {
		staging := newGenerationHelperStaging(t, temporary, "valid-"+service)
		runCoreGenerationHelper(t, helper, staging, input, "rpc", "generate", "--service", service)
	}

	protocolDrift := map[string][]byte{
		"document":            appendProtocolDocumentField(t, coreIR),
		"extra-method":        appendProtocolMethod(t, coreIR),
		"method-io":           bytes.Replace(coreIR, []byte(`"input":"core.v1.HealthRequest"`), []byte(`"input":"core.v1.LoginRequest"`), 1),
		"field-shape":         bytes.Replace(coreIR, []byte(`"jsonName":"ready"`), []byte(`"jsonName":"readiness"`), 1),
		"missing-rpc-context": mutateCoreCheckPermissionProtocol(t, coreIR, func(method, _ map[string]any) { delete(method, "rpcContext") }),
		"tenant-source": mutateCoreCheckPermissionProtocol(t, coreIR, func(method, _ map[string]any) {
			findProtocolContextBinding(t, method, "tenant-id")["source"] = "trace-id"
		}),
		"tenant-path": mutateCoreCheckPermissionProtocol(t, coreIR, func(method, _ map[string]any) {
			findProtocolContextBinding(t, method, "tenant-id")["rpcPath"] = []any{"core.v1.CheckPermissionRequest#2"}
		}),
		"tenant-type": mutateCoreCheckPermissionProtocol(t, coreIR, func(_ map[string]any, request map[string]any) {
			request["fields"].([]any)[0].(map[string]any)["type"].(map[string]any)["name"] = "string"
		}),
	}
	for name, input := range protocolDrift {
		t.Run("protocol-"+name, func(t *testing.T) {
			assertGenerationHelperRejects(t, helper, newGenerationHelperStaging(t, temporary, "protocol-"+name), input, "rpc", "generate", "--service", "core")
		})
	}

	apiIR := focusedCoreAPIIR()
	credentialDrift := map[string][]byte{
		"id":       bytes.Replace(apiIR, []byte(`"id":"primary"`), []byte(`"id":"secondary"`), 1),
		"type":     bytes.Replace(apiIR, []byte(`"type":"bearer"`), []byte(`"type":"session"`), 1),
		"location": bytes.Replace(apiIR, []byte(`"in":"header"`), []byte(`"in":"cookie"`), 1),
		"name":     bytes.Replace(apiIR, []byte(`"name":"authorization"`), []byte(`"name":"x-authorization"`), 1),
	}
	for name, input := range credentialDrift {
		t.Run("credential-"+name, func(t *testing.T) {
			assertGenerationHelperRejects(t, helper, newGenerationHelperStaging(t, temporary, "credential-"+name), input, "api", "generate", "--core-service", "core")
		})
	}
}

func TestSourceBundleCoreGenerationHelperRunsStagedGoTestInRestrictedCoreGenerationEnvironment(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	helperModule := filepath.Join(temporary, "helper")
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(helperModule, os.DirFS(helperSource)); err != nil {
		t.Fatal(err)
	}
	writeCoreGenerationHelperModule(t, helperModule)
	helper := filepath.Join(temporary, "generation-helper")
	runFrameworkConsumer(t, helperModule, os.Environ(), "go", "build", "-o", helper, ".")
	staging := newGenerationHelperStaging(t, temporary, "restricted-rpc")

	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	goExecutable, err = filepath.EvalSymlinks(goExecutable)
	if err != nil {
		t.Fatal(err)
	}
	toolPath := filepath.Join(temporary, "tool-bin")
	if err := os.MkdirAll(toolPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(goExecutable, filepath.Join(toolPath, "go")); err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"PATH=" + toolPath,
		"GOROOT=" + runtime.GOROOT(),
		"GOMODCACHE=" + filepath.Join(temporary, "gomodcache"),
		"GOPROXY=file://" + filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")),
		"GOSUMDB=off",
		"HOME=" + filepath.Join(temporary, "home"),
		"TMPDIR=" + filepath.Join(temporary, "tmp"),
		"GOPATH=" + filepath.Join(temporary, "gopath"),
		"GOCACHE=" + filepath.Join(temporary, "gocache"),
		"GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "GOFLAGS=", "CGO_ENABLED=0",
	}
	for _, name := range []string{"gomodcache", "home", "tmp", "gopath", "gocache"} {
		if err := os.MkdirAll(filepath.Join(temporary, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(helper, "rpc", "generate", "--service", "core")
	command.Dir = staging
	command.Env = environment
	command.Stdin = bytes.NewReader(sourceBundleProtocolIR(t, "core"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restricted Core generation helper failed: %v\n%s", err, output)
	}
}

func TestSourceBundleCoreGenerationProviderTargetsMaterializedCoreFacts(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	module := filepath.Join(temporary, "generation-tool")
	source := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-tool")
	if err := os.CopyFS(module, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/nexa-generation-tool\n\ngo 1.25.0\n\nrequire " + nexaModulePath + " v0.0.0\n\nreplace " + nexaModulePath + " => " + filepath.ToSlash(repositoryRoot(t)) + "\n"
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	environment := overriddenEnvironment(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off")
	runFrameworkConsumer(t, module, environment, "go", "test", "-mod=mod", ".")
}

func TestSourceBundleCoreLifecycleDefersRuntimeUntilGeneratedTransportsExist(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	helperModule := filepath.Join(temporary, "helper")
	helperSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper")
	if err := os.CopyFS(helperModule, os.DirFS(helperSource)); err != nil {
		t.Fatal(err)
	}
	writeCoreGenerationHelperModule(t, helperModule)
	helper := filepath.Join(temporary, "generation-helper")
	runFrameworkConsumer(t, helperModule, os.Environ(), "go", "build", "-o", helper, ".")

	consumer := filepath.Join(temporary, "consumer")
	if err := os.MkdirAll(filepath.Join(consumer, "backend", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), []byte("module example.com/nexa-generation-consumer\n\ngo 1.25.0\n\nrequire golang.org/x/crypto v0.48.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	environment := overriddenEnvironment(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")), "GOSUMDB=off")
	coreappSource := filepath.Join(repositoryRoot(t), "plugins", "service", "core", "_bundle", "backend", "core", "coreapp")
	if err := os.CopyFS(filepath.Join(consumer, "backend", "core", "coreapp"), os.DirFS(coreappSource)); err != nil {
		t.Fatal(err)
	}
	coreCommandSource := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "core")
	if err := os.CopyFS(filepath.Join(consumer, "cmd", "core"), os.DirFS(coreCommandSource)); err != nil {
		t.Fatal(err)
	}
	restoreCoreCommand := deferCoreCommandUntilGenerated(t, consumer)
	if _, err := os.Stat(filepath.Join(consumer, "cmd", "core")); !os.IsNotExist(err) {
		t.Fatalf("Core command exists before transport generation: %v", err)
	}
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "tidy")

	apiInput := focusedCoreAPIIR()
	apiStaging := filepath.Join(temporary, "api-staging")
	if err := os.MkdirAll(apiStaging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiStaging, "go.mod"), []byte("module example.com/nexa-api-staging\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCoreGenerationHelper(t, helper, apiStaging, apiInput, "api", "generate", "--core-service", "core")
	if err := os.CopyFS(filepath.Join(consumer, "backend", "core", "apitransport"), os.DirFS(filepath.Join(apiStaging, "backend", "core", "apitransport"))); err != nil {
		t.Fatal(err)
	}
	rpcInput := sourceBundleProtocolIR(t, "core")
	runCoreGenerationHelper(t, helper, consumer, rpcInput, "rpc", "generate", "--service", "core")
	accountRPCInput := sourceBundleProtocolIR(t, "account")
	runCoreGenerationHelper(t, helper, consumer, accountRPCInput, "rpc", "generate", "--service", "account")
	restoreCoreCommand()
	runFrameworkConsumer(t, consumer, environment, "go", "mod", "tidy")
	binary := filepath.Join(temporary, "core")
	runFrameworkConsumer(t, consumer, environment, "go", "build", "-mod=readonly", "-o", binary, "./cmd/core")

	command := exec.Command(binary, "--listen", "127.0.0.1:0", "--rpc-listen", "127.0.0.1:0")
	command.Dir = consumer
	command.Env = environment
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		_ = command.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("Core did not publish generated transport addresses: %s", stderr.String())
	}
	var endpoints struct {
		HTTP string `json:"http"`
		RPC  string `json:"rpc"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &endpoints); err != nil || !strings.HasPrefix(endpoints.HTTP, "http://127.0.0.1:") || !strings.HasPrefix(endpoints.RPC, "127.0.0.1:") {
		t.Fatalf("Core transport endpoints = %#v, %v, line=%q", endpoints, err, scanner.Text())
	}
	rpcClient, err := rpc.Dial("tcp", endpoints.RPC)
	if err != nil {
		t.Fatal(err)
	}
	defer rpcClient.Close()
	var health struct{ Ready bool }
	if err := rpcClient.Call("Core.Health", struct{}{}, &health); err != nil || !health.Ready {
		t.Fatalf("generated RPC health = %#v, %v", health, err)
	}
	response, err := http.Get(endpoints.HTTP + "/auth/permissions/core.authorization.check")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("permission without bearer status = %d", response.StatusCode)
	}
	var providers struct {
		Items []any `json:"items"`
	}
	getJSON(t, endpoints.HTTP+"/auth/providers", http.StatusOK, &providers)
	if len(providers.Items) != 0 {
		t.Fatalf("default ProviderSet = %#v", providers.Items)
	}
	assertCrossIdentityRevokeIsolation(t, endpoints.HTTP)
}

func TestSourceBundleCoreCatalogSelectsCoreAndAccountProxyOperations(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "services.yaml")
	content := mustReadFile(t, path)
	catalog, err := servicecatalog.Parse("services.yaml", content)
	if err != nil {
		t.Fatal(err)
	}
	for _, serviceID := range []string{"core", "account"} {
		service, ok := catalog.Lookup(serviceID)
		if !ok {
			t.Fatalf("service %q missing", serviceID)
		}
		bindings := service.CapabilityBindings()
		if len(bindings) != 1 || bindings[0].ID() != composition.CapabilityID || bindings[0].APIVersion() != composition.CapabilityVersion {
			t.Fatalf("%s capability bindings = %#v", serviceID, bindings)
		}
	}
}

func TestSourceBundleCoreCompositionRendersCoreAndAccountProxyArtifacts(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	if err := os.CopyFS(temporary, os.DirFS(filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core"))); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(temporary, os.DirFS(filepath.Join(repositoryRoot(t), "plugins", "service", "core", "_bundle"))); err != nil {
		t.Fatal(err)
	}
	repository, err := os.OpenRoot(temporary)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	catalog, err := servicecatalog.Load(repository, "services.yaml")
	if err != nil {
		t.Fatal(err)
	}
	protocols := make([]generationprotocol.Document, 0, 2)
	for _, service := range []string{"core", "account"} {
		protocols = append(protocols, compileSourceBundleProtocol(t, service))
	}
	native, err := generationhttpapi.Load(context.Background(), generationhttpapi.LoadOptions{
		RepositoryRoot: temporary,
		EntryFile:      "backend/core/desc/core.api",
	})
	if err != nil {
		t.Fatal(err)
	}
	document, err := composition.Build(catalog, protocols, native, composition.BuildOptions{
		CoreServiceID:      "core",
		ConsumerModulePath: "example.com/nexa-generation-consumer",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := composition.Render(document, composition.RenderOptions{CoreRoot: "backend/core"})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) == 0 {
		t.Fatal("Core composition rendered no artifacts")
	}
	var coreProxy bool
	for _, artifact := range artifacts {
		if artifact.Path == "backend/core/desc/generated/core.generated.api" {
			t.Fatal("Core proxy fragment collides with the API aggregate path")
		}
		if artifact.ID == "api.core" && artifact.Path == "backend/core/desc/generated/core.proxy.generated.api" {
			coreProxy = true
		}
	}
	if !coreProxy {
		t.Fatal("Core proxy fragment uses no collision-free canonical path")
	}
	generated, err := composition.GeneratedAPI(document)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := generationhttpapi.Merge(native, generated)
	if err != nil {
		t.Fatal(err)
	}
	apiInput, err := generationhttpapi.CanonicalJSON(merged)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(temporary, "composition-staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "go.mod"), []byte("module example.com/nexa-generation-consumer\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apiImports := make([]string, 0)
	for _, artifact := range artifacts {
		name := filepath.Join(staging, filepath.FromSlash(artifact.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		writeConsumerFile(t, name, string(artifact.Content))
		if filepath.Ext(artifact.Path) == ".api" {
			apiImports = append(apiImports, filepath.Base(artifact.Path))
		}
	}
	sort.Strings(apiImports)
	var aggregate strings.Builder
	aggregate.WriteString("syntax = \"v1\"\nimport (\n")
	for _, name := range apiImports {
		aggregate.WriteString("  \"")
		aggregate.WriteString(name)
		aggregate.WriteString("\"\n")
	}
	aggregate.WriteString(")\n")
	writeConsumerFile(t, filepath.Join(staging, "backend", "core", "desc", "generated", "core.generated.api"), aggregate.String())

	helperModule := filepath.Join(temporary, "composition-helper")
	if err := os.CopyFS(helperModule, os.DirFS(filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", "cmd", "generation-helper"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helperModule, "go.mod"), []byte(fmt.Sprintf("module example.com/core-helper\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\nreplace github.com/nxnminieye/nexa => %s\n", repositoryRoot(t))), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(temporary, "composition-helper-bin")
	runFrameworkConsumer(t, helperModule, os.Environ(), "go", "build", "-mod=mod", "-o", helper, ".")
	command := exec.Command(helper, "api", "generate", "--core-service", "core")
	command.Dir = staging
	command.Env = append(os.Environ(), "NEXA_FRAMEWORK_ROOT="+repositoryRoot(t))
	command.Stdin = bytes.NewReader(apiInput)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Core composition staging helper failed: %v\n%s", err, output)
	}
	for _, artifact := range append(append([]composition.RenderedArtifact(nil), artifacts...), composition.RenderedArtifact{Path: "backend/core/desc/generated/core.generated.api"}) {
		if filepath.Ext(artifact.Path) != ".api" {
			continue
		}
		name := filepath.Join(staging, filepath.FromSlash(artifact.Path))
		parsed, err := goctlparser.Parse(name, nil)
		if err != nil {
			t.Fatalf("parse staged API %s: %v", artifact.Path, err)
		}
		if err := parsed.Validate(); err != nil {
			t.Fatalf("validate staged API %s: %v", artifact.Path, err)
		}
	}
	byRef := make(map[string]provenance.Source)
	for _, source := range merged.Sources() {
		byRef[source.Ref.String()] = source
	}
	for _, artifact := range artifacts {
		for _, ref := range artifact.Sources {
			if _, exists := byRef[ref.String()]; exists {
				continue
			}
			resolved := false
			for _, protocolDocument := range protocols {
				if source, ok := protocolDocument.Source(ref); ok {
					byRef[ref.String()] = source
					resolved = true
					break
				}
			}
			if !resolved {
				if source, ok := catalog.Source(ref); ok {
					byRef[ref.String()] = source
					resolved = true
				}
			}
			if !resolved {
				t.Fatalf("composition source %q has no owner", ref.String())
			}
		}
	}
	sources := make([]provenance.Source, 0, len(byRef))
	for _, source := range byRef {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Ref.String() < sources[j].Ref.String() })
	tool := toolchain.Tool{
		ID: "consumer.api-go", Version: "v1.0.0", Executable: helper, Args: []string{"api"},
		Probe: toolchain.ExecutableProbe{Args: []string{"version"}, ExpectedVersion: "nexa-core-generation-helper v1.0.0"},
	}
	generationRepository := t.TempDir()
	generationStaging := t.TempDir()
	if _, err := apigo.Plan(context.Background(), merged, artifacts, apigo.Options{
		CoreServiceID: "core", RepositoryRoot: generationRepository, StagingRoot: generationStaging,
		Emit: func(name string, content []byte) error {
			target := filepath.Join(generationStaging, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			return os.WriteFile(target, content, 0o644)
		},
		Tool: tool, Runner: directHelperRunner{t: t, environment: append(os.Environ(), "NEXA_FRAMEWORK_ROOT="+repositoryRoot(t))}, Sources: sources,
	}); err != nil {
		if typed, ok := err.(*apigo.Error); ok {
			t.Fatalf("Core apigo verification failed: %v (%s/%s/%s)", err, typed.Code(), typed.Stage(), typed.Reason())
		}
		t.Fatalf("Core apigo verification failed: %T %v", err, err)
	}
}

type directHelperRunner struct {
	t           *testing.T
	environment []string
}

func (runner directHelperRunner) Run(ctx context.Context, request toolchain.Request) (toolchain.Result, error) {
	if err := ctx.Err(); err != nil {
		return toolchain.Result{}, err
	}
	arguments := append(append([]string(nil), request.Tool.Args...), request.Args...)
	command := exec.CommandContext(ctx, request.Tool.Executable, arguments...)
	command.Dir = request.WorkDir
	command.Env = append([]string(nil), runner.environment...)
	command.Stdin = bytes.NewReader(request.Stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		runner.t.Fatalf("direct generation helper failed: %v\n%s", err, output)
	}
	return toolchain.Result{
		ToolID: request.Tool.ID, Version: request.Tool.Version,
		ExecutableVersion: request.Tool.Probe.ExpectedVersion, Stdout: output,
	}, nil
}

func TestSourceBundleCoreDetachedFrontendAndMigrationFactsValidateStructurally(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	bundle := filepath.Join(repositoryRoot(t), "plugins", "service", "core", "_bundle")
	if err := os.CopyFS(temporary, os.DirFS(bundle)); err != nil {
		t.Fatal(err)
	}
	validateDetachedFrontendFacts(t, temporary)
	validateDetachedMigrationFacts(t, temporary)
}

func runCoreGenerationHelper(t *testing.T, helper, directory string, input []byte, arguments ...string) {
	t.Helper()
	command := exec.Command(helper, arguments...)
	command.Dir = directory
	command.Env = os.Environ()
	command.Stdin = bytes.NewReader(input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generation helper %v: %v\n%s", arguments, err, output)
	}
}

type sourceBundleProtocolResolver map[string][]byte

func (resolver sourceBundleProtocolResolver) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, ok := resolver[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func sourceBundleProtocolIR(t *testing.T, service string) []byte {
	t.Helper()
	document := compileSourceBundleProtocol(t, service)
	encoded, err := generationprotocol.CanonicalJSON(document)
	if err != nil {
		t.Fatalf("encode %s ProtocolIR fixture: %v", service, err)
	}
	return encoded
}

func writeCoreTenantIDContractTest(t *testing.T, consumer string) {
	t.Helper()
	storePath := filepath.Join(consumer, "cmd", "core", "store.go")
	if bytes.Contains(mustReadFile(t, storePath), []byte("strconv.ParseInt")) {
		t.Fatal("Core fixture parses business tenant code as an integer")
	}
	testSource := []byte(`package main

import "testing"

func TestTenantIDMappingIsStablePositiveAndDistinct(t *testing.T) {
	store := newMemoryStore()
	first := store.tenantID("tenant-a")
	again := store.tenantID("tenant-a")
	other := store.tenantID("tenant-b")
	if first <= 0 || first != again || first == other {
		t.Fatalf("tenant IDs = first:%d again:%d other:%d", first, again, other)
	}
}
`)
	if err := os.WriteFile(filepath.Join(consumer, "cmd", "core", "tenant_id_contract_test.go"), testSource, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mutateCoreCheckPermissionProtocol(t *testing.T, input []byte, mutate func(method, request map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(input, &document); err != nil {
		t.Fatal(err)
	}
	var method, request map[string]any
	for _, rawFile := range document["files"].([]any) {
		file := rawFile.(map[string]any)
		for _, rawMessage := range file["messages"].([]any) {
			candidate := rawMessage.(map[string]any)
			if candidate["fullName"] == "core.v1.CheckPermissionRequest" {
				request = candidate
			}
		}
		for _, rawService := range file["services"].([]any) {
			for _, rawMethod := range rawService.(map[string]any)["methods"].([]any) {
				candidate := rawMethod.(map[string]any)
				if candidate["fullName"] == "core.v1.CoreService.CheckPermission" {
					method = candidate
				}
			}
		}
	}
	if method == nil || request == nil {
		t.Fatal("CheckPermission protocol nodes missing")
	}
	mutate(method, request)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func findProtocolContextBinding(t *testing.T, method map[string]any, source string) map[string]any {
	t.Helper()
	rpcContext, ok := method["rpcContext"].(map[string]any)
	if !ok {
		t.Fatal("method rpcContext missing")
	}
	for _, raw := range rpcContext["contextFields"].([]any) {
		binding := raw.(map[string]any)
		if binding["source"] == source {
			return binding
		}
	}
	t.Fatalf("context binding %q missing", source)
	return nil
}

func compileSourceBundleProtocol(t *testing.T, service string) generationprotocol.Document {
	t.Helper()
	entry := "backend/" + service + "/desc/" + service + ".proto"
	var source string
	switch service {
	case "core":
		source = filepath.Join(repositoryRoot(t), "plugins", "service", "core", "_bundle", filepath.FromSlash(entry))
	case "account":
		source = filepath.Join(repositoryRoot(t), "integration", "testdata", "source_bundle_core", filepath.FromSlash(entry))
	default:
		t.Fatalf("unsupported protocol fixture service %q", service)
	}
	resolver := sourceBundleProtocolResolver{
		entry:                            mustReadFile(t, source),
		"nexa/protocol/v1/options.proto": mustReadFile(t, filepath.Join(repositoryRoot(t), "generation", "protocol", "nexa", "protocol", "v1", "options.proto")),
	}
	document, err := generationprotocol.Compile(context.Background(), generationprotocol.CompileOptions{ServiceID: service, EntryFiles: []string{entry}, Resolver: resolver})
	if err != nil {
		t.Fatalf("compile %s ProtocolIR fixture: %v", service, err)
	}
	return document
}

func focusedCoreAPIIR() []byte {
	input := []byte(`{"apiVersion":"nexa.dev/http-api-ir/v1","kind":"HTTPAPIIR","operations":[{"auth":{"credentials":[],"mode":"none"},"id":"account.get","method":"GET","path":"/accounts/{id}"},{"auth":{"credentials":[],"mode":"none"},"id":"core.auth.login","method":"POST","path":"/auth/login"},{"auth":{"credentials":[],"mode":"none"},"id":"core.auth.providers","method":"GET","path":"/auth/providers"},{"auth":{"credentials":[],"mode":"none"},"id":"core.auth.refresh","method":"POST","path":"/auth/refresh"},{"auth":{"credentials":[],"mode":"none"},"id":"core.auth.register","method":"POST","path":"/auth/register"},{"auth":{"credentials":[{"id":"primary","in":"header","name":"authorization","type":"bearer"}],"mode":"required"},"id":"core.auth.revoke","method":"POST","path":"/auth/revoke"},{"auth":{"credentials":[],"mode":"none"},"id":"core.health","method":"GET","path":"/health"},{"auth":{"credentials":[{"id":"primary","in":"header","name":"authorization","type":"bearer"}],"mode":"required"},"id":"core.authorization.check","method":"GET","path":"/auth/permissions/{permission}"}]}`)
	input = bytes.ReplaceAll(input, []byte(`"id":"core.auth.revoke"`), []byte(`"id":"core.auth.revoke","permission":"core.session.revoke"`))
	return bytes.ReplaceAll(input, []byte(`"id":"core.authorization.check"`), []byte(`"id":"core.authorization.check","permission":"core.authorization.check"`))
}

func appendProtocolDocumentField(t *testing.T, input []byte) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(input, &document); err != nil {
		t.Fatal(err)
	}
	document["unexpected"] = true
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func appendProtocolMethod(t *testing.T, input []byte) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(input, &document); err != nil {
		t.Fatal(err)
	}
	files := document["files"].([]any)
	for _, fileValue := range files {
		file := fileValue.(map[string]any)
		for _, serviceValue := range file["services"].([]any) {
			service := serviceValue.(map[string]any)
			if service["fullName"] != "core.v1.CoreService" {
				continue
			}
			methods := service["methods"].([]any)
			clone := make(map[string]any, len(methods[0].(map[string]any)))
			for key, value := range methods[0].(map[string]any) {
				clone[key] = value
			}
			clone["fullName"] = "core.v1.CoreService.Watch"
			clone["sourceRef"] = "repo:backend/core/desc/core.proto#method:core.v1.CoreService.Watch"
			service["methods"] = append(methods, clone)
			encoded, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			return encoded
		}
	}
	t.Fatal("Core service missing from ProtocolIR fixture")
	return nil
}

func newGenerationHelperStaging(t *testing.T, temporary, name string) string {
	t.Helper()
	staging := filepath.Join(temporary, name)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "go.mod"), []byte("module example.com/"+strings.ReplaceAll(name, "_", "-")+"\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return staging
}

func assertGenerationHelperRejects(t *testing.T, helper, directory string, input []byte, arguments ...string) {
	t.Helper()
	command := exec.Command(helper, arguments...)
	command.Dir = directory
	command.Stdin = bytes.NewReader(input)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("generation helper accepted drift for %v: %s", arguments, output)
	}
}

func assertPackageGraphExcludes(t *testing.T, consumer string, environment []string, target string, forbidden ...string) {
	t.Helper()
	packages := strings.Fields(string(runFrameworkConsumer(t, consumer, environment, "go", "list", "-mod=readonly", "-deps", target)))
	for _, packagePath := range packages {
		for _, prefix := range forbidden {
			if packagePath == prefix || strings.HasPrefix(packagePath, prefix+"/") {
				t.Fatalf("%s unexpectedly depends on %q", target, packagePath)
			}
		}
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func deferCoreCommandUntilGenerated(t *testing.T, consumer string) func() {
	t.Helper()
	command := filepath.Join(consumer, "cmd", "core")
	if info, err := os.Stat(command); err != nil || !info.IsDir() {
		t.Fatalf("stage Core command before generation: %v", err)
	}
	staged := filepath.Join(t.TempDir(), "core-command")
	if err := os.CopyFS(staged, os.DirFS(command)); err != nil {
		t.Fatalf("stage Core command before generation: %v", err)
	}
	if err := os.RemoveAll(command); err != nil {
		t.Fatalf("remove Core command before generation: %v", err)
	}
	restored := false
	return func() {
		t.Helper()
		if restored {
			t.Fatal("Core command restored more than once")
		}
		if _, err := os.Stat(command); !os.IsNotExist(err) {
			t.Fatalf("restore Core command over existing path: %v", err)
		}
		if err := os.CopyFS(command, os.DirFS(staged)); err != nil {
			t.Fatalf("restore Core command after generation: %v", err)
		}
		restored = true
	}
}

func writeToolModule(t *testing.T, root, modulePath, framework string) {
	t.Helper()
	content := "module " + modulePath + "\n\ngo 1.25.0\n\nrequire " + nexaModulePath + " v0.0.0\n\nreplace " + nexaModulePath + " => " + filepath.ToSlash(framework) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCoreGenerationHelperModule(t *testing.T, root string) {
	t.Helper()
	writeToolModule(t, root, "example.com/core-helper", repositoryRoot(t))
	tidyModuleGraph(t, root)
}

func executableInEnvironment(environment []string, name string) bool {
	for _, value := range environment {
		pathValue, found := strings.CutPrefix(value, "PATH=")
		if !found {
			continue
		}
		for _, directory := range filepath.SplitList(pathValue) {
			info, err := os.Stat(filepath.Join(directory, name))
			if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
				return true
			}
		}
		return false
	}
	return false
}

func makeTreeWritableOnCleanup(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			mode := os.FileMode(0o600)
			if entry.IsDir() {
				mode = 0o700
			}
			_ = os.Chmod(path, mode)
			return nil
		})
	})
}

func generatedSnapshot(t *testing.T, consumer string) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	roots := []string{
		".nexa/generation", "backend/account/ent", "backend/account/generated", "backend/account/internal/pb", "backend/account/rpctransport",
		"backend/account/desc/account.crud.generated.proto", "backend/core/generated",
		"backend/core/desc/generated", "backend/core/ent", "backend/core/internal", "backend/core/rpctransport", "backend/core/apitransport",
	}
	for _, relative := range roots {
		absolute := filepath.Join(consumer, filepath.FromSlash(relative))
		info, err := os.Stat(absolute)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			result[relative] = append([]byte(nil), mustReadFile(t, absolute)...)
			continue
		}
		if err := filepath.WalkDir(absolute, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			name, err := filepath.Rel(consumer, path)
			if err != nil {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			result[filepath.ToSlash(name)] = content
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func equalByteSnapshots(left, right map[string][]byte) bool {
	return snapshotDifference(left, right) == ""
}

func snapshotDifference(left, right map[string][]byte) string {
	keys := make([]string, 0, len(left))
	for key := range left {
		keys = append(keys, key)
	}
	for key := range right {
		if _, exists := left[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		leftValue, leftExists := left[key]
		rightValue, rightExists := right[key]
		if !leftExists || !rightExists {
			return fmt.Sprintf("%s existence before=%t after=%t", key, leftExists, rightExists)
		}
		if !bytes.Equal(leftValue, rightValue) {
			leftDigest := sha256.Sum256(leftValue)
			rightDigest := sha256.Sum256(rightValue)
			return fmt.Sprintf("%s digest before=%x after=%x", key, leftDigest, rightDigest)
		}
	}
	return ""
}

func buildCoreBinary(t *testing.T, consumer string, environment []string, binary string) string {
	t.Helper()
	runFrameworkConsumer(t, consumer, environment, "go", "build", "-mod=readonly", "-o", binary, "./cmd/core")
	digest := sha256.Sum256(mustReadFile(t, binary))
	return fmt.Sprintf("sha256:%x", digest)
}

func runCoreBinaryBehavior(t *testing.T, consumer string, environment []string, binary, binarySHA string, withProvider bool, providerPath, oldProviderPath string) {
	t.Helper()
	arguments := []string{"--listen", "127.0.0.1:0", "--rpc-listen", "127.0.0.1:0"}
	if withProvider {
		arguments = append(arguments, "--with-provider")
	}
	command := exec.Command(binary, arguments...)
	command.Dir = consumer
	command.Env = environment
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	command.Stdout = stdoutWriter
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		stdoutWriter.Close()
		t.Fatal(err)
	}
	if err := stdoutWriter.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("close Core runtime parent stdout writer: %v", err)
	}
	waited := false
	defer func() {
		if waited {
			return
		}
		_ = command.Process.Signal(os.Interrupt)
		_ = command.Wait()
	}()
	if err := stdout.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = command.Process.Kill()
		waitErr := command.Wait()
		waited = true
		t.Fatalf("set Core runtime stdout deadline: %v (pid=%d binary=%s exit=%v stderr=%q)", err, command.Process.Pid, binarySHA, waitErr, stderr.String())
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		scannerErr := scanner.Err()
		_ = command.Process.Kill()
		waitErr := command.Wait()
		waited = true
		t.Fatalf("Core runtime did not publish generated transport addresses: pid=%d binary=%s scanner=%v exit=%v stderr=%q", command.Process.Pid, binarySHA, scannerErr, waitErr, stderr.String())
	}
	line := append([]byte(nil), scanner.Bytes()...)
	var endpoints struct {
		HTTP string `json:"http"`
		RPC  string `json:"rpc"`
	}
	if err := json.Unmarshal(line, &endpoints); err != nil || !strings.HasPrefix(endpoints.HTTP, "http://127.0.0.1:") || !strings.HasPrefix(endpoints.RPC, "127.0.0.1:") {
		t.Fatalf("Core transport endpoints = %#v, %v, line=%q stderr=%s", endpoints, err, line, stderr.String())
	}
	rpcClient, err := rpc.Dial("tcp", endpoints.RPC)
	if err != nil {
		t.Fatal(err)
	}
	defer rpcClient.Close()
	var rpcHealth struct{ Ready bool }
	if err := rpcClient.Call("Core.Health", struct{}{}, &rpcHealth); err != nil || !rpcHealth.Ready {
		t.Fatalf("generated Core RPC health = %#v, %v", rpcHealth, err)
	}
	var health struct {
		Ready bool `json:"ready"`
	}
	getJSON(t, endpoints.HTTP+"/health", http.StatusOK, &health)
	if !health.Ready {
		t.Fatal("generated Core HTTP health is not ready")
	}
	if oldProviderPath != "" {
		getJSON(t, endpoints.HTTP+oldProviderPath, http.StatusNotFound, nil)
	}
	var providers struct {
		Items []struct {
			ID           string `json:"id"`
			Protocol     string `json:"protocol"`
			Capabilities struct {
				Authenticate  bool `json:"authenticate"`
				AutoProvision bool `json:"autoProvision"`
				GroupClaims   bool `json:"groupClaims"`
			} `json:"capabilities"`
		} `json:"items"`
	}
	getJSON(t, endpoints.HTTP+providerPath, http.StatusOK, &providers)
	if withProvider {
		if len(providers.Items) != 1 || providers.Items[0].ID != "fixture" || providers.Items[0].Protocol != "fixture" || !providers.Items[0].Capabilities.Authenticate || !providers.Items[0].Capabilities.AutoProvision || !providers.Items[0].Capabilities.GroupClaims {
			t.Fatalf("provider composition descriptor = %#v", providers.Items)
		}
		return
	}
	if len(providers.Items) != 0 {
		t.Fatalf("default Core ProviderSet = %#v", providers.Items)
	}
	assertCrossIdentityRevokeIsolation(t, endpoints.HTTP)

	var account struct {
		Name string `json:"name"`
	}
	getJSON(t, endpoints.HTTP+"/accounts/account-1", http.StatusOK, &account)
	if account.Name != "account:account-1" {
		t.Fatalf("generated account proxy = %#v", account)
	}
	getJSON(t, endpoints.HTTP+"/auth/permissions/core.session.revoke", http.StatusUnauthorized, nil)
	getJSONWithBearer(t, endpoints.HTTP+"/auth/permissions/core.session.revoke", "invalid", http.StatusUnauthorized, nil)
	postJSON(t, endpoints.HTTP+"/auth/register", map[string]string{
		"tenant": "tenant-a", "username": "local-alice", "password": "local-password",
	}, http.StatusCreated, nil)
	var login struct {
		SessionID    string `json:"sessionId"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	postJSON(t, endpoints.HTTP+"/auth/login", map[string]string{
		"tenant": "tenant-a", "username": "local-alice", "password": "local-password",
	}, http.StatusOK, &login)
	if login.SessionID == "" || login.AccessToken == "" || login.RefreshToken == "" {
		t.Fatalf("Core login response = %#v", login)
	}
	var refreshed struct {
		SessionID    string `json:"sessionId"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	postJSON(t, endpoints.HTTP+"/auth/refresh", map[string]string{"refreshToken": login.RefreshToken}, http.StatusOK, &refreshed)
	if refreshed.SessionID == "" || refreshed.SessionID == login.SessionID || refreshed.RefreshToken == login.RefreshToken {
		t.Fatalf("Core refresh response = %#v", refreshed)
	}
	getJSONWithBearer(t, endpoints.HTTP+"/auth/permissions/core.session.revoke", login.AccessToken, http.StatusUnauthorized, nil)
	var permission struct {
		Allowed bool `json:"allowed"`
	}
	getJSONWithBearer(t, endpoints.HTTP+"/auth/permissions/core.session.revoke", refreshed.AccessToken, http.StatusOK, &permission)
	if !permission.Allowed {
		t.Fatal("Core authorizer denied the configured local role")
	}
	postJSON(t, endpoints.HTTP+"/auth/revoke", map[string]string{"sessionId": refreshed.SessionID}, http.StatusUnauthorized, nil)
	postJSONWithBearer(t, endpoints.HTTP+"/auth/revoke", "invalid", map[string]string{"sessionId": refreshed.SessionID}, http.StatusUnauthorized, nil)
	postJSONWithBearer(t, endpoints.HTTP+"/auth/revoke", refreshed.AccessToken, map[string]string{"sessionId": refreshed.SessionID}, http.StatusOK, nil)
	postJSON(t, endpoints.HTTP+"/auth/refresh", map[string]string{"refreshToken": refreshed.RefreshToken}, http.StatusUnauthorized, nil)
}

func assertCrossIdentityRevokeIsolation(t *testing.T, endpoint string) {
	t.Helper()
	for _, username := range []string{"isolation-alice", "isolation-bob"} {
		postJSON(t, endpoint+"/auth/register", map[string]string{
			"tenant": "tenant-isolation", "username": username, "password": "local-password",
		}, http.StatusCreated, nil)
	}
	type loginResponse struct {
		SessionID   string `json:"sessionId"`
		AccessToken string `json:"accessToken"`
	}
	var alice, bob loginResponse
	postJSON(t, endpoint+"/auth/login", map[string]string{
		"tenant": "tenant-isolation", "username": "isolation-alice", "password": "local-password",
	}, http.StatusOK, &alice)
	postJSON(t, endpoint+"/auth/login", map[string]string{
		"tenant": "tenant-isolation", "username": "isolation-bob", "password": "local-password",
	}, http.StatusOK, &bob)
	if alice.SessionID == "" || alice.AccessToken == "" || bob.AccessToken == "" {
		t.Fatalf("cross-identity login responses alice=%#v bob=%#v", alice, bob)
	}
	postJSONWithBearer(t, endpoint+"/auth/revoke", bob.AccessToken, map[string]string{"sessionId": alice.SessionID}, http.StatusUnauthorized, nil)
	var permission struct {
		Allowed bool `json:"allowed"`
	}
	getJSONWithBearer(t, endpoint+"/auth/permissions/core.session.revoke", alice.AccessToken, http.StatusOK, &permission)
	if !permission.Allowed {
		t.Fatal("first user's session became unusable after cross-identity revoke rejection")
	}
}

func getJSON(t *testing.T, endpoint string, wantStatus int, output any) {
	getJSONWithBearer(t, endpoint, "", wantStatus, output)
}

func getJSONWithBearer(t *testing.T, endpoint, bearer string, wantStatus int, output any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d", endpoint, response.StatusCode, wantStatus)
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	}
}

func postJSON(t *testing.T, endpoint string, input any, wantStatus int, output any) {
	postJSONWithBearer(t, endpoint, "", input, wantStatus, output)
}

func postJSONWithBearer(t *testing.T, endpoint, bearer string, input any, wantStatus int, output any) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d", endpoint, response.StatusCode, wantStatus)
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
