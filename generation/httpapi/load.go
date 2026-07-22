package httpapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
	goctlast "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/ast"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

const contractVersion = "nexa.dev/http-api/v1"

type authoredIndex struct {
	typeFiles  map[string]string
	routeFiles map[string]string
	metadata   map[string]map[string]string
	seenFiles  map[string]bool
	stack      map[string]bool
}

func Load(ctx context.Context, options LoadOptions) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	root, entry, err := resolveLoadPaths(options.RepositoryRoot, options.EntryFile)
	if err != nil {
		return Document{}, err
	}
	parsed, err := goctlparser.Parse(entry, nil)
	if err != nil {
		return Document{}, invalid("parser_error", relativeOrBase(root, entry), "", err.Error())
	}
	if err := parsed.Validate(); err != nil {
		return Document{}, invalid("parser_validation_failed", relativeOrBase(root, entry), "", err.Error())
	}
	if version := parsed.Info.Properties["nexaContractVersion"]; version == "" {
		return Document{}, invalid("contract_version_missing", relativeOrBase(root, entry), "/info/nexaContractVersion", "HTTP API contract version is required")
	} else if version != contractVersion {
		return Document{}, invalid("contract_version_unsupported", relativeOrBase(root, entry), "/info/nexaContractVersion", "HTTP API contract version is not supported")
	}
	index := authoredIndex{typeFiles: map[string]string{}, routeFiles: map[string]string{}, metadata: map[string]map[string]string{}, seenFiles: map[string]bool{}, stack: map[string]bool{}}
	if err := index.collect(root, entry); err != nil {
		return Document{}, err
	}
	types, err := projectNativeTypes(ctx, root, parsed.Types, index.typeFiles, options.SourceResolver)
	if err != nil {
		return Document{}, err
	}
	operations, err := projectNativeOperations(parsed.Service.Groups, index, types)
	if err != nil {
		return Document{}, err
	}
	if err := validateTypeCycles(types); err != nil {
		return Document{}, err
	}
	return newDocument(types, operations, nil)
}

func resolveLoadPaths(repositoryRoot, entryFile string) (string, string, error) {
	if repositoryRoot == "" || entryFile == "" {
		return "", "", invalid("load_options_invalid", "", "", "repository root and entry file are required")
	}
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return "", "", invalid("repository_root_invalid", repositoryRoot, "", err.Error())
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", invalid("repository_root_invalid", repositoryRoot, "", err.Error())
	}
	entry := entryFile
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(root, filepath.FromSlash(entry))
	}
	entry, err = filepath.Abs(entry)
	if err != nil {
		return "", "", invalid("entry_file_invalid", entryFile, "", err.Error())
	}
	rel, err := filepath.Rel(root, entry)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", invalid("entry_file_outside_repository", entryFile, "", "entry file must be inside repository root")
	}
	if filepath.Ext(entry) != ".api" {
		return "", "", invalid("entry_file_invalid", entryFile, "", "entry file must use .api extension")
	}
	return root, entry, nil
}

