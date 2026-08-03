package httpapi_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/api"
	"github.com/nxnminieye/nexa/generation/httpapi"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func TestGeneratedDocumentRendersOnlyDerivedConventionTags(t *testing.T) {
	method := generatedSource(t, "rpc/sample.proto", "method:sample.Get", "method")
	message := generatedSource(t, "rpc/sample.proto", "message:sample.GetRequest", "message")
	owner, err := httpapi.NewGeneratedProvenance([]provenance.Source{method, message})
	if err != nil {
		t.Fatal(err)
	}
	firstSource, err := sourcecomment.ParseSourceRef("proto://rpc/sample.proto#sample.Get")
	if err != nil {
		t.Fatal(err)
	}
	document, err := httpapi.NewGeneratedDocument(httpapi.GeneratedDocumentSpec{
		Types: []httpapi.GeneratedTypeSpec{
			{SemanticID: "sample.GetRequest", Name: "GetRequest", FirstSource: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetRequest"), Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: owner, Fields: []httpapi.GeneratedFieldSpec{{SemanticID: "sample.GetRequest.id", FirstSource: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetRequest.id"), Path: []string{"ID"}, Required: true, ValueType: httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}, Provenance: owner}}},
			{SemanticID: "sample.GetResponse", Name: "GetResponse", FirstSource: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetResponse"), Shape: httpapi.ValueTypeSpec{Kind: httpapi.ValueObject}, Provenance: owner, Fields: []httpapi.GeneratedFieldSpec{{SemanticID: "sample.GetResponse.displayName", FirstSource: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetResponse.displayName"), Path: []string{"DisplayName"}, Required: true, ValueType: httpapi.ValueTypeSpec{Kind: httpapi.ValueScalar, Name: "string"}, Provenance: owner}}},
		},
		Operations: []httpapi.GeneratedOperationSpec{{ID: "sample.sample.Get", Method: api.MethodGET, Path: "/samples/{id}", RequestType: "GetRequest", ResponseType: "GetResponse", Auth: httpapi.AuthSpec{Mode: api.AuthRequired}, Permission: "sample.read", Provenance: owner, FirstSource: firstSource}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := httpapi.ValidateConvention(document); err != nil {
		t.Fatalf("ValidateConvention: %v", err)
	}
	rendered, err := httpapi.RenderGenerated(document)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	if !strings.Contains(text, "`path:\"id\"`") || !strings.Contains(text, "`json:\"displayName\"`") {
		t.Fatalf("generated tags are not deterministic:\n%s", text)
	}
	if strings.Contains(text, "nexaOperationId") || strings.Contains(text, "nexaAuthMode") || strings.Contains(text, "nexaPermission") || strings.Contains(text, "Credential") || strings.Contains(text, "ErrorProjection") || strings.Contains(text, "Capability") {
		t.Fatalf("legacy metadata leaked:\n%s", text)
	}
	for _, source := range []string{
		`// @nexa $source: "proto://rpc/sample.proto#sample.GetRequest"`,
		`// @nexa $source: "proto://rpc/sample.proto#sample.GetRequest.id"`,
		`// @nexa $source: "proto://rpc/sample.proto#sample.Get"`,
	} {
		if !strings.Contains(text, source) {
			t.Fatalf("generated source identity %q missing:\n%s", source, text)
		}
	}
	if err := httpapi.VerifyRenderedGenerated("sample.generated.api", rendered, document); err != nil {
		t.Fatal(err)
	}
	upstream, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{
		{SemanticID: "sample.GetRequest", Kind: sourcecomment.NodeMessage, Stage: sourcecomment.StageProto, Source: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetRequest"), NativeCanonical: []byte("request")},
		{SemanticID: "sample.GetRequest.id", Kind: sourcecomment.NodeProtoField, Stage: sourcecomment.StageProto, Source: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetRequest.id"), NativeCanonical: []byte("id")},
		{SemanticID: "sample.GetResponse", Kind: sourcecomment.NodeMessage, Stage: sourcecomment.StageProto, Source: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetResponse"), NativeCanonical: []byte("response")},
		{SemanticID: "sample.GetResponse.displayName", Kind: sourcecomment.NodeProtoField, Stage: sourcecomment.StageProto, Source: mustSourceCommentRef(t, "proto://rpc/sample.proto#sample.GetResponse.displayName"), NativeCanonical: []byte("displayName")},
		{SemanticID: "sample.sample.Get", Kind: sourcecomment.NodeRPC, Stage: sourcecomment.StageProto, Source: firstSource, NativeCanonical: []byte("get"), Facts: []sourcecomment.Directive{
			mustDirective(t, `// @nexa auth: "required"`),
			mustDirective(t, `// @nexa permission: "sample.read"`),
			mustDirective(t, `// @nexa http.method: "GET"`),
			mustDirective(t, `// @nexa http.path: "/samples/{id}"`),
		}},
	}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	projection, err := httpapi.ProjectionForRenderedGenerated(document, "sample.generated.api", rendered, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Lock == nil {
		t.Fatal("generated HTTP API projection has no source projection lock")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.generated.api"), rendered, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := httpapi.Load(context.Background(), httpapi.LoadOptions{RepositoryRoot: root, EntryFile: "sample.generated.api", SourceProjection: &projection})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Operation("sample.sample.Get"); !ok {
		t.Fatal("projected generated operation identity missing")
	}
	changed := bytes.Replace(rendered, []byte("/samples/:id"), []byte("/other/:id"), 1)
	if err := httpapi.VerifyRenderedGenerated("sample.generated.api", changed, document); err == nil {
		t.Fatal("semantic drift accepted")
	}
}

func mustDirective(t *testing.T, raw string) sourcecomment.Directive {
	t.Helper()
	value, selected, failure := sourcecomment.ParseLine(sourcecomment.Line{Text: raw, CommentPrefix: "//", Location: sourcecomment.Location{File: "rpc/sample.proto", Line: 1, Column: 1}})
	if !selected || failure != nil {
		t.Fatalf("directive = %#v, %v, %v", value, selected, failure)
	}
	return value
}

func mustSourceCommentRef(t *testing.T, raw string) sourcecomment.SourceRef {
	t.Helper()
	value, err := sourcecomment.ParseSourceRef(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func generatedSource(t *testing.T, path, fragment, payload string) provenance.Source {
	t.Helper()
	ref, err := provenance.RepositoryRef(path, fragment)
	if err != nil {
		t.Fatal(err)
	}
	return provenance.Source{Ref: ref, Digest: provenance.SHA256([]byte(payload))}
}
