package httpapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/zeromicro/go-zero/tools/goctl/api/spec"
	goctlast "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/ast"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

const contractVersion = httpconvention.APIVersion

type authoredIndex struct {
	typeFiles  map[string]string
	routeFiles map[string]string
	seenFiles  map[string]bool
	stack      map[string]bool
	factNodes  []sourcecomment.NodeInput
	facts      sourcecomment.FactGraph
	projection *SourceProjection
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
	index := authoredIndex{typeFiles: map[string]string{}, routeFiles: map[string]string{}, seenFiles: map[string]bool{}, stack: map[string]bool{}, projection: options.SourceProjection}
	if err := index.collect(root, entry); err != nil {
		return Document{}, err
	}
	if err := index.buildFactGraph(); err != nil {
		return Document{}, err
	}
	types, err := projectNativeTypes(ctx, root, parsed.Types, index.typeFiles, options.SourceResolver)
	if err != nil {
		return Document{}, err
	}
	operations, responseTypes, err := projectNativeOperations(parsed.Service.Groups, index, types)
	if err != nil {
		return Document{}, err
	}
	types = append(types, responseTypes...)
	return newDocument(types, operations, nil, index.facts)
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
	if err := validateRepositoryFile(root, entry); err != nil {
		return "", "", invalid("entry_file_invalid", entryFile, "", err.Error())
	}
	return root, entry, nil
}

func validateRepositoryFile(root, filename string) error {
	rel, err := filepath.Rel(root, filename)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("file must remain inside repository root")
	}
	current := root
	parts := strings.Split(rel, string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("file path contains symlink")
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return errors.New("file is not regular")
		}
	}
	return nil
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
	if err := validateRepositoryFile(root, abs); err != nil {
		return invalid("source_path_invalid", rel, "", err.Error())
	}
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
	targets := map[int]*sourcecomment.Target{}
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
			if err := i.addAPITypeNode(rel, value.Expr, value.Type.HeadCommentGroup, targets); err != nil {
				return err
			}
		case *goctlast.TypeGroupStmt:
			for _, expression := range value.ExprList {
				if err := i.addType(expression.Name.RawText(), rel); err != nil {
					return err
				}
				if err := i.addAPITypeNode(rel, expression, expression.Name.HeadCommentGroup, targets); err != nil {
					return err
				}
			}
		case *goctlast.ServiceStmt:
			properties, prefix, err := parseServerProperties(value.AtServerStmt, rel)
			if err != nil {
				return err
			}
			group := properties["group"]
			for _, route := range value.Routes {
				pathValue, err := normalizeRoutePath(prefix, route.Route.Path.Format(""))
				if err != nil {
					return invalid("route_path_invalid", rel, "", err.Error())
				}
				key := strings.ToUpper(route.Route.Method.RawText()) + "\x00" + pathValue
				if _, exists := i.routeFiles[key]; exists {
					return invalid("route_collision", rel, "", "HTTP API route is duplicated")
				}
				operationID, err := sourcecomment.CanonicalAPIOperationID(group, route.AtHandler.Name.RawText())
				if err != nil {
					return invalid("operation_id_invalid", rel, "", err.Error())
				}
				source, err := sourcecomment.ParseSourceRef("api://" + rel + "#" + operationID)
				if err != nil {
					return invalid("operation_source_invalid", rel, "", err.Error())
				}
				semanticID := i.projectedSemanticID(source, operationID, sourcecomment.NodeAPIOperation)
				target := sourcecomment.Target{SemanticID: semanticID, Kind: sourcecomment.NodeAPIOperation, Stage: sourcecomment.StageAPI, Source: source}
				head, _ := route.AtHandler.CommentGroup()
				for _, comment := range head {
					copyTarget := target
					targets[comment.Pos().Line] = &copyTarget
				}
				i.factNodes = append(i.factNodes, sourcecomment.NodeInput{
					SemanticID: semanticID, Kind: sourcecomment.NodeAPIOperation, Stage: sourcecomment.StageAPI,
					Source: source, Location: sourcecomment.Location{File: rel, Line: route.AtHandler.Pos().Line, Column: route.AtHandler.Pos().Column},
					NativeCanonical: []byte(strings.Join([]string{group, route.AtHandler.Name.RawText(), strings.ToUpper(route.Route.Method.RawText()), pathValue}, "\x00")),
				})
				i.routeFiles[key] = rel
			}
		}
	}
	textLines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	lines := make([]sourcecomment.Line, len(textLines))
	for index, text := range textLines {
		lines[index] = sourcecomment.Line{Text: text, CommentPrefix: "//", Location: sourcecomment.Location{File: rel, Line: index + 1, Column: 1}, Target: targets[index+1]}
	}
	parsedFacts, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), rel, lines)
	if len(diagnostics) > 0 {
		return sourceCommentInvalid(diagnostics[0])
	}
	for _, fact := range parsedFacts.Facts() {
		index, ok := i.factNode(fact.Target().Source.String())
		if !ok {
			return invalid("source_comment_target_missing", rel, "", "HTTP API source-comment target is missing")
		}
		i.factNodes[index].Facts = append(i.factNodes[index].Facts, fact.Directive())
	}
	for _, inherited := range parsedFacts.Sources() {
		index, ok := i.factNode(inherited.Target().Source.String())
		if !ok {
			return invalid("source_comment_target_missing", rel, "", "HTTP API source-comment target is missing")
		}
		source := inherited.Source()
		i.factNodes[index].SourceDirective = &source
		i.factNodes[index].SourceLocation = inherited.Location()
	}
	return nil
}

