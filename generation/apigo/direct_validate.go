package apigo

import (
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"strings"
)

func validateDirectAPIGoOutput(repository string, request APIGoRequest) error {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return err
	}
	defer root.Close()
	files := map[string][]*ast.File{}
	fileSet := token.NewFileSet()
	for _, scope := range request.OutputScopes {
		if err := fs.WalkDir(root.FS(), scope.Path, func(name string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil || entry.IsDir() || path.Ext(name) != ".go" {
				return walkErr
			}
			content, readErr := fs.ReadFile(root.FS(), name)
			if readErr != nil {
				return readErr
			}
			parsed, parseErr := parser.ParseFile(fileSet, name, content, parser.ParseComments|parser.AllErrors)
			if parseErr != nil {
				return parseErr
			}
			directory := path.Dir(name)
			files[directory] = append(files[directory], parsed)
			return nil
		}); err != nil {
			return err
		}
	}
	checker := &directAPIGoImporter{prefix: request.ModulePath, files: files, fileSet: fileSet, fallback: importer.Default(), packages: map[string]*types.Package{}, checking: map[string]bool{}}
	for directory := range files {
		if _, err := checker.check(directory); err != nil {
			return err
		}
	}
	return nil
}

type directAPIGoImporter struct {
	prefix   string
	files    map[string][]*ast.File
	fileSet  *token.FileSet
	fallback types.Importer
	packages map[string]*types.Package
	checking map[string]bool
}

func (i *directAPIGoImporter) Import(importPath string) (*types.Package, error) {
	prefix := strings.TrimSuffix(i.prefix, "/") + "/"
	if strings.HasPrefix(importPath, prefix) {
		if directory := strings.TrimPrefix(importPath, prefix); i.files[directory] != nil {
			return i.check(directory)
		}
	}
	value, err := i.fallback.Import(importPath)
	if err == nil {
		return value, nil
	}
	stub := types.NewPackage(importPath, path.Base(importPath))
	stub.MarkComplete()
	return stub, nil
}

func (i *directAPIGoImporter) check(directory string) (*types.Package, error) {
	if value := i.packages[directory]; value != nil {
		return value, nil
	}
	if i.checking[directory] {
		return nil, errors.New("generated API Go package import cycle")
	}
	i.checking[directory] = true
	defer delete(i.checking, directory)
	value, err := (&types.Config{Importer: i, IgnoreFuncBodies: true}).Check(strings.TrimSuffix(i.prefix, "/")+"/"+directory, i.fileSet, i.files[directory], nil)
	if err != nil {
		return nil, err
	}
	i.packages[directory] = value
	return value, nil
}
