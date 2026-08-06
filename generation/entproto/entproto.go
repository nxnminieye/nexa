// Package entproto projects explicitly selected Ent CRUD facts into a Proto fragment.
package entproto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
	"github.com/nxnminieye/nexa/generation/internal/crudbuild"
	"github.com/nxnminieye/nexa/generation/internal/entityload"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
)

var ErrNoCRUDSelection = errors.New("no Ent schema declares crud.operations")

type Options struct {
	RepositoryRoot string
	SchemaDir      string
	ServiceID      string
	ProtoPackage   string
	GoPackage      string
	MultiTenant    bool
}

// Generate loads the consumer Ent graph and renders the selected CRUD fragment.
func Generate(ctx context.Context, options Options) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("generation context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := canonicalRepository(options.RepositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("repository root is invalid: %w", err)
	}
	schema, err := provenance.ParseDomainSource(filepath.ToSlash(options.SchemaDir))
	if err != nil {
		return nil, fmt.Errorf("schema directory is invalid: %w", err)
	}
	moduleRoot, importPath, err := resolveSchemaModule(root, schema.String())
	if err != nil {
		return nil, err
	}
	graph, err := loadGraph(moduleRoot, importPath)
	if err != nil {
		return nil, fmt.Errorf("load Ent graph: %w", err)
	}
	document, err := entityload.LoadDirectCRUD(ctx, root, schema, graph)
	if err != nil {
		return nil, err
	}
	selected := false
	for _, item := range document.Entities() {
		if _, ok := item.CRUD(); ok {
			selected = true
			break
		}
	}
	if !selected {
		return nil, ErrNoCRUDSelection
	}
	built, _, err := crudbuild.Build(document, crudbuild.Spec{
		ServiceID: options.ServiceID, ProtoPackage: options.ProtoPackage, GoPackage: options.GoPackage,
		MultiTenant: crudbuild.MultiTenantConfig{Enabled: options.MultiTenant},
	})
	if err != nil {
		return nil, err
	}
	return crudbuild.Render(built)
}

var loadMu sync.Mutex

func loadGraph(moduleRoot, importPath string) (*gen.Graph, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	previous, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(moduleRoot); err != nil {
		return nil, err
	}
	defer func() { _ = os.Chdir(previous) }()
	return entc.LoadGraph(importPath, &gen.Config{Target: filepath.Join(moduleRoot, ".nexa-ent-proto-target"), BuildFlags: []string{"-mod=readonly"}})
}

func resolveSchemaModule(repositoryRoot, schema string) (string, string, error) {
	current := filepath.Join(repositoryRoot, filepath.FromSlash(schema))
	for {
		info, err := os.Stat(current)
		if err != nil || !info.IsDir() {
			return "", "", fmt.Errorf("schema directory is unavailable")
		}
		moduleFile := filepath.Join(current, "go.mod")
		data, readErr := os.ReadFile(moduleFile)
		if readErr == nil {
			parsed, parseErr := modfile.Parse(moduleFile, data, nil)
			if parseErr != nil || parsed.Module == nil || parsed.Module.Mod.Path == "" {
				return "", "", fmt.Errorf("schema module is invalid")
			}
			relative, relErr := filepath.Rel(current, filepath.Join(repositoryRoot, filepath.FromSlash(schema)))
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return "", "", fmt.Errorf("schema module escapes repository")
			}
			importPath := parsed.Module.Mod.Path
			if relative != "." {
				importPath += "/" + filepath.ToSlash(relative)
			}
			return current, importPath, nil
		}
		if current == repositoryRoot {
			return "", "", fmt.Errorf("schema module is not found")
		}
		parent := filepath.Dir(current)
		if parent == current || !pathContained(parent, repositoryRoot) {
			return "", "", fmt.Errorf("schema module is not found")
		}
		current = parent
	}
}

func canonicalRepository(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", os.ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(value)
	if err != nil || canonical != value {
		return "", os.ErrInvalid
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", os.ErrInvalid
	}
	return canonical, nil
}

func pathContained(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
