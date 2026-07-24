package apigo

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/nxnminieye/nexa/generation/directwrite"
)

func snapshotManualScopeFiles(repository string, scopes []directwrite.OutputScope) (map[string][]byte, error) {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	result := map[string][]byte{}
	for _, scope := range scopes {
		err := fs.WalkDir(root.FS(), scope.Path, func(name string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return errors.New("API output scope contains a non-regular entry")
			}
			file, err := root.Open(filepath.FromSlash(name))
			if err != nil {
				return err
			}
			content, readErr := io.ReadAll(file)
			closeErr := file.Close()
			if readErr != nil || closeErr != nil {
				return errors.Join(readErr, closeErr)
			}
			if !apiGeneratedMarker(name, content) {
				result[name] = content
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return result, nil
}

func apiGeneratedMarker(name string, content []byte) bool {
	if path.Ext(name) == ".go" {
		parsed, err := parser.ParseFile(token.NewFileSet(), name, content, parser.ParseComments)
		return err == nil && ast.IsGenerated(parsed)
	}
	window := content
	if len(window) > 4096 {
		window = window[:4096]
	}
	return bytes.Contains(window, []byte("Code generated")) && bytes.Contains(window, []byte("DO NOT EDIT"))
}

func verifyManualScopeFiles(repository string, before map[string][]byte) error {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, content := range before {
		info, err := root.Lstat(filepath.FromSlash(name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("manual output file was removed or replaced")
		}
		file, err := root.Open(filepath.FromSlash(name))
		if err != nil {
			return errors.New("manual output file was removed or replaced")
		}
		after, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || !bytes.Equal(content, after) {
			return errors.New("manual output file changed")
		}
	}
	return nil
}

func rejectNewUnmarkedFiles(repository string, scopes []directwrite.OutputScope, before map[string][]byte) error {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, scope := range scopes {
		err := fs.WalkDir(root.FS(), scope.Path, func(name string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			if _, existed := before[name]; existed {
				return nil
			}
			info, err := entry.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return errors.New("new output entry is invalid")
			}
			file, err := root.Open(filepath.FromSlash(name))
			if err != nil {
				return err
			}
			content, readErr := io.ReadAll(file)
			_ = file.Close()
			if readErr != nil {
				return readErr
			}
			if !apiGeneratedMarker(name, content) {
				return errors.New("tool created unmarked manual output")
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
