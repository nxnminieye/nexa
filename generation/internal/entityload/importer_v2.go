package entityload

import (
	"context"
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"entgo.io/ent/entc/gen"
	"entgo.io/ent/entc/load"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/provenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

type V2Spec struct {
	RepositoryRoot string
	ModuleDir      string
	ModulePath     string
	SchemaDir      string
	BuildTags      []string
	Environment    []string
}

type ImporterV2 struct {
	ImportPath string
	TypeNames  []string
	Source     []byte
	Sources    []provenance.Source
}

type V2Error struct{ code, reason, pointer, source string }

func (e *V2Error) Error() string {
	if e == nil {
		return ""
	}
	return e.code + ": " + e.reason
}
func (e *V2Error) Owner() string { return "entityload" }
func (e *V2Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}
func (e *V2Error) Reason() string {
	if e == nil {
		return ""
	}
	return e.reason
}
func (e *V2Error) Pointer() string {
	if e == nil {
		return ""
	}
	return e.pointer
}
func (e *V2Error) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}

func InputV2Error(reason, pointer string) error {
	return &V2Error{code: "entity_input_invalid", reason: reason, pointer: pointer}
}
func GraphV2Error(reason, source string) error {
	return &V2Error{code: "entity_graph_load_failed", reason: reason, source: source}
}

func DiscoverV2(ctx context.Context, spec V2Spec) (ImporterV2, error) {
	moduleRoot, schemaRoot, importPath, err := validateV2Roots(spec)
	if err != nil {
		return ImporterV2{}, err
	}
	flags := []string{"-mod=readonly"}
	tags := append([]string(nil), spec.BuildTags...)
	sort.Strings(tags)
	if len(tags) > 0 {
		flags = append(flags, "-tags="+strings.Join(tags, ","))
	}
	config := &packages.Config{
		Context: ctx, Dir: moduleRoot, Env: append([]string(nil), spec.Environment...),
		BuildFlags: flags, Mode: packages.NeedName | packages.NeedTypes | packages.NeedImports | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedModule,
	}
	loaded, err := packages.Load(config, importPath)
	if err != nil || len(loaded) != 1 || packages.PrintErrors(loaded) != 0 || loaded[0].Types == nil || loaded[0].PkgPath != importPath {
		return ImporterV2{}, GraphV2Error("graph_load_failed", spec.SchemaDir)
	}
	pkg := loaded[0]
	var entPackage *types.Package
	for _, imported := range pkg.Types.Imports() {
		if imported.Path() == "entgo.io/ent" {
			entPackage = imported
			break
		}
	}
	if entPackage == nil {
		return ImporterV2{}, GraphV2Error("graph_load_failed", spec.SchemaDir)
	}
	ifaceObject := entPackage.Scope().Lookup("Interface")
	iface, ok := ifaceObject.Type().Underlying().(*types.Interface)
	if !ok {
		return ImporterV2{}, GraphV2Error("graph_load_failed", spec.SchemaDir)
	}
	names := make([]string, 0)
	for _, name := range pkg.Types.Scope().Names() {
		if !token.IsExported(name) {
			continue
		}
		object, ok := pkg.Types.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		typeValue := object.Type()
		if _, ok := typeValue.Underlying().(*types.Struct); !ok {
			continue
		}
		if types.Implements(typeValue, iface) || types.Implements(types.NewPointer(typeValue), iface) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ImporterV2{}, GraphV2Error("graph_load_failed", spec.SchemaDir)
	}
	sources, err := importerSources(spec.RepositoryRoot, schemaRoot, pkg.CompiledGoFiles)
	if err != nil {
		return ImporterV2{}, InputV2Error("schema_source_invalid", "/schemaDir")
	}
	return ImporterV2{ImportPath: importPath, TypeNames: names, Source: renderImporterV2(importPath, names), Sources: sources}, nil
}

func renderImporterV2(importPath string, names []string) []byte {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n\t\"encoding/json\"\n\t\"os\"\n\n\t\"entgo.io/ent\"\n\t\"entgo.io/ent/entc/load\"\n\tschema ")
	b.WriteString(strconv.Quote(importPath))
	b.WriteString("\n)\n\nfunc main() {\n\tvalues := []ent.Interface{")
	for _, name := range names {
		b.WriteString("&schema.")
		b.WriteString(name)
		b.WriteString("{},")
	}
	b.WriteString("}\n\trecords := make([]json.RawMessage, len(values))\n\tfor i, value := range values {\n\t\tencoded, err := load.MarshalSchema(value)\n\t\tif err != nil { panic(err) }\n\t\trecords[i] = encoded\n\t}\n\tif err := json.NewEncoder(os.Stdout).Encode(records); err != nil { panic(err) }\n}\n")
	return []byte(b.String())
}

