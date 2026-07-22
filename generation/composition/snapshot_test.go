package composition_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/composition"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func TestSnapshotRoundTripIsCanonicalAndDefensive(t *testing.T) {
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, validProtocolSource(false))}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := composition.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("generated/composition-ir.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := composition.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	readback, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(readback, canonical) {
		t.Fatalf("CanonicalJSON() = %s, %v; want %s", readback, err, canonical)
	}
	readback[0] ^= 0xff
	again, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(again, canonical) {
		t.Fatal("snapshot canonical bytes alias caller memory")
	}
	schema := composition.Schema()
	var schemaDocument map[string]any
	if len(schema) == 0 || json.Unmarshal(schema, &schemaDocument) != nil || schemaDocument["additionalProperties"] != false {
		t.Fatalf("Schema() is not a strict JSON schema: %s", schema)
	}
	schema[0] ^= 0xff
	if bytes.Equal(schema, composition.Schema()) {
		t.Fatal("Schema() aliases caller memory")
	}
}

func TestParseSnapshotRejectsUnknownNoncanonicalAndDigestTampering(t *testing.T) {
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, validProtocolSource(false))}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := composition.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("generated/composition-ir.json")
	unknown := append([]byte(`{"unknown":true,`), canonical[1:]...)
	if _, err := composition.ParseSnapshot(source, unknown); err == nil {
		t.Fatal("ParseSnapshot() accepted an unknown member")
	}
	if _, err := composition.ParseSnapshot(source, append(append([]byte(nil), canonical...), '\n')); err == nil {
		t.Fatal("ParseSnapshot() accepted noncanonical bytes")
	}
	marker := []byte(`"sourceDigest":"sha256:`)
	index := bytes.Index(canonical, marker)
	if index < 0 {
		t.Fatalf("canonical document has no source digest: %s", canonical)
	}
	tampered := append([]byte(nil), canonical...)
	hex := index + len(marker)
	if tampered[hex] == 'a' {
		tampered[hex] = 'b'
	} else {
		tampered[hex] = 'a'
	}
	if _, err := composition.ParseSnapshot(source, tampered); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("ParseSnapshot() digest tamper error = %v", err)
	}
	pathTamper := bytes.Replace(canonical, []byte(`"id":"account.v1.GetAccountRequest#1"`), []byte(`"id":"account.v1.GetAccountRequest#9"`), 1)
	if bytes.Equal(pathTamper, canonical) {
		t.Fatalf("canonical document has no typed request path: %s", canonical)
	}
	if _, err := composition.ParseSnapshot(source, pathTamper); err == nil {
		t.Fatal("ParseSnapshot() accepted a typed path whose field number disagrees with its resolved descriptor")
	}
	var provenanceSwap map[string]any
	if err := json.Unmarshal(canonical, &provenanceSwap); err != nil {
		t.Fatal(err)
	}
	operation := provenanceSwap["operations"].([]any)[0].(map[string]any)
	operation["requestProvenance"], operation["responseProvenance"] = operation["responseProvenance"], operation["requestProvenance"]
	swapped := canonicalTestJSON(t, provenanceSwap)
	if _, err := composition.ParseSnapshot(source, swapped); err == nil {
		t.Fatal("ParseSnapshot() accepted request/response provenance with exchanged source roles")
	}
	for name, mutate := range map[string]func(map[string]any){
		"auth combination": func(operation map[string]any) {
			operation["auth"].(map[string]any)["mode"] = "none"
		},
		"path binding": func(operation map[string]any) {
			operation["path"] = "/accounts/{missing}"
		},
		"error code": func(operation map[string]any) {
			operation["errors"].([]any)[0].(map[string]any)["project"].(map[string]any)["code"] = "Invalid Code"
		},
		"tenant context type": func(operation map[string]any) {
			for _, raw := range operation["contextFields"].([]any) {
				binding := raw.(map[string]any)
				if binding["source"] == "tenant-id" {
					binding["valueType"] = map[string]any{"kind": "scalar", "name": "string"}
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var semanticTamper map[string]any
			if err := json.Unmarshal(canonical, &semanticTamper); err != nil {
				t.Fatal(err)
			}
			mutate(semanticTamper["operations"].([]any)[0].(map[string]any))
			if _, err := composition.ParseSnapshot(source, canonicalTestJSON(t, semanticTamper)); err == nil {
				t.Fatal("ParseSnapshot() accepted HTTP API semantics rejected by the public API contract")
			}
		})
	}
}

func TestParseSnapshotRejectsSplicedNestedTypedPath(t *testing.T) {
	sourceText := `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
message Address { string city = 1; }
message GetAccountRequest { Address address = 1; }
message GetAccountResponse { string name = 1; }
service AccountService {
  rpc Get(GetAccountRequest) returns (GetAccountResponse) {
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.get" method: GET path: "/accounts"
      auth: { mode: REQUIRED credentials: { id: "primary" type: BEARER location: HEADER name: "Authorization" } }
      permission: "account.read"
      request_fields: { http_field: "city" rpc_field: "address.city" }
      response_fields: { rpc_field: "name" http_field: "name" }
    };
  }
}`
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, sourceText)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := composition.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(canonical, &wire); err != nil {
		t.Fatal(err)
	}
	operation := wire["operations"].([]any)[0].(map[string]any)
	request := operation["requestFields"].([]any)[0].(map[string]any)
	response := operation["responseFields"].([]any)[0].(map[string]any)
	requestPath := request["rpcPath"].([]any)
	responseLeaf := response["rpcPath"].([]any)[0]
	requestPath[1] = responseLeaf
	request["provenance"] = response["provenance"]
	tampered := canonicalTestJSON(t, wire)
	snapshotSource, _ := provenance.ParseDomainSource("generated/composition-ir.json")
	if _, err := composition.ParseSnapshot(snapshotSource, tampered); err == nil {
		t.Fatal("ParseSnapshot() accepted a nested path spliced from another message owner")
	}
}

func canonicalTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
