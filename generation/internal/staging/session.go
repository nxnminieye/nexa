package staging

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

type Session struct {
	repositoryRoot string
	root           string
	files          *os.Root
	closeOnce      sync.Once
	closeErr       error
}

func Begin(repositoryPath string) (*Session, error) {
	repository, err := filepath.Abs(repositoryPath)
	if err != nil {
		return nil, err
	}
	repository, err = filepath.EvalSymlinks(filepath.Clean(repository))
	if err != nil {
		return nil, err
	}
	created, err := os.MkdirTemp(repository, ".nexa-generation-staging-")
	if err != nil {
		return nil, err
	}
	created, err = filepath.EvalSymlinks(created)
	if err != nil {
		_ = os.RemoveAll(created)
		return nil, err
	}
	files, err := os.OpenRoot(created)
	if err != nil {
		_ = os.RemoveAll(created)
		return nil, err
	}
	return &Session{repositoryRoot: repository, root: created, files: files}, nil
}

func (s *Session) CanonicalRepositoryRoot() string {
	if s == nil {
		return ""
	}
	return s.repositoryRoot
}

func (s *Session) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Session) Emit(name string, content []byte) error {
	if s == nil || s.files == nil || !validPath(name) {
		return os.ErrInvalid
	}
	if err := ensureDirectories(s.files, filepath.ToSlash(filepath.Dir(name))); err != nil {
		return err
	}
	file, err := s.files.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func (s *Session) Read(name string) ([]byte, error) {
	if s == nil || s.files == nil || !validPath(name) {
		return nil, os.ErrInvalid
	}
	info, err := s.files.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o644 {
		return nil, errors.Join(os.ErrInvalid, err)
	}
	file, err := s.files.Open(name)
	if err != nil {
		return nil, err
	}
	content, readErr := io.ReadAll(file)
	closeErr := file.Close()
	return content, errors.Join(readErr, closeErr)
}

func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = errors.Join(s.files.Close(), os.RemoveAll(s.root))
		s.files = nil
	})
	return s.closeErr
}

func validPath(name string) bool {
	return fs.ValidPath(name) && name != "." && filepath.IsLocal(name)
}

func ensureDirectories(root *os.Root, directory string) error {
	if directory == "." || directory == "" {
		return nil
	}
	current := ""
	for _, component := range splitSlash(directory) {
		current = filepath.ToSlash(filepath.Join(current, component))
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.Join(os.ErrInvalid, err)
		}
	}
	return nil
}

func splitSlash(value string) []string {
	var result []string
	for value != "." && value != "" {
		directory, base := filepath.Split(value)
		if base != "" {
			result = append([]string{base}, result...)
		}
		value = filepath.Clean(directory)
	}
	return result
}
