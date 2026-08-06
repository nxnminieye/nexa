package protocol_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestMergeCombinesValidatedProtocolDocumentsAndFactGraphs(t *testing.T) {
	left := compileProtocolFile(t, "health.proto", `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "proto3";
package sample.v1;
message HealthRequest {}
message HealthResponse {}
service SampleService {
  // @nexa description.en-US: "Check service health."
  rpc Health(HealthRequest) returns (HealthResponse);
}`)
	right := compileProtocolFile(t, "runtime.proto", `// @nexa $contract: "nexa.dev/source-comment/v1"
syntax = "proto3";
package sample.v1;
message VersionRequest {}
message VersionResponse {}
service SampleService {
  // @nexa description.en-US: "Read the running version."
  rpc Version(VersionRequest) returns (VersionResponse);
}`)

	merged, err := protocol.Merge(left, right)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := merged.Service("sample.v1.SampleService")
	if !ok || len(service.Methods()) != 2 || service.Methods()[0].FullName() != "sample.v1.SampleService.Health" || service.Methods()[1].FullName() != "sample.v1.SampleService.Version" {
		t.Fatalf("merged service = %#v, %v", service.Methods(), ok)
	}
	if files := merged.Files(); len(files) != 2 || files[0].Path() != "health.proto" || files[1].Path() != "runtime.proto" {
		t.Fatalf("merged files = %#v", files)
	}
	for _, method := range []string{"Health", "Version"} {
		operationID, err := sourcecomment.CanonicalRPCOperationID("sample", "sample.v1.SampleService."+method)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := merged.FactGraph().Fact(sourcecomment.FactID{SemanticID: operationID, Key: "description.en-US"}); !ok {
			t.Fatalf("merged FactGraph is missing %s", operationID)
		}
	}
	reversed, err := protocol.Merge(right, left)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := protocol.CanonicalJSON(merged)
	if err != nil {
		t.Fatal(err)
	}
	reversedCanonical, err := protocol.CanonicalJSON(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, reversedCanonical) {
		t.Fatal("merged ProtocolIR depends on input order")
	}
}

func TestMergeDeduplicatesTheSameImportedSource(t *testing.T) {
	resolver := mapResolver{
		"common.proto":  `syntax = "proto3"; package sample.v1; message Empty {}`,
		"health.proto":  `syntax = "proto3"; package sample.v1; import "common.proto"; message HealthRequest {} service SampleService { rpc Health(HealthRequest) returns (Empty); }`,
		"runtime.proto": `syntax = "proto3"; package sample.v1; import "common.proto"; message VersionRequest {} service SampleService { rpc Version(VersionRequest) returns (Empty); }`,
	}
	compile := func(entry string) protocol.Document {
		t.Helper()
		document, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{entry}, Resolver: resolver})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	merged, err := protocol.Merge(compile("health.proto"), compile("runtime.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if files := merged.Files(); len(files) != 3 || files[0].Path() != "common.proto" {
		t.Fatalf("merged files = %#v", files)
	}
}

func TestMergeRejectsAProtocolIdentityDeclaredByDifferentSources(t *testing.T) {
	left := compileProtocolFile(t, "left.proto", `syntax = "proto3"; package sample.v1; message Request {} message Response {} service SampleService { rpc Get(Request) returns (Response); }`)
	right := compileProtocolFile(t, "right.proto", `syntax = "proto3"; package sample.v1; message OtherRequest {} message OtherResponse {} service SampleService { rpc Get(OtherRequest) returns (OtherResponse); }`)

	_, err := protocol.Merge(left, right)
	owner, ok := err.(*protocol.Error)
	if !ok || owner.Code() != "protocol_merge_failed" || owner.Reason() != "method_conflict" || owner.Source() != "right.proto" {
		t.Fatalf("Merge() error = %#v", err)
	}
}

func compileProtocolFile(t *testing.T, path, source string) protocol.Document {
	t.Helper()
	document, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{path}, Resolver: mapResolver{path: source}})
	if err != nil {
		t.Fatalf("Compile(%s) error = %v", path, err)
	}
	return document
}
