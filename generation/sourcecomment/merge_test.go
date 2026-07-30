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
