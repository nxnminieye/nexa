package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/nexactl/host"
	sourceadapter "github.com/nxnminieye/nexa/plugins/nexactl/source"
	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

const sourceConsumerOperationID = "op_0123456789abcdef0123456789abcdef"

type fixedOperationIDs struct{}

func (fixedOperationIDs) NewOperationID() (string, error) { return sourceConsumerOperationID, nil }

func TestPrivateSourceCompositionIsExplicitAndCardinalityIndependent(t *testing.T) {
	provider, err := privateProvider()
	if err != nil {
		t.Fatal(err)
	}
	unlinked, err := host.New(host.Options{Version: "v0.0.0-test", OperationIDs: fixedOperationIDs{}})
	if err != nil {
		t.Fatal(err)
	}
	assertNoSourceComposition(t, unlinked)
	assertNoSourceComposition(t, unlinked)

	second := additionalProvider(t)
	for _, test := range []struct {
		name      string
		providers []sourceplugin.Provider
	}{{"zero", nil}, {"one", []sourceplugin.Provider{provider}}, {"many", []sourceplugin.Provider{second, provider}}} {
		t.Run(test.name, func(t *testing.T) {
			composed := composePrivateHost(t, test.providers...)
			inspection := composed.Inspect()
			if len(inspection.Plugins) != 1 || len(inspection.Capabilities) != 1 || inspection.Capabilities[0].ID != sourceadapter.CapabilityID {
				t.Fatalf("inspection = %#v", inspection)
			}
			paths := sourceCommandPaths(inspection)
			want := []string{"source check", "source detach", "source diff", "source materialize", "source plan", "source status", "source upgrade"}
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("source commands = %#v", paths)
			}
		})
	}

	zero := composePrivateHost(t)
	digest := provenance.SHA256([]byte("absent")).String()
	result := executeHost(t, zero, []string{
		"source", "plan", "--repo-root", t.TempDir(), "--provider", "absent", "--version", "v0.1.0", "--profile", "default",
		"--target", "generated-module", "--manifest-digest", digest, "--tree-digest", digest, "--json",
	})
	assertExecution(t, result, 6, false)
}