func (i *authoredIndex) collect(root, filename string) error {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return invalid("import_invalid", filename, "", err.Error())
	}
	if i.stack[abs] {
		return invalid("import_cycle", relativeOrBase(root, abs), "", "HTTP API import cycle detected")
	}
	if i.seenFiles[abs] {
		return nil
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return invalid("import_outside_repository", filename, "", "HTTP API import must remain inside repository root")
	}
	rel = filepath.ToSlash(rel)
	data, err := os.ReadFile(abs)
	if err != nil {
		return invalid("source_read_failed", rel, "", err.Error())
	}
	p := goctlparser.New(abs, data)
	doc := p.Parse()
	if err := p.CheckErrors(); err != nil {
		return invalid("parser_error", rel, "", err.Error())
	}
	i.stack[abs], i.seenFiles[abs] = true, true
	defer delete(i.stack, abs)
	for _, statement := range doc.Stmts {
		switch value := statement.(type) {
		case *goctlast.ImportLiteralStmt:
			if err := i.collect(root, filepath.Join(filepath.Dir(abs), filepath.FromSlash(value.Value.RawText()))); err != nil {
				return err
			}
		case *goctlast.ImportGroupStmt:
			for _, item := range value.Values {
				if err := i.collect(root, filepath.Join(filepath.Dir(abs), filepath.FromSlash(item.RawText()))); err != nil {
					return err
				}
			}
		case *goctlast.TypeLiteralStmt:
			if err := i.addType(value.Expr.Name.RawText(), rel); err != nil {
				return err
			}
		case *goctlast.TypeGroupStmt:
			for _, expression := range value.ExprList {
				if err := i.addType(expression.Name.RawText(), rel); err != nil {
					return err
				}
			}
		case *goctlast.ServiceStmt:
			metadata, prefix, err := parseRawServerMetadata(value.AtServerStmt, rel)
			if err != nil {
				return err
			}
			for _, route := range value.Routes {
				path, err := normalizeRoutePath(prefix, route.Route.Path.Format(""))
				if err != nil {
					return invalid("route_path_invalid", rel, "", err.Error())
				}
				key := strings.ToUpper(route.Route.Method.RawText()) + "\x00" + path
				if _, exists := i.routeFiles[key]; exists {
					return invalid("route_collision", rel, "", "HTTP API route is duplicated")
				}
				i.routeFiles[key], i.metadata[key] = rel, cloneStrings(metadata)
			}
		}
	}
	return nil
}

func (i *authoredIndex) addType(name, source string) error {
	if _, exists := i.typeFiles[name]; exists {
		return invalid("type_collision", source, "", "HTTP API type is duplicated")
	}
	i.typeFiles[name] = source
	return nil
}

func relativeOrBase(root, value string) string {
	rel, err := filepath.Rel(root, value)
	if err != nil {
		return filepath.Base(value)
	}
	return filepath.ToSlash(rel)
}

func cloneStrings(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func projectNativeTypes(ctx context.Context, root string, inputs []spec.Type, sources map[string]string, resolver SourceResolver) ([]*typeState, error) {
	result := make([]*typeState, 0, len(inputs))
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		structure, ok := input.(spec.DefineStruct)
		if !ok {
			return nil, invalid("type_unsupported", "", "", fmt.Sprintf("HTTP API type %T is unsupported", input))
		}
		source := sources[structure.RawName]
		if source == "" {
			return nil, invalid("type_source_unresolved", "", "", "HTTP API type declaring file cannot be resolved")
		}
		state := &typeState{name: structure.RawName, shape: ValueType{kind: ValueObject}, fieldIndex: map[string]int{}}
		provenanceValue, err := nativeProvenance(source, "type:"+state.name, canonicalTypeNode{APIVersion: typeNodeVersion, Kind: "type", Name: state.name, Shape: canonicalValueOf(state.shape)})
		if err != nil {
			return nil, invalid("type_source_invalid", source, "", err.Error())
		}
		state.provenance = provenanceValue
		if err := appendMembers(ctx, state, source, nil, structure.Members, resolver); err != nil {
			return nil, err
		}
		sort.Slice(state.fields, func(left, right int) bool {
			return pathKey(state.fields[left].path) < pathKey(state.fields[right].path)
		})
		state.fieldIndex = map[string]int{}
		for index, field := range state.fields {
			state.fieldIndex[pathKey(field.path)] = index
		}
		result = append(result, state)
	}
	return result, nil
}

