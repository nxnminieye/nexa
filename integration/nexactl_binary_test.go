package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	core "github.com/nxnminieye/nexa/plugins/service/core"
	"github.com/nxnminieye/nexa/sourceplugin/release"
)

func TestReferenceNexactlInspect(t *testing.T) {
	binary := buildNexactl(t)
	stdout, stderr, exit := runBinary(t, binary, "inspect", "--json")
	if exit != 0 || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}

	var envelope protocol.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode success envelope: %v\nstdout: %s", err, stdout)
	}
	if !envelope.OK || envelope.APIVersion != protocol.EnvelopeVersion {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	if !protocol.IsValidOperationID(envelope.OperationID) {
		t.Fatalf("invalid operation ID %q", envelope.OperationID)
	}

	var inspection struct {
		Binary struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"binary"`
		Plugins []struct {
			ID              string `json:"id"`
			Version         string `json:"version"`
			ContractVersion string `json:"contractVersion"`
			Provides        []struct {
				ID      string `json:"id"`
				Version string `json:"version"`
			} `json:"provides"`
			Requires []struct {
				ID      string `json:"id"`
				Version string `json:"version"`
			} `json:"requires"`
		} `json:"plugins"`
		Capabilities []struct {
			ID               string `json:"id"`
			Version          string `json:"version"`
			ProviderPluginID string `json:"providerPluginId"`
		} `json:"capabilities"`
		Commands []struct {
			Path        []string        `json:"path"`
			InputSchema json.RawMessage `json:"inputSchema"`
			Flags       []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"flags"`
			SideEffect     string `json:"sideEffect"`
			DelegatedTools []struct {
				ID      string   `json:"id"`
				Version string   `json:"version"`
				Inputs  []string `json:"inputs"`
				Writes  []string `json:"writes"`
			} `json:"delegatedTools"`
			OwnerPluginID string `json:"ownerPluginId"`
		} `json:"commands"`
	}
	encodedResult, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatalf("encode inspection result: %v", err)
	}
	if err := json.Unmarshal(encodedResult, &inspection); err != nil {
		t.Fatalf("decode inspection result: %v", err)
	}
	if len(inspection.Plugins) != 3 || len(inspection.Capabilities) != 5 || len(inspection.Commands) != 14 {
		t.Fatalf("unexpected reference composition: %#v", inspection)
	}
	wantPlugins := []struct{ id, version string }{
		{id: "generation", version: "v0.1.0"},
		{id: "governance", version: "v0.1.0"},
		{id: "source", version: inspection.Binary.Version},
	}
	if inspection.Binary.Name != "nexactl" || inspection.Binary.Version == "" {
		t.Fatalf("unexpected binary identity: %#v", inspection.Binary)
	}
	for index, want := range wantPlugins {
		got := inspection.Plugins[index]
		if got.ID != want.id || got.Version != want.version || got.ContractVersion != "nexa.dev/nexactl-plugin/v1" || len(got.Requires) != 0 {
			t.Fatalf("plugin[%d] = %#v", index, got)
		}
	}
	wantGenerationProvides := []struct{ id, version string }{
		{id: "generation.rpc", version: "v1.0.0"},
		{id: "generation.api", version: "v1.0.0"},
		{id: "generation.frontend", version: "v1.0.0"},
	}
	if len(inspection.Plugins[0].Provides) != len(wantGenerationProvides) ||
		len(inspection.Plugins[1].Provides) != 1 || inspection.Plugins[1].Provides[0].ID != "governance.validation" ||
		len(inspection.Plugins[2].Provides) != 1 || inspection.Plugins[2].Provides[0].ID != "source.bundle" {
		t.Fatalf("unexpected plugin capabilities: %#v", inspection.Plugins)
	}
	for index, want := range wantGenerationProvides {
		got := inspection.Plugins[0].Provides[index]
		if got.ID != want.id || got.Version != want.version {
			t.Fatalf("generation provides[%d] = %#v", index, got)
		}
	}
	wantCapabilities := []struct{ id, provider string }{
		{id: "generation.api", provider: "generation"},
		{id: "generation.frontend", provider: "generation"},
		{id: "generation.rpc", provider: "generation"},
		{id: "governance.validation", provider: "governance"},
		{id: "source.bundle", provider: "source"},
	}
	for index, want := range wantCapabilities {
		got := inspection.Capabilities[index]
		if got.ID != want.id || got.Version != "v1.0.0" || got.ProviderPluginID != want.provider {
			t.Fatalf("capability[%d] = %#v", index, got)
		}
	}
	wantCommands := []struct {
		path, owner, sideEffect string
		flags                   []string
		required                []bool
	}{
		{path: "generation api generate", owner: "generation", sideEffect: "repository-write", flags: []string{"repo-root", "provider", "service", "overwrite-logic"}, required: []bool{true, true, true, false}},
		{path: "generation frontend generate", owner: "generation", sideEffect: "repository-write", flags: []string{"repo-root", "provider", "service"}, required: []bool{true, true, true}},
		{path: "generation rpc generate", owner: "generation", sideEffect: "repository-write", flags: []string{"repo-root", "provider", "service", "overwrite-logic"}, required: []bool{true, true, true, false}},
		{path: "governance skill validate", owner: "governance", sideEffect: "repository-read", flags: []string{"root"}, required: []bool{true}},
		{path: "inspect", owner: "nexactl.host", sideEffect: "none"},
		{path: "skills sync", owner: "governance", sideEffect: "repository-write", flags: []string{"repo-root"}, required: []bool{true}},
		{path: "source check", owner: "source", sideEffect: "repository-read", flags: []string{"repo-root", "provider", "version", "profile", "target", "manifest-digest", "tree-digest"}, required: []bool{true, true, true, true, true, true, true}},
		{path: "source detach", owner: "source", sideEffect: "repository-write", flags: []string{"repo-root", "provider", "target"}, required: []bool{true, true, true}},
		{path: "source diff", owner: "source", sideEffect: "repository-read", flags: []string{"repo-root", "provider", "target"}, required: []bool{true, true, true}},
		{path: "source materialize", owner: "source", sideEffect: "repository-write", flags: []string{"repo-root", "provider", "version", "profile", "target", "manifest-digest", "tree-digest", "expected-plan-digest"}, required: []bool{true, true, true, true, true, true, true, false}},
		{path: "source plan", owner: "source", sideEffect: "repository-read", flags: []string{"repo-root", "provider", "version", "profile", "target", "manifest-digest", "tree-digest"}, required: []bool{true, true, true, true, true, true, true}},
		{path: "source status", owner: "source", sideEffect: "repository-read", flags: []string{"repo-root", "provider", "target"}, required: []bool{true, true, true}},
		{path: "source upgrade", owner: "source", sideEffect: "repository-write", flags: []string{"repo-root", "provider", "version", "profile", "target", "manifest-digest", "tree-digest", "expected-plan-digest"}, required: []bool{true, true, true, true, true, true, true, false}},
		{path: "version", owner: "nexactl.host", sideEffect: "none"},
	}
	for index, command := range inspection.Commands {
		want := wantCommands[index]
		if path := strings.Join(command.Path, " "); path != want.path || command.OwnerPluginID != want.owner || command.SideEffect != want.sideEffect {
			t.Fatalf("command[%d] = %#v, want %#v", index, command, want)
		}
		var flagNames []string
		var required []bool
		for flagIndex, flag := range command.Flags {
			flagNames = append(flagNames, flag.Name)
			required = append(required, flag.Required)
			wantType := "string"
			if flag.Name == "overwrite-logic" {
				wantType = "bool"
			}
			if flag.Type != wantType {
				t.Fatalf("command[%d] flag[%d] type = %q", index, flagIndex, flag.Type)
			}
		}
		if !reflect.DeepEqual(flagNames, want.flags) || !reflect.DeepEqual(required, want.required) {
			t.Fatalf("command[%d] flags = %v required=%v, want %v required=%v", index, flagNames, required, want.flags, want.required)
		}
		if len(command.DelegatedTools) != 0 {
			t.Fatalf("zero-provider command[%d] delegated tools = %#v", index, command.DelegatedTools)
		}
	}
	provider, err := core.New()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := release.FromProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	var sourceReleases []struct {
		ProviderID     string   `json:"providerId"`
		ModulePath     string   `json:"modulePath"`
		PackagePath    string   `json:"packagePath"`
		Version        string   `json:"version"`
		ManifestDigest string   `json:"manifestDigest"`
		TreeDigest     string   `json:"treeDigest"`
		Profiles       []string `json:"profiles"`
	}
	for _, command := range inspection.Commands {
		if strings.Join(command.Path, " ") != "source plan" {
			continue
		}
		var schema struct {
			Releases []struct {
				ProviderID     string   `json:"providerId"`
				ModulePath     string   `json:"modulePath"`
				PackagePath    string   `json:"packagePath"`
				Version        string   `json:"version"`
				ManifestDigest string   `json:"manifestDigest"`
				TreeDigest     string   `json:"treeDigest"`
				Profiles       []string `json:"profiles"`
			} `json:"x-nexa-source-releases"`
		}
		if err := json.Unmarshal(command.InputSchema, &schema); err != nil {
			t.Fatalf("decode source plan input schema: %v", err)
		}
		sourceReleases = schema.Releases
	}
	wantProfiles := []string{"backend"}
	if len(sourceReleases) != 1 || sourceReleases[0].ProviderID != ref.ProviderID() ||
		sourceReleases[0].ModulePath != ref.ModulePath() || sourceReleases[0].PackagePath != ref.PackagePath() ||
		sourceReleases[0].Version != ref.Version() || sourceReleases[0].ManifestDigest != ref.ManifestDigest().String() ||
		sourceReleases[0].TreeDigest != ref.TreeDigest().String() || !reflect.DeepEqual(sourceReleases[0].Profiles, wantProfiles) {
		t.Fatalf("source releases = %#v", sourceReleases)
	}
}

