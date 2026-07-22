package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	modmodule "golang.org/x/mod/module"
)

func TestSourceExternalConsumer(t *testing.T) {
	temporary := t.TempDir()
	consumer := filepath.Join(temporary, "consumer")
	fixture := filepath.Join(repositoryRoot(t), "fixtures", "consumers", "source-composition")
	if err := os.CopyFS(consumer, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy source consumer: %v", err)
	}

	framework := filepath.Join(temporary, "framework")
	if err := materializeHermeticModuleDirectory(repositoryRoot(t), framework, modmodule.Version{Path: nexaModulePath, Version: "v0.8.0"}); err != nil {
		t.Fatalf("materialize framework module: %v", err)
	}
	moduleFile := readModuleFile(t, filepath.Join(consumer, "go.mod"))
	addLocalModuleReplace(t, moduleFile, consumer, nexaModulePath, framework)
	formatted, err := moduleFile.Format()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "go.mod"), formatted, 0o644); err != nil {
		t.Fatal(err)
	}
	makeModuleFilesReadOnly(t, framework)
	environment := isolatedExternalGoEnvironment(t, filepath.Join(temporary, "go-environment"))
	environment = overriddenEnvironment(
		environment,
		"GOPROXY=file://"+filepath.ToSlash(filepath.Join(rootModuleCache(t), "cache", "download")),
	)

	t.Cleanup(func() {
		clean := exec.Command("go", "clean", "-modcache")
		clean.Env = environment
		_ = clean.Run()
	})
	command := exec.Command("go", "test", "-mod=readonly", "./...")
	command.Dir = consumer
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("verify source external consumer: %v\n%s", err, output)
	}
}

func makeModuleFilesReadOnly(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			return os.Chmod(path, 0o444)
		}
		return nil
	}); err != nil {
		t.Fatalf("make framework module read-only: %v", err)
	}
}
