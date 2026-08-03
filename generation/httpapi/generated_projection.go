package httpapi

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/nxnminieye/nexa/generation/httpconvention"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	goctlast "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/ast"
	goctlparser "github.com/zeromicro/go-zero/tools/goctl/pkg/parser/api/parser"
)

// ProjectionForRenderedGenerated derives semantic projection expectations from
// a generated document and its rendered .api bytes. The caller supplies the
// validated upstream graph; no author-provided field mapping is accepted.
func ProjectionForRenderedGenerated(document Document, source string, rendered []byte, upstream sourcecomment.FactGraph) (SourceProjection, error) {
	if document.state == nil || !documentHasOnly(document, NodeFactGenerated) || source == "" {
		return SourceProjection{}, invalid("projection_input_invalid", source, "", "generated document and source path are required")
	}
	native, err := renderedProjectionNative(source, rendered)
	if err != nil {
		return SourceProjection{}, err
	}
	projection := SourceProjection{Upstream: upstream}
	appendNode := func(local string, semanticID string, kind sourcecomment.NodeKind, firstSource sourcecomment.SourceRef) error {
		downstream, parseErr := sourcecomment.ParseSourceRef("api://" + source + "#" + local)
		if parseErr != nil {
			return invalid("projection_source_invalid", source, "", parseErr.Error())
		}
		value, ok := native[downstream.String()]
		if !ok || value.kind != kind {
			return invalid("projection_node_missing", source, "", "rendered generated semantic node is missing")
		}
		projection.Nodes = append(projection.Nodes, sourcecomment.ProjectionExpectation{
			Downstream: downstream, Upstream: firstSource, SemanticID: semanticID, Kind: kind,
			ExpectedNativeCanonical: append([]byte(nil), value.native...),
		})
		return nil
	}
	for _, item := range document.state.types {
		if err := appendNode(item.name, item.semanticID, sourcecomment.NodeAPIType, item.firstSource); err != nil {
			return SourceProjection{}, err
		}
		for _, field := range item.fields {
			if len(field.path) != 1 {
				return SourceProjection{}, invalid("projection_field_invalid", source, "", "generated field path is not canonical")
			}
			name, nameErr := httpconvention.CanonicalName(field.path[0])
			if nameErr != nil {
				return SourceProjection{}, invalid("projection_field_invalid", source, "", nameErr.Error())
			}
			if err := appendNode(item.name+"."+name, field.semanticID, sourcecomment.NodeAPIField, field.firstSource); err != nil {
				return SourceProjection{}, err
			}
		}
	}
	for _, operation := range document.state.operations {
		handler := "generated" + hex.EncodeToString([]byte(operation.id))
		local, localErr := sourcecomment.CanonicalAPIOperationID("", handler)
		if localErr != nil {
			return SourceProjection{}, invalid("projection_operation_invalid", source, "", localErr.Error())
		}
		if err := appendNode(local, operation.id, sourcecomment.NodeAPIOperation, operation.firstSource); err != nil {
			return SourceProjection{}, err
		}
	}
	lock, err := sourcecomment.NewProjectionLock(projection.Nodes, projection.InheritedFacts)
	if err != nil {
		return SourceProjection{}, invalid("projection_lock_invalid", source, "", err.Error())
	}
	projection.Lock = &lock
	return projection, nil
}

type renderedProjectionNode struct {
	kind   sourcecomment.NodeKind
	native []byte
}

func renderedProjectionNative(source string, rendered []byte) (map[string]renderedProjectionNode, error) {
	parser := goctlparser.New(source, rendered)
	document := parser.Parse()
	if err := parser.CheckErrors(); err != nil {
		return nil, invalid("projection_parser_error", source, "", err.Error())
	}
	result := map[string]renderedProjectionNode{}
	add := func(local string, kind sourcecomment.NodeKind, native []byte) error {
		ref, err := sourcecomment.ParseSourceRef("api://" + source + "#" + local)
		if err != nil {
			return err
		}
		if _, duplicate := result[ref.String()]; duplicate {
			return fmt.Errorf("rendered semantic node %s is duplicated", local)
		}
		result[ref.String()] = renderedProjectionNode{kind: kind, native: append([]byte(nil), native...)}
		return nil
	}
	for _, statement := range document.Stmts {
		switch value := statement.(type) {
		case *goctlast.TypeLiteralStmt:
			if err := addRenderedTypeProjection(value.Expr, add); err != nil {
				return nil, invalid("projection_node_invalid", source, "", err.Error())
			}
		case *goctlast.TypeGroupStmt:
			for _, expression := range value.ExprList {
				if err := addRenderedTypeProjection(expression, add); err != nil {
					return nil, invalid("projection_node_invalid", source, "", err.Error())
				}
			}
		case *goctlast.ServiceStmt:
			properties, prefix, err := parseServerProperties(value.AtServerStmt, source)
			if err != nil {
				return nil, err
			}
			for _, route := range value.Routes {
				pathValue, pathErr := normalizeRoutePath(prefix, route.Route.Path.Format(""))
				if pathErr != nil {
					return nil, pathErr
				}
				local, identityErr := sourcecomment.CanonicalAPIOperationID(properties["group"], route.AtHandler.Name.RawText())
				if identityErr != nil {
					return nil, identityErr
				}
				native := []byte(strings.Join([]string{properties["group"], route.AtHandler.Name.RawText(), strings.ToUpper(route.Route.Method.RawText()), pathValue}, "\x00"))
				if err := add(local, sourcecomment.NodeAPIOperation, native); err != nil {
					return nil, err
				}
			}
		}
	}
	return result, nil
}

func addRenderedTypeProjection(expression *goctlast.TypeExpr, add func(string, sourcecomment.NodeKind, []byte) error) error {
	name := expression.Name.RawText()
	if err := add(name, sourcecomment.NodeAPIType, canonicalAPITypeNative(expression)); err != nil {
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
		tag := ""
		if element.Tag != nil {
			tag = element.Tag.RawText()
		}
		external, _, _, err := externalFieldName(element.Name[0].RawText(), tag)
		if err != nil {
			return err
		}
		if err := add(name+"."+external, sourcecomment.NodeAPIField, canonicalAPIElementNative(element)); err != nil {
			return err
		}
	}
	return nil
}
