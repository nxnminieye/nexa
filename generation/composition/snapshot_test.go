package composition_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gowebpki/jcs"
	generationapi "github.com/nxnminieye/nexa/generation/api"
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

func TestCompositionIRVersionConstantsAndV2TypeClosure(t *testing.T) {
	if composition.APIVersionV1 != "nexa.dev/composition-ir/v1" || composition.APIVersionV2 != "nexa.dev/composition-ir/v2" || composition.CurrentAPIVersion != composition.APIVersionV2 || composition.APIVersion != composition.APIVersionV2 {
		t.Fatalf("composition versions are inconsistent")
	}
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, objectProtocolSource())}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
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
	if wire["apiVersion"] != composition.APIVersionV2 || len(wire["types"].([]any)) != 2 {
		t.Fatalf("v2 snapshot = %s", canonical)
	}
	source, _ := provenance.ParseDomainSource("generated/composition-ir-v2.json")
	snapshot, err := composition.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, _ := snapshot.CanonicalJSON()
	if !bytes.Equal(roundTrip, canonical) {
		t.Fatal("v2 snapshot does not round trip")
	}
	for name, schema := range map[string][]byte{"v1": composition.SchemaV1(), "v2": composition.SchemaV2()} {
		var value map[string]any
		if len(schema) == 0 || json.Unmarshal(schema, &value) != nil || value["additionalProperties"] != false {
			t.Fatalf("%s schema is invalid", name)
		}
	}
	if !bytes.Equal(composition.Schema(), composition.SchemaV2()) {
		t.Fatal("Schema() is not the current v2 schema")
	}
}

func TestParseSnapshotReadsFixedLegacyV1CanonicalBytes(t *testing.T) {
	legacy := []byte(`{"apiVersion":"nexa.dev/composition-ir/v1","consumerModulePath":"example.com/consumer","coreServiceId":"core","kind":"CompositionIR","operations":[],"sourceDigest":"sha256:b69c2b986427008bd25b57cfb4f7d4a0e13dd42d63278a8f709593aac5b32633","sources":[]}`)
	if bytes.Contains(legacy, []byte(`"types"`)) {
		t.Fatal("legacy v1 fixture contains v2 types")
	}
	source, _ := provenance.ParseDomainSource("generated/composition-ir-v1.json")
	snapshot, err := composition.ParseSnapshot(source, legacy)
	if err != nil {
		t.Fatal(err)
	}
	readback, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(readback, legacy) {
		t.Fatalf("legacy readback = %s, %v", readback, err)
	}
}

