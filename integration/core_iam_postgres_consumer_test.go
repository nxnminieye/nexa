package integration_test

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCoreIAMPostgresConsumer(t *testing.T) {
	dsn := os.Getenv("NEXA_CORE_IAM_TEST_DSN")
	if dsn == "" {
		if os.Getenv("NEXA_CORE_IAM_REQUIRE_POSTGRES") == "1" {
			t.Fatal("NEXA_CORE_IAM_TEST_DSN is required")
		}
		t.Skip("NEXA_CORE_IAM_TEST_DSN is not configured")
	}
	repository := repositoryRoot(t)
	consumer := t.TempDir()
	copyCoreIAMTree(t, filepath.Join(repository, "plugins/service/core/_bundle/backend/core/coreapp"), filepath.Join(consumer, "coreapp"))
	copyCoreIAMTree(t, filepath.Join(repository, "plugins/service/core/_bundle/backend/core/migrations"), filepath.Join(consumer, "migrations"))
	copyCoreIAMTree(t, filepath.Join(repository, "integration/testdata/core_iam_postgres_consumer"), consumer)
	if err := os.Rename(filepath.Join(consumer, "go.mod.txt"), filepath.Join(consumer, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(consumer, "go.sum.txt"), filepath.Join(consumer, "go.sum")); err != nil {
		t.Fatal(err)
	}

	environment := append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off", "NEXA_CORE_IAM_TEST_DSN="+dsn)
	command := exec.Command("go", "test", "-mod=mod", "./...", "-count=1")
	command.Dir, command.Env = consumer, environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("materialized Core IAM consumer: %v\n%s", err, output)
	}
}

func copyCoreIAMTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err = io.Copy(output, input); err != nil {
			output.Close()
			return err
		}
		return output.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
}