func appendMembers(ctx context.Context, owner *typeState, source string, parent []string, members []spec.Member, resolver SourceResolver) error {
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}
		names := memberNames(member)
		if len(names) == 0 {
			nested, ok := member.Type.(spec.NestedStruct)
			if !ok {
				return invalid("anonymous_field_unsupported", source, "", "anonymous fields must be inline structs")
			}
			if err := appendMembers(ctx, owner, source, parent, nested.Members, resolver); err != nil {
				return err
			}
			continue
		}
		for _, name := range names {
			path := append(append([]string(nil), parent...), name)
			valueType, err := projectValueType(member.Type)
			if err != nil {
				return invalid("field_type_invalid", source, "", err.Error())
			}
			binding, hasBinding, origin, hasOrigin, optional, err := parseFieldTags(member.Tag)
			if err != nil {
				return invalid("field_tags_invalid", source, "", err.Error())
			}
			field := &fieldState{ownerType: owner.name, path: path, required: !optional && valueType.kind != ValueOptional, valueType: valueType, binding: binding, hasBinding: hasBinding, origin: origin, hasOrigin: hasOrigin}
			envelope := canonicalFieldNode{APIVersion: fieldNodeVersion, Kind: "field", OwnerType: owner.name, Path: append([]string(nil), path...), Required: field.required, ValueType: canonicalValueOf(valueType)}
			if hasBinding {
				envelope.Binding = &canonicalBinding{Location: string(binding.location), Name: binding.name}
			}
			if hasOrigin {
				envelope.Origin = &canonicalOrigin{Ref: origin.Ref.String(), Digest: origin.Digest.String()}
			}
			provenanceValue, err := nativeProvenance(source, "field:"+owner.name+"."+pathKey(path), envelope)
			if err != nil {
				return invalid("field_source_invalid", source, "", err.Error())
			}
			field.provenance = provenanceValue
			if hasOrigin {
				if resolver == nil {
					return invalid("field_origin_unresolved", source, "", "field origin requires a source resolver")
				}
				if local, _ := provenanceValue.NativeSource(); local == origin {
					return invalid("field_origin_redundant", source, "", "field origin duplicates its local owner")
				}
				if err := resolver.Resolve(ctx, origin.Ref, origin.Digest); err != nil {
					return invalid("field_origin_unresolved", source, "", err.Error())
				}
			}
			key := pathKey(path)
			if _, exists := owner.fieldIndex[key]; exists {
				return invalid("field_collision", source, "", "HTTP API field is duplicated")
			}
			owner.fieldIndex[key] = len(owner.fields)
			owner.fields = append(owner.fields, field)
			if nested, ok := member.Type.(spec.NestedStruct); ok {
				if err := appendMembers(ctx, owner, source, path, nested.Members, resolver); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func memberNames(member spec.Member) []string {
	if member.IsInline || strings.TrimSpace(member.Name) == "" {
		return nil
	}
	parts := strings.Split(member.Name, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func projectValueType(value spec.Type) (ValueType, error) {
	switch typed := value.(type) {
	case spec.PrimitiveType:
		return ValueType{kind: ValueScalar, name: typed.RawName}, nil
	case spec.InterfaceType:
		return ValueType{kind: ValueScalar, name: typed.RawName}, nil
	case spec.DefineStruct:
		return ValueType{kind: ValueRef, name: typed.RawName}, nil
	case spec.NestedStruct:
		return ValueType{kind: ValueObject}, nil
	case spec.PointerType:
		inner, err := projectValueType(typed.Type)
		if err != nil {
			return ValueType{}, err
		}
		return ValueType{kind: ValueOptional, element: &inner}, nil
	case spec.ArrayType:
		inner, err := projectValueType(typed.Value)
		if err != nil {
			return ValueType{}, err
		}
		return ValueType{kind: ValueArray, element: &inner}, nil
	case spec.MapType:
		key := ValueType{kind: ValueScalar, name: typed.Key}
		value, err := projectValueType(typed.Value)
		if err != nil {
			return ValueType{}, err
		}
		return ValueType{kind: ValueMap, key: &key, value: &value}, nil
	default:
		return ValueType{}, fmt.Errorf("unsupported formal API type %T", value)
	}
}
