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
			if _, err := replacetree.Prepare(repository, test.generated, test.extensions, nil); err == nil {
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
	if _, err := replacetree.Prepare(repository, "generated", []string{"extensions"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file remains: %v", err)
	}
	if info, err := os.Stat(filepath.Join(repository, "generated")); err != nil || !info.IsDir() {
		t.Fatalf("generated scope not recreated: %#v %v", info, err)
	}
}

func TestUserLogicCreateSkipAndExplicitOverwrite(t *testing.T) {
	repository := t.TempDir()
	target := replacetree.UserLogicFile{Path: "logic/sample.go", Content: []byte("package logic\n\nconst Value = 1\n")}
	prepared, err := replacetree.Prepare(repository, "generated", []string{"extensions"}, []replacetree.UserLogicFile{target})
	if err != nil {
		t.Fatal(err)
	}
	result, err := prepared.WriteUserLogic(false)
	if err != nil || len(result) != 1 || result[0].Action != replacetree.UserLogicCreated {
		t.Fatalf("create result = %#v, %v", result, err)
	}
	path := filepath.Join(repository, filepath.FromSlash(target.Path))
	if err := os.WriteFile(path, []byte("package logic\n\nconst Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prepared, err = replacetree.Prepare(repository, "generated", []string{"extensions"}, []replacetree.UserLogicFile{target})
	if err != nil {
		t.Fatal(err)
	}
	result, err = prepared.WriteUserLogic(false)
	if err != nil || result[0].Action != replacetree.UserLogicSkipped {
		t.Fatalf("skip result = %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "package logic\n\nconst Value = 2\n" {
		t.Fatalf("skip changed existing logic: %q, %v", data, readErr)
	}
	result, err = prepared.WriteUserLogic(true)
	if err != nil || result[0].Action != replacetree.UserLogicOverwritten {
		t.Fatalf("overwrite result = %#v, %v", result, err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != string(target.Content) {
		t.Fatalf("overwrite content = %q, %v", data, readErr)
	}
}

func TestUserLogicSafetyValidationPrecedesReplacement(t *testing.T) {
	tests := []struct {
		name       string
		generated  string
		extensions []string
		logic      []replacetree.UserLogicFile
		setup      func(string)
	}{
		{name: "generated overlap", generated: "generated", logic: []replacetree.UserLogicFile{{Path: "generated/logic.go"}}},
		{name: "extension overlap", generated: "generated", extensions: []string{"extensions"}, logic: []replacetree.UserLogicFile{{Path: "extensions/logic.go"}}},
		{name: "logic collision", generated: "generated", logic: []replacetree.UserLogicFile{{Path: "logic.go"}, {Path: "LOGIC.go"}}},
		{name: "logic symlink", generated: "generated", logic: []replacetree.UserLogicFile{{Path: "linked/logic.go"}}, setup: func(repository string) {
			if err := os.Symlink(t.TempDir(), filepath.Join(repository, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			stale := filepath.Join(repository, "generated", "stale.go")
			if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
				t.Fatal(err)
			}
			if test.setup != nil {
				test.setup(repository)
			}
			if _, err := replacetree.Prepare(repository, test.generated, test.extensions, test.logic); err == nil {
				t.Fatal("unsafe user logic accepted")
			}
			if _, err := os.Stat(stale); err != nil {
				t.Fatalf("validation changed generated output: %v", err)
			}
		})
	}
}