func TestPrivateSourceExactLifecyclePreservesConsumerEditsOnDetach(t *testing.T) {
	provider, err := privateProvider()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	identity := provider.Manifest().Identity()
	goModule, err := privateSource.ReadFile("provider/source/go.mod.txt")
	if err != nil {
		t.Fatal(err)
	}
	sampleTest, err := privateSource.ReadFile("provider/source/sample_test.go")
	if err != nil {
		t.Fatal(err)
	}
	upgradedSource := []byte("package neutral\n\nfunc Message() string {\n\treturn \"materialized-neutral\"\n}\n\nfunc Version() string {\n\treturn \"v0.2.0\"\n}\n")
	upgradedManifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: identity.ProviderID(), ModulePath: identity.ModulePath(), PackagePath: identity.PackagePath(), Version: "v0.2.0"},
		Files: []sourceplugin.FileSpec{
			{Path: "go.mod", Mode: sourceplugin.Mode0644, Size: int64(len(goModule)), Digest: provenance.SHA256(goModule)},
			{Path: "sample.go", Mode: sourceplugin.Mode0644, Size: int64(len(upgradedSource)), Digest: provenance.SHA256(upgradedSource)},
			{Path: "sample_test.go", Mode: sourceplugin.Mode0644, Size: int64(len(sampleTest)), Digest: provenance.SHA256(sampleTest)},
		},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{"go.mod", "sample.go", "sample_test.go"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	upgradedTree, err := sourceplugin.NewTree(upgradedManifest, []sourceplugin.TreeInput{
		{Path: "go.mod", Content: goModule}, {Path: "sample.go", Content: upgradedSource}, {Path: "sample_test.go", Content: sampleTest},
	}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	upgradedProvider, err := sourceplugin.NewProvider(upgradedManifest, upgradedTree)
	if err != nil {
		t.Fatal(err)
	}
	upgradedRef, err := release.FromProvider(upgradedProvider)
	if err != nil {
		t.Fatal(err)
	}
	composed := composePrivateHost(t, provider, upgradedProvider)
	repository := t.TempDir()
	exact := exactFlags(repository, ref)
	managed := managedFlags(repository, ref.ProviderID())

	for _, command := range []string{"plan", "materialize", "check"} {
		result := executeHost(t, composed, append([]string{"source", command}, exact...))
		assertExecution(t, result, 0, true)
	}
	generated := filepath.Join(repository, "generated-module", "sample.go")
	upgrade := executeHost(t, composed, append([]string{"source", "upgrade"}, exactFlags(repository, upgradedRef)...))
	assertExecution(t, upgrade, 0, true)
	var upgradedResult struct {
		Operation string `json:"operation"`
	}
	decodeResult(t, upgrade.envelope.Result, &upgradedResult)
	upgradedContent, err := os.ReadFile(generated)
	if err != nil || upgradedResult.Operation != "upgrade" || !bytes.Equal(upgradedContent, upgradedSource) {
		t.Fatalf("upgrade result=%#v content=%q err=%v", upgradedResult, upgradedContent, err)
	}
	for _, command := range []string{"status", "diff"} {
		result := executeHost(t, composed, append([]string{"source", command}, managed...))
		assertExecution(t, result, 0, true)
	}
	edited := []byte("package neutral\n\nfunc Message() string { return \"consumer-edit\" }\n")
	if err := os.WriteFile(generated, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	modified := executeHost(t, composed, append([]string{"source", "status"}, managed...))
	assertExecution(t, modified, 0, true)
	var status struct {
		State string `json:"state"`
	}
	decodeResult(t, modified.envelope.Result, &status)
	if status.State != "managed-modified" {
		t.Fatalf("status = %#v", status)
	}

	detached := executeHost(t, composed, append([]string{"source", "detach"}, managed...))
	assertExecution(t, detached, 0, true)
	content, err := os.ReadFile(generated)
	if err != nil || !bytes.Equal(content, edited) {
		t.Fatalf("detached consumer edit = %q err=%v", content, err)
	}
	repeated := executeHost(t, composed, append([]string{"source", "detach"}, managed...))
	assertExecution(t, repeated, 3, false)
}

func TestMaterializedModuleRunsWithoutSourceStateOrFrameworkAvailability(t *testing.T) {
	provider, err := privateProvider()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	repository := t.TempDir()
	composed := composePrivateHost(t, provider)
	materialized := executeHost(t, composed, append([]string{"source", "materialize"}, exactFlags(repository, ref)...))
	assertExecution(t, materialized, 0, true)

	neutral := filepath.Join(t.TempDir(), "neutral")
	if err := os.CopyFS(neutral, os.DirFS(filepath.Join(repository, "generated-module"))); err != nil {
		t.Fatalf("copy neutral module: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(repository, ".nexa", "source")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(repository); err != nil {
		t.Fatal(err)
	}
	runNeutralModuleTests(t, neutral)
}

func composePrivateHost(t *testing.T, providers ...sourceplugin.Provider) *host.Host {
	t.Helper()
	composed, err := privateHost(host.Options{Version: "v0.0.0-test", OperationIDs: fixedOperationIDs{}}, providers...)
	if err != nil {
		t.Fatal(err)
	}
	return composed
}

func assertNoSourceComposition(t *testing.T, composed *host.Host) {
	t.Helper()
	inspection := composed.Inspect()
	if len(inspection.Plugins) != 0 || len(inspection.Capabilities) != 0 || len(sourceCommandPaths(inspection)) != 0 {
		t.Fatalf("provider changed unlinked host: %#v", inspection)
	}
}

func sourceCommandPaths(inspection host.Inspection) []string {
	var paths []string
	for _, command := range inspection.Commands {
		if len(command.Path) > 0 && command.Path[0] == "source" {
			paths = append(paths, strings.Join(command.Path, " "))
		}
	}
	return paths
}

type hostResult struct {
	envelope protocol.Envelope
	exit     int
	stderr   string
}

func executeHost(t *testing.T, composed *host.Host, arguments []string) hostResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := composed.Execute(context.Background(), arguments, &stdout, &stderr)
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var envelope protocol.Envelope
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", stdout.String(), err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("stdout contains multiple values: %q", stdout.String())
	}
	return hostResult{envelope: envelope, exit: exit, stderr: stderr.String()}
}

func assertExecution(t *testing.T, result hostResult, exit int, ok bool) {
	t.Helper()
	if result.exit != exit || result.stderr != "" || result.envelope.OK != ok || result.envelope.OperationID != sourceConsumerOperationID {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", result.exit, result.stderr, result.envelope)
	}
}

func exactFlags(repository string, ref release.Ref) []string {
	return []string{
		"--repo-root", repository, "--provider", ref.ProviderID(), "--version", ref.Version(), "--profile", "default",
		"--target", "generated-module", "--manifest-digest", ref.ManifestDigest().String(), "--tree-digest", ref.TreeDigest().String(), "--json",
	}
}

func managedFlags(repository, providerID string) []string {
	return []string{"--repo-root", repository, "--provider", providerID, "--target", "generated-module", "--json"}
}

func additionalProvider(t *testing.T) sourceplugin.Provider {
	t.Helper()
	content := []byte("secondary\n")
	manifest, err := sourceplugin.NewManifest(sourceplugin.ManifestSpec{
		Identity: sourceplugin.IdentitySpec{ProviderID: "private.secondary", ModulePath: "example.test/secondary", PackagePath: "example.test/secondary/provider", Version: "v0.1.0"},
		Files:    []sourceplugin.FileSpec{{Path: "secondary.txt", Mode: sourceplugin.Mode0644, Size: int64(len(content)), Digest: provenance.SHA256(content)}},
		Profiles: []sourceplugin.ProfileSpec{{ID: "default", Files: []string{"secondary.txt"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sourceplugin.NewTree(manifest, []sourceplugin.TreeInput{{Path: "secondary.txt", Content: content}}, sourceplugin.DefaultTreeLimits())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := sourceplugin.NewProvider(manifest, tree)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func decodeResult(t *testing.T, source, target any) {
	t.Helper()
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		t.Fatal(err)
	}
}

func runNeutralModuleTests(t *testing.T, moduleRoot string) {
	t.Helper()
	isolation := t.TempDir()
	paths := map[string]string{
		"HOME": filepath.Join(isolation, "home"), "GOPATH": filepath.Join(isolation, "gopath"),
		"GOMODCACHE": filepath.Join(isolation, "gomodcache"), "GOCACHE": filepath.Join(isolation, "gocache"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{
		"GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off",
		"HOME=" + paths["HOME"], "GOPATH=" + paths["GOPATH"], "GOMODCACHE=" + paths["GOMODCACHE"], "GOCACHE=" + paths["GOCACHE"],
	}
	for _, name := range []string{"PATH", "TMPDIR", "SYSTEMROOT", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	command := exec.Command("go", "test", "-mod=readonly", "./...")
	command.Dir = moduleRoot
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			t.Fatalf("neutral module exit=%d: %s", exitError.ExitCode(), output)
		}
		t.Fatalf("test neutral module: %v", err)
	}
}
