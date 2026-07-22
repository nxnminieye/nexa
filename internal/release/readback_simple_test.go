package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRemoteReadbackFetchesFixedRefsAndHashesArtifacts(t *testing.T) {
	repository := t.TempDir()
	runReleaseGit(t, repository, "init", "-b", "main")
	runReleaseGit(t, repository, "config", "user.name", "Release Test")
	runReleaseGit(t, repository, "config", "user.email", "release@example.com")
	artifact := []byte("release artifact\n")
	writeReleaseTestFile(t, filepath.Join(repository, "dist", "artifact.txt"), artifact, 0o644)
	runReleaseGit(t, repository, "add", "dist/artifact.txt")
	runReleaseGit(t, repository, "commit", "-m", "release candidate")
	runReleaseGit(t, repository, "tag", "-a", "v0.1.0-rc.1", "-m", "v0.1.0-rc.1")
	commit := strings.TrimSpace(runReleaseGit(t, repository, "rev-parse", "HEAD"))
	refsBefore := runReleaseGit(t, repository, "show-ref")

	result, err := ReadRemoteReadback(context.Background(), RemoteReadbackSpec{
		Repository: repository, BranchRef: "refs/heads/main", TagRef: "refs/tags/v0.1.0-rc.1",
		ExpectedCommit: commit, ArtifactPaths: []string{"dist/artifact.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit != commit || result.BranchRef != "refs/heads/main" || result.TagRef != "refs/tags/v0.1.0-rc.1" ||
		len(result.Artifacts) != 1 || result.Artifacts[0].Path != "dist/artifact.txt" || result.Artifacts[0].SHA256 != digestBytes(artifact) {
		t.Fatalf("readback = %#v", result)
	}
	if refsAfter := runReleaseGit(t, repository, "show-ref"); refsAfter != refsBefore {
		t.Fatalf("source repository refs changed:\nbefore=%s\nafter=%s", refsBefore, refsAfter)
	}
}

func TestReadRemoteReadbackRejectsLightweightTag(t *testing.T) {
	repository := t.TempDir()
	runReleaseGit(t, repository, "init", "-b", "main")
	runReleaseGit(t, repository, "config", "user.name", "Release Test")
	runReleaseGit(t, repository, "config", "user.email", "release@example.com")
	writeReleaseTestFile(t, filepath.Join(repository, "artifact.txt"), []byte("artifact\n"), 0o644)
	runReleaseGit(t, repository, "add", "artifact.txt")
	runReleaseGit(t, repository, "commit", "-m", "candidate")
	runReleaseGit(t, repository, "tag", "v0.1.0-rc.1")
	commit := strings.TrimSpace(runReleaseGit(t, repository, "rev-parse", "HEAD"))

	_, err := ReadRemoteReadback(context.Background(), RemoteReadbackSpec{
		Repository: repository, BranchRef: "refs/heads/main", TagRef: "refs/tags/v0.1.0-rc.1",
		ExpectedCommit: commit, ArtifactPaths: []string{"artifact.txt"},
	})
	if err == nil {
		t.Fatal("lightweight tag accepted")
	}
}

func TestReadbackArtifactBoundsRejectCountSingleAndTotal(t *testing.T) {
	if err := validateReadbackArtifactBounds(maxReadbackArtifacts+1, 0, 0); err == nil {
		t.Fatal("artifact count over limit accepted")
	}
	if err := validateReadbackArtifactBounds(1, maxReadbackArtifactBytes+1, 0); err == nil {
		t.Fatal("single artifact over limit accepted")
	}
	if err := validateReadbackArtifactBounds(2, 1, maxReadbackTotalBytes+1); err == nil {
		t.Fatal("artifact total over limit accepted")
	}
}

func TestReadRemoteReadbackRejectsSymlinkArtifact(t *testing.T) {
	repository := t.TempDir()
	runReleaseGit(t, repository, "init", "-b", "main")
	runReleaseGit(t, repository, "config", "user.name", "Release Test")
	runReleaseGit(t, repository, "config", "user.email", "release@example.com")
	if err := os.Symlink("target.txt", filepath.Join(repository, "artifact-link")); err != nil {
		t.Fatal(err)
	}
	runReleaseGit(t, repository, "add", "artifact-link")
	runReleaseGit(t, repository, "commit", "-m", "symlink artifact")
	runReleaseGit(t, repository, "tag", "-a", "v0.1.0-rc.1", "-m", "v0.1.0-rc.1")
	commit := strings.TrimSpace(runReleaseGit(t, repository, "rev-parse", "HEAD"))
	_, err := ReadRemoteReadback(context.Background(), RemoteReadbackSpec{
		Repository: repository, BranchRef: "refs/heads/main", TagRef: "refs/tags/v0.1.0-rc.1",
		ExpectedCommit: commit, ArtifactPaths: []string{"artifact-link"},
	})
	if err == nil {
		t.Fatal("symlink artifact accepted")
	}
}

func runReleaseGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}