func ProjectV2(spec V2Spec, importer ImporterV2, stdout []byte) (entity.Document, error) {
	var records []json.RawMessage
	if json.Unmarshal(stdout, &records) != nil || len(records) != len(importer.TypeNames) {
		return entity.Document{}, GraphV2Error("graph_load_failed", spec.SchemaDir)
	}
	schemas := make([]*load.Schema, len(records))
	for index, record := range records {
		value, err := load.UnmarshalSchema(record)
		if err != nil {
			return entity.Document{}, GraphV2Error("graph_load_failed", spec.SchemaDir)
		}
		schemas[index] = value
	}
	graph, err := gen.NewGraph(&gen.Config{}, schemas...)
	if err != nil {
		return entity.Document{}, GraphV2Error("graph_load_failed", spec.SchemaDir)
	}
	resolver := func(position string) (provenance.DomainSource, error) {
		filename, err := sourceFilename(position)
		if err != nil {
			return provenance.DomainSource{}, err
		}
		if !filepath.IsAbs(filename) {
			filename = filepath.Join(filepath.Join(spec.RepositoryRoot, filepath.FromSlash(spec.ModuleDir)), filename)
		}
		filename = filepath.Clean(filename)
		rel, err := filepath.Rel(spec.RepositoryRoot, filename)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return provenance.DomainSource{}, fmt.Errorf("schema source escapes repository")
		}
		source, err := provenance.ParseDomainSource(filepath.ToSlash(rel))
		if err != nil || source.String() != spec.SchemaDir && !strings.HasPrefix(source.String(), spec.SchemaDir+"/") {
			return provenance.DomainSource{}, fmt.Errorf("schema source is outside schema directory")
		}
		return source, nil
	}
	projection, err := projectLoadedGraph(graph, importer.Sources, resolver, mustDomainSourceV2(spec.SchemaDir))
	if err != nil {
		return entity.Document{}, GraphV2Error("source_projection_failed", spec.SchemaDir)
	}
	document, err := adoptProjection(projection, mustDomainSourceV2(spec.SchemaDir))
	if err != nil {
		return entity.Document{}, GraphV2Error("source_projection_failed", spec.SchemaDir)
	}
	return document, nil
}

func validateV2Roots(spec V2Spec) (string, string, string, error) {
	if spec.RepositoryRoot == "" || !filepath.IsAbs(spec.RepositoryRoot) {
		return "", "", "", InputV2Error("module_root_invalid", "/moduleDir")
	}
	moduleRoot := filepath.Clean(filepath.Join(spec.RepositoryRoot, filepath.FromSlash(spec.ModuleDir)))
	schemaRoot := filepath.Clean(filepath.Join(spec.RepositoryRoot, filepath.FromSlash(spec.SchemaDir)))
	for _, value := range []string{spec.RepositoryRoot, moduleRoot, schemaRoot} {
		resolved, err := filepath.EvalSymlinks(value)
		if err != nil || resolved != value {
			return "", "", "", InputV2Error("module_root_invalid", "/moduleDir")
		}
	}
	if !pathInsideV2(schemaRoot, moduleRoot) || !pathInsideV2(moduleRoot, spec.RepositoryRoot) {
		return "", "", "", InputV2Error("schema_dir_escape", "/schemaDir")
	}
	moduleFile := filepath.Join(moduleRoot, "go.mod")
	info, err := os.Lstat(moduleFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", "", InputV2Error("module_root_invalid", "/moduleDir")
	}
	data, err := os.ReadFile(moduleFile)
	parsed, parseErr := modfile.Parse(moduleFile, data, nil)
	if err != nil || parseErr != nil || parsed.Module == nil || parsed.Module.Mod.Path != spec.ModulePath {
		return "", "", "", InputV2Error("module_path_mismatch", "/modulePath")
	}
	relative, _ := filepath.Rel(moduleRoot, schemaRoot)
	importPath := spec.ModulePath
	if relative != "." {
		importPath += "/" + filepath.ToSlash(relative)
	}
	return moduleRoot, schemaRoot, importPath, nil
}

func importerSources(repository, schemaRoot string, files []string) ([]provenance.Source, error) {
	result := make([]provenance.Source, 0, len(files))
	for _, file := range files {
		file = filepath.Clean(file)
		if !pathInsideV2(file, schemaRoot) {
			return nil, fmt.Errorf("source outside schema")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(repository, file)
		if err != nil {
			return nil, err
		}
		ref, err := provenance.RepositoryRef(filepath.ToSlash(rel), "")
		if err != nil {
			return nil, err
		}
		result = append(result, provenance.Source{Ref: ref, Digest: provenance.SHA256(data)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref.String() < result[j].Ref.String() })
	return result, nil
}

func pathInsideV2(value, root string) bool {
	rel, err := filepath.Rel(root, value)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func mustDomainSourceV2(value string) provenance.DomainSource {
	source, _ := provenance.ParseDomainSource(value)
	return source
}
