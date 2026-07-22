package release

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	maxReadbackArtifacts     = 1024
	maxReadbackArtifactBytes = 256 << 20
	maxReadbackTotalBytes    = 1 << 30
)

type RemoteReadbackSpec struct {
	Repository     string
	BranchRef      string
	TagRef         string
	ExpectedCommit string
	ArtifactPaths  []string
}

type RemoteArtifact struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type RemoteReadback struct {
	BranchRef string           `json:"branchRef"`
	TagRef    string           `json:"tagRef"`
	Commit    string           `json:"commit"`
	Artifacts []RemoteArtifact `json:"artifacts"`
}

// ReadRemoteReadback fetches fixed branch and tag refs into a disposable bare
// repository. It performs no writes to the source repository or hosting API.
func ReadRemoteReadback(ctx context.Context, spec RemoteReadbackSpec) (RemoteReadback, error) {
	if ctx == nil || strings.TrimSpace(spec.Repository) == "" || !validRemoteRef(spec.BranchRef, "refs/heads/") ||
		!validRemoteRef(spec.TagRef, "refs/tags/") || !commitPattern.MatchString(spec.ExpectedCommit) || len(spec.ArtifactPaths) == 0 {
		return RemoteReadback{}, fmt.Errorf("remote readback input is invalid")
	}
	if err := validateReadbackArtifactBounds(len(spec.ArtifactPaths), 0, 0); err != nil {
		return RemoteReadback{}, err
	}
	paths := append([]string(nil), spec.ArtifactPaths...)
	sort.Strings(paths)
	for index, artifactPath := range paths {
		if !safeArchivePath(artifactPath) || strings.Contains(artifactPath, ":") || index > 0 && paths[index-1] == artifactPath {
			return RemoteReadback{}, fmt.Errorf("remote artifact path is invalid")
		}
	}
	scratch, err := os.MkdirTemp("", "nexa-release-readback-")
	if err != nil {
		return RemoteReadback{}, fmt.Errorf("create readback directory: %w", err)
	}
	defer os.RemoveAll(scratch)
	bare := filepath.Join(scratch, "repository.git")
	if _, err := runGit(ctx, "", "init", "--bare", bare); err != nil {
		return RemoteReadback{}, err
	}
	branchDestination, tagDestination := "refs/nexa-readback/branch", "refs/nexa-readback/tag"
	if _, err := runGit(ctx, "", "-C", bare, "fetch", "--no-tags", "--", spec.Repository,
		"+"+spec.BranchRef+":"+branchDestination, "+"+spec.TagRef+":"+tagDestination); err != nil {
		return RemoteReadback{}, err
	}
	branchType, err := gitText(ctx, bare, "cat-file", "-t", branchDestination)
	if err != nil || branchType != "commit" {
		return RemoteReadback{}, fmt.Errorf("remote branch does not identify a commit")
	}
	tagType, err := gitText(ctx, bare, "cat-file", "-t", tagDestination)
	if err != nil || tagType != "tag" {
		return RemoteReadback{}, fmt.Errorf("remote tag is not annotated")
	}
	branchCommit, err := gitLine(ctx, bare, "rev-parse", "--verify", branchDestination+"^{commit}")
	if err != nil {
		return RemoteReadback{}, err
	}
	tagCommit, err := gitLine(ctx, bare, "rev-parse", "--verify", tagDestination+"^{commit}")
	if err != nil {
		return RemoteReadback{}, err
	}
	if branchCommit != spec.ExpectedCommit || tagCommit != spec.ExpectedCommit {
		return RemoteReadback{}, fmt.Errorf("remote refs do not resolve to the expected commit")
	}
	result := RemoteReadback{
		BranchRef: spec.BranchRef, TagRef: spec.TagRef, Commit: spec.ExpectedCommit,
		Artifacts: make([]RemoteArtifact, 0, len(paths)),
	}
	var totalSize int64
	for _, artifactPath := range paths {
		oid, size, err := readGitBlobMetadata(ctx, bare, spec.ExpectedCommit, artifactPath)
		if err != nil {
			return RemoteReadback{}, err
		}
		if totalSize > maxReadbackTotalBytes-size || validateReadbackArtifactBounds(len(paths), size, totalSize+size) != nil {
			return RemoteReadback{}, fmt.Errorf("remote artifacts exceed size limits")
		}
		content, err := runGit(ctx, "", "-C", bare, "cat-file", "blob", oid)
		if err != nil || int64(len(content)) != size {
			return RemoteReadback{}, fmt.Errorf("remote artifact content is invalid")
		}
		result.Artifacts = append(result.Artifacts, RemoteArtifact{
			Path: artifactPath, Size: int64(len(content)), SHA256: digestBytes(content),
		})
		totalSize += size
	}
	return result, nil
}

func readGitBlobMetadata(ctx context.Context, bare, commit, artifactPath string) (string, int64, error) {
	output, err := runGit(ctx, "", "-C", bare, "ls-tree", "-z", commit, "--", artifactPath)
	if err != nil || len(output) == 0 || output[len(output)-1] != 0 || bytes.Count(output, []byte{0}) != 1 {
		return "", 0, fmt.Errorf("remote artifact tree entry is invalid")
	}
	metadata, name, found := bytes.Cut(output[:len(output)-1], []byte{'\t'})
	fields := strings.Fields(string(metadata))
	if !found || string(name) != artifactPath || len(fields) != 3 ||
		(fields[0] != "100644" && fields[0] != "100755") || fields[1] != "blob" || !commitPattern.MatchString(fields[2]) {
		return "", 0, fmt.Errorf("remote artifact is not a regular blob")
	}
	objectType, err := gitText(ctx, bare, "cat-file", "-t", fields[2])
	if err != nil || objectType != "blob" {
		return "", 0, fmt.Errorf("remote artifact object type is invalid")
	}
	sizeText, err := gitText(ctx, bare, "cat-file", "-s", fields[2])
	if err != nil {
		return "", 0, err
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("remote artifact size is invalid")
	}
	return fields[2], size, nil
}

func validateReadbackArtifactBounds(count int, singleSize, totalSize int64) error {
	if count < 1 || count > maxReadbackArtifacts || singleSize < 0 || singleSize > maxReadbackArtifactBytes ||
		totalSize < 0 || totalSize > maxReadbackTotalBytes {
		return fmt.Errorf("remote artifact bounds are invalid")
	}
	return nil
}

func validRemoteRef(ref, prefix string) bool {
	if !strings.HasPrefix(ref, prefix) || len(ref) == len(prefix) || strings.Contains(ref, "..") || strings.ContainsAny(ref, " ~^:?*[\\") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(ref, prefix), "/") {
		if segment == "" || segment == "." || strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".lock") {
			return false
		}
	}
	return true
}

func gitLine(ctx context.Context, bare string, arguments ...string) (string, error) {
	line, err := gitText(ctx, bare, arguments...)
	if err != nil {
		return "", err
	}
	if !commitPattern.MatchString(line) {
		return "", fmt.Errorf("git returned an invalid commit")
	}
	return line, nil
}

func gitText(ctx context.Context, bare string, arguments ...string) (string, error) {
	output, err := runGit(ctx, "", append([]string{"-C", bare}, arguments...)...)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(output))
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return "", fmt.Errorf("git returned invalid text")
	}
	return line, nil
}

func runGit(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf("git %s: %w: %s", arguments[0], err, strings.TrimSpace(string(exitError.Stderr)))
		}
		return nil, fmt.Errorf("git %s: %w", arguments[0], err)
	}
	return output, nil
}
