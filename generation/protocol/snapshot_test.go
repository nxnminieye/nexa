package protocol_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func TestSnapshotReadsIndependentRPCContext(t *testing.T) {
	source := `syntax = "proto3";
package sample.v1;
import "nexa/protocol/v1/options.proto";
message Request { int64 tenant_id = 1; string request_id = 2; }
message Response {}
service SampleService {
  rpc Get(Request) returns (Response) {
    option (nexa.protocol.v1.rpc_context) = {
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
      context_fields: { source: REQUEST_ID rpc_field: "request_id" }
    };
  }
}`
	document := compileProtocol(t, source)
	canonical, err := protocol.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte(`"httpProxy"`)) || !bytes.Contains(canonical, []byte(`"rpcContext":{"contextFields"`)) {
		t.Fatalf("CanonicalJSON() = %s", canonical)
	}
	domain, err := provenance.ParseDomainSource("generated/protocol-ir-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.ParseSnapshot(domain, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}
	method, ok := snapshot.Method("sample.v1.SampleService.Get")
	if !ok {
		t.Fatal("snapshot method missing")
	}
	fields := method.RPCContext().ContextFields()
	if len(fields) != 2 || fields[0].Source() != protocol.ContextRequestID || fields[1].Source() != protocol.ContextTenantID {
		t.Fatalf("RPCContext().ContextFields() = %#v", fields)
	}
}

func TestSnapshotRejectsContextBindingUniquenessTampering(t *testing.T) {
	source := replaceOnce(validProxyProto(""), "int64 tenant_id = 2;", "int64 tenant_id = 2; string request_id = 3; string trace_id = 4;")
	source = replaceOnce(source, `      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }`, `      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
      context_fields: { source: REQUEST_ID rpc_field: "request_id" }
      context_fields: { source: TRACE_ID rpc_field: "trace_id" }`)
	canonical, err := protocol.CanonicalJSON(compileProtocol(t, source))
	if err != nil {
		t.Fatal(err)
	}
	domain, err := provenance.ParseDomainSource("generated/protocol-ir-v2-tampered.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]any) []any{
		"source reused for different path": func(contexts []any) []any {
			contexts[2].(map[string]any)["source"] = "request-id"
			return []any{contexts[0], contexts[2], contexts[1]}
		},
		"rpc path reused by different source": func(contexts []any) []any {
			contexts[2].(map[string]any)["rpcPath"] = contexts[0].(map[string]any)["rpcPath"]
			return contexts
		},
	} {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(canonical, &root); err != nil {
				t.Fatal(err)
			}
			method := firstMethod(root)
			rpcContext := method["rpcContext"].(map[string]any)
			rpcContext["contextFields"] = mutate(rpcContext["contextFields"].([]any))
			tampered := recanonicalizeProtocolSnapshot(t, root)
			if _, err := protocol.ParseSnapshot(domain, tampered); err == nil {
				t.Fatal("ParseSnapshot() accepted context binding uniqueness tamper")
			}
		})
	}
}