func TestReferenceNexactlGenerationWithoutProviderIsUnavailable(t *testing.T) {
	binary := buildNexactl(t)
	stdout, stderr, exit := runBinary(t, binary,
		"generation", "rpc", "generate",
		"--repo-root", t.TempDir(),
		"--provider", "consumer",
		"--service", "accounts",
		"--json",
	)
	if exit != 6 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "capability_unavailable" ||
		envelope.Error.Domain != "nexactl.generation" || envelope.Error.Category != protocol.CategoryUnavailable {
		t.Fatalf("unexpected failure envelope: %#v", envelope)
	}
}

func TestReferenceNexactlGovernanceSkillValidation(t *testing.T) {
	binary := buildNexactl(t)
	root := filepath.Join(t.TempDir(), "router")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	valid := []byte("---\nname: router\ndescription: Use when routing framework work\n---\n")
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), valid, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinary(
		t,
		binary,
		"governance", "skill", "validate", "--root", root, "--json",
	)
	if exit != 0 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || !protocol.IsValidOperationID(envelope.OperationID) {
		t.Fatalf("unexpected success envelope: %#v", envelope)
	}

	invalid := []byte("---\nname: other\ndescription: Use when routing framework work\n---\n")
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), invalid, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit = runBinary(
		t,
		binary,
		"governance", "skill", "validate", "--root", root, "--json",
	)
	if exit != 3 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "skill_manifest_invalid" ||
		envelope.Error.Domain != "nexactl.governance" || len(envelope.Error.Details) == 0 {
		t.Fatalf("unexpected failure envelope: %#v", envelope)
	}
}

