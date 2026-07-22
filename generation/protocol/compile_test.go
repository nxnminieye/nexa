package protocol_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
)

type concurrentFailureResolver struct {
	mu      sync.Mutex
	arrived int
	release chan struct{}
}

func (r *concurrentFailureResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	r.mu.Lock()
	r.arrived++
	if r.arrived == 2 {
		close(r.release)
	}
	release := r.release
	r.mu.Unlock()
	select {
	case <-release:
		return nil, errors.New("cannot open " + path)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestCompileSelectsCanonicalResolverFailureAcrossConcurrentEntries(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		resolver := &concurrentFailureResolver{release: make(chan struct{})}
		_, err := protocol.Compile(context.Background(), protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{"z.proto", "a.proto"}, Resolver: resolver})
		owner, ok := err.(*protocol.Error)
		if !ok || owner.Code() != "protocol_resolver_failed" || owner.Source() != "a.proto" {
			t.Fatalf("iteration %d: Compile() error = %#v", iteration, err)
		}
	}
}

type memoryResolver struct {
	mu      sync.Mutex
	sources map[string]string
	opens   map[string]int
}

func (r *memoryResolver) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opens[path]++
	value, ok := r.sources[path]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return io.NopCloser(strings.NewReader(value)), nil
}

func TestCompileLinksImportsAndPreservesProtoSemantics(t *testing.T) {
	resolver := &memoryResolver{sources: map[string]string{
		"sample/v1/common.proto": `syntax = "proto3";
package sample.v1;
message Common { string trace_id = 1; }
enum State { STATE_UNSPECIFIED = 0; STATE_READY = 1; }
`,
		"sample/v1/service.proto": `syntax = "proto3";
package sample.v1;
import "sample/v1/common.proto";
message GetRequest {
  string id = 1;
  optional string note = 2;
  map<string, Common> metadata = 3;
  oneof selector { int64 sequence = 4; string alias = 5; }
}
message GetResponse { State state = 1; Common common = 2; }
service SampleService {
  rpc Get(GetRequest) returns (GetResponse);
  rpc Watch(stream GetRequest) returns (stream GetResponse);
}
`,
	}, opens: map[string]int{}}

	document, err := protocol.Compile(context.Background(), protocol.CompileOptions{
		ServiceID:  "sample",
		EntryFiles: []string{"sample/v1/service.proto"},
		Resolver:   resolver,
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	files := document.Files()
	if len(files) != 2 || files[0].Path() != "sample/v1/common.proto" || files[1].Path() != "sample/v1/service.proto" {
		t.Fatalf("Files() = %#v", filePaths(files))
	}
	request, ok := document.Message("sample.v1.GetRequest")
	if !ok || request.FullName() != "sample.v1.GetRequest" || request.FilePath() != "sample/v1/service.proto" {
		t.Fatalf("Message() = %#v, %v", request, ok)
	}
	fields := request.Fields()
	if len(fields) != 5 {
		t.Fatalf("Fields() count = %d", len(fields))
	}
	assertField(t, fields[0], 1, "id", "id", protocol.CardinalitySingular, protocol.PresenceImplicit, protocol.TypeScalar, "string", "")
	assertField(t, fields[1], 2, "note", "note", protocol.CardinalitySingular, protocol.PresenceExplicit, protocol.TypeScalar, "string", "")
	assertField(t, fields[2], 3, "metadata", "metadata", protocol.CardinalityRepeated, protocol.PresenceMap, protocol.TypeMap, "", "")
	mapType := fields[2].Type()
	if mapType.Key().Kind() != protocol.TypeScalar || mapType.Key().Name() != "string" || mapType.Value().Kind() != protocol.TypeMessage || mapType.Value().Name() != "sample.v1.Common" {
		t.Fatalf("map Type() = %#v", mapType)
	}
	assertField(t, fields[3], 4, "sequence", "sequence", protocol.CardinalitySingular, protocol.PresenceOneof, protocol.TypeScalar, "int64", "selector")
	assertField(t, fields[4], 5, "alias", "alias", protocol.CardinalitySingular, protocol.PresenceOneof, protocol.TypeScalar, "string", "selector")

	state, ok := document.Enum("sample.v1.State")
	if !ok || len(state.Values()) != 2 || state.Values()[1].Name() != "STATE_READY" || state.Values()[1].Number() != 1 {
		t.Fatalf("Enum() = %#v, %v", state, ok)
	}
	service, ok := document.Service("sample.v1.SampleService")
	if !ok || len(service.Methods()) != 2 {
		t.Fatalf("Service() = %#v, %v", service, ok)
	}
	get, ok := document.Method("sample.v1.SampleService.Get")
	if !ok || get.Input() != "sample.v1.GetRequest" || get.Output() != "sample.v1.GetResponse" || get.ClientStreaming() || get.ServerStreaming() {
		t.Fatalf("Get method = %#v, %v", get, ok)
	}
	watch, _ := document.Method("sample.v1.SampleService.Watch")
	if !watch.ClientStreaming() || !watch.ServerStreaming() {
		t.Fatalf("Watch streaming flags = %v, %v", watch.ClientStreaming(), watch.ServerStreaming())
	}
	if get.Location().Line() == 0 || get.Location().Column() == 0 || get.Location().File() != "sample/v1/service.proto" {
		t.Fatalf("Get Location() = %#v", get.Location())
	}

	copyFields := request.Fields()
	copyFields[0] = protocol.Field{}
	if again, _ := document.Message("sample.v1.GetRequest"); again.Fields()[0].Name() != "id" {
		t.Fatal("Message.Fields() aliases immutable document storage")
	}
}

func TestCompileRejectsInvalidInputsAndProjectsContext(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		options protocol.CompileOptions
		code    string
		reason  string
	}{
		{name: "service", ctx: context.Background(), options: protocol.CompileOptions{EntryFiles: []string{"a.proto"}, Resolver: &memoryResolver{}}, code: "protocol_input_invalid", reason: "service_id_invalid"},
		{name: "entry", ctx: context.Background(), options: protocol.CompileOptions{ServiceID: "sample", Resolver: &memoryResolver{}}, code: "protocol_input_invalid", reason: "entry_files_missing"},
		{name: "resolver", ctx: context.Background(), options: protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{"a.proto"}}, code: "protocol_input_invalid", reason: "resolver_missing"},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name    string
		ctx     context.Context
		options protocol.CompileOptions
		code    string
		reason  string
	}{name: "context", ctx: cancelled, options: protocol.CompileOptions{ServiceID: "sample", EntryFiles: []string{"a.proto"}, Resolver: &memoryResolver{}}, code: "protocol_compile_cancelled", reason: "context_cancelled"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := protocol.Compile(test.ctx, test.options)
			owner, ok := err.(*protocol.Error)
			if !ok || owner.Code() != test.code || owner.Reason() != test.reason {
				t.Fatalf("Compile() error = %#v", err)
			}
		})
	}
}

func assertField(t *testing.T, field protocol.Field, number int, name, jsonName string, cardinality protocol.Cardinality, presence protocol.Presence, kind protocol.TypeKind, typeName, oneof string) {
	t.Helper()
	if field.Number() != number || field.Name() != name || field.JSONName() != jsonName || field.Cardinality() != cardinality || field.Presence() != presence || field.Type().Kind() != kind || field.Type().Name() != typeName || field.Oneof() != oneof {
		t.Fatalf("Field() = number=%d name=%q json=%q cardinality=%q presence=%q type=%q/%q oneof=%q", field.Number(), field.Name(), field.JSONName(), field.Cardinality(), field.Presence(), field.Type().Kind(), field.Type().Name(), field.Oneof())
	}
}

func filePaths(files []protocol.File) []string {
	result := make([]string, len(files))
	for i, file := range files {
		result[i] = file.Path()
	}
	return result
}
