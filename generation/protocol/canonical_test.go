package protocol_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func TestMethodCanonicalSourceJSONAndProvenanceAreExact(t *testing.T) {
	document := compileProtocol(t, validProxyProto(""))
	method, _ := document.Method("sample.v1.SampleService.GetSample")
	want := []byte(`{"apiVersion":"nexa.dev/proto-method-node/v2","clientStreaming":false,"fullName":"sample.v1.SampleService.GetSample","httpProxy":{"auth":{"credentials":[{"id":"primary","in":"header","name":"authorization","type":"bearer"}],"mode":"required"},"errors":[{"match":{"code":"not_found","domain":"sample"},"project":{"code":"sample_not_found","domain":"api","httpStatus":404}}],"method":"GET","operationId":"sample.get","path":"/samples/{id}","permission":"sample.read","requestFields":[{"httpField":"id","rpcPath":["sample.v1.GetSampleRequest#1"]}],"responseFields":[{"httpField":"displayName","rpcPath":["sample.v1.GetSampleResponse#1"]}]},"input":"sample.v1.GetSampleRequest","kind":"method","output":"sample.v1.GetSampleResponse","rpcContext":{"contextFields":[{"rpcPath":["sample.v1.GetSampleRequest#2"],"source":"tenant-id"}]},"serverStreaming":false}`)
	independent, err := jcs.Transform(want)
	if err != nil || !bytes.Equal(independent, want) {
		t.Fatalf("golden is not independent JCS: %v", err)
	}
	if got := method.CanonicalSourceJSON(); !bytes.Equal(got, want) {
		t.Fatalf("CanonicalSourceJSON() = %s", got)
	}
	source := method.Source()
	if source.Ref.String() != "repo:sample.proto#method%3Asample.v1.SampleService.GetSample" {
		t.Fatalf("Source().Ref = %s", source.Ref.String())
	}
	sum := sha256.Sum256(want)
	if source.Digest.String() != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("Source().Digest = %s", source.Digest.String())
	}
	owned := method.CanonicalSourceJSON()
	owned[0] = 0
	if method.CanonicalSourceJSON()[0] == 0 {
		t.Fatal("CanonicalSourceJSON() aliases owner bytes")
	}
	if found, ok := document.Source(source.Ref); !ok || found != source {
		t.Fatalf("Document.Source() = %#v, %v", found, ok)
	}
}

func TestOwnerDigestsIgnoreCommentsAndNarrowFieldChanges(t *testing.T) {
	first := compileProtocol(t, validProxyProto(""))
	commented := compileProtocol(t, "// ordinary comment\n"+validProxyProto(""))
	firstMethod, _ := first.Method("sample.v1.SampleService.GetSample")
	commentMethod, _ := commented.Method("sample.v1.SampleService.GetSample")
	if firstMethod.Source().Digest != commentMethod.Source().Digest {
		t.Fatal("method digest changed because of a source comment/location")
	}
	changed := compileProtocol(t, replaceOnce(validProxyProto(""), "string id = 1", "int64 id = 1"))
	request, _ := first.Message("sample.v1.GetSampleRequest")
	changedRequest, _ := changed.Message("sample.v1.GetSampleRequest")
	if request.Source().Digest != changedRequest.Source().Digest {
		t.Fatal("message owner digest includes child field semantics")
	}
	if request.Fields()[0].Source().Digest == changedRequest.Fields()[0].Source().Digest {
		t.Fatal("field semantic change did not change its owner digest")
	}
}

func TestDocumentCanonicalSnapshotRoundTripIsStrictAndReadOnly(t *testing.T) {
	document := compileProtocol(t, validProxyProto(""))
	canonical, err := protocol.CanonicalJSON(document)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	source, err := provenance.ParseDomainSource("generated/protocol-ir.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("Snapshot.CanonicalJSON() = %s, %v", roundTrip, err)
	}
	if method, ok := snapshot.Method("sample.v1.SampleService.GetSample"); !ok || method.HTTPProxy().OperationID() != "sample.get" {
		t.Fatalf("Snapshot.Method() = %#v, %v", method, ok)
	}
	roundTrip[0] = 0
	again, _ := snapshot.CanonicalJSON()
	if again[0] == 0 {
		t.Fatal("Snapshot.CanonicalJSON() aliases immutable state")
	}
	unknown := append([]byte(nil), canonical...)
	unknown = bytes.Replace(unknown, []byte(`"apiVersion"`), []byte(`"extra":true,"apiVersion"`), 1)
	if _, err := protocol.ParseSnapshot(source, unknown); err == nil {
		t.Fatal("ParseSnapshot() accepted an unknown field")
	}
	if len(protocol.Schema()) == 0 {
		t.Fatal("Schema() returned no bytes")
	}
}

