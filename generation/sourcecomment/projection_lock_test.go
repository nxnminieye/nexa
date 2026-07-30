package sourcecomment

import (
	"bytes"
	"strings"
	"testing"
)

func TestProjectionLockCanonicalRoundTrip(t *testing.T) {
	upstream := mustProjectionSourceRef(t, "ent://schema/account.go#Account")
	downstream := mustProjectionSourceRef(t, "proto://desc/account.proto#Account")
	lock, err := NewProjectionLock(
		[]ProjectionExpectation{{Downstream: downstream, Upstream: upstream, SemanticID: "Account", Kind: NodeMessage, ExpectedNativeCanonical: []byte(`{"name":"Account"}`)}},
		[]InheritedFactExpectation{{ID: FactID{SemanticID: "Account", Key: "scope"}, FirstSource: upstream, Value: StringValue("tenant")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := lock.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseProjectionLock(canonical)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := parsed.CanonicalJSON()
	if err != nil || !bytes.Equal(rendered, canonical) {
		t.Fatalf("round trip = %q, %v", rendered, err)
	}
	if len(parsed.Nodes()) != 1 || len(parsed.Facts()) != 1 {
		t.Fatalf("lock = %#v %#v", parsed.Nodes(), parsed.Facts())
	}
}

func TestProjectionLockRejectsUnknownNonCanonicalAndDuplicateState(t *testing.T) {
	upstream := mustProjectionSourceRef(t, "ent://schema/account.go#Account")
	downstream := mustProjectionSourceRef(t, "proto://desc/account.proto#Account")
	if _, err := NewProjectionLock([]ProjectionExpectation{
		{Downstream: downstream, Upstream: upstream, SemanticID: "Account", Kind: NodeMessage},
		{Downstream: downstream, Upstream: upstream, SemanticID: "Account", Kind: NodeMessage},
	}, nil); err == nil {
		t.Fatal("duplicate projection lock node accepted")
	}
	valid, err := NewProjectionLock(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := valid.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(encoded), `"kind":"SourceProjectionLock"`, `"kind":"SourceProjectionLock","extra":true`, 1)
	if _, err := ParseProjectionLock([]byte(unknown)); err == nil {
		t.Fatal("unknown lock field accepted")
	}
	if _, err := ParseProjectionLock(bytes.TrimSpace(encoded)); err == nil {
		t.Fatal("non-canonical lock accepted")
	}
}

func TestProjectionLockValidatesInheritedGraphNodes(t *testing.T) {
	upstream := mustProjectionSourceRef(t, "ent://schema/account.go#Account")
	downstream := mustProjectionSourceRef(t, "proto://desc/account.proto#Account")
	expected := []byte(`{"name":"Account"}`)
	lock, err := NewProjectionLock([]ProjectionExpectation{{
		Downstream: downstream, Upstream: upstream, SemanticID: "Account", Kind: NodeMessage, ExpectedNativeCanonical: expected,
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	graph, diagnostics := BuildGraph(StandardRegistry(), BuildInput{Nodes: []NodeInput{{
		SemanticID: "Account", Kind: NodeMessage, Stage: StageProto, Source: downstream, NativeCanonical: expected,
	}}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if err := lock.ValidateFactGraph(graph); err != nil {
		t.Fatalf("matching inherited node failed lock validation: %v", err)
	}
	drifted, diagnostics := BuildGraph(StandardRegistry(), BuildInput{Nodes: []NodeInput{{
		SemanticID: "Account", Kind: NodeMessage, Stage: StageProto, Source: downstream, NativeCanonical: []byte(`{"name":"Renamed"}`),
	}}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if err := lock.ValidateFactGraph(drifted); err == nil {
		t.Fatal("mutated inherited node passed lock validation")
	}
}

func mustProjectionSourceRef(t *testing.T, value string) SourceRef {
	t.Helper()
	result, err := ParseSourceRef(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
