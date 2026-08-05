package entityload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"entgo.io/ent/entc/gen"
	"github.com/nxnminieye/nexa/generation/entmixin"
	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

type entCommentSource struct {
	path provenance.DomainSource
	data []byte
}

type entCommentTarget struct {
	target   sourcecomment.Target
	line     int
	native   []byte
	standard *entmixin.FieldMetadata
	// Synthetic targets are native Ent nodes without an explicit Go builder call.
	synthetic bool
}

type entFieldNative struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Optional      bool   `json:"optional"`
	Nillable      bool   `json:"nillable"`
	Immutable     bool   `json:"immutable"`
	HasDefault    bool   `json:"hasDefault"`
	Sensitive     bool   `json:"sensitive"`
	IsIdentity    bool   `json:"isIdentity"`
	IsTenantField bool   `json:"isTenantField"`
}

func parseEntFactGraph(graph *gen.Graph, sources []entCommentSource) (sourcecomment.FactGraph, []sourcecomment.Diagnostic, error) {
	if graph == nil {
		return sourcecomment.FactGraph{}, nil, fmt.Errorf("entity graph is unavailable")
	}
	nodes := make(map[string]*gen.Type, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node == nil || node.Name == "" {
			continue
		}
		if _, duplicate := nodes[node.Name]; duplicate {
			return sourcecomment.FactGraph{}, nil, fmt.Errorf("entity schema %q is duplicated", node.Name)
		}
		nodes[node.Name] = node
	}
	sortedSources := append([]entCommentSource(nil), sources...)
	sort.Slice(sortedSources, func(i, j int) bool { return sortedSources[i].path.String() < sortedSources[j].path.String() })
	var inputs []sourcecomment.NodeInput
	var diagnostics []sourcecomment.Diagnostic
	seenNodes := make(map[string]struct{})
	for _, source := range sortedSources {
		parsedInputs, parsedDiagnostics, err := parseEntCommentSource(source, nodes)
		if err != nil {
			return sourcecomment.FactGraph{}, nil, err
		}
		for _, input := range parsedInputs {
			key := input.Source.String()
			if _, duplicate := seenNodes[key]; duplicate {
				return sourcecomment.FactGraph{}, nil, fmt.Errorf("entity source node %q is duplicated", key)
			}
			seenNodes[key] = struct{}{}
			inputs = append(inputs, input)
		}
		diagnostics = append(diagnostics, parsedDiagnostics...)
	}
	if len(diagnostics) > 0 {
		return sourcecomment.FactGraph{}, diagnostics, nil
	}
	facts, graphDiagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: inputs})
	return facts, graphDiagnostics, nil
}

