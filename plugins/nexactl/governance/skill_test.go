package governance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/plugins/nexactl/governance"
)

func TestValidateSkillAcceptsOneSkillOrImmediateSkillRoot(t *testing.T) {
	parent := t.TempDir()
	single := writeSkill(t, parent, "single-skill", "single-skill", "Use when validating one skill")
	if err := governance.ValidateSkill(single); err != nil {
		t.Fatalf("ValidateSkill(single) error = %v", err)
	}

	root := filepath.Join(parent, "skills")
	writeSkill(t, root, "zulu", "zulu", "Use when zulu behavior is needed")
	writeSkill(t, root, "alpha", "alpha", "Use when alpha behavior is needed")
	if err := governance.ValidateSkill(root); err != nil {
		t.Fatalf("ValidateSkill(root) error = %v", err)
	}
}

func TestValidateSkillRejectsInvalidStructuredManifests(t *testing.T) {
	tests := []struct {
		name      string
		contents  string
		folder    string
		issueCode string
	}{
		{
			name:      "malformed YAML",
			folder:    "malformed",
			contents:  "---\nname: [\ndescription: invalid\n---\n",
			issueCode: "skill_frontmatter_invalid",
		},
		{
			name:      "multiple YAML documents",
			folder:    "multiple",
			contents:  "---\nname: multiple\ndescription: first\n...\nname: second\ndescription: second\n---\n",
			issueCode: "skill_frontmatter_invalid",
		},
		{
			name:      "missing name",
			folder:    "missing-name",
			contents:  "---\ndescription: Use when a name is missing\n---\n",
			issueCode: "skill_name_missing",
		},
		{
			name:      "empty description",
			folder:    "empty-description",
			contents:  "---\nname: empty-description\ndescription: '   '\n---\n",
			issueCode: "skill_description_missing",
		},
		{
			name:      "invalid lower hyphen name",
			folder:    "invalid-name",
			contents:  "---\nname: Invalid_Name\ndescription: Use when invalid names are tested\n---\n",
			issueCode: "skill_name_invalid",
		},
		{
			name:      "folder mismatch",
			folder:    "router",
			contents:  "---\nname: other-name\ndescription: Use when routing framework work\n---\n",
			issueCode: "skill_name_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), tt.folder)
			mustWriteFile(t, filepath.Join(root, "SKILL.md"), []byte(tt.contents))
			err := governance.ValidateSkill(root)
			assertGovernanceError(t, err, "skill_manifest_invalid", tt.issueCode)
		})
	}
}

func TestValidateSkillRejectsInvalidSelectionsWithoutRawFilesystemErrors(t *testing.T) {
	t.Run("non-directory root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "private-root-name")
		mustWriteFile(t, root, []byte("not a directory"))
		err := governance.ValidateSkill(root)
		assertGovernanceError(t, err, "skill_manifest_invalid", "skill_root_not_directory")
		assertNoRawPath(t, err, root)
	})

	t.Run("missing SKILL.md", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "skills")
		mustMkdirAll(t, filepath.Join(root, "missing-manifest"))
		err := governance.ValidateSkill(root)
		assertGovernanceError(t, err, "skill_manifest_invalid", "skill_manifest_missing")
	})

	t.Run("root symlink", func(t *testing.T) {
		parent := t.TempDir()
		target := writeSkill(t, parent, "target", "target", "Use when testing target skills")
		root := filepath.Join(parent, "linked")
		if err := os.Symlink(target, root); err != nil {
			t.Fatal(err)
		}
		err := governance.ValidateSkill(root)
		assertGovernanceError(t, err, "skill_manifest_invalid", "skill_path_unsafe")
	})

	t.Run("child symlink", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "skills")
		mustMkdirAll(t, root)
		target := writeSkill(t, parent, "outside", "outside", "Use when testing outside skills")
		if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
			t.Fatal(err)
		}
		err := governance.ValidateSkill(root)
		assertGovernanceError(t, err, "skill_manifest_invalid", "skill_path_unsafe")
	})

	t.Run("manifest symlink", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "linked-manifest")
		mustMkdirAll(t, root)
		target := filepath.Join(parent, "outside.md")
		mustWriteFile(t, target, []byte("---\nname: linked-manifest\ndescription: Use when linked\n---\n"))
		if err := os.Symlink(target, filepath.Join(root, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		err := governance.ValidateSkill(root)
		assertGovernanceError(t, err, "skill_manifest_invalid", "skill_path_unsafe")
	})
}

func TestValidateSkillIgnoresEntriesThatAreNotSkillDirectories(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skills")
	writeSkill(t, root, "alpha", "alpha", "Use when alpha is needed")
	readme := filepath.Join(root, "README.md")
	mustWriteFile(t, readme, []byte("skill collection notes"))
	if err := os.Symlink(readme, filepath.Join(root, "current-notes")); err != nil {
		t.Fatal(err)
	}

	if err := governance.ValidateSkill(root); err != nil {
		t.Fatalf("ValidateSkill() error = %v", err)
	}
}

func assertNoRawPath(t *testing.T, err error, secretPath string) {
	t.Helper()
	payload := protocol.Project(err)
	encoded, encodeErr := json.Marshal(payload)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if strings.Contains(string(encoded), secretPath) {
		t.Fatalf("raw path leaked: %s", encoded)
	}
}
