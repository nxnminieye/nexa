package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCoreIAMTransportGeneration(t *testing.T) {
	repository := repositoryRoot(t)
	consumer := t.TempDir()
	copyCoreIAMTree(t, filepath.Join(repository, "plugins/service/core/_bundle/backend/core"), filepath.Join(consumer, "backend/core"))
	copyCoreIAMTree(t, filepath.Join(repository, "integration/testdata/core_iam_postgres_consumer"), consumer)
	if err := os.Rename(filepath.Join(consumer, "go.mod.txt"), filepath.Join(consumer, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(consumer, "go.sum.txt"), filepath.Join(consumer, "go.sum")); err != nil {
		t.Fatal(err)
	}
	runCoreIAMTransportGenerator(t, repository, consumer)
	environment := append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	command := exec.Command("go", "test", "-mod=mod", "./...", "-run", "^$", "-count=1")
	command.Dir, command.Env = consumer, environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compile generated Core transport consumer: %v\n%s", err, output)
	}
	assertCoreTransportPBDriftRejected(t, repository, consumer)
	assertCoreTransportProtoDriftRejected(t, repository, consumer)
}

func assertCoreTransportProtoDriftRejected(t *testing.T, repository, consumer string) {
	t.Helper()
	protoPath := filepath.Join(consumer, "backend/core/rpc/desc/core.proto")
	proto, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protoPath, append(proto, []byte("\n// fixture provenance drift\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	outputs := []string{"generated/rpc_generated.go", "generated/api_generated.go"}
	before := make(map[string][]byte, len(outputs))
	for _, relative := range outputs {
		before[relative], err = os.ReadFile(filepath.Join(consumer, relative))
		if err != nil {
			t.Fatal(err)
		}
	}
	command := coreIAMTransportGeneratorCommand(repository, consumer)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("Core transport generation accepted stale Proto bindings: %s", output)
	}
	for _, relative := range outputs {
		after, readErr := os.ReadFile(filepath.Join(consumer, relative))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(before[relative], after) {
			t.Fatalf("failed Core transport generation changed %s", relative)
		}
	}
}

func assertCoreTransportPBDriftRejected(t *testing.T, repository, consumer string) {
	t.Helper()
	pbPath := filepath.Join(consumer, "generated/transportpb/core.pb.go")
	pb, err := os.ReadFile(pbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pbPath, append(pb, []byte("\n// fixture generated body drift\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	outputs := []string{"generated/rpc_generated.go", "generated/api_generated.go"}
	before := make(map[string][]byte, len(outputs))
	for _, relative := range outputs {
		before[relative], err = os.ReadFile(filepath.Join(consumer, relative))
		if err != nil {
			t.Fatal(err)
		}
	}
	command := coreIAMTransportGeneratorCommand(repository, consumer)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("Core transport generation accepted modified generated Proto: %s", output)
	}
	for _, relative := range outputs {
		after, readErr := os.ReadFile(filepath.Join(consumer, relative))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(before[relative], after) {
			t.Fatalf("failed Core transport generation changed %s", relative)
		}
	}
	if err := os.WriteFile(pbPath, pb, 0o644); err != nil {
		t.Fatal(err)
	}
}

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
	copyCoreIAMTree(t, filepath.Join(repository, "plugins/service/core/_bundle/backend/core"), filepath.Join(consumer, "backend/core"))
	copyCoreIAMTree(t, filepath.Join(repository, "integration/testdata/core_iam_postgres_consumer"), consumer)
	if err := os.Rename(filepath.Join(consumer, "go.mod.txt"), filepath.Join(consumer, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(consumer, "go.sum.txt"), filepath.Join(consumer, "go.sum")); err != nil {
		t.Fatal(err)
	}
	runCoreIAMTransportGenerator(t, repository, consumer)

	preparationEnvironment := append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local")
	prepare := exec.Command("go", "mod", "download")
	prepare.Dir, prepare.Env = consumer, preparationEnvironment
	if output, err := prepare.CombinedOutput(); err != nil {
		t.Fatalf("prepare materialized Core IAM consumer dependencies: %v\n%s", err, output)
	}

	environment := append(preparationEnvironment, "GOPROXY=off", "GOSUMDB=off", "NEXA_CORE_IAM_TEST_DSN="+dsn)
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

func runCoreIAMTransportGenerator(t *testing.T, repository, consumer string) {
	t.Helper()
	proto, err := os.ReadFile(filepath.Join(consumer, "backend/core/rpc/desc/core.proto"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(proto)
	wantSourceDigest := "sha256:" + hex.EncodeToString(digest[:])
	wantFiles := []string{"generated/rpc_generated.go", "generated/api_generated.go"}
	wantProtoBindings := []string{"generated/transportpb/core.pb.go", "generated/transportpb/core_grpc.pb.go"}
	stalePath := filepath.Join(consumer, "generated/stale.go")
	if err := os.WriteFile(stalePath, []byte("package generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var firstContents map[string][]byte
	for run := 0; run < 2; run++ {
		command := coreIAMTransportGeneratorCommand(repository, consumer)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("generate Core transport projection (run %d): %v\n%s", run+1, err, output)
		}
		var envelope struct {
			APIVersion    string            `json:"apiVersion"`
			Generator     string            `json:"generator"`
			Kind          string            `json:"kind"`
			Source        string            `json:"source"`
			SourceSHA256  string            `json:"sourceSha256"`
			Outputs       map[string]string `json:"outputs"`
			ProtoBindings map[string]struct {
				SourceSHA256 string `json:"sourceSha256"`
				OutputSHA256 string `json:"outputSha256"`
			} `json:"protoBindings"`
			ServiceMethods int `json:"serviceMethods"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(output), &envelope); err != nil {
			t.Fatalf("decode Core transport generation result (run %d): %v", run+1, err)
		}
		if envelope.APIVersion != "nexa.dev/core-iam-transport-generator/v1" || envelope.Generator != "nexa-core-iam-transport-gen v1.0.0" || envelope.Kind != "CoreIAMTransportGeneration" || envelope.Source != "backend/core/rpc/desc/core.proto" || envelope.SourceSHA256 != wantSourceDigest || envelope.ServiceMethods != 35 || len(envelope.Outputs) != len(wantFiles)+len(wantProtoBindings) || len(envelope.ProtoBindings) != len(wantProtoBindings) {
			t.Fatalf("Core transport generation identity (run %d) = %#v", run+1, envelope)
		}
		for _, relative := range wantProtoBindings {
			binding := envelope.ProtoBindings[relative]
			content, readErr := os.ReadFile(filepath.Join(consumer, filepath.FromSlash(relative)))
			if readErr != nil {
				t.Fatal(readErr)
			}
			contentDigest := sha256.Sum256(content)
			outputDigest := "sha256:" + hex.EncodeToString(contentDigest[:])
			if binding.SourceSHA256 != wantSourceDigest || binding.OutputSHA256 != outputDigest || envelope.Outputs[relative] != outputDigest {
				t.Fatalf("generated Proto binding %s (run %d) = %#v", relative, run+1, binding)
			}
		}
		contents := make(map[string][]byte, len(wantFiles))
		for _, relative := range wantFiles {
			content, readErr := os.ReadFile(filepath.Join(consumer, relative))
			if readErr != nil {
				t.Fatalf("read generated Core transport %s (run %d): %v", relative, run+1, readErr)
			}
			contentDigest := sha256.Sum256(content)
			if envelope.Outputs[relative] != "sha256:"+hex.EncodeToString(contentDigest[:]) {
				t.Fatalf("generated Core transport %s digest (run %d) = %q", relative, run+1, envelope.Outputs[relative])
			}
			contents[relative] = content
		}
		if run == 0 {
			if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
				t.Fatalf("stale generated file remains after replacement: %v", err)
			}
			firstContents = contents
		} else {
			for _, relative := range wantFiles {
				if !bytes.Equal(firstContents[relative], contents[relative]) {
					t.Fatalf("Core transport generation is not repeatable for %s", relative)
				}
			}
		}
	}
}

func coreIAMTransportGeneratorCommand(repository, consumer string) *exec.Cmd {
	command := exec.Command("go",
		"run", "./cmd/core-transport-gen",
		"--repository-root", consumer,
		"--generated-scope", "generated",
		"--proto", "backend/core/rpc/desc/core.proto",
		"--module-path", "example.com/core-iam-consumer",
		"--package", "coretransport",
		"--core-api-import", "example.com/core-iam-consumer/backend/core/api",
		"--core-rpc-import", "example.com/core-iam-consumer/backend/core/rpc/coreapp",
		"--transport-import", "example.com/core-iam-consumer/generated/transportpb",
		"--rpc-output", "generated/rpc_generated.go",
		"--api-output", "generated/api_generated.go",
		"--proto-output", "generated/transportpb/core.pb.go",
		"--grpc-output", "generated/transportpb/core_grpc.pb.go",
	)
	command.Dir = repository
	command.Env = append(os.Environ(), "GOWORK=off", "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	return command
}