func TestParseSnapshotReadsFixedLegacyV1OperationBytes(t *testing.T) {
	legacy := []byte(`{"apiVersion":"nexa.dev/composition-ir/v1","consumerModulePath":"example.com/consumer","coreServiceId":"core","kind":"CompositionIR","operations":[{"auth":{"credentials":[],"mode":"none"},"contextFields":[{"provenance":{"sources":["repo:account/v1/account.proto#field%3Aaccount.v1.GetRequest.tenant_id","repo:account/v1/account.proto#method%3Aaccount.v1.AccountService.Get","repo:project/services.yaml#service%3Aaccount%2Fbinding%3Anexa.dev%2Fgeneration-api-proxy%40nexa.dev%2Fgeneration-api-proxy%2Fv1"]},"required":true,"rpcPath":[{"fullName":"account.v1.GetRequest.tenant_id","id":"account.v1.GetRequest#2","number":2,"sourceRef":"repo:account/v1/account.proto#field%3Aaccount.v1.GetRequest.tenant_id","typeKind":"scalar","typeName":"int64"}],"source":"tenant-id","valueType":{"kind":"scalar","name":"int64"}}],"errors":[],"id":"account.get","inputName":"account.v1.GetRequest","method":"GET","methodFullName":"account.v1.AccountService.Get","operationProvenance":{"sources":["repo:account/v1/account.proto#method%3Aaccount.v1.AccountService.Get","repo:project/services.yaml#service%3Aaccount%2Fbinding%3Anexa.dev%2Fgeneration-api-proxy%40nexa.dev%2Fgeneration-api-proxy%2Fv1"]},"outputName":"account.v1.GetResponse","path":"/accounts/{id}","permission":"","requestFields":[{"httpField":"id","provenance":{"sources":["repo:account/v1/account.proto#field%3Aaccount.v1.GetRequest.id","repo:account/v1/account.proto#method%3Aaccount.v1.AccountService.Get","repo:project/services.yaml#service%3Aaccount%2Fbinding%3Anexa.dev%2Fgeneration-api-proxy%40nexa.dev%2Fgeneration-api-proxy%2Fv1"]},"required":true,"rpcPath":[{"fullName":"account.v1.GetRequest.id","id":"account.v1.GetRequest#1","number":1,"sourceRef":"repo:account/v1/account.proto#field%3Aaccount.v1.GetRequest.id","typeKind":"scalar","typeName":"string"}],"valueType":{"kind":"scalar","name":"string"}}],"requestProvenance":{"sources":["repo:account/v1/account.proto#message%3Aaccount.v1.GetRequest","repo:account/v1/account.proto#method%3Aaccount.v1.AccountService.Get","repo:project/services.yaml#service%3Aaccount%2Fbinding%3Anexa.dev%2Fgeneration-api-proxy%40nexa.dev%2Fgeneration-api-proxy%2Fv1"]},"requestType":"AccountGetRequest","responseFields":[{"httpField":"name","provenance":{"sources":["repo:account/v1/account.proto#field%3Aaccount.v1.GetResponse.name","repo:account/v1/account.proto#method%3Aaccount.v1.AccountService.Get","repo:project/services.yaml#service%3Aaccount%2Fbinding%3Anexa.dev%2Fgeneration-api-proxy%40nexa.dev%2Fgeneration-api-proxy%2Fv1"]},"required":true,"rpcPath":[{"fullName":"account.v1.GetResponse.name","id":"account.v1.GetResponse#1","number":1,"sourceRef":"repo:account/v1/account.proto#field%3Aaccount.v1.GetResponse.name","typeKind":"scalar","typeName":"string"}],"valueType":{"kind":"scalar","name":"string"}}],"responseProvenance":{"sources":["repo:account/v1/account.proto#message%3Aaccount.v1.GetResponse","repo:account/v1/account.proto#method%3Aaccount.v1.AccountService.Get","repo:project/services.yaml#service%3Aaccount%2Fbinding%3Anexa.dev%2Fgeneration-api-proxy%40nexa.dev%2Fgeneration-api-proxy%2Fv1"]},"responseType":"AccountGetResponse","serviceId":"account"}],"sourceDigest":"sha256:ca6a8479cd8fc9d789dc04ff8ff02e99d13933b0b7841d00c7e3a59694322900","sources":[{"digest":"sha256:11201eab90b47efbdf40135593252e5bf621885d3527ed74913bb4c66f70da7f","ref":"repo:account/v1/account.proto#field%3Aaccount.v1.GetRequest.id"},{"digest":"sha256:c4b417807c82f6821d8b8aab9eaa70c74eec668e7764cdd881ac9196ac034b4a","ref":"repo:account/v1/account.proto#field%3Aaccount.v1.GetRequest.tenant_id"},{"digest":"sha256:ada731fa0136969d16b1cbd9ee7069cde32907365b1f244d4489919824c0d6d4","ref":"repo:account/v1/account.proto#field%3Aaccount.v1.GetResponse.name"},{"digest":"sha256:e8305fea70d2d2dab74695ad2adc08e279e3e2a1f45890cd5a5595a1fc7704fa","ref":"repo:account/v1/account.proto#message%3Aaccount.v1.GetRequest"},{"digest":"sha256:c1d7995ab17639facf2ccd1c46c4b98541d87b9c452a082c2fbdd8fc7eb626d9","ref":"repo:account/v1/account.proto#message%3Aaccount.v1.GetResponse"},{"digest":"sha256:4310433f0e76ac915fca7fb4d77ca74908f6c12f01505ee763f46ae1af2b0347","ref":"repo:account/v1/account.proto#method%3Aaccount.v1.AccountService.Get"},{"digest":"sha256:0918422484a8865d97b34f95fe5237e8d770992dc9cc38ea85979cc3f83247bc","ref":"repo:project/services.yaml#service%3Aaccount%2Fbinding%3Anexa.dev%2Fgeneration-api-proxy%40nexa.dev%2Fgeneration-api-proxy%2Fv1"}]}`)
	source, _ := provenance.ParseDomainSource("generated/composition-ir-v1-operation.json")
	snapshot, err := composition.ParseSnapshot(source, legacy)
	if err != nil {
		t.Fatal(err)
	}
	readback, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(readback, legacy) {
		t.Fatalf("legacy operation readback = %s, %v", readback, err)
	}
	var root map[string]any
	if err := json.Unmarshal(legacy, &root); err != nil {
		t.Fatal(err)
	}
	request := compositionOperation(root, "account.get")["requestFields"].([]any)[0].(map[string]any)
	request["rpcPath"].([]any)[0].(map[string]any)["jsonName"] = "id"
	if _, err := composition.ParseSnapshot(source, canonicalTestJSON(t, root)); err == nil {
		t.Fatal("ParseSnapshot() accepted a v2 path witness in a v1 snapshot")
	}
}

