package directwrite

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type fileSystem interface {
	Lstat(string) (fs.FileInfo, error)
	ReadDir(string) ([]fs.DirEntry, error)
	Mkdir(string, fs.FileMode) error
	Remove(string) error
	RemoveAll(string) error
	WriteExclusive(string, []byte, fs.FileMode) error
	EvalSymlinks(string) (string, error)
}

type osFileSystem struct{}

func (osFileSystem) Lstat(name string) (fs.FileInfo, error)     { return os.Lstat(name) }
func (osFileSystem) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }
func (osFileSystem) Mkdir(name string, mode fs.FileMode) error  { return os.Mkdir(name, mode) }
func (osFileSystem) Remove(name string) error                   { return os.Remove(name) }
func (osFileSystem) RemoveAll(name string) error                { return os.RemoveAll(name) }
func (osFileSystem) EvalSymlinks(name string) (string, error)   { return filepath.EvalSymlinks(name) }
func (osFileSystem) WriteExclusive(name string, content []byte, mode fs.FileMode) error {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err = file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err = file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func write(ctx context.Context, repositoryRoot string, mutations MutationSet, files fileSystem) (WriteReport, error) {
	set, err := normalizeMutations(mutations)
	if err != nil {
		return WriteReport{}, err
	}
	root, err := validateRepositoryRoot(repositoryRoot, files)
	if err != nil {
		return WriteReport{}, err
	}
	if err := preflightFileSystem(root, set, files); err != nil {
		return WriteReport{}, err
	}
	report := WriteReport{CompletedWrites: []string{}, CompletedDeletes: []string{}}
	evidence := ChangeEvidenceComplete
	for _, scope := range set.scopes {
		if scope.Mode != OutputModeReplaceTree {
			continue
		}
		if err := canceled(ctx, report, evidence); err != nil {
			return report, err
		}
		if err := verifyScopeAncestors(root, scope.Path, files); err != nil {
			return report, partialFailure(scope.Path, "replace-tree path changed after preflight", report, err, evidence)
		}
		absolute := rootedPath(root, scope.Path)
		if err := files.RemoveAll(absolute); err != nil {
			return report, uncertainPartialFailure(scope.Path, "replace-tree could not be cleared", report, err)
		}
		evidence = ChangeEvidenceHostOnly
		if err := makeDirectories(root, scope.Path, files); err != nil {
			return report, uncertainPartialFailure(scope.Path, "replace-tree root could not be created", report, err)
		}
	}
	for _, target := range set.deletes {
		if err := canceled(ctx, report, evidence); err != nil {
			return report, err
		}
		if err := verifyActionPath(root, target, true, files); err != nil {
			return report, partialFailure(target, "delete path changed after preflight", report, err, evidence)
		}
		absolute := rootedPath(root, target)
		if _, err := files.Lstat(absolute); err == nil {
			if err := files.Remove(absolute); err != nil {
				return report, uncertainPartialFailure(target, "file could not be deleted", report, err)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return report, partialFailure(target, "delete path could not be inspected", report, err, evidence)
		}
		report.CompletedDeletes = addCanonicalPath(report.CompletedDeletes, target)
	}
	for _, output := range set.writes {
		if err := canceled(ctx, report, evidence); err != nil {
			return report, err
		}
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(output.Path)))
		if parent != "." {
			if err := makeDirectories(root, parent, files); err != nil {
				return report, uncertainPartialFailure(output.Path, "write parent could not be created", report, err)
			}
		}
		if err := verifyActionPath(root, output.Path, true, files); err != nil {
			return report, partialFailure(output.Path, "write path changed after preflight", report, err, evidence)
		}
		absolute := rootedPath(root, output.Path)
		unlinked := false
		if info, err := files.Lstat(absolute); err == nil {
			if !info.Mode().IsRegular() {
				return report, partialFailure(output.Path, "write target is not a regular file", report, fs.ErrInvalid, evidence)
			}
			if err := files.Remove(absolute); err != nil {
				return report, uncertainPartialFailure(output.Path, "existing generated file could not be unlinked", report, err)
			}
			unlinked = true
			report.CompletedDeletes = addCanonicalPath(report.CompletedDeletes, output.Path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return report, partialFailure(output.Path, "write target could not be inspected", report, err, evidence)
		}
		if err := files.WriteExclusive(absolute, output.Content, 0o644); err != nil {
			return report, uncertainPartialFailure(output.Path, "generated file could not be created", report, err)
		}
		if unlinked {
			report.CompletedDeletes = removeCanonicalPath(report.CompletedDeletes, output.Path)
		}
		report.CompletedWrites = addCanonicalPath(report.CompletedWrites, output.Path)
	}
	return report, nil
}

func validateRepositoryRoot(root string, files fileSystem) (string, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", directError(ErrorPathDenied, root, "repository root must be canonical and absolute", WriteReport{}, nil)
	}
	info, err := files.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", directError(ErrorPathDenied, root, "repository root must be an existing non-symlink directory", WriteReport{}, err)
	}
	canonical, err := files.EvalSymlinks(root)
	if err != nil || filepath.Clean(canonical) != root {
		return "", directError(ErrorPathDenied, root, "repository root contains a symlink alias", WriteReport{}, err)
	}
	return root, nil
}

