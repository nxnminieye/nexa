package protocol_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
)

func TestOptionsProtoIsEmbeddedAndCompilesAsAnImport(t *testing.T) {
	if len(protocol.OptionsProto()) == 0 {
		t.Fatal("OptionsProto() returned no bytes")
	}
	resolver := resolverFunc(func(_ context.Context, path string) (io.ReadCloser, error) {
		if path != "service.proto" {
			return nil, io.ErrUnexpectedEOF
		}
		return io.NopCloser(strings.NewReader(`syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message Request {}
message Response {}
service Sample { rpc Get(Request) returns (Response) { option (nexa.protocol.v1.http_proxy) = { operation_id: "sample.get" method: GET path: "/sample" auth: { mode: NONE } }; } }
`)), nil
	})
	document, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{"service.proto"}, Resolver: resolver})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	method, ok := document.Method("sample.v1.Sample.Get")
	if !ok {
		t.Fatal("linked method missing")
	}
	if _, ok := method.HTTPProxy(); !ok {
		t.Fatal("typed HTTP proxy option was not retained")
	}
}

type resolverFunc func(context.Context, string) (io.ReadCloser, error)

func (f resolverFunc) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	return f(ctx, path)
}
