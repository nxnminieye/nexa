package protocol_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestCompileBuildsTypedFactsWithoutChangingProtoIdentity(t *testing.T) {
	document := compileProtocol(t, validCommentProto(""))
	method, ok := document.Method("sample.v1.SampleService.GetSample")
	if !ok {
		t.Fatal("method missing")
	}
	if method.Input() != "sample.v1.GetSampleRequest" || method.Output() != "sample.v1.GetSampleResponse" {
		t.Fatalf("method identity = %q -> %q", method.Input(), method.Output())
	}
	input, ok := document.Message(method.Input())
	if !ok || len(input.Fields()) != 2 || input.Fields()[0].FullName() != "sample.v1.GetSampleRequest.id" || input.Fields()[1].Type().Name() != "int64" {
		t.Fatalf("input fields = %#v, %v", input.Fields(), ok)
	}
	want := map[string]string{
		"auth":        "required",
		"permission":  "sample.read",
		"http.method": "GET",
		"http.path":   "/samples/{id}",
	}
	operationID, err := sourcecomment.CanonicalRPCOperationID(document.ServiceID(), method.FullName())
	if err != nil {
		t.Fatal(err)
	}
	for key, expected := range want {
		fact, exists := document.FactGraph().Fact(sourcecomment.FactID{SemanticID: operationID, Key: key})
		actual, stringValue := fact.Value().String()
		if !exists || !stringValue || actual != expected || fact.FirstSource().String() != "proto://sample.proto#sample.v1.SampleService.GetSample" {
			t.Fatalf("fact %q = %#v, %v", key, fact, exists)
		}
	}
}

func TestCompileRejectsInvalidSourceComments(t *testing.T) {
	tests := []struct {
		name, source string
		code         sourcecomment.Code
	}{
		{name: "missing path pair", source: strings.Replace(validCommentProto(""), "// @nexa http.path: \"/samples/{id}\"\n", "", 1), code: sourcecomment.CodeInvalidValue},
		{name: "invalid auth", source: strings.Replace(validCommentProto(""), `// @nexa auth: "required"`, `// @nexa auth: "optional"`, 1), code: sourcecomment.CodeInvalidValue},
		{name: "unknown fact", source: strings.Replace(validCommentProto(""), `// @nexa permission: "sample.read"`, "// @nexa permission: \"sample.read\"\n  // @nexa transport.rename: \"get\"", 1), code: sourcecomment.CodeUnknownKey},
		{name: "misplaced", source: strings.Replace(validCommentProto(""), "message GetSampleResponse", "// @nexa permission: \"sample.read\"\nmessage GetSampleResponse", 1), code: sourcecomment.CodeInvalidTarget},
		{name: "streaming", source: validCommentProto("stream "), code: sourcecomment.CodeInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := protocol.Compile(context.Background(), compileOptions(test.source))
			owner, ok := err.(*protocol.Error)
			if !ok || owner.Code() != "protocol_source_comment_invalid" || owner.Reason() != string(test.code) {
				t.Fatalf("Compile() error = %#v", err)
			}
		})
	}
}

func TestCompileRejectsSourceDirectiveOnLocalRPC(t *testing.T) {
	source := strings.Replace(validCommentProto(""), `// @nexa auth: "required"`, "// @nexa $source: \"ent://schema/sample.go#Sample.Get\"\n  // @nexa auth: \"required\"", 1)
	_, err := protocol.Compile(context.Background(), compileOptions(source))
	owner, ok := err.(*protocol.Error)
	if !ok || owner.Reason() != string(sourcecomment.CodeSourceMismatch) {
		t.Fatalf("Compile() error = %#v", err)
	}
}

func compileProtocol(t *testing.T, source string) protocol.Document {
	t.Helper()
	document, err := protocol.Compile(context.Background(), compileOptions(source))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return document
}

func compileOptions(source string) protocol.CompileOptions {
	return protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{"sample.proto"}, Resolver: mapResolver{"sample.proto": source}}
}

type mapResolver map[string]string

func (r mapResolver) Open(_ context.Context, path string) (io.ReadCloser, error) {
	source, ok := r[path]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return io.NopCloser(strings.NewReader(source)), nil
}

func validCommentProto(streaming string) string {
	return `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "proto3";
package sample.v1;
message GetSampleRequest { string id = 1; int64 tenant_id = 2; }
message GetSampleResponse { string display_name = 1; }
service SampleService {
  // @nexa auth: "required"
  // @nexa permission: "sample.read"
  // @nexa http.method: "GET"
  // @nexa http.path: "/samples/{id}"
  rpc GetSample(GetSampleRequest) returns (` + streaming + `GetSampleResponse);
}`
}
