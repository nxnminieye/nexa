package skills_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	frameworkskills "github.com/nxnminieye/nexa/skills"
)

func TestEmbeddedAssetsContainAllNexaSkills(t *testing.T) {
	var skillNames []string
	fileCount := 0
	err := fs.WalkDir(frameworkskills.Files(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if entry.IsDir() && !strings.Contains(path, "/") {
			skillNames = append(skillNames, path)
		}
		if entry.Type().IsRegular() {
			fileCount++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(skillNames)
	want := []string{
		"nexa-ai-first-cli",
		"nexa-controlled-generation",
		"nexa-development-workflow",
		"nexa-framework-router",
	}
	if !reflect.DeepEqual(skillNames, want) {
		t.Fatalf("embedded skill names = %v, want %v", skillNames, want)
	}
	if fileCount != 8 {
		t.Fatalf("embedded file count = %d, want 8", fileCount)
	}
}

func TestEmbeddedAssetsMatchSynchronizedRepositoryProjectionWhenPresent(t *testing.T) {
	repositoryProjection := filepath.Join("..", ".codex", "skills")
	if _, err := os.Stat(repositoryProjection); os.IsNotExist(err) {
		t.Skip("repository .codex skill projection is not present in this module distribution")
	} else if err != nil {
		t.Fatal(err)
	}

	embedded := map[string][]byte{}
	if err := fs.WalkDir(frameworkskills.Files(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := fs.ReadFile(frameworkskills.Files(), path)
		if err != nil {
			return err
		}
		embedded[filepath.FromSlash(path)] = contents
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	projected := map[string][]byte{}
	entries, err := os.ReadDir(repositoryProjection)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "nexa-") {
			continue
		}
		root := filepath.Join(repositoryProjection, entry.Name())
		if err := filepath.WalkDir(root, func(path string, child fs.DirEntry, err error) error {
			if err != nil || child.IsDir() {
				return err
			}
			if !child.Type().IsRegular() {
				t.Fatalf("projected skill asset %s is not a regular file", path)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(repositoryProjection, path)
			if err != nil {
				return err
			}
			projected[relative] = contents
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	if len(embedded) != len(projected) {
		t.Fatalf("embedded files = %d, projected files = %d", len(embedded), len(projected))
	}
	for path, want := range embedded {
		got, ok := projected[path]
		if !ok {
			t.Errorf("synchronized projection %s is missing", path)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("synchronized projection %s differs from embedded source", path)
		}
	}
}