func TestParseSnapshotRejectsV2ProjectedTypeTampering(t *testing.T) {
	protocolSource := strings.Replace(objectProtocolSource(),
		"message Settings { string locale = 1; }",
		`enum State { STATE_UNSPECIFIED = 0; STATE_ACTIVE = 1; }
enum OtherState { OTHER_STATE_UNSPECIFIED = 0; }
message Settings {
  string locale = 1;
  sint32 signed_count = 2;
  fixed32 fixed_count = 3;
  sint64 signed_total = 4;
  fixed64 fixed_total = 5;
  double ratio = 6;
  float score = 7;
  State state = 8;
  optional string note = 9;
  repeated int64 checkpoints = 10;
}`,
		1,
	)
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, protocolSource)}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := composition.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(map[string]any){
		"dangling ref": func(root map[string]any) {
			member := compositionType(root, "AccountAccountV1Member")
			settings := compositionField(member, "settings")
			settings["valueType"].(map[string]any)["element"].(map[string]any)["name"] = "MissingType"
		},
		"duplicate type name": func(root map[string]any) {
			types := root["types"].([]any)
			root["types"] = append(types, types[0])
		},
		"duplicate field number": func(root map[string]any) {
			fields := compositionType(root, "AccountAccountV1Member")["fields"].([]any)
			fields[1].(map[string]any)["number"] = fields[0].(map[string]any)["number"]
		},
		"field order": func(root map[string]any) {
			fields := compositionType(root, "AccountAccountV1Member")["fields"].([]any)
			fields[0], fields[1] = fields[1], fields[0]
		},
		"type provenance": func(root map[string]any) {
			provenance := compositionType(root, "AccountAccountV1Member")["provenance"].(map[string]any)
			provenance["sources"] = provenance["sources"].([]any)[:1]
		},
		"field provenance": func(root map[string]any) {
			provenance := compositionField(compositionType(root, "AccountAccountV1Member"), "role_codes")["provenance"].(map[string]any)
			provenance["sources"] = provenance["sources"].([]any)[:2]
		},
		"message source ref": func(root map[string]any) {
			member, settings := compositionType(root, "AccountAccountV1Member"), compositionType(root, "AccountAccountV1Settings")
			member["messageSourceRef"] = settings["messageSourceRef"]
		},
		"field source ref": func(root map[string]any) {
			member := compositionType(root, "AccountAccountV1Member")
			compositionField(member, "role_codes")["fieldSourceRef"] = compositionField(member, "id")["fieldSourceRef"]
		},
		"nested scalar shape": func(root map[string]any) {
			locale := compositionField(compositionType(root, "AccountAccountV1Settings"), "locale")
			locale["valueType"].(map[string]any)["name"] = "int64"
		},
		"scalar alias identity": func(root map[string]any) {
			field := compositionField(compositionType(root, "AccountAccountV1Settings"), "signed_count")
			field["protoType"].(map[string]any)["name"] = "sfixed32"
		},
		"enum identity": func(root map[string]any) {
			field := compositionField(compositionType(root, "AccountAccountV1Settings"), "state")
			field["protoType"].(map[string]any)["name"] = "account.v1.OtherState"
		},
		"enum runtime": func(root map[string]any) {
			field := compositionField(compositionType(root, "AccountAccountV1Settings"), "state")
			field["valueType"].(map[string]any)["name"] = "string"
		},
		"json name": func(root map[string]any) {
			field := compositionField(compositionType(root, "AccountAccountV1Settings"), "signed_count")
			field["jsonName"] = "signedCountChanged"
		},
		"nested wrapper": func(root map[string]any) {
			field := compositionField(compositionType(root, "AccountAccountV1Settings"), "checkpoints")
			base := field["valueType"].(map[string]any)["element"]
			field["valueType"].(map[string]any)["element"] = map[string]any{"kind": "optional", "element": base}
		},
		"wrapper required": func(root map[string]any) {
			field := compositionField(compositionType(root, "AccountAccountV1Settings"), "note")
			field["required"] = true
		},
		"object binding wrong same-service ref": func(root map[string]any) {
			binding := compositionBinding(compositionOperation(root, "account.replace"), "requestFields", "settings")
			binding["valueType"].(map[string]any)["name"] = "AccountAccountV1Member"
		},
		"scalar binding changed to ref": func(root map[string]any) {
			binding := compositionBinding(compositionOperation(root, "account.replace"), "requestFields", "roleCodes")
			binding["valueType"].(map[string]any)["element"] = map[string]any{"kind": "ref", "name": "AccountAccountV1Settings"}
		},
	}
	source, _ := provenance.ParseDomainSource("generated/composition-ir-v2-tampered.json")
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(canonical, &root); err != nil {
				t.Fatal(err)
			}
			mutate(root)
			if _, err := composition.ParseSnapshot(source, canonicalTestJSON(t, root)); err == nil {
				t.Fatal("ParseSnapshot() accepted projected type tampering")
			}
		})
	}
}

