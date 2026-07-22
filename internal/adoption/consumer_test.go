package adoption

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunConsumerUsesLocalFrameworkAndOrdinaryGoChecks(t *testing.T) {
	frameworkRoot := repositoryRoot(t)
	fixtureRoot := filepath.Join(t.TempDir(), "fixture")
	mustWrite(t, filepath.Join(fixtureRoot, "main.go"), `package main

import "github.com/nxnminieye/nexa/provenance"

func main() { _ = provenance.SHA256([]byte("local")) }
`)
	workRoot := filepath.Join(t.TempDir(), "consumer")

	result, err := RunConsumer(context.Background(), ConsumerRequest{
		FixtureRoot:   fixtureRoot,
		FrameworkRoot: frameworkRoot,
		WorkRoot:      workRoot,
		Checks:        []Check{CheckTest, CheckBuild, CheckVet},
	})
	if err != nil {
		t.Fatalf("RunConsumer() error = %v; result=%#v", err, result)
	}
	if len(result.Commands) != 4 {
		t.Fatalf("commands = %#v, want tidy plus three checks", result.Commands)
	}
	for _, command := range result.Commands {
		if command.ExitCode != 0 {
			t.Fatalf("command failed: %#v", command)
		}
	}
	if _, err := os.Lstat(workRoot); !os.IsNotExist(err) {
		t.Fatalf("work root was not cleaned: %v", err)
	}
}

func TestRunConsumerReturnsOrdinaryCommandFailureAndCleansWorkRoot(t *testing.T) {
	fixtureRoot := filepath.Join(t.TempDir(), "fixture")
	mustWrite(t, filepath.Join(fixtureRoot, "main.go"), "package main\nfunc main() {}\n")
	mustWrite(t, filepath.Join(fixtureRoot, "main_test.go"), `package main

import "testing"

func TestFailure(t *testing.T) { t.Fatal("fixture failure") }
`)
	workRoot := filepath.Join(t.TempDir(), "consumer")

	result, err := RunConsumer(context.Background(), ConsumerRequest{
		FixtureRoot: fixtureRoot, FrameworkRoot: repositoryRoot(t), WorkRoot: workRoot,
		Checks: []Check{CheckTest},
	})
	if err == nil || !strings.Contains(err.Error(), "go test") {
		t.Fatalf("RunConsumer() error = %v, want ordinary go test failure; result=%#v", err, result)
	}
	if len(result.Commands) == 0 || result.Commands[len(result.Commands)-1].ExitCode == 0 {
		t.Fatalf("failure result = %#v", result)
	}
	if _, statErr := os.Lstat(workRoot); !os.IsNotExist(statErr) {
		t.Fatalf("work root was not cleaned: %v", statErr)
	}
}

func TestRunConsumerRejectsFrameworkRootWithoutGoModule(t *testing.T) {
	fixtureRoot := filepath.Join(t.TempDir(), "fixture")
	mustWrite(t, filepath.Join(fixtureRoot, "main.go"), "package main\nfunc main() {}\n")

	_, err := RunConsumer(context.Background(), ConsumerRequest{
		FixtureRoot: fixtureRoot, FrameworkRoot: t.TempDir(), WorkRoot: filepath.Join(t.TempDir(), "consumer"),
	})
	if err == nil || !strings.Contains(err.Error(), "framework root") {
		t.Fatalf("RunConsumer() error = %v", err)
	}
}

func TestRunInspectionReturnsOrdinaryCommandOutput(t *testing.T) {
	result, err := RunInspection(context.Background(), InspectionRequest{
		Binary: "/bin/echo",
		Args:   []string{"--json", "inspect"},
	})
	if err != nil {
		t.Fatalf("RunInspection() error = %v", err)
	}
	if result.Name != "nexactl inspect" || result.ExitCode != 0 || result.Stdout != "--json inspect\n" || result.Stderr != "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunConsumerRejectsWorkRootInsideFixtureBeforeWrite(t *testing.T) {
	fixtureRoot := filepath.Join(t.TempDir(), "fixture")
	sentinel := filepath.Join(fixtureRoot, "sentinel.txt")
	mustWrite(t, sentinel, "unchanged\n")
	workRoot := filepath.Join(fixtureRoot, "consumer")
	request := ConsumerRequest{FixtureRoot: fixtureRoot, FrameworkRoot: repositoryRoot(t), WorkRoot: workRoot}

	if _, err := resolveConsumerPaths(request); err == nil || !strings.Contains(err.Error(), "overlaps fixture root") {
		t.Fatalf("resolveConsumerPaths() error = %v", err)
	}
	_, err := RunConsumer(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "overlaps fixture root") {
		t.Fatalf("RunConsumer() error = %v", err)
	}
	if _, statErr := os.Lstat(workRoot); !os.IsNotExist(statErr) {
		t.Fatalf("overlapping work root was created: %v", statErr)
	}
	if content, readErr := os.ReadFile(sentinel); readErr != nil || string(content) != "unchanged\n" {
		t.Fatalf("fixture sentinel changed: content=%q error=%v", content, readErr)
	}
}

func TestRunConsumerRejectsWorkRootInsideFrameworkBeforeWrite(t *testing.T) {
	frameworkRoot := filepath.Join(t.TempDir(), "framework")
	mustWrite(t, filepath.Join(frameworkRoot, "go.mod"), "module github.com/nxnminieye/nexa\n\ngo 1.25.0\n")
	workParent := filepath.Join(frameworkRoot, "scratch")
	if err := os.MkdirAll(workParent, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureRoot := filepath.Join(t.TempDir(), "fixture")
	mustWrite(t, filepath.Join(fixtureRoot, "main.go"), "package main\nfunc main() {}\n")
	workRoot := filepath.Join(workParent, "consumer")

	_, err := RunConsumer(context.Background(), ConsumerRequest{
		FixtureRoot: fixtureRoot, FrameworkRoot: frameworkRoot, WorkRoot: workRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "overlaps framework root") {
		t.Fatalf("RunConsumer() error = %v", err)
	}
	if _, statErr := os.Lstat(workRoot); !os.IsNotExist(statErr) {
		t.Fatalf("overlapping work root was created: %v", statErr)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func mustWrite(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
