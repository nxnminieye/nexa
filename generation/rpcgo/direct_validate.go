package rpcgo

import (
	"context"
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

	"github.com/bufbuild/protocompile"
)

func validateDirectRPCGoOutput(repository string, request RPCGoRequest) error {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return err
	}
	defer root.Close()
	goFiles := map[string][]*ast.File{}
	protoFiles := map[string]string{}
	fileSet := token.NewFileSet()
	for _, scope := range request.OutputScopes {
		if err := fs.WalkDir(root.FS(), scope.Path, func(name string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			content, readErr := fs.ReadFile(root.FS(), name)
			if readErr != nil {
				return readErr
			}
			switch path.Ext(name) {
			case ".go":
				parsed, parseErr := parser.ParseFile(fileSet, name, content, parser.ParseComments|parser.AllErrors)
				if parseErr != nil {
					return parseErr
				}
				goFiles[path.Dir(name)] = append(goFiles[path.Dir(name)], parsed)
			case ".proto":
				protoFiles[name] = string(content)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if len(protoFiles) != 0 {
		entries := make([]string, 0, len(protoFiles))
		for name := range protoFiles {
			entries = append(entries, name)
		}
		resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(protoFiles)}
		if _, err := (&protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}).Compile(context.Background(), entries...); err != nil {
			return err
		}
	}
	checker := &directRPCGoImporter{prefix: request.ModulePath, files: goFiles, fileSet: fileSet, fallback: importer.Default(), packages: map[string]*types.Package{}, checking: map[string]bool{}}
	for directory := range goFiles {
		if _, err := checker.check(directory); err != nil {
			return err
		}
	}
	return nil
}

type directRPCGoImporter struct {
	prefix   string
	files    map[string][]*ast.File
	fileSet  *token.FileSet
	fallback types.Importer
	packages map[string]*types.Package
	checking map[string]bool
}

func (i *directRPCGoImporter) Import(importPath string) (*types.Package, error) {
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

func (i *directRPCGoImporter) check(directory string) (*types.Package, error) {
	if value := i.packages[directory]; value != nil {
		return value, nil
	}
	if i.checking[directory] {
		return nil, errors.New("generated RPC Go package import cycle")
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