func preflightFileSystem(root string, set normalizedMutationSet, files fileSystem) error {
	for _, scope := range set.scopes {
		if err := verifyScopeAncestors(root, scope.Path, files); err != nil {
			return directError(ErrorPathDenied, scope.Path, "output scope is denied by the current repository tree", WriteReport{}, err)
		}
	}
	for _, output := range set.writes {
		if scopeForPath(set.scopes, output.Path).Mode == OutputModeReplaceTree {
			continue
		}
		if err := verifyActionPath(root, output.Path, true, files); err != nil {
			return directError(ErrorPathDenied, output.Path, "write path is denied by the current repository tree", WriteReport{}, err)
		}
	}
	for _, target := range set.deletes {
		if err := verifyActionPath(root, target, true, files); err != nil {
			return directError(ErrorPathDenied, target, "delete path is denied by the current repository tree", WriteReport{}, err)
		}
	}
	return nil
}

func verifyScopeAncestors(root, relative string, files fileSystem) error {
	return verifyComponents(root, relative, false, files)
}

func verifyActionPath(root, relative string, finalRegular bool, files fileSystem) error {
	return verifyComponents(root, relative, finalRegular, files)
}

func verifyComponents(root, relative string, finalRegular bool, files fileSystem) error {
	current := root
	parts := strings.Split(relative, "/")
	for index, component := range parts {
		entries, err := files.ReadDir(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var exact fs.DirEntry
		for _, entry := range entries {
			if foldedComponent(entry.Name()) != foldedComponent(component) {
				continue
			}
			if entry.Name() != component {
				return fs.ErrExist
			}
			exact = entry
		}
		if exact == nil {
			return nil
		}
		info, err := exact.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fs.ErrInvalid
		}
		last := index == len(parts)-1
		if !last && !info.IsDir() {
			return fs.ErrInvalid
		}
		if last && finalRegular && !info.Mode().IsRegular() {
			return fs.ErrInvalid
		}
		if last && !finalRegular && !info.IsDir() {
			return fs.ErrInvalid
		}
		current = filepath.Join(current, component)
	}
	return nil
}

func makeDirectories(root, relative string, files fileSystem) error {
	current := root
	for _, component := range strings.Split(relative, "/") {
		entries, err := files.ReadDir(current)
		if err != nil {
			return err
		}
		var exact fs.DirEntry
		for _, entry := range entries {
			if foldedComponent(entry.Name()) == foldedComponent(component) {
				if entry.Name() != component {
					return fs.ErrExist
				}
				exact = entry
			}
		}
		next := filepath.Join(current, component)
		if exact == nil {
			if err := files.Mkdir(next, 0o755); err != nil {
				return err
			}
		} else {
			info, err := exact.Info()
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				if err != nil {
					return err
				}
				return fs.ErrInvalid
			}
		}
		current = next
	}
	return nil
}

func rootedPath(root, relative string) string {
	return filepath.Join(root, filepath.FromSlash(relative))
}

func canceled(ctx context.Context, report WriteReport, evidence ChangeEvidence) error {
	if err := ctx.Err(); err != nil {
		return directErrorWithEvidence(ErrorCanceled, "", "direct write was canceled", report, err, evidence)
	}
	return nil
}

func partialFailure(path, message string, report WriteReport, cause error, evidence ChangeEvidence) error {
	return directErrorWithEvidence(ErrorPartialWrite, path, message, report, cause, evidence)
}

func uncertainPartialFailure(path, message string, report WriteReport, cause error) error {
	return directErrorWithEvidence(ErrorPartialWrite, path, message, report, cause, ChangeEvidenceHostOnly)
}

func scopeForPath(scopes []OutputScope, target string) OutputScope {
	for _, scope := range scopes {
		equal, related := compareTopology(scope.Path, target)
		if related && !equal && len(foldedPath(scope.Path)) < len(foldedPath(target)) {
			return scope
		}
	}
	return OutputScope{}
}

func addCanonicalPath(paths []string, value string) []string {
	index := 0
	for index < len(paths) && paths[index] < value {
		index++
	}
	if index < len(paths) && paths[index] == value {
		return paths
	}
	paths = append(paths, "")
	copy(paths[index+1:], paths[index:])
	paths[index] = value
	return paths
}

func removeCanonicalPath(paths []string, value string) []string {
	for index, item := range paths {
		if item == value {
			return append(paths[:index], paths[index+1:]...)
		}
	}
	return paths
}
