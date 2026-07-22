package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAPIContractDoesNotChangeMinimalHostComposition(t *testing.T) {
	temporary := t.TempDir()
	moduleRoot := filepath.Join(temporary, "consumer")
	if err := os.MkdirAll(filepath.Join(moduleRoot, "cmd", "baseline"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(moduleRoot, "cmd", "with-api"), 0o755); err != nil {
		t.Fatal(err)
	}
	module := "module example.com/api-contract-optional\n\ngo 1.25.0\n\nrequire github.com/nxnminieye/nexa v0.0.0\n\nreplace github.com/nxnminieye/nexa => " + repositoryRoot(t) + "\n"
	writeConsumerFile(t, filepath.Join(moduleRoot, "go.mod"), module)
	writeConsumerFile(t, filepath.Join(moduleRoot, "cmd", "baseline", "main.go"), minimalHostProgram(false))
	writeConsumerFile(t, filepath.Join(moduleRoot, "cmd", "with-api", "main.go"), minimalHostProgram(true))

	buildEnvironment := prepareHermeticExternalModule(t, temporary, moduleRoot)
	t.Cleanup(func() {
		clean := exec.Command("go", "clean", "-modcache")
		clean.Env = buildEnvironment
		_ = clean.Run()
	})
	baseline := filepath.Join(temporary, "baseline")
	withAPI := filepath.Join(temporary, "with-api")
	buildExternalProgram(t, moduleRoot, buildEnvironment, baseline, "./cmd/baseline")
	buildExternalProgram(t, moduleRoot, buildEnvironment, withAPI, "./cmd/with-api")

	emptyWorkingDirectory := filepath.Join(temporary, "empty-runtime")
	if err := os.Mkdir(emptyWorkingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeEnvironment := isolatedRuntimeEnvironment(os.Environ())
	baselineOutput := runExternalProgram(t, baseline, emptyWorkingDirectory, runtimeEnvironment)
	withAPIOutput := runExternalProgram(t, withAPI, emptyWorkingDirectory, runtimeEnvironment)
	if string(baselineOutput) != string(withAPIOutput) {
		t.Fatalf("inspection changed after API contract use:\nbaseline=%s\nwith-api=%s", baselineOutput, withAPIOutput)
	}

	var inspection struct {
		Plugins      []any `json:"plugins"`
		Capabilities []any `json:"capabilities"`
		Commands     []struct {
			Path []string `json:"path"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(withAPIOutput, &inspection); err != nil {
		t.Fatalf("decode inspection: %v", err)
	}
	if len(inspection.Plugins) != 0 || len(inspection.Capabilities) != 0 {
		t.Fatalf("unexpected composition: %#v", inspection)
	}
	paths := make([][]string, len(inspection.Commands))
	for index, command := range inspection.Commands {
		paths[index] = command.Path
	}
	if want := [][]string{{"inspect"}, {"version"}}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("commands = %#v, want %#v", paths, want)
	}
}

func minimalHostProgram(withAPI bool) string {
	apiImport := ""
	apiUse := ""
	if withAPI {
		apiImport = "\n\t\"github.com/nxnminieye/nexa/generation/api\""
		apiUse = `
	manifest, err := api.NewManifest(api.ManifestSpec{})
	if err != nil { panic(err) }
	if _, err := manifest.CanonicalJSON(); err != nil { panic(err) }
`
	}
	return `package main

import (
	"encoding/json"
	"os"

	"github.com/nxnminieye/nexa/nexactl/host"` + apiImport + `
)

func main() {` + apiUse + `
	composed, err := host.New(host.Options{Version: "v0.0.0-test"})
	if err != nil { panic(err) }
	if err := json.NewEncoder(os.Stdout).Encode(composed.Inspect()); err != nil { panic(err) }
}
`
}

func writeConsumerFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildExternalProgram(t *testing.T, moduleRoot string, environment []string, output, packagePath string) {
	t.Helper()
	command := exec.Command("go", "build", "-mod=readonly", "-o", output, packagePath)
	command.Dir = moduleRoot
	command.Env = environment
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", packagePath, err, combined)
	}
}

func runExternalProgram(t *testing.T, binary, workingDirectory string, environment []string) []byte {
	t.Helper()
	command := exec.Command(binary)
	command.Dir = workingDirectory
	command.Env = environment
	stdout, err := command.Output()
	if err != nil {
		t.Fatalf("run %s: %v", binary, err)
	}
	return stdout
}

func isolatedRuntimeEnvironment(environment []string) []string {
	blocked := []string{"HOME=", "XDG_CONFIG_HOME=", "NEXA_API_MANIFEST=", "NEXA_CREDENTIAL_FILE="}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		skip := false
		for _, prefix := range blocked {
			if strings.HasPrefix(entry, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			result = append(result, entry)
		}
	}
	return result
}
