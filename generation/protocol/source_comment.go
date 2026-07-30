package protocol

import (
	"fmt"
	"strings"

	"github.com/bufbuild/protocompile/linker"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func buildProtoFactGraph(paths []string, files map[string]linker.File, resolver *resolverAdapter, state *documentState, projection *SourceProjection) (sourcecomment.FactGraph, error) {
	nodes := make([]sourcecomment.NodeInput, 0, len(state.messages)+len(state.methods))
	byFile := make(map[string][]int, len(paths))
	appendNode := func(filePath, sourceSymbol, semanticID string, kind sourcecomment.NodeKind, location locationState, native []byte) error {
		source, err := sourcecomment.ParseSourceRef("proto://" + filePath + "#" + sourceSymbol)
		if err != nil {
			return protocolError("protocol_source_comment_invalid", string(sourcecomment.CodeInvalidTarget), filePath, "", "Proto semantic source reference is invalid")
		}
		byFile[filePath] = append(byFile[filePath], len(nodes))
		nodes = append(nodes, sourcecomment.NodeInput{
			SemanticID:      semanticID,
			Kind:            kind,
			Stage:           sourcecomment.StageProto,
			Source:          source,
			Location:        sourcecomment.Location{File: filePath, Line: location.line, Column: location.column},
			NativeCanonical: append([]byte(nil), native...),
		})
		return nil
	}
	for _, file := range state.files {
		for _, message := range file.messages {
			if err := appendNode(file.path, message.fullName, message.fullName, sourcecomment.NodeMessage, message.location, message.canonicalSource); err != nil {
				return sourcecomment.FactGraph{}, err
			}
			for _, field := range message.fields {
				if err := appendNode(file.path, field.fullName, field.fullName, sourcecomment.NodeProtoField, field.location, field.canonicalSource); err != nil {
					return sourcecomment.FactGraph{}, err
				}
			}
		}
		for _, service := range file.services {
			for _, method := range service.methods {
				operationID, err := sourcecomment.CanonicalRPCOperationID(state.serviceID, method.fullName)
				if err != nil {
					return sourcecomment.FactGraph{}, protocolError("protocol_source_comment_invalid", string(sourcecomment.CodeSemanticCollision), file.path, "", err.Error())
				}
				if err := appendNode(file.path, method.fullName, operationID, sourcecomment.NodeRPC, method.location, method.canonicalSource); err != nil {
					return sourcecomment.FactGraph{}, err
				}
			}
		}
	}

	for _, filePath := range paths {
		if files[filePath] == nil {
			return sourcecomment.FactGraph{}, protocolError("protocol_compile_failed", "descriptor_missing", filePath, "", "Compiled Proto descriptor is missing")
		}
		raw := resolver.source(filePath)
		if raw == nil {
			return sourcecomment.FactGraph{}, protocolError("protocol_resolver_failed", "source_missing", filePath, "", "Compiled Proto source is unavailable for source-comment validation")
		}
		textLines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		targets := make(map[int]*sourcecomment.Target)
		for _, index := range byFile[filePath] {
			node := &nodes[index]
			descriptor := files[filePath].FindDescriptorByName(protoreflect.FullName(node.Source.Symbol()))
			if descriptor == nil || files[filePath].SourceLocations().ByDescriptor(descriptor).LeadingComments == "" {
				continue
			}
			target := sourcecomment.Target{SemanticID: node.SemanticID, Kind: node.Kind, Stage: node.Stage, Source: node.Source}
			for line := node.Location.Line - 1; line >= 1; line-- {
				trimmed := strings.TrimSpace(textLines[line-1])
				if !strings.HasPrefix(trimmed, "//") {
					break
				}
				if previous := targets[line]; previous != nil && previous.Source.String() != target.Source.String() {
					return sourcecomment.FactGraph{}, protocolError("protocol_source_comment_invalid", string(sourcecomment.CodeSemanticCollision), filePath, "", "Proto leading comment is attached to multiple semantic nodes")
				}
				copyTarget := target
				targets[line] = &copyTarget
			}
		}
		lines := make([]sourcecomment.Line, len(textLines))
		for index, text := range textLines {
			lines[index] = sourcecomment.Line{Text: text, CommentPrefix: "//", Location: sourcecomment.Location{File: filePath, Line: index + 1, Column: 1}, Target: targets[index+1]}
		}
		parsed, diagnostics := sourcecomment.ParseFile(sourcecomment.StandardRegistry(), filePath, lines)
		if len(diagnostics) > 0 {
			return sourcecomment.FactGraph{}, sourceCommentError(diagnostics[0])
		}
		for _, fact := range parsed.Facts() {
			index, ok := findProtoNode(nodes, fact.Target().Source.String())
			if !ok {
				return sourcecomment.FactGraph{}, protocolError("protocol_source_comment_invalid", string(sourcecomment.CodeInvalidTarget), filePath, "", "Proto source-comment target is missing")
			}
			nodes[index].Facts = append(nodes[index].Facts, fact.Directive())
		}
		for _, inherited := range parsed.Sources() {
			index, ok := findProtoNode(nodes, inherited.Target().Source.String())
			if !ok {
				return sourcecomment.FactGraph{}, protocolError("protocol_source_comment_invalid", string(sourcecomment.CodeInvalidTarget), filePath, "", "Proto source-comment target is missing")
			}
			source := inherited.Source()
			nodes[index].SourceDirective = &source
			nodes[index].SourceLocation = inherited.Location()
		}
	}

	input := sourcecomment.BuildInput{Nodes: nodes}
	var graph sourcecomment.FactGraph
	var diagnostics []sourcecomment.Diagnostic
	if projection == nil {
		graph, diagnostics = sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), input)
	} else {
		input.Projections = append(input.Projections, projection.Nodes...)
		input.InheritedFacts = append(input.InheritedFacts, projection.InheritedFacts...)
		graph, diagnostics = sourcecomment.ExtendGraph(sourcecomment.StandardRegistry(), projection.Upstream, input)
	}
	if len(diagnostics) > 0 {
		return sourcecomment.FactGraph{}, sourceCommentError(diagnostics[0])
	}
	if projection != nil && projection.Lock != nil {
		if err := projection.Lock.ValidateFactGraph(graph); err != nil {
			return sourcecomment.FactGraph{}, protocolError("protocol_source_comment_invalid", string(sourcecomment.CodeInheritedNodeChanged), "", "", err.Error())
		}
	}
	for _, method := range state.methods {
		if !method.clientStreaming && !method.serverStreaming {
			continue
		}
		operationID, err := sourcecomment.CanonicalRPCOperationID(state.serviceID, method.fullName)
		if err != nil {
			return sourcecomment.FactGraph{}, protocolError("protocol_source_comment_invalid", string(sourcecomment.CodeSemanticCollision), method.filePath, "", err.Error())
		}
		if _, ok := graph.Fact(sourcecomment.FactID{SemanticID: operationID, Key: "http.method"}); ok {
			return sourcecomment.FactGraph{}, &Error{code: "protocol_source_comment_invalid", reason: string(sourcecomment.CodeInvalidValue), source: method.filePath, line: method.location.line, column: method.location.column, message: "Streaming RPC cannot declare HTTP projection facts"}
		}
	}
	return graph, nil
}

func findProtoNode(nodes []sourcecomment.NodeInput, source string) (int, bool) {
	for index := range nodes {
		if nodes[index].Source.String() == source {
			return index, true
		}
	}
	return 0, false
}

func sourceCommentError(value sourcecomment.Diagnostic) *Error {
	message := value.Suggestion
	if value.FactID != "" {
		message = fmt.Sprintf("%s (%s)", message, value.FactID)
	}
	return &Error{code: "protocol_source_comment_invalid", reason: string(value.Code), source: value.File, line: value.Line, column: value.Column, message: message}
}