func TestReferenceNexactlSkillsSync(t *testing.T) {
	binary := buildNexactl(t)
	repository := t.TempDir()
	stdout, stderr, exit := runBinary(t, binary, "skills", "sync", "--repo-root", repository, "--json")
	if exit != 0 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || !protocol.IsValidOperationID(envelope.OperationID) {
		t.Fatalf("unexpected success envelope: %#v", envelope)
	}
	encoded, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		APIVersion string   `json:"apiVersion"`
		Target     string   `json:"target"`
		Skills     []string `json:"skills"`
		FileCount  int      `json:"fileCount"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	wantSkills := []string{"nexa-ai-first-cli", "nexa-controlled-generation", "nexa-development-workflow", "nexa-framework-router"}
	if result.APIVersion != "nexa.dev/governance-skill-sync-result/v1" || result.Target != ".codex/skills" ||
		result.FileCount != 8 || !reflect.DeepEqual(result.Skills, wantSkills) {
		t.Fatalf("sync result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(repository, ".codex", "skills", "nexa-framework-router", "SKILL.md")); err != nil {
		t.Fatalf("synced router skill: %v", err)
	}
}

func TestMinimalNexactlInitializationFailureIsStable(t *testing.T) {
	binary := buildNexactl(t, "-ldflags", "-X main.buildVersion=secret-invalid-version")
	stdout, stderr, exit := runBinary(t, binary, "inspect", "--json")
	if exit != 70 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("decode failure envelope: %v\nstdout: %s", err, stdout)
	}
	if envelope.APIVersion != protocol.EnvelopeVersion || envelope.OK ||
		envelope.OperationID != protocol.SentinelOperationID || envelope.Error == nil {
		t.Fatalf("unexpected failure envelope: %#v", envelope)
	}
	if envelope.Error.Code != "host_initialization_failed" ||
		envelope.Error.Domain != "nexactl.bootstrap" ||
		envelope.Error.Category != protocol.CategoryInternal {
		t.Fatalf("unexpected bootstrap error: %#v", envelope.Error)
	}
	if strings.Count(stdout, "\n") != 1 || strings.Contains(stdout, "\n  ") {
		t.Fatalf("bootstrap JSON is not compact: %q", stdout)
	}
	wantDiagnostic := "nexactl.bootstrap operation=" + protocol.SentinelOperationID + " failure=host_initialization_failed\n"
	if stderr != wantDiagnostic {
		t.Fatalf("unexpected stable diagnostic %q", stderr)
	}
	combined := stdout + stderr
	if strings.Contains(combined, "secret-invalid-version") || strings.Contains(combined, "valid semantic version") {
		t.Fatalf("raw initialization failure leaked: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestMinimalNexactlInitializationFailureDefaultsToIndentedJSON(t *testing.T) {
	binary := buildNexactl(t, "-ldflags", "-X main.buildVersion=invalid-version")
	tests := []struct {
		name string
		args []string
	}{
		{name: "without json", args: []string{"inspect"}},
		{name: "json after terminator", args: []string{"inspect", "--", "--json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, exit := runBinary(t, binary, tt.args...)
			if exit != 70 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			var envelope protocol.Envelope
			if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
				t.Fatalf("decode failure envelope: %v\nstdout: %s", err, stdout)
			}
			if !strings.Contains(stdout, "\n  ") {
				t.Fatalf("bootstrap JSON is not indented: %q", stdout)
			}
		})
	}
}

func TestRepositoryRootFromGoEnv(t *testing.T) {
	t.Run("absolute go.mod", func(t *testing.T) {
		moduleFile := filepath.Join(t.TempDir(), "go.mod")
		root, err := repositoryRootFromGoEnv([]byte(moduleFile+"\n"), nil)
		if err != nil {
			t.Fatalf("repositoryRootFromGoEnv() error = %v", err)
		}
		if root != filepath.Dir(moduleFile) {
			t.Fatalf("root = %q, want %q", root, filepath.Dir(moduleFile))
		}
	})

	tests := []struct {
		name       string
		output     []byte
		commandErr error
		wantError  string
	}{
		{name: "command failure", commandErr: errors.New("secret command failure"), wantError: "go env GOMOD failed"},
		{name: "empty output", output: []byte(" \n"), wantError: "go env GOMOD returned no module"},
		{name: "devnull", output: []byte(os.DevNull + "\n"), wantError: "go env GOMOD returned no module"},
		{name: "relative path", output: []byte("relative/go.mod\n"), wantError: "go env GOMOD returned a non-absolute path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repositoryRootFromGoEnv(tt.output, tt.commandErr)
			if err == nil || err.Error() != tt.wantError {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
		})
	}
}

func buildNexactl(t *testing.T, buildFlags ...string) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "nexactl")
	arguments := []string{"build", "-o", binary}
	arguments = append(arguments, buildFlags...)
	arguments = append(arguments, "./cmd/nexactl")
	command := exec.Command("go", arguments...)
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build nexactl: %v\n%s", err, output)
	}
	return binary
}

func runBinary(t *testing.T, binary string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()

	command := exec.Command(binary, args...)
	binaryRoot, err := filepath.EvalSymlinks(filepath.Dir(binary))
	if err != nil {
		t.Fatalf("canonicalize nexactl runtime root: %v", err)
	}
	cacheRoot := filepath.Join(binaryRoot, "source-release-cache")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatalf("create isolated nexactl runtime: %v", err)
	}
	command.Env = overriddenEnvironment(
		os.Environ(),
		"NEXA_SOURCE_CACHE="+cacheRoot,
	)
	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	command.Stdout = &stdoutBuffer
	command.Stderr = &stderrBuffer
	err = command.Run()
	if err == nil {
		return stdoutBuffer.String(), stderrBuffer.String(), 0
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run nexactl: %v", err)
	}
	return stdoutBuffer.String(), stderrBuffer.String(), exitError.ExitCode()
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	command := exec.Command("go", "env", "GOMOD")
	command.Env = append(os.Environ(), "GOWORK=off")
	output, commandErr := command.Output()
	root, err := repositoryRootFromGoEnv(output, commandErr)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func repositoryRootFromGoEnv(output []byte, commandErr error) (string, error) {
	if commandErr != nil {
		return "", errors.New("go env GOMOD failed")
	}
	moduleFile := strings.TrimSpace(string(output))
	if moduleFile == "" || moduleFile == os.DevNull {
		return "", errors.New("go env GOMOD returned no module")
	}
	if !filepath.IsAbs(moduleFile) {
		return "", errors.New("go env GOMOD returned a non-absolute path")
	}
	return filepath.Dir(moduleFile), nil
}
