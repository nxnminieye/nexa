package entexec

import (
	"os"
	"path/filepath"
	"strings"
)

func canonicalExistingDirectory(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", os.ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return "", os.ErrInvalid
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", os.ErrInvalid
	}
	return canonical, nil
}

func pathContainedBy(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func pathsOverlap(left, right string) bool {
	return pathContainedBy(left, right) || pathContainedBy(right, left)
}

func createOwnedScratchRoot(parent string, hook func(projectionEvent)) (*ownedScratchRoot, error) {
	if _, err := canonicalExistingDirectory(parent); err != nil {
		return nil, projectError("scratch_create_failed", "/scratch")
	}
	rootPath, err := os.MkdirTemp(parent, ".nexa-ent-")
	if err != nil {
		return nil, projectError("scratch_create_failed", "/scratch")
	}
	if err := os.Chmod(rootPath, 0o700); err != nil {
		_ = os.RemoveAll(rootPath)
		return nil, projectError("scratch_create_failed", "/scratch")
	}
	rootHandle, err := os.OpenRoot(rootPath)
	if err != nil {
		_ = os.RemoveAll(rootPath)
		return nil, projectError("scratch_create_failed", "/scratch")
	}
	rootInfo, err := rootHandle.Stat(".")
	if err != nil || !rootInfo.IsDir() {
		_ = rootHandle.Close()
		_ = os.RemoveAll(rootPath)
		return nil, projectError("scratch_create_failed", "/scratch")
	}
	return &ownedScratchRoot{rootPath: rootPath, rootHandle: rootHandle, rootInfo: rootInfo}, nil
}

func (o *ownedScratchRoot) validatePathIdentity() error {
	if o == nil || o.closed || o.rootHandle == nil {
		return cleanupError("cleanup_identity_invalid")
	}
	handleInfo, handleErr := o.rootHandle.Stat(".")
	pathInfo, pathErr := os.Lstat(o.rootPath)
	if handleErr != nil || pathErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !handleInfo.IsDir() || !pathInfo.IsDir() ||
		o.rootInfo == nil || !os.SameFile(o.rootInfo, handleInfo) || !os.SameFile(o.rootInfo, pathInfo) {
		return cleanupError("cleanup_identity_invalid")
	}
	return nil
}

func (o *ownedScratchRoot) cleanup() error {
	if o == nil || o.closed {
		return nil
	}
	if err := o.validatePathIdentity(); err != nil {
		o.closed = true
		_ = o.rootHandle.Close()
		return err
	}
	o.closed = true
	if err := o.rootHandle.Close(); err != nil {
		return cleanupError("cleanup_failed")
	}
	if err := os.RemoveAll(o.rootPath); err != nil {
		return cleanupError("cleanup_failed")
	}
	return nil
}

func (s *Scratch) Cleanup() error {
	if s == nil || s.state == nil {
		return readbackError("scratch_state_invalid", "/scratch")
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.cleaned {
		return s.state.cleanupErr
	}
	if s.state.running {
		return cleanupError("cleanup_identity_invalid")
	}
	err := s.state.owner.cleanup()
	s.state.cleaned = true
	s.state.cleanupErr = err
	return err
}

func (s *Scratch) acquireProcess(repository, staging string) (string, func(), error) {
	if s == nil || s.state == nil {
		return "", nil, newProcessError("tool_input_invalid", "input", "scratch_state_invalid", "/scratch", "", 0)
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.cleaned || s.state.running || s.state.owner == nil || s.state.location.state == nil ||
		s.state.location.state.repositoryRoot != repository || s.state.staging != staging {
		return "", nil, newProcessError("tool_input_invalid", "input", "scratch_state_invalid", "/scratch", "", 0)
	}
	if err := s.state.owner.validatePathIdentity(); err != nil {
		return "", nil, newProcessError("tool_input_invalid", "input", "scratch_state_invalid", "/scratch", "", 0)
	}
	s.state.running = true
	released := false
	return s.state.root, func() {
		s.state.mu.Lock()
		defer s.state.mu.Unlock()
		if !released {
			s.state.running = false
			released = true
		}
	}, nil
}