func (i *authoredIndex) addAPITypeNode(rel string, expression *goctlast.TypeExpr, head goctlast.CommentGroup, targets map[int]*sourcecomment.Target) error {
	typeName := expression.Name.RawText()
	if err := i.addFactNode(rel, typeName, sourcecomment.NodeAPIType, expression.Pos().Line, expression.Pos().Column, canonicalAPITypeNative(expression), head, targets); err != nil {
		return err
	}
	structure, ok := expression.DataType.(*goctlast.StructDataType)
	if !ok {
		return nil
	}
	for _, element := range structure.Elements {
		if element.IsAnonymous() || len(element.Name) != 1 {
			continue
		}
		name := element.Name[0].RawText()
		tag := ""
		if element.Tag != nil {
			tag = element.Tag.RawText()
		}
		externalName, _, _, err := externalFieldName(name, tag)
		if err != nil {
			return invalid("field_tags_invalid", rel, "", err.Error())
		}
		headComments, _ := element.CommentGroup()
		semanticID := typeName + "." + externalName
		if err := i.addFactNode(rel, semanticID, sourcecomment.NodeAPIField, element.Pos().Line, element.Pos().Column, canonicalAPIElementNative(element), headComments, targets); err != nil {
			return err
		}
	}
	return nil
}

func canonicalAPITypeNative(expression *goctlast.TypeExpr) []byte {
	return []byte(expression.Name.RawText() + "\x00" + canonicalAPIDataTypeNative(expression.DataType))
}

func canonicalAPIElementNative(element *goctlast.ElemExpr) []byte {
	names := make([]string, len(element.Name))
	for index, name := range element.Name {
		names[index] = name.RawText()
	}
	tag := ""
	if element.Tag != nil {
		tag = element.Tag.RawText()
	}
	return []byte(strings.Join(names, ",") + "\x00" + canonicalAPIDataTypeNative(element.DataType) + "\x00" + tag)
}

func canonicalAPIDataTypeNative(value goctlast.DataType) string {
	structure, ok := value.(*goctlast.StructDataType)
	if !ok {
		return value.Format("")
	}
	parts := make([]string, len(structure.Elements))
	for index, element := range structure.Elements {
		parts[index] = string(canonicalAPIElementNative(element))
	}
	return "{" + strings.Join(parts, "\x01") + "}"
}

