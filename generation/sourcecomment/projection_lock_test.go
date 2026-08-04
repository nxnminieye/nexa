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
	upstreamNode := NodeInput{SemanticID: "Account", Kind: NodeMessage, Stage: StageEnt, Source: upstream, NativeCanonical: []byte(`{"name":"Account"}`)}
	downstreamSource := upstream
	graph, diagnostics := BuildGraph(StandardRegistry(), BuildInput{
		Nodes: []NodeInput{
			upstreamNode,
			{SemanticID: "Account", Kind: NodeMessage, Stage: StageProto, Source: downstream, SourceDirective: &downstreamSource, NativeCanonical: expected},
		},
		Projections: []ProjectionExpectation{{Downstream: downstream, Upstream: upstream, SemanticID: "Account", Kind: NodeMessage, ExpectedNativeCanonical: expected}},
	})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if err := lock.ValidateFactGraph(graph); err != nil {
		t.Fatalf("matching inherited node failed lock validation: %v", err)
	}
	drifted := graph
	drifted.nodes = make([]graphNode, len(graph.nodes))
	copy(drifted.nodes, graph.nodes)
	for index := range drifted.nodes {
		if drifted.nodes[index].source.String() == downstream.String() {
			drifted.nodes[index].native = []byte(`{"name":"Renamed"}`)
		}
	}
	if err := lock.ValidateFactGraph(drifted); err == nil {
		t.Fatal("mutated inherited node passed lock validation")
	}
	missingSource := graph
	missingSource.nodes = make([]graphNode, 0, len(graph.nodes)-1)
	for _, node := range graph.nodes {
		if node.source.String() != upstream.String() {
			missingSource.nodes = append(missingSource.nodes, node)
		}
	}
	if err := lock.ValidateFactGraph(missingSource); err == nil {
		t.Fatal("projection lock accepted a missing first source")
	}
}

func TestFactGraphProducesOneProjectionLockForAllStages(t *testing.T) {
	ent := mustProjectionSourceRef(t, "ent://schema/account.go#Account")
	proto := mustProjectionSourceRef(t, "proto://desc/account.proto#Account")
	api := mustProjectionSourceRef(t, "api://desc/account.api#Account")
	protoNative := []byte(`{"name":"Account"}`)
	apiNative := []byte(`{"name":"Account","kind":"api"}`)
	scope := StringValue("tenant")
	entDirective := Directive{key: "scope", value: scope, location: Location{File: "schema/account.go", Line: 1}}
	graph, diagnostics := BuildGraph(StandardRegistry(), BuildInput{
		Nodes: []NodeInput{
			{SemanticID: "Account", Kind: NodeSchema, Stage: StageEnt, Source: ent, NativeCanonical: []byte("schema Account"), Facts: []Directive{entDirective}},
			{SemanticID: "Account", Kind: NodeMessage, Stage: StageProto, Source: proto, SourceDirective: &ent, NativeCanonical: protoNative},
			{SemanticID: "Account", Kind: NodeAPIType, Stage: StageAPI, Source: api, SourceDirective: &proto, NativeCanonical: apiNative},
		},
		Projections: []ProjectionExpectation{
			{Downstream: proto, Upstream: ent, SemanticID: "Account", Kind: NodeMessage, ExpectedNativeCanonical: protoNative},
			{Downstream: api, Upstream: proto, SemanticID: "Account", Kind: NodeAPIType, ExpectedNativeCanonical: apiNative},
		},
		InheritedFacts: []InheritedFactExpectation{{ID: FactID{SemanticID: "Account", Key: "scope"}, FirstSource: ent, Value: scope}},
	})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	lock, err := graph.ProjectionLock()
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Nodes()) != 2 || len(lock.Facts()) != 1 {
		t.Fatalf("lock = %#v %#v", lock.Nodes(), lock.Facts())
	}
	if err := lock.ValidateFactGraph(graph); err != nil {
		t.Fatal(err)
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
