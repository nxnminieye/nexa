package integration_test

import (
	"os"
	"os/exec"
	"testing"
)

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
