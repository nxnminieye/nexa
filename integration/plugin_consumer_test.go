package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrivatePluginConsumer(t *testing.T) {
	temporary := t.TempDir()
	moduleRoot := filepath.Join(temporary, "consumer")
	fixtureRoot := filepath.Join(repositoryRoot(t), "fixtures", "consumers", "plugin-composition")
	if err := os.CopyFS(moduleRoot, os.DirFS(fixtureRoot)); err != nil {
		t.Fatalf("copy private plugin consumer: %v", err)
	}
	environment := prepareHermeticExternalModule(t, temporary, moduleRoot)
	t.Cleanup(func() {
		clean := exec.Command("go", "clean", "-modcache")
		clean.Env = environment
		_ = clean.Run()
	})

	command := exec.Command("go", "test", "-mod=readonly", "./...")
	command.Dir = moduleRoot
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("verify private plugin consumer: %v\n%s", err, output)
	}
}