func TestSnapshotRejectsDescriptorTamperingAfterAllDigestsAreRecomputed(t *testing.T) {
	document := compileProtocol(t, validProxyProto(""))
	canonical, err := protocol.CanonicalJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("generated/protocol-ir-tampered.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "field owner", mutate: func(root map[string]any) { firstField(root)["fullName"] = "other.v1.id" }},
		{name: "field type target", mutate: func(root map[string]any) {
			firstField(root)["type"] = map[string]any{"kind": "message", "name": "sample.v1.Missing"}
		}},
		{name: "presence type mismatch", mutate: func(root map[string]any) { firstField(root)["presence"] = "map" }},
		{name: "method input", mutate: func(root map[string]any) { firstMethod(root)["input"] = "sample.v1.Missing" }},
		{name: "tenant context type", mutate: func(root map[string]any) {
			fields := root["files"].([]any)[0].(map[string]any)["messages"].([]any)[0].(map[string]any)["fields"].([]any)
			fields[1].(map[string]any)["type"] = map[string]any{"kind": "scalar", "name": "string"}
		}},
		{name: "request rpc path root", mutate: func(root map[string]any) {
			firstMethod(root)["httpProxy"].(map[string]any)["requestFields"].([]any)[0].(map[string]any)["rpcPath"] = []any{"sample.v1.GetSampleResponse#1"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(canonical, &root); err != nil {
				t.Fatal(err)
			}
			test.mutate(root)
			tampered := recanonicalizeProtocolSnapshot(t, root)
			if _, err := protocol.ParseSnapshot(source, tampered); err == nil {
				t.Fatal("ParseSnapshot() accepted a self-consistent descriptor tamper")
			}
		})
	}
}

func firstField(root map[string]any) map[string]any {
	return root["files"].([]any)[0].(map[string]any)["messages"].([]any)[0].(map[string]any)["fields"].([]any)[0].(map[string]any)
}

func firstMethod(root map[string]any) map[string]any {
	return root["files"].([]any)[0].(map[string]any)["services"].([]any)[0].(map[string]any)["methods"].([]any)[0].(map[string]any)
}

func recanonicalizeProtocolSnapshot(t *testing.T, root map[string]any) []byte {
	t.Helper()
	sourceByRef := map[string]map[string]any{}
	for _, raw := range root["sources"].([]any) {
		item := raw.(map[string]any)
		sourceByRef[item["ref"].(string)] = item
	}
	for _, rawFile := range root["files"].([]any) {
		file := rawFile.(map[string]any)
		for _, rawMessage := range file["messages"].([]any) {
			message := rawMessage.(map[string]any)
			setTestSourceDigest(t, sourceByRef[message["sourceRef"].(string)], map[string]any{"apiVersion": "nexa.dev/proto-message-node/v1", "kind": "message", "fullName": message["fullName"]})
			for _, rawField := range message["fields"].([]any) {
				field := rawField.(map[string]any)
				owner := cloneWithout(field, "sourceRef")
				owner["apiVersion"], owner["kind"] = "nexa.dev/proto-field-node/v1", "field"
				setTestSourceDigest(t, sourceByRef[field["sourceRef"].(string)], owner)
			}
		}
		for _, rawService := range file["services"].([]any) {
			for _, rawMethod := range rawService.(map[string]any)["methods"].([]any) {
				method := rawMethod.(map[string]any)
				owner := cloneWithout(method, "sourceRef")
				owner["apiVersion"], owner["kind"] = "nexa.dev/proto-method-node/v2", "method"
				setTestSourceDigest(t, sourceByRef[method["sourceRef"].(string)], owner)
			}
		}
	}
	set := map[string]any{"apiVersion": "nexa.dev/protocol-source-set/v1", "sources": root["sources"]}
	root["sourceDigest"] = provenance.SHA256(mustTestJCS(t, set)).String()
	return mustTestJCS(t, root)
}

func setTestSourceDigest(t *testing.T, source map[string]any, owner map[string]any) {
	t.Helper()
	source["digest"] = provenance.SHA256(mustTestJCS(t, owner)).String()
}
func cloneWithout(value map[string]any, excluded string) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		if key != excluded {
			result[key] = item
		}
	}
	return result
}
func mustTestJCS(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
