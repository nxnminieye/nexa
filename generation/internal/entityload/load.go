package entityload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/buildinput"
	"github.com/nxnminieye/nexa/generation/internal/entexec"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func LoadCurrentProcess(ctx context.Context, spec entexec.Spec) (document entity.Document, resultErr error) {
	run, err := entexec.Begin(ctx, spec)
	if err != nil {
		return entity.Document{}, err
	}
	defer func() {
		if cleanupErr := run.Cleanup(); cleanupErr != nil && resultErr == nil {
			document = entity.Document{}
			resultErr = cleanupErr
		}
	}()
	if err := run.VerifyPreLoad(); err != nil {
		return entity.Document{}, err
	}
	if err := run.ClaimLoad(); err != nil {
		return entity.Document{}, err
	}
	schemaImport, err := schemaImportPath(run, spec.SchemaDir)
	if err != nil {
		return entity.Document{}, err
	}
	flags := []string{"-mod=readonly"}
	tags := append([]string(nil), spec.BuildTags...)
	sort.Strings(tags)
	if len(tags) > 0 {
		flags = append(flags, "-tags="+strings.Join(tags, ","))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return entity.Document{}, err
	}
	graph, err := entc.LoadGraph(schemaImport, &gen.Config{
		Target: filepath.Join(cwd, "generated"), BuildFlags: flags,
	})
	if err != nil {
		return entity.Document{}, fmt.Errorf("load Ent graph: %w", err)
	}
	moduleSources, err := run.ModuleSources()
	if err != nil {
		return entity.Document{}, err
	}
	inputs, err := run.Inputs()
	if err != nil {
		return entity.Document{}, err
	}
	commentSources, err := retainedEntCommentSources(run, spec.SchemaDir, inputs)
	if err != nil {
		return entity.Document{}, err
	}
	facts, diagnostics, err := parseEntFactGraph(graph, commentSources)
	if err != nil {
		return entity.Document{}, err
	}
	if len(diagnostics) > 0 {
		return entity.Document{}, sourceCommentDiagnosticsError(diagnostics)
	}
	projection, err := projectLoadedGraph(graph, facts, moduleSources, retainedSourceResolver(run, spec.RepositoryRoot, spec.SchemaDir, inputs), spec.SchemaDir)
	if err != nil {
		return entity.Document{}, err
	}
	if err := run.VerifyPostLoad(); err != nil {
		return entity.Document{}, err
	}
	return adoptProjection(projection, facts, spec.SchemaDir)
}

func projectLoadedGraph(graph *gen.Graph, facts sourcecomment.FactGraph, moduleSources []provenance.Source, resolve sourceResolver, source provenance.DomainSource) (entityvalue.Projection, error) {
	projection, err := projectGraph(graph, facts, moduleSources, resolve)
	if err != nil {
		return entityvalue.Projection{}, entity.AdoptLoadedDocumentError(err, source)
	}
	return projection, nil
}

func retainedEntCommentSources(run *entexec.Run, schemaDir provenance.DomainSource, inputs []buildinput.RetainedBuildInput) ([]entCommentSource, error) {
	result := make([]entCommentSource, 0)
	schema := schemaDir.String()
	for _, input := range inputs {
		if input.Kind != "go" || !input.Module.HasRepositoryPath || !strings.HasSuffix(input.Path, ".go") {
			continue
		}
		root := strings.Trim(filepath.ToSlash(input.Module.RepositoryPath), "/")
		if root == "." {
			root = ""
		}
		path := strings.Trim(strings.TrimPrefix(root+"/"+filepath.ToSlash(input.Path), "/"), "/")
		if path != schema && !strings.HasPrefix(path, schema+"/") {
			continue
		}
		source, err := provenance.ParseDomainSource(path)
		if err != nil {
			return nil, fmt.Errorf("invalid retained Ent source %q: %w", path, err)
		}
		data, err := run.ReadRetainedInput(input)
		if err != nil {
			return nil, err
		}
		result = append(result, entCommentSource{path: source, data: data})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].path.String() < result[j].path.String() })
	return result, nil
}

func sourceCommentDiagnosticsError(values []sourcecomment.Diagnostic) error {
	first := values[0]
	location := first.File
	if first.Line > 0 {
		location += ":" + strconv.Itoa(first.Line)
	}
	return fmt.Errorf("%s: %s (%s): %s", location, first.Code, first.Category, first.Suggestion)
}

func adoptProjection(projection entityvalue.Projection, facts sourcecomment.FactGraph, source provenance.DomainSource) (entity.Document, error) {
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		return entity.Document{}, entity.AdoptLoadedDocumentError(err, source)
	}
	return entity.AdoptLoadedDocumentWithFactGraph(value, facts)
}

func schemaImportPath(run *entexec.Run, schemaDir provenance.DomainSource) (string, error) {
	modules, err := run.LocalModules()
	if err != nil {
		return "", err
	}
	schema := schemaDir.String()
	bestLength := -1
	importPath := ""
	for _, local := range modules {
		if !local.HasRepositoryPath {
			continue
		}
		root := strings.Trim(filepath.ToSlash(local.RepositoryPath), "/")
		if root == "." {
			root = ""
		}
		if root != "" && schema != root && !strings.HasPrefix(schema, root+"/") {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(schema, root), "/")
		candidate := local.Module.Path
		if relative != "" {
			candidate += "/" + relative
		}
		if len(root) > bestLength {
			bestLength = len(root)
			importPath = candidate
		}
	}
	if importPath == "" {
		return "", fmt.Errorf("schema module is not retained")
	}
	return importPath, nil
}

func retainedSourceResolver(run *entexec.Run, repository string, schemaDir provenance.DomainSource, inputs []buildinput.RetainedBuildInput) sourceResolver {
	return func(position string) (provenance.DomainSource, error) {
		filename, err := sourceFilename(position)
		if err != nil {
			return provenance.DomainSource{}, err
		}
		if !filepath.IsAbs(filename) {
			filename = filepath.Join(repository, filename)
		}
		filename = filepath.Clean(filename)
		for _, input := range inputs {
			if !input.Module.HasRepositoryPath {
				continue
			}
			moduleRoot := repository
			if input.Module.RepositoryPath != "." {
				moduleRoot = filepath.Join(repository, filepath.FromSlash(input.Module.RepositoryPath))
			}
			candidate := filepath.Clean(filepath.Join(moduleRoot, filepath.FromSlash(input.Path)))
			if candidate != filename {
				continue
			}
			if _, err := run.ReadRetainedInput(input); err != nil {
				return provenance.DomainSource{}, err
			}
			relative, err := filepath.Rel(repository, candidate)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return provenance.DomainSource{}, fmt.Errorf("schema source escapes repository")
			}
			source, err := provenance.ParseDomainSource(filepath.ToSlash(relative))
			if err != nil || source.String() != schemaDir.String() && !strings.HasPrefix(source.String(), schemaDir.String()+"/") {
				return provenance.DomainSource{}, fmt.Errorf("schema source is outside schema directory")
			}
			return source, nil
		}
		return provenance.DomainSource{}, fmt.Errorf("schema source is not retained")
	}
}

func sourceFilename(position string) (string, error) {
	separator := strings.LastIndexByte(position, ':')
	if separator <= 0 || separator == len(position)-1 {
		return "", fmt.Errorf("schema position is invalid")
	}
	if _, err := strconv.Atoi(position[separator+1:]); err != nil {
		return "", fmt.Errorf("schema position is invalid")
	}
	return position[:separator], nil
}