func (i *authoredIndex) addFactNode(rel, semanticID string, kind sourcecomment.NodeKind, line, column int, native []byte, comments goctlast.CommentGroup, targets map[int]*sourcecomment.Target) error {
	source, err := sourcecomment.ParseSourceRef("api://" + rel + "#" + semanticID)
	if err != nil {
		return invalid("source_comment_node_invalid", rel, "", err.Error())
	}
	semanticID = i.projectedSemanticID(source, semanticID, kind)
	target := sourcecomment.Target{SemanticID: semanticID, Kind: kind, Stage: sourcecomment.StageAPI, Source: source}
	for _, comment := range comments {
		copyTarget := target
		targets[comment.Pos().Line] = &copyTarget
	}
	i.factNodes = append(i.factNodes, sourcecomment.NodeInput{
		SemanticID: semanticID, Kind: kind, Stage: sourcecomment.StageAPI, Source: source,
		Location: sourcecomment.Location{File: rel, Line: line, Column: column}, NativeCanonical: append([]byte(nil), native...),
	})
	return nil
}

func (i *authoredIndex) projectedSemanticID(source sourcecomment.SourceRef, local string, kind sourcecomment.NodeKind) string {
	if i.projection == nil {
		return local
	}
	for _, expected := range i.projection.Nodes {
		if expected.Downstream.String() == source.String() && expected.Kind == kind {
			return expected.SemanticID
		}
	}
	return local
}

func (i *authoredIndex) factNode(source string) (int, bool) {
	for index := range i.factNodes {
		if i.factNodes[index].Source.String() == source {
			return index, true
		}
	}
	return 0, false
}

func (i *authoredIndex) buildFactGraph() error {
	input := sourcecomment.BuildInput{Nodes: i.factNodes}
	var graph sourcecomment.FactGraph
	var diagnostics []sourcecomment.Diagnostic
	if i.projection == nil {
		graph, diagnostics = sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), input)
	} else {
		input.Projections = append(input.Projections, i.projection.Nodes...)
		input.InheritedFacts = append(input.InheritedFacts, i.projection.InheritedFacts...)
		graph, diagnostics = sourcecomment.ExtendGraph(sourcecomment.StandardRegistry(), i.projection.Upstream, input)
	}
	if len(diagnostics) > 0 {
		return sourceCommentInvalid(diagnostics[0])
	}
	i.facts = graph
	return nil
}

func sourceCommentInvalid(value sourcecomment.Diagnostic) error {
	pointer := fmt.Sprintf(":%d:%d", value.Line, value.Column)
	message := value.Suggestion
	if value.FactID != "" {
		message += " (" + value.FactID + ")"
	}
	return invalid("source_comment_invalid", value.File, pointer, message)
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
		state := &typeState{name: structure.RawName, semanticID: structure.RawName, shape: ValueType{kind: ValueObject}, fieldIndex: map[string]int{}}
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
			externalName, transport, hasTransport, err := externalFieldName(name, member.Tag)
			if err != nil {
				return invalid("field_tags_invalid", source, "", err.Error())
			}
			path := append(append([]string(nil), parent...), externalName)
			valueType, err := projectValueType(member.Type)
			if err != nil {
				return invalid("field_type_invalid", source, "", err.Error())
			}
			origin, hasOrigin, err := parseFieldTags(member.Tag)
			if err != nil {
				return invalid("field_tags_invalid", source, "", err.Error())
			}
			field := &fieldState{ownerType: owner.name, semanticID: owner.name + "." + pathKey(path), path: path, required: valueType.kind != ValueOptional, valueType: valueType, transport: transport, hasTransport: hasTransport, origin: origin, hasOrigin: hasOrigin}
			envelope := canonicalFieldNode{APIVersion: fieldNodeVersion, Kind: "field", OwnerType: owner.name, Path: append([]string(nil), path...), Required: field.required, ValueType: canonicalValueOf(valueType)}
			if hasTransport {
				envelope.Transport = string(transport)
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
