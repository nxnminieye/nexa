package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	modmodule "golang.org/x/mod/module"
)

func TestGenerationPluginExternalConsumer(t *testing.T) {
	base := canonicalIntegrationDirectory(t, t.TempDir())
	consumer := filepath.Join(base, "consumer")
	framework := filepath.Join(consumer, ".framework")
	if err := os.CopyFS(consumer, os.DirFS(filepath.Join(repositoryRoot(t), "fixtures", "consumers", "generation"))); err != nil {
		t.Fatalf("copy generation consumer: %v", err)
	}
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatal(err)
	}
	moduleFile := readModuleFile(t, filepath.Join(consumer, "go.mod"))
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

	environment := overriddenEnvironment(
		os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local",
		"GOMODCACHE="+rootModuleCache(t),
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")), "GOSUMDB=off",
	)
	helper := filepath.Join(base, "generation-helper")
	runGenerationConsumerCommand(t, consumer, environment, "go", "build", "-mod=readonly", "-o", helper, "./cmd/generation-helper")
	runGenerationConsumerCommand(t, consumer, environment, "git", "init", "-q")
	runGenerationConsumerCommand(t, consumer, environment, "git", "config", "user.name", "Nexa Test")
	runGenerationConsumerCommand(t, consumer, environment, "git", "config", "user.email", "nexa-test@example.invalid")
	runGenerationConsumerCommand(t, consumer, environment, "git", "add", ".")
	runGenerationConsumerCommand(t, consumer, environment, "git", "commit", "-qm", "expected generation fixture")
	for attempt := 1; attempt <= 2; attempt++ {
		output := runGenerationConsumerCommand(t, consumer, environment, "go", "run", "-mod=readonly", "./cmd/verify", "--repo-root", consumer, "--helper", helper)
		if strings.TrimSpace(string(output)) != "generation-consumer-ok" {
			t.Fatalf("generation consumer attempt %d output = %q", attempt, output)
		}
		runGenerationConsumerCommand(t, consumer, environment, "git", "diff", "--exit-code", "--ignore-submodules=dirty")
	}
	runGenerationConsumerCommand(t, consumer, environment, "go", "test", "-mod=readonly", "./...")
}

func runGenerationConsumerCommand(t *testing.T, directory string, environment []string, name string, args ...string) []byte {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return output
}
