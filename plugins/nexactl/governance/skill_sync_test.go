package governance_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/nxnminieye/nexa/cli/protocol"
	"github.com/nxnminieye/nexa/plugins/nexactl/governance"
	frameworkskills "github.com/nxnminieye/nexa/skills"
)

type skillSyncResult struct {
	APIVersion string   `json:"apiVersion"`
	Target     string   `json:"target"`
	Skills     []string `json:"skills"`
	FileCount  int      `json:"fileCount"`
}

func TestSyncSkillsWritesEmbeddedAssetsAndValidatesManagedSkills(t *testing.T) {
	repository := t.TempDir()
	result := requireSkillSync(t, repository)
	wantSkills := []string{
		"nexa-ai-first-cli",
		"nexa-controlled-generation",
		"nexa-development-workflow",
		"nexa-framework-router",
	}
	if result.APIVersion != "nexa.dev/governance-skill-sync-result/v1" || result.Target != ".codex/skills" ||
		result.FileCount != 8 || !reflect.DeepEqual(result.Skills, wantSkills) {
		t.Fatalf("result = %#v", result)
	}

	assertSyncedAssets(t, repository)
	for _, name := range wantSkills {
		if err := governance.ValidateSkill(filepath.Join(repository, ".codex", "skills", name)); err != nil {
			t.Fatalf("ValidateSkill(%s) error = %v", name, err)
		}
	}
}

func TestSyncSkillsRestoresManagedDirectoryAndPreservesForeignSkill(t *testing.T) {
	repository := t.TempDir()
	requireSkillSync(t, repository)
	managed := filepath.Join(repository, ".codex", "skills", "nexa-framework-router")
	mustWriteFile(t, filepath.Join(managed, "SKILL.md"), []byte("locally modified"))
	mustWriteFile(t, filepath.Join(managed, "stale.txt"), []byte("remove me"))
	foreign := filepath.Join(repository, ".codex", "skills", "consumer-private", "keep.txt")
	mustWriteFile(t, foreign, []byte("keep me"))

	requireSkillSync(t, repository)
	if _, err := os.Stat(filepath.Join(managed, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("managed stale file remains: %v", err)
	}
	if contents, err := os.ReadFile(foreign); err != nil || string(contents) != "keep me" {
		t.Fatalf("foreign skill changed: contents=%q err=%v", contents, err)
	}
	assertSyncedAssets(t, repository)
}

func TestSyncSkillsRemovesRetiredNexaSkillAndPreservesConsumerSkill(t *testing.T) {
	repository := t.TempDir()
	retired := filepath.Join(repository, ".codex", "skills", "nexa-retired")
	mustWriteFile(t, filepath.Join(retired, "SKILL.md"), []byte("retired"))
	consumer := filepath.Join(repository, ".codex", "skills", "consumer-private", "keep.txt")
	mustWriteFile(t, consumer, []byte("keep me"))

	result := requireSkillSync(t, repository)
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Fatalf("retired Nexa skill remains: %v", err)
	}
	if contents, err := os.ReadFile(consumer); err != nil || string(contents) != "keep me" {
		t.Fatalf("consumer skill changed: contents=%q err=%v", contents, err)
	}
	for _, name := range result.Skills {
		if _, err := os.Stat(filepath.Join(repository, ".codex", "skills", name, "SKILL.md")); err != nil {
			t.Fatalf("current Nexa skill %s missing: %v", name, err)
		}
	}
}

func TestSyncSkillsRejectsRepositorySymlinkEscapeWithoutOutsideWrites(t *testing.T) {
	repository := t.TempDir()
	outside := t.TempDir()
	marker := filepath.Join(outside, "marker.txt")
	mustWriteFile(t, marker, []byte("unchanged"))
	if err := os.Symlink(outside, filepath.Join(repository, ".codex")); err != nil {
		t.Fatal(err)
	}

	assertSkillSyncFailure(t, repository, "skill_target_unsafe")
	if contents, readErr := os.ReadFile(marker); readErr != nil || string(contents) != "unchanged" {
		t.Fatalf("outside path changed: contents=%q err=%v", contents, readErr)
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("outside entries = %v", entries)
	}
}