func parseEntCommentSource(source entCommentSource, schemas map[string]*gen.Type) ([]sourcecomment.NodeInput, []sourcecomment.Diagnostic, error) {
	if source.path.String() == "" || len(source.data) == 0 {
		return nil, nil, fmt.Errorf("entity comment source is invalid")
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, source.path.String(), source.data, parser.ParseComments|parser.AllErrors)
	if err != nil {
		return nil, nil, fmt.Errorf("parse Ent source comments %s: %w", source.path.String(), err)
	}
	targets, err := entTargets(set, file, source.path, schemas)
	if err != nil {
		return nil, nil, err
	}
	byLine := make(map[int]*entCommentTarget, len(targets))
	for index := range targets {
		target := &targets[index]
		if target.synthetic {
			continue
		}
		if previous := byLine[target.line]; previous != nil {
			return nil, nil, fmt.Errorf("Ent source %s has ambiguous semantic nodes on line %d", source.path.String(), target.line)
		}
		byLine[target.line] = target
	}
	lines := make([]sourcecomment.Line, 0)
	for _, group := range file.Comments {
		if !commentGroupHasNexa(group) {
			continue
		}
		var target *sourcecomment.Target
		nextLine := set.Position(group.End()).Line + 1
		if bound := byLine[nextLine]; bound != nil {
			copyTarget := bound.target
			target = &copyTarget
		}
		for _, comment := range group.List {
			position := set.Position(comment.Pos())
			lines = append(lines, sourcecomment.Line{
				Text:          comment.Text,
				CommentPrefix: "//",
				Location: sourcecomment.Location{
					File: source.path.String(), Line: position.Line, Column: position.Column,
				},
				Target: target,
			})
		}
	}
	parsed, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), source.path.String(), lines)
	if len(diagnostics) > 0 {
		return nil, diagnostics, nil
	}
	inputsBySource := make(map[string]*sourcecomment.NodeInput, len(targets))
	for _, target := range targets {
		value := &sourcecomment.NodeInput{
			SemanticID: target.target.SemanticID,
			Kind:       target.target.Kind,
			Stage:      target.target.Stage,
			Source:     target.target.Source,
			Location: sourcecomment.Location{
				File: source.path.String(), Line: target.line, Column: 1,
			},
			NativeCanonical: append([]byte(nil), target.native...),
		}
		if target.standard != nil {
			for _, raw := range target.standard.Directives() {
				directive, selected, failure := sourcecomment.ParseLine(sourcecomment.Line{
					Text:          "// @nexa " + raw,
					CommentPrefix: "//",
					Location:      sourcecomment.Location{File: source.path.String(), Line: target.line, Column: 1},
					Target:        &target.target,
				})
				if failure != nil {
					return nil, []sourcecomment.Diagnostic{*failure}, nil
				}
				if selected {
					value.Facts = append(value.Facts, directive)
				}
			}
		}
		canonical, canonicalErr := httpconvention.CanonicalName(lastSemanticSegment(target.target.SemanticID))
		if canonicalErr == nil {
			value.TransformedIdentifiers = []string{canonical}
		}
		inputsBySource[target.target.Source.String()] = value
	}
	for _, fact := range parsed.Facts() {
		input := inputsBySource[fact.Target().Source.String()]
		if input == nil {
			return nil, nil, fmt.Errorf("Ent fact target %q was not resolved", fact.Target().Source.String())
		}
		input.Facts = append(input.Facts, fact.Directive())
	}
	for _, binding := range parsed.Sources() {
		input := inputsBySource[binding.Target().Source.String()]
		if input == nil {
			return nil, nil, fmt.Errorf("Ent source target %q was not resolved", binding.Target().Source.String())
		}
		sourceRef := binding.Source()
		input.SourceDirective = &sourceRef
		input.SourceLocation = binding.Location()
	}
	result := make([]sourcecomment.NodeInput, 0, len(inputsBySource))
	for _, input := range inputsBySource {
		result = append(result, *input)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Source.String() < result[j].Source.String() })
	return result, nil, nil
}

func entTargets(set *token.FileSet, file *ast.File, source provenance.DomainSource, schemas map[string]*gen.Type) ([]entCommentTarget, error) {
	var targets []entCommentTarget
	var inspectErr error
	declared := make([]struct {
		node *gen.Type
		line int
	}, 0, len(file.Decls))
	explicitFields := make(map[string]bool)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			node := schemas[typeSpec.Name.Name]
			if node == nil {
				continue
			}
			ref, err := entSourceRef(source, node.Name)
			if err != nil {
				return nil, err
			}
			line := set.Position(general.Pos()).Line
			declared = append(declared, struct {
				node *gen.Type
				line int
			}{node: node, line: line})
			targets = append(targets, entCommentTarget{
				target: sourcecomment.Target{SemanticID: node.Name, Kind: sourcecomment.NodeSchema, Stage: sourcecomment.StageEnt, Source: ref},
				line:   line, native: []byte("schema:" + node.Name),
			})
			if node.ID != nil && !node.ID.UserDefined {
				identityRef, identityErr := entSourceRef(source, node.Name+"."+node.ID.Name)
				if identityErr != nil {
					return nil, identityErr
				}
				identityNative, identityErr := canonicalEntField(node.ID, true)
				if identityErr != nil {
					return nil, identityErr
				}
				targets = append(targets, entCommentTarget{
					target:    sourcecomment.Target{SemanticID: node.Name + "." + node.ID.Name, Kind: sourcecomment.NodeField, Stage: sourcecomment.StageEnt, Source: identityRef},
					line:      line,
					native:    identityNative,
					synthetic: true,
				})
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "Fields" || function.Body == nil {
			return true
		}
		owner := receiverName(function.Recv)
		schema := schemas[owner]
		if schema == nil {
			return true
		}
		fields := schemaFields(schema)
		ast.Inspect(function.Body, func(candidate ast.Node) bool {
			call, ok := candidate.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := entFieldCall(call)
			if !ok {
				return true
			}
			field := fields[name]
			if field == nil {
				return true
			}
			explicitFields[schema.Name+"."+field.Name] = true
			standard, standardErr := fieldStandardMetadata(field)
			if standardErr != nil {
				inspectErr = standardErr
				return false
			}
			ref, refErr := entSourceRef(source, schema.Name+"."+field.Name)
			if refErr != nil {
				inspectErr = refErr
				return false
			}
			native, nativeErr := canonicalEntField(field, field == schema.ID)
			if nativeErr != nil {
				inspectErr = nativeErr
				return false
			}
			targets = append(targets, entCommentTarget{
				target: sourcecomment.Target{SemanticID: schema.Name + "." + field.Name, Kind: sourcecomment.NodeField, Stage: sourcecomment.StageEnt, Source: ref},
				line:   set.Position(call.Pos()).Line, native: native, standard: standard,
			})
			return false
		})
		return inspectErr == nil
	})
	if inspectErr != nil {
		return nil, inspectErr
	}
	for _, item := range declared {
		for _, field := range item.node.Fields {
			if field == nil {
				continue
			}
			semanticID := item.node.Name + "." + field.Name
			if explicitFields[semanticID] {
				continue
			}
			standard, standardErr := fieldStandardMetadata(field)
			if standardErr != nil {
				return nil, standardErr
			}
			if standard == nil {
				continue
			}
			ref, refErr := entSourceRef(source, semanticID)
			if refErr != nil {
				return nil, refErr
			}
			native, nativeErr := canonicalEntField(field, false)
			if nativeErr != nil {
				return nil, nativeErr
			}
			targets = append(targets, entCommentTarget{
				target: sourcecomment.Target{SemanticID: semanticID, Kind: sourcecomment.NodeField, Stage: sourcecomment.StageEnt, Source: ref},
				line:   item.line, native: native, standard: standard, synthetic: true,
			})
		}
	}
	return targets, nil
}

