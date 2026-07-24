//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package directwrite

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

const exactModeHelperEnv = "NEXA_DIRECTWRITE_EXACT_MODE_HELPER"

func TestWriteCreatesExact0644UnderRestrictiveUmask(t *testing.T) {
	if os.Getenv(exactModeHelperEnv) != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestWriteCreatesExact0644UnderRestrictiveUmask$", "-test.count=1")
		command.Env = append(os.Environ(), exactModeHelperEnv+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("umask helper failed: %v\n%s", err, output)
		}
		return
	}

	root := canonicalTempDir(t)
	previous := syscall.Umask(0o077)
	defer syscall.Umask(previous)

	if _, err := Write(context.Background(), root, MutationSet{
		Scopes: []OutputScope{{Path: "gen", Mode: OutputModeFileSet}},
		Writes: []OutputFile{{Path: "gen/a.go", Content: []byte("generated")}},
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "gen/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %04o, want 0644", got)
	}
}