func TestSyncSkillsRejectsManagedSkillSymlinkWithoutOutsideWrites(t *testing.T) {
	repository := t.TempDir()
	outside := t.TempDir()
	marker := filepath.Join(outside, "marker.txt")
	mustWriteFile(t, marker, []byte("unchanged"))
	mustMkdirAll(t, filepath.Join(repository, ".codex", "skills"))
	if err := os.Symlink(outside, filepath.Join(repository, ".codex", "skills", "nexa-framework-router")); err != nil {
		t.Fatal(err)
	}

	assertSkillSyncFailure(t, repository, "skill_target_unsafe")
	if contents, readErr := os.ReadFile(marker); readErr != nil || string(contents) != "unchanged" {
		t.Fatalf("outside path changed: contents=%q err=%v", contents, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(repository, ".codex", "skills", "nexa-framework-router")); statErr != nil {
		t.Fatalf("managed symlink changed during failed preflight: %v", statErr)
	}
	for _, name := range []string{"nexa-ai-first-cli", "nexa-controlled-generation", "nexa-development-workflow"} {
		if _, statErr := os.Stat(filepath.Join(repository, ".codex", "skills", name)); !os.IsNotExist(statErr) {
			t.Fatalf("managed directory %s was written before preflight completed: %v", name, statErr)
		}
	}
}

func TestSyncSkillsPreflightsRetiredNexaSkillBeforeReplacingCurrentSkills(t *testing.T) {
	repository := t.TempDir()
	requireSkillSync(t, repository)
	current := filepath.Join(repository, ".codex", "skills", "nexa-framework-router", "SKILL.md")
	mustWriteFile(t, current, []byte("must remain after failed preflight"))
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repository, ".codex", "skills", "nexa-retired")); err != nil {
		t.Fatal(err)
	}

	assertSkillSyncFailure(t, repository, "skill_target_unsafe")
	if contents, err := os.ReadFile(current); err != nil || string(contents) != "must remain after failed preflight" {
		t.Fatalf("current skill changed before retired preflight completed: contents=%q err=%v", contents, err)
	}
	if _, err := os.Lstat(filepath.Join(repository, ".codex", "skills", "nexa-retired")); err != nil {
		t.Fatalf("retired symlink changed during failed preflight: %v", err)
	}
}

func TestSyncSkillsRejectsRelativeOrMissingRepositoryRoot(t *testing.T) {
	for _, root := range []string{"relative", filepath.Join(t.TempDir(), "missing")} {
		assertSkillSyncFailure(t, root, "skill_repo_root_invalid")
	}
}

func requireSkillSync(t *testing.T, repository string) skillSyncResult {
	t.Helper()
	envelope, stderr, exit := runSkillSync(t, repository)
	if exit != 0 || stderr != "" || !envelope.OK {
		t.Fatalf("exit=%d stderr=%q envelope=%#v", exit, stderr, envelope)
	}
	return decodeResult[skillSyncResult](t, envelope.Result)
}

func assertSkillSyncFailure(t *testing.T, repository, issueCode string) {
	t.Helper()
	envelope, _, exit := runSkillSync(t, repository)
	if exit == 0 || envelope.OK || envelope.Error == nil {
		t.Fatalf("exit=%d envelope=%#v, want structured failure", exit, envelope)
	}
	if envelope.Error.Code != "skill_sync_failed" || envelope.Error.Domain != "nexactl.governance" ||
		envelope.Error.Category != protocol.CategoryInput {
		t.Fatalf("unexpected error payload: %#v", envelope.Error)
	}
	for _, issue := range decodeIssues(t, envelope.Error.Details) {
		if issue.Code == issueCode {
			return
		}
	}
	t.Fatalf("issue %q missing from %#v", issueCode, envelope.Error)
}

func runSkillSync(t *testing.T, repository string) (protocol.Envelope, string, int) {
	t.Helper()
	candidate, err := governance.New()
	if err != nil {
		t.Fatal(err)
	}
	return executePlugin(t, candidate, "skills", "sync", "--repo-root", repository, "--json")
}

func assertSyncedAssets(t *testing.T, repository string) {
	t.Helper()
	var paths []string
	if err := fs.WalkDir(frameworkskills.Files(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		paths = append(paths, path)
		want, err := fs.ReadFile(frameworkskills.Files(), path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(repository, ".codex", "skills", filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("synced asset %s differs", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	if len(paths) != 8 {
		t.Fatalf("synced file paths = %v", paths)
	}
}
