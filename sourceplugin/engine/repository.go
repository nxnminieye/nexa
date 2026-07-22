package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/provenance"
	"github.com/nxnminieye/nexa/sourceplugin"
)

const repositorySnapshotDomain = "nexa-source-repository-snapshot-v1\x00"

type repositorySnapshot struct {
	files  map[string]FileState
	digest provenance.Digest
}

func validateRepositoryRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", newError(ErrInput, "source_request_invalid", "repository_root_invalid", "/repositoryRoot", "repository")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", newError(ErrInput, "source_request_invalid", "repository_root_invalid", "/repositoryRoot", "repository")
	}
	return root, nil
}

func scanTarget(root, target string, limits sourceplugin.TreeLimits) (repositorySnapshot, error) {
	if err := validateTargetComponents(root, target); err != nil {
		return repositorySnapshot{}, err
	}
	targetPath := filepath.Join(root, filepath.FromSlash(target))
	files := make(map[string]FileState)
	info, err := os.Lstat(targetPath)
	if os.IsNotExist(err) {
		return repositorySnapshot{files: files, digest: digestSnapshot(files)}, nil
	}
	if err != nil {
		return repositorySnapshot{}, newError(ErrConflict, "source_repository_conflict", "target_read_failed", "/target", "snapshot")
	}
	if !info.IsDir() {
		files[""] = stateFromInfo(targetPath, info, nil)
		return repositorySnapshot{files: files, digest: digestSnapshot(files)}, nil
	}
	var total int64
	err = filepath.WalkDir(targetPath, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return newError(ErrConflict, "source_repository_conflict", "target_read_failed", "/target", "snapshot")
		}
		if current == targetPath {
			return nil
		}
		rel, relErr := filepath.Rel(targetPath, current)
		if relErr != nil {
			return newError(ErrInternal, "source_engine_internal", "path_projection_failed", "", "snapshot")
		}
		path := filepath.ToSlash(rel)
		repositoryPath := pathpkgJoin(target, path)
		if repositoryPath == ".nexa/source" || strings.HasPrefix(repositoryPath, ".nexa/source/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return newError(ErrConflict, "source_repository_conflict", "target_read_failed", "/target", "snapshot")
		}
		if len(files) >= limits.MaxFiles {
			return newError(ErrConflict, "source_repository_conflict", "target_file_count_exceeded", "/target", "snapshot")
		}
		if info.IsDir() {
			files[path] = stateFromInfo(current, info, nil)
			return nil
		}
		var content []byte
		if info.Mode().IsRegular() {
			if info.Size() > limits.MaxFileBytes || total > limits.MaxTotalBytes-info.Size() {
				return newError(ErrConflict, "source_repository_conflict", "target_bytes_exceeded", "/target", "snapshot")
			}
			content, statErr = os.ReadFile(current)
			if statErr != nil || int64(len(content)) != info.Size() {
				return newError(ErrConflict, "source_repository_conflict", "target_read_failed", "/target", "snapshot")
			}
			total += int64(len(content))
		} else if info.Mode()&os.ModeSymlink != 0 {
			link, readErr := os.Readlink(current)
			if readErr != nil {
				return newError(ErrConflict, "source_repository_conflict", "target_read_failed", "/target", "snapshot")
			}
			content = []byte(link)
		}
		files[path] = stateFromInfo(current, info, content)
		return nil
	})
	if err != nil {
		return repositorySnapshot{}, err
	}
	return repositorySnapshot{files: files, digest: digestSnapshot(files)}, nil
}

func validateTargetComponents(root, target string) error {
	current := root
	segments := strings.Split(target, "/")
	for index, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return newError(ErrConflict, "source_repository_conflict", "target_read_failed", "/target", "snapshot")
		}
		if index < len(segments)-1 && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return newError(ErrConflict, "source_repository_conflict", "target_component_invalid", "/target", "snapshot")
		}
	}
	return nil
}

func pathpkgJoin(left, right string) string { return path.Join(left, right) }

func stateFromInfo(_ string, info os.FileInfo, content []byte) FileState {
	typeOf := FileOther
	switch {
	case info.Mode().IsRegular():
		typeOf = FileRegular
	case info.IsDir():
		typeOf = FileDirectory
	case info.Mode()&os.ModeSymlink != 0:
		typeOf = FileSymlink
	}
	digest := provenance.Digest{}
	if typeOf == FileRegular || typeOf == FileSymlink {
		digest = provenance.SHA256(content)
	}
	size := info.Size()
	if typeOf == FileDirectory {
		size = 0
	}
	return FileState{typeOf: typeOf, mode: uint32(info.Mode().Perm()), size: size, digest: digest, content: append([]byte(nil), content...)}
}

func digestSnapshot(files map[string]FileState) provenance.Digest {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	h := sha256.New()
	_, _ = h.Write([]byte(repositorySnapshotDomain))
	var length [8]byte
	for _, path := range paths {
		state := files[path]
		for _, value := range []string{path, string(state.typeOf), state.digest.String()} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = h.Write(length[:])
			_, _ = h.Write([]byte(value))
		}
		binary.BigEndian.PutUint64(length[:], uint64(state.mode))
		_, _ = h.Write(length[:])
		binary.BigEndian.PutUint64(length[:], uint64(state.size))
		_, _ = h.Write(length[:])
	}
	digest, _ := provenance.ParseDigest("sha256:" + hex.EncodeToString(h.Sum(nil)))
	return digest
}

func safeJoin(root, relative string) (string, error) {
	joined := filepath.Join(root, filepath.FromSlash(relative))
	if joined == root || !strings.HasPrefix(joined, root+string(os.PathSeparator)) {
		return "", newError(ErrInternal, "source_engine_internal", "path_projection_failed", "", "repository")
	}
	return joined, nil
}
