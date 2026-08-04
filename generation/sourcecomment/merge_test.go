package sourcecomment_test

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestMergeGraphsPreservesFactsAndRechecksSemanticCollisions(t *testing.T) {
	leftNode := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Account", "ent://schema/account.go#Account", "account")
	leftNode.Facts = []sourcecomment.Directive{directive(t, `// @nexa scope: "tenant"`, 2)}
	rightNode := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "Audit", "proto://rpc/audit.proto#audit.v1.Audit", "audit")
	rightNode.Facts = []sourcecomment.Directive{directive(t, `// @nexa scope: "global"`, 2)}
	left, leftDiagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{leftNode}})
	right, rightDiagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{rightNode}})
	if len(leftDiagnostics) != 0 || len(rightDiagnostics) != 0 {
		t.Fatal(leftDiagnostics, rightDiagnostics)
	}
	merged, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), left, right)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	for _, id := range []sourcecomment.FactID{{SemanticID: "Account", Key: "scope"}, {SemanticID: "Audit", Key: "scope"}} {
		if _, ok := merged.Fact(id); !ok {
			t.Fatalf("fact %s missing", id.String())
		}
	}

	colliding := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "account", "ent://schema/other.go#account", "other")
	conflict, conflictDiagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{colliding}})
	if len(conflictDiagnostics) != 0 {
		t.Fatal(conflictDiagnostics)
	}
	_, diagnostics = sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), left, conflict)
	assertContainsCode(t, diagnostics, sourcecomment.CodeSemanticCollision)
}

func TestMergeGraphsDeduplicatesOnlyIdenticalAncestorInputs(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeSchema, "Account", "ent://schema/account.go#Account", "schema Account")
	ent.Facts = []sourcecomment.Directive{directive(t, `// @nexa scope: "tenant"`, 2)}
	ancestor, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{ent}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	proto := node(t, sourcecomment.StageProto, sourcecomment.NodeMessage, "Account", "proto://desc/account.proto#Account", "message Account")
	proto.SourceDirective = refPointer(ent.Source)
	projection := sourcecomment.ProjectionExpectation{
		Downstream: proto.Source, Upstream: ent.Source, SemanticID: "Account", Kind: sourcecomment.NodeMessage,
		ExpectedNativeCanonical: []byte("message Account"),
	}
	scope, _ := ancestor.Fact(sourcecomment.FactID{SemanticID: "Account", Key: "scope"})
	inherited := sourcecomment.InheritedFactExpectation{ID: scope.ID(), Value: scope.Value(), FirstSource: scope.FirstSource()}
	descendant, diagnostics := sourcecomment.ExtendGraph(sourcecomment.StandardRegistry(), ancestor, sourcecomment.BuildInput{
		Nodes: []sourcecomment.NodeInput{proto}, Projections: []sourcecomment.ProjectionExpectation{projection},
		InheritedFacts: []sourcecomment.InheritedFactExpectation{inherited},
	})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	merged, diagnostics := sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), ancestor, descendant)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	lock, err := merged.ProjectionLock()
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.Nodes()) != 1 || len(lock.Facts()) != 1 {
		t.Fatalf("lock = %#v %#v", lock.Nodes(), lock.Facts())
	}

	drifted := ent
	drifted.NativeCanonical = []byte("schema Changed")
	conflict, conflictDiagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{drifted}})
	if len(conflictDiagnostics) != 0 {
		t.Fatal(conflictDiagnostics)
	}
	_, diagnostics = sourcecomment.MergeGraphs(sourcecomment.StandardRegistry(), ancestor, conflict)
	assertContainsCode(t, diagnostics, sourcecomment.CodeSemanticCollision)
}