func commentGroupHasNexa(group *ast.CommentGroup) bool {
	for _, comment := range group.List {
		if strings.HasPrefix(strings.TrimSpace(comment.Text), "// @nexa") {
			return true
		}
	}
	return false
}

func receiverName(receivers *ast.FieldList) string {
	if receivers == nil || len(receivers.List) != 1 {
		return ""
	}
	expression := receivers.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	identifier, _ := expression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func entFieldCall(call *ast.CallExpr) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return "", false
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok || owner.Name != "field" {
		return "", false
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(literal.Value)
	return name, err == nil && name != ""
}

func schemaFields(node *gen.Type) map[string]*gen.Field {
	result := make(map[string]*gen.Field, len(node.Fields)+1)
	for _, field := range node.Fields {
		if field != nil {
			result[field.Name] = field
		}
	}
	if node.ID != nil {
		result[node.ID.Name] = node.ID
	}
	return result
}

func entSourceRef(source provenance.DomainSource, symbol string) (sourcecomment.SourceRef, error) {
	return sourcecomment.ParseSourceRef("ent://" + source.String() + "#" + symbol)
}

func fieldStandardMetadata(field *gen.Field) (*entmixin.FieldMetadata, error) {
	if field == nil || field.Annotations == nil {
		return nil, nil
	}
	value, ok := field.Annotations[entmixin.FieldAnnotationName]
	if !ok {
		return nil, nil
	}
	metadata, err := entmixin.DecodeFieldAnnotation(value)
	if err != nil {
		return nil, err
	}
	return &metadata, nil
}

func canonicalEntField(field *gen.Field, identity bool) ([]byte, error) {
	typeID, ok := scalarType(field.Type)
	if !ok {
		return nil, fmt.Errorf("field %s type is unsupported", field.Name)
	}
	value := entFieldNative{
		Name: field.Name, Type: typeID, Optional: field.Optional, Nillable: field.Nillable,
		Immutable: field.Immutable, HasDefault: field.Default, Sensitive: field.Sensitive(), IsIdentity: identity,
	}
	standard, err := fieldStandardMetadata(field)
	if err != nil {
		return nil, err
	}
	if standard != nil {
		value.IsTenantField = standard.Tenant
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return bytes.TrimSpace(encoded), nil
}

func lastSemanticSegment(value string) string {
	if index := strings.LastIndexByte(value, '.'); index >= 0 {
		return value[index+1:]
	}
	return value
}
