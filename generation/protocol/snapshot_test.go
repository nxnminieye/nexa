package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
)

func TestSnapshotRetainsOnlyMethodIdentity(t *testing.T) {
	canonical, err := protocol.CanonicalJSON(compileProtocol(t, validCommentProto("")))
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("generated/protocol.json")
	snapshot, err := protocol.ParseSnapshot(source, canonical)
	if err != nil {
		t.Fatalf("ParseSnapshot() error = %v", err)
	}
	method, ok := snapshot.Method("sample.v1.SampleService.GetSample")
	if !ok || method.FullName() != "sample.v1.SampleService.GetSample" {
		t.Fatalf("Method() = %#v, %v", method, ok)
	}
}

func TestSnapshotClosedSchemaRejectsRemovedProtocolMetadata(t *testing.T) {
	canonical, err := protocol.CanonicalJSON(compileProtocol(t, validCommentProto("")))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(canonical, &root); err != nil {
		t.Fatal(err)
	}
	firstMethod(root)["contextFields"] = []any{}
	mutated, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := provenance.ParseDomainSource("generated/protocol.json")
	if _, err := protocol.ParseSnapshot(source, mutated); err == nil {
		t.Fatal("ParseSnapshot() accepted removed protocol metadata")
	}
}
