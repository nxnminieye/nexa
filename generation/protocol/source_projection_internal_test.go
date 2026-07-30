package protocol

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestCompileExtendsValidatedEntFactsAndRejectsInheritedProtoDrift(t *testing.T) {
	const plain = `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "proto3";
package sample.v1;
message Record { string name = 1; }
`
	baseline, err := Compile(context.Background(), CompileOptions{ServiceID: "sample", EntryFiles: []string{"sample.proto"}, Resolver: internalResolver{"sample.proto": plain}})
	if err != nil {
		t.Fatal(err)
	}
	message := baseline.state.messages["sample.v1.Record"]
	field := message.fields[0]

	entMessage := mustSourceCommentRef(t, "ent://schema/record.go#sample.v1.Record")
	entField := mustSourceCommentRef(t, "ent://schema/record.go#sample.v1.Record.name")
	label := mustSourceDirective(t, `// @nexa label.zh-CN: "名称"`)
	upstream, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{
		{SemanticID: "sample.v1.Record", Kind: sourcecomment.NodeSchema, Stage: sourcecomment.StageEnt, Source: entMessage, NativeCanonical: []byte("record")},
		{SemanticID: "sample.v1.Record.name", Kind: sourcecomment.NodeField, Stage: sourcecomment.StageEnt, Source: entField, NativeCanonical: []byte("string"), Facts: []sourcecomment.Directive{label}},
	}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	projection := &SourceProjection{Upstream: upstream, Nodes: []sourcecomment.ProjectionExpectation{
		{Downstream: mustSourceCommentRef(t, "proto://sample.proto#sample.v1.Record"), Upstream: entMessage, SemanticID: "sample.v1.Record", Kind: sourcecomment.NodeMessage, ExpectedNativeCanonical: message.canonicalSource},
		{Downstream: mustSourceCommentRef(t, "proto://sample.proto#sample.v1.Record.name"), Upstream: entField, SemanticID: "sample.v1.Record.name", Kind: sourcecomment.NodeProtoField, ExpectedNativeCanonical: field.canonicalSource},
	}}
	projected := `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "proto3";
package sample.v1;
// @nexa $source: "ent://schema/record.go#sample.v1.Record"
message Record {
  // @nexa $source: "ent://schema/record.go#sample.v1.Record.name"
  string name = 1;
}
`
	document, err := Compile(context.Background(), CompileOptions{ServiceID: "sample", EntryFiles: []string{"sample.proto"}, Resolver: internalResolver{"sample.proto": projected}, SourceProjection: projection})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := document.FactGraph().Fact(sourcecomment.FactID{SemanticID: "sample.v1.Record.name", Key: "label.zh-CN"}); !ok {
		t.Fatal("inherited field fact missing")
	}

	drifted := strings.Replace(projected, "string name = 1", "int64 name = 1", 1)
	_, err = Compile(context.Background(), CompileOptions{ServiceID: "sample", EntryFiles: []string{"sample.proto"}, Resolver: internalResolver{"sample.proto": drifted}, SourceProjection: projection})
	owner, ok := err.(*Error)
	if !ok || owner.Reason() != string(sourcecomment.CodeInheritedNodeChanged) {
		t.Fatalf("drift error = %#v", err)
	}
}

type internalResolver map[string]string

func (r internalResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r[path])), nil
}

func mustSourceCommentRef(t *testing.T, raw string) sourcecomment.SourceRef {
	t.Helper()
	value, err := sourcecomment.ParseSourceRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustSourceDirective(t *testing.T, raw string) sourcecomment.Directive {
	t.Helper()
	value, selected, failure := sourcecomment.ParseLine(sourcecomment.Line{Text: raw, CommentPrefix: "//", Location: sourcecomment.Location{File: "schema/record.go", Line: 1, Column: 1}})
	if !selected || failure != nil {
		t.Fatalf("directive = %#v, %v, %v", value, selected, failure)
	}
	return value
}
