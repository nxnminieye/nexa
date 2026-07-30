package protocol_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func TestCanonicalMethodContainsOnlyNativeRPCFacts(t *testing.T) {
	canonical, err := protocol.CanonicalJSON(compileProtocol(t, validCommentProto("")))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	method := firstMethod(root)
	want := []string{"clientStreaming", "fullName", "input", "output", "serverStreaming", "sourceRef"}
	if len(method) != len(want) {
		t.Fatalf("method = %#v", method)
	}
	for _, key := range want {
		if _, ok := method[key]; !ok {
			t.Fatalf("method missing %q: %#v", key, method)
		}
	}
}

func TestCanonicalSnapshotRoundTrip(t *testing.T) {
	canonical, err := protocol.CanonicalJSON(compileProtocol(t, validCommentProto("")))
	if err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("generated/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}
	roundTrip, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(roundTrip, canonical) {
		t.Fatalf("CanonicalJSON() = %s, %v", roundTrip, err)
	}
}

func TestSnapshotSchemaRejectsTransportMetadata(t *testing.T) {
	canonical, err := protocol.CanonicalJSON(compileProtocol(t, validCommentProto("")))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	firstMethod(root)["transport"] = map[string]any{"method": "GET"}
	mutated, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("generated/protocol.json")
	if _, err := protocol.ParseSnapshot(source, mutated); err == nil {
		t.Fatal("ParseSnapshot() accepted transport metadata")
	}
}

func firstMethod(root map[string]any) map[string]any {
	return root["files"].([]any)[0].(map[string]any)["services"].([]any)[0].(map[string]any)["methods"].([]any)[0].(map[string]any)
}
