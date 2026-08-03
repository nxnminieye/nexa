package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestLoadExtendsValidatedProtoFactsAndRejectsInheritedAPIDrift(t *testing.T) {
	root := t.TempDir()
	const plain = `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
type Record { Name string }
type Request {}
service sample {
  // @nexa auth: "none"
  @handler Get
  get /records (Request) returns (Record)
}
`
	writeProjectionAPI(t, root, plain)
	baseline := authoredIndex{typeFiles: map[string]string{}, routeFiles: map[string]string{}, seenFiles: map[string]bool{}, stack: map[string]bool{}}
	if err := baseline.collect(root, filepath.Join(root, "sample.api")); err != nil {
		t.Fatal(err)
	}
	canonical := map[string][]byte{}
	for _, node := range baseline.factNodes {
		canonical[node.Source.String()] = node.NativeCanonical
	}

	protoMessage := mustHTTPSourceRef(t, "proto://rpc/record.proto#sample.v1.Record")
	protoField := mustHTTPSourceRef(t, "proto://rpc/record.proto#sample.v1.Record.name")
	label := mustHTTPDirective(t, `// @nexa label.zh-CN: "名称"`)
	upstream, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{
		{SemanticID: "sample.v1.Record", Kind: sourcecomment.NodeMessage, Stage: sourcecomment.StageProto, Source: protoMessage, NativeCanonical: []byte("message")},
		{SemanticID: "sample.v1.Record.name", Kind: sourcecomment.NodeProtoField, Stage: sourcecomment.StageProto, Source: protoField, NativeCanonical: []byte("string"), Facts: []sourcecomment.Directive{label}},
	}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	apiMessage := mustHTTPSourceRef(t, "api://sample.api#Record")
	apiField := mustHTTPSourceRef(t, "api://sample.api#Record.name")
	projection := &SourceProjection{Upstream: upstream, Nodes: []sourcecomment.ProjectionExpectation{
		{Downstream: apiMessage, Upstream: protoMessage, SemanticID: "sample.v1.Record", Kind: sourcecomment.NodeAPIType, ExpectedNativeCanonical: canonical[apiMessage.String()]},
		{Downstream: apiField, Upstream: protoField, SemanticID: "sample.v1.Record.name", Kind: sourcecomment.NodeAPIField, ExpectedNativeCanonical: canonical[apiField.String()]},
	}}
	lock, err := sourcecomment.NewProjectionLock(projection.Nodes, projection.InheritedFacts)
	if err != nil {
		t.Fatal(err)
	}
	projection.Lock = &lock
	projected := `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "v1"
info (nexaContractVersion: "nexa.dev/http-convention/v1")
// @nexa $source: "proto://rpc/record.proto#sample.v1.Record"
type Record {
  // @nexa $source: "proto://rpc/record.proto#sample.v1.Record.name"
  Name string
}
type Request {}
service sample {
  // @nexa auth: "none"
  @handler Get
  get /records (Request) returns (Record)
}
`
	writeProjectionAPI(t, root, projected)
	document, err := Load(context.Background(), LoadOptions{RepositoryRoot: root, EntryFile: "sample.api", SourceProjection: projection})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := document.FactGraph().Fact(sourcecomment.FactID{SemanticID: "sample.v1.Record.name", Key: "label.zh-CN"}); !ok {
		t.Fatal("inherited API field fact missing")
	}

	writeProjectionAPI(t, root, strings.Replace(projected, "Name string", "Name int64", 1))
	_, err = Load(context.Background(), LoadOptions{RepositoryRoot: root, EntryFile: "sample.api", SourceProjection: projection})
	owner, ok := err.(*Error)
	if !ok || owner.Reason() != "source_comment_invalid" {
		t.Fatalf("drift error = %#v", err)
	}

	missing := strings.Replace(projected, "  // @nexa $source: \"proto://rpc/record.proto#sample.v1.Record.name\"\n  Name string\n", "", 1)
	writeProjectionAPI(t, root, missing)
	_, err = Load(context.Background(), LoadOptions{RepositoryRoot: root, EntryFile: "sample.api", SourceProjection: &SourceProjection{
		Upstream: upstream, Nodes: projection.Nodes, InheritedFacts: projection.InheritedFacts, Lock: projection.Lock,
	}})
	if err == nil {
		t.Fatal("projection lock accepted a missing inherited API field")
	}
}

func writeProjectionAPI(t *testing.T, root, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "sample.api"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustHTTPSourceRef(t *testing.T, raw string) sourcecomment.SourceRef {
	t.Helper()
	value, err := sourcecomment.ParseSourceRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustHTTPDirective(t *testing.T, raw string) sourcecomment.Directive {
	t.Helper()
	value, selected, failure := sourcecomment.ParseLine(sourcecomment.Line{Text: raw, CommentPrefix: "//", Location: sourcecomment.Location{File: "rpc/record.proto", Line: 1, Column: 1}})
	if !selected || failure != nil {
		t.Fatalf("directive = %#v, %v, %v", value, selected, failure)
	}
	return value
}
