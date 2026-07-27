package replacetree_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nxnminieye/nexa/generation/replacetree"
)

func TestPrepareRejectsUnsafeScopesBeforeReplacement(t *testing.T) {
	tests := []struct {
		name       string
		generated  string
		extensions []string
		setup      func(string)
	}{
		{name: "traversal", generated: "../outside"},
		{name: "git casefold", generated: ".GIT/generated"},
		{name: "overlap", generated: "generated", extensions: []string{"generated/hooks"}},
		{name: "casefold collision", generated: "Generated", extensions: []string{"generated"}},
		{name: "symlink component", generated: "linked/generated", setup: func(repository string) {
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(repository, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			if err := os.MkdirAll(filepath.Join(repository, "keep"), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.setup != nil {
				test.setup(repository)
			}
			if _, err := replacetree.Prepare(repository, test.generated, test.extensions); err == nil {
				t.Fatal("unsafe scope accepted")
			}
			if _, err := os.Stat(filepath.Join(repository, "keep")); err != nil {
				t.Fatalf("validation changed repository: %v", err)
			}
		})
	}
}

func TestPrepareReplacesWholeGeneratedDirectory(t *testing.T) {
	repository := t.TempDir()
	stale := filepath.Join(repository, "generated", "nested", "stale.go")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := replacetree.Prepare(repository, "generated", []string{"extensions"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file remains: %v", err)
	}
	if info, err := os.Stat(filepath.Join(repository, "generated")); err != nil || !info.IsDir() {
		t.Fatalf("generated scope not recreated: %#v %v", info, err)
	}
}