func TestParseSnapshotRejectsRehashedProjectedMessageSourceTampering(t *testing.T) {
	document, err := composition.Build(parseCatalog(t, true), []protocol.Document{compileProtocol(t, objectProtocolSource())}, loadNative(t, "health.get", "/health"), composition.BuildOptions{CoreServiceID: "core", ConsumerModulePath: "example.com/consumer"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := composition.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	member := compositionType(root, "AccountAccountV1Member")
	messageRef := member["messageSourceRef"].(string)
	for _, raw := range root["sources"].([]any) {
		source := raw.(map[string]any)
		if source["ref"] == messageRef {
			source["digest"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
	}
	root["sourceDigest"] = compositionSourceDigest(t, root["sources"].([]any))
	snapshotSource, _ := provenance.ParseDomainSource("generated/composition-ir-v2-message-digest-tampered.json")
	if _, err := composition.ParseSnapshot(snapshotSource, canonicalTestJSON(t, root)); err == nil {
		t.Fatal("ParseSnapshot() accepted a rehashed projected message source digest")
	}
}

func compositionSourceDigest(t *testing.T, values []any) string {
	t.Helper()
	sources := make([]provenance.Source, len(values))
	for index, raw := range values {
		item := raw.(map[string]any)
		ref, refErr := provenance.ParseSourceRef(item["ref"].(string))
		digest, digestErr := provenance.ParseDigest(item["digest"].(string))
		if refErr != nil || digestErr != nil {
			t.Fatalf("parse source %d: %v %v", index, refErr, digestErr)
		}
		sources[index] = provenance.Source{Ref: ref, Digest: digest}
	}
	digest, err := generationapi.ComputeSourceDigest(sources)
	if err != nil {
		t.Fatal(err)
	}
	return digest.String()
}

func TestParseSnapshotValidatesExactV2RPCPathWitnesses(t *testing.T) {
	sourceText := `syntax = "proto3";
package account.v1;
import "nexa/protocol/v1/options.proto";
enum State { STATE_UNSPECIFIED = 0; STATE_ACTIVE = 1; }
message Details { string value = 1 [json_name = "customValue"]; }
message Other { string value = 1; }
message ReplaceRequest {
  Details details = 1 [json_name = "customDetails"];
  optional string note = 2;
  repeated sint32 codes = 3;
  State state = 4;
  repeated State states = 5;
  Details object = 6;
  repeated Details objects = 7;
  Other other = 8;
  int64 tenant_id = 9;
}
message ReplaceResponse { string result = 1 [json_name = "customResult"]; }
service AccountService {
  rpc Replace(ReplaceRequest) returns (ReplaceResponse) {
    option (nexa.protocol.v1.rpc_context) = {
      context_fields: { source: TENANT_ID rpc_field: "tenant_id" }
    };
    option (nexa.protocol.v1.http_proxy) = {
      operation_id: "account.replace" method: POST path: "/accounts/replace"
      auth: { mode: NONE }
      request_fields: { http_field: "nestedValue" rpc_field: "details.value" }
      request_fields: { http_field: "note" rpc_field: "note" }
      request_fields: { http_field: "codes" rpc_field: "codes" }
      request_fields: { http_field: "state" rpc_field: "state" }
      request_fields: { http_field: "states" rpc_field: "states" }
      request_fields: { http_field: "object" rpc_field: "object" }
      request_fields: { http_field: "objects" rpc_field: "objects" }
      request_fields: { http_field: "other" rpc_field: "other" }
      response_fields: { rpc_field: "result" http_field: "result" }
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
	snapshotSource, _ := provenance.ParseDomainSource("generated/composition-ir-v2-paths.json")
	if _, err := composition.ParseSnapshot(snapshotSource, canonical); err != nil {
		t.Fatalf("valid v2 path witnesses rejected: %v", err)
	}

	requestSegment := func(root map[string]any) map[string]any {
		binding := compositionBinding(compositionOperation(root, "account.replace"), "requestFields", "nestedValue")
		return binding["rpcPath"].([]any)[0].(map[string]any)
	}
	contextSegment := func(root map[string]any) map[string]any {
		operation := compositionOperation(root, "account.replace")
		binding := operation["contextFields"].([]any)[0].(map[string]any)
		return binding["rpcPath"].([]any)[0].(map[string]any)
	}
	responseSegment := func(root map[string]any) map[string]any {
		binding := compositionBinding(compositionOperation(root, "account.replace"), "responseFields", "result")
		return binding["rpcPath"].([]any)[0].(map[string]any)
	}
	tests := map[string]func(map[string]any){
		"missing jsonName":          func(root map[string]any) { delete(requestSegment(root), "jsonName") },
		"missing cardinality":       func(root map[string]any) { delete(requestSegment(root), "cardinality") },
		"missing presence":          func(root map[string]any) { delete(requestSegment(root), "presence") },
		"missing typeName":          func(root map[string]any) { delete(requestSegment(root), "typeName") },
		"unknown witness property":  func(root map[string]any) { requestSegment(root)["unknown"] = true },
		"changed id":                func(root map[string]any) { requestSegment(root)["id"] = "account.v1.ReplaceRequest#99" },
		"changed fullName":          func(root map[string]any) { requestSegment(root)["fullName"] = "account.v1.ReplaceRequest.other" },
		"changed number":            func(root map[string]any) { requestSegment(root)["number"] = float64(99) },
		"changed request jsonName":  func(root map[string]any) { requestSegment(root)["jsonName"] = "changedDetails" },
		"changed context jsonName":  func(root map[string]any) { contextSegment(root)["jsonName"] = "changedTenant" },
		"changed response jsonName": func(root map[string]any) { responseSegment(root)["jsonName"] = "changedResult" },
		"changed cardinality":       func(root map[string]any) { requestSegment(root)["cardinality"] = "repeated" },
		"changed presence":          func(root map[string]any) { requestSegment(root)["presence"] = "implicit" },
		"changed typeKind":          func(root map[string]any) { requestSegment(root)["typeKind"] = "scalar" },
		"changed typeName":          func(root map[string]any) { requestSegment(root)["typeName"] = "account.v1.Other" },
		"request source swap":       func(root map[string]any) { requestSegment(root)["sourceRef"] = contextSegment(root)["sourceRef"] },
		"context source swap":       func(root map[string]any) { contextSegment(root)["sourceRef"] = responseSegment(root)["sourceRef"] },
		"response source swap":      func(root map[string]any) { responseSegment(root)["sourceRef"] = requestSegment(root)["sourceRef"] },
		"wrong reachable object ref": func(root map[string]any) {
			binding := compositionBinding(compositionOperation(root, "account.replace"), "requestFields", "object")
			binding["valueType"].(map[string]any)["name"] = "AccountAccountV1Other"
		},
		"outer scalar ref disagreement": func(root map[string]any) {
			binding := compositionBinding(compositionOperation(root, "account.replace"), "requestFields", "codes")
			binding["valueType"].(map[string]any)["element"] = map[string]any{"kind": "ref", "name": "AccountAccountV1Details"}
		},
		"outer required disagreement": func(root map[string]any) {
			binding := compositionBinding(compositionOperation(root, "account.replace"), "requestFields", "note")
			binding["required"] = true
		},
		"context witness disagreement": func(root map[string]any) { contextSegment(root)["presence"] = "explicit" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(canonical, &root); err != nil {
				t.Fatal(err)
			}
			mutate(root)
			if _, err := composition.ParseSnapshot(snapshotSource, canonicalTestJSON(t, root)); err == nil {
				t.Fatal("ParseSnapshot() accepted path witness tampering")
			}
		})
	}
}

func compositionOperation(root map[string]any, id string) map[string]any {
	for _, raw := range root["operations"].([]any) {
		value := raw.(map[string]any)
		if value["id"] == id {
			return value
		}
	}
	return nil
}

func compositionBinding(operation map[string]any, collection, httpField string) map[string]any {
	for _, raw := range operation[collection].([]any) {
		value := raw.(map[string]any)
		if value["httpField"] == httpField {
			return value
		}
	}
	return nil
}

func compositionType(root map[string]any, name string) map[string]any {
	for _, raw := range root["types"].([]any) {
		value := raw.(map[string]any)
		if value["name"] == name {
			return value
		}
	}
	return nil
}

func compositionField(projected map[string]any, protoName string) map[string]any {
	for _, raw := range projected["fields"].([]any) {
		value := raw.(map[string]any)
		if value["protoName"] == protoName {
			return value
		}
	}
	return nil
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
