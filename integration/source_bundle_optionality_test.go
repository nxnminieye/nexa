package integration_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	modmodule "golang.org/x/mod/module"
)

func TestFrameworkMinimumBuildsStartsAndReportsHealthWithoutOptionalPackages(t *testing.T) {
	temporary := canonicalIntegrationDirectory(t, t.TempDir())
	consumer := filepath.Join(temporary, "consumer")
	fixture := filepath.Join(repositoryRoot(t), "fixtures", "consumers", "framework-minimum")
	if err := os.MkdirAll(consumer, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "go.sum", "main.go", "main_test.go"} {
		content, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatalf("read Framework Minimum %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(consumer, name), content, 0o644); err != nil {
			t.Fatalf("write Framework Minimum %s: %v", name, err)
		}
	}
	framework := filepath.Join(temporary, "framework")
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatal(err)
	}
	moduleFile := readModuleFile(t, filepath.Join(consumer, "go.mod"))
	if err := moduleFile.DropReplace(nexaModulePath, ""); err != nil {
		t.Fatal(err)
	}
	if err := moduleFile.AddReplace(nexaModulePath, "", filepath.ToSlash(framework), ""); err != nil {
		t.Fatal(err)
	}
	formatted, err := moduleFile.Format()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), formatted, 0o644); err != nil {
		t.Fatal(err)
	}
	environment := isolatedExternalGoEnvironment(t, filepath.Join(temporary, "go-environment"))
	environment = overriddenEnvironment(environment,
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")),
	)
	makeTreeWritableOnCleanup(t, filepath.Join(temporary, "go-environment", "gomodcache"))

	runFrameworkConsumer(t, consumer, environment, "go", "mod", "tidy")
	runFrameworkConsumer(t, consumer, environment, "go", "test", "-mod=readonly", "./...")
	packages := strings.Fields(string(runFrameworkConsumer(t, consumer, environment, "go", "list", "-mod=readonly", "-deps", "./...")))
	foundHost := false
	for _, packagePath := range packages {
		foundHost = foundHost || packagePath == nexaModulePath+"/nexactl/host"
		for _, forbidden := range []string{
			nexaModulePath + "/generation",
			nexaModulePath + "/plugins/nexactl/generation",
			nexaModulePath + "/plugins/nexactl/source",
			nexaModulePath + "/plugins/service/core",
			nexaModulePath + "/plugins/service/job",
			nexaModulePath + "/plugins/service/quality",
			nexaModulePath + "/sourceplugin",
		} {
			if packagePath == forbidden || strings.HasPrefix(packagePath, forbidden+"/") {
				t.Fatalf("Framework Minimum runtime unexpectedly depends on optional package %q", packagePath)
			}
		}
	}
	if !foundHost {
		t.Fatal("Framework Minimum did not construct the public nexactl host")
	}

	binary := filepath.Join(temporary, "minimum")
	runFrameworkConsumer(t, consumer, environment, "go", "build", "-mod=readonly", "-o", binary, ".")
	command := exec.Command(binary, "--listen", "127.0.0.1:0")
	command.Dir = consumer
	command.Env = environment
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		_ = command.Wait()
	})

	address := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			address <- strings.TrimSpace(scanner.Text())
			return
		}
		address <- ""
	}()
	var endpoint string
	select {
	case endpoint = <-address:
	case <-time.After(10 * time.Second):
		t.Fatal("Framework Minimum did not publish its listen address")
	}
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
		t.Fatalf("listen endpoint = %q", endpoint)
	}
	response, err := http.Get(endpoint + "/health")
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	defer response.Body.Close()
	var health struct {
		Ready bool `json:"ready"`
	}
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if response.StatusCode != http.StatusOK || !health.Ready {
		t.Fatalf("health status=%d body=%#v", response.StatusCode, health)
	}
}

func runFrameworkConsumer(t *testing.T, directory string, environment []string, name string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
	return output
}
