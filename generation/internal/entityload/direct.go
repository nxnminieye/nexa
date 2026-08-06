package entityload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"entgo.io/ent/entc/gen"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/provenance"
)

// LoadDirectCRUD projects CRUD-selected schemas from an already loaded Ent graph.
// It does not run Ent code generation or create a repository copy.
func LoadDirectCRUD(ctx context.Context, repositoryRoot string, schemaDir provenance.DomainSource, graph *gen.Graph) (entity.Document, error) {
	if ctx == nil {
		return entity.Document{}, fmt.Errorf("entity load context is required")
	}
	if err := ctx.Err(); err != nil {
		return entity.Document{}, err
	}
	root, err := canonicalRepository(repositoryRoot)
	if err != nil {
		return entity.Document{}, fmt.Errorf("repository root is invalid: %w", err)
	}
	schema := schemaDir.String()
	if schema == "" || filepath.IsAbs(schema) || filepath.Clean(filepath.FromSlash(schema)) != filepath.FromSlash(schema) {
		return entity.Document{}, fmt.Errorf("schema directory is invalid")
	}
	schemaPath := filepath.Join(root, filepath.FromSlash(schema))
	info, err := os.Stat(schemaPath)
	if err != nil || !info.IsDir() {
		return entity.Document{}, fmt.Errorf("schema directory is unavailable")
	}
	sources, err := readEntCommentSources(root, schemaPath)
	if err != nil {
		return entity.Document{}, err
	}
	facts, diagnostics, err := parseEntFactGraph(graph, sources)
	if err != nil {
		return entity.Document{}, err
	}
	if len(diagnostics) > 0 {
		return entity.Document{}, sourceCommentDiagnosticsError(diagnostics)
	}
	projection, err := projectCRUDGraph(graph, facts, nil, directSourceResolver(root, schema))
	if err != nil {
		return entity.Document{}, entity.AdoptLoadedDocumentError(err, schemaDir)
	}
	return adoptProjection(projection, facts, schemaDir)
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

func readEntCommentSources(repositoryRoot, schemaPath string) ([]entCommentSource, error) {
	var result []entCommentSource
	err := filepath.WalkDir(schemaPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("schema source contains symlink: %s", path)
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("schema source escapes repository")
		}
		source, err := provenance.ParseDomainSource(filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		result = append(result, entCommentSource{path: source, data: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read Ent sources: %w", err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].path.String() < result[j].path.String() })
	return result, nil
}

func directSourceResolver(repositoryRoot, schema string) sourceResolver {
	return func(position string) (provenance.DomainSource, error) {
		filename, err := sourceFilename(position)
		if err != nil {
			return provenance.DomainSource{}, err
		}
		if !filepath.IsAbs(filename) {
			filename = filepath.Join(repositoryRoot, filename)
		}
		filename = filepath.Clean(filename)
		relative, err := filepath.Rel(repositoryRoot, filename)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return provenance.DomainSource{}, fmt.Errorf("schema source escapes repository")
		}
		value := filepath.ToSlash(relative)
		if value != schema && !strings.HasPrefix(value, schema+"/") {
			return provenance.DomainSource{}, fmt.Errorf("schema source is outside schema directory")
		}
		return provenance.ParseDomainSource(value)
	}
}
