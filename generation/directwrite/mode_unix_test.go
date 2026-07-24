//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package directwrite

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteCreatesExact0644UnderRestrictiveUmask(t *testing.T) {
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
