package sourcecomment_test

import (
	"testing"

	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestExtendGraphPreservesInheritedFactsAndAllowsLocalAdditions(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "Record.name", "ent://schema/record.go#Record.name", "string")
	ent.Facts = []sourcecomment.Directive{directive(t, `// @nexa label.zh-CN: "名称"`, 2)}
	upstream, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{ent}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}

	projected := node(t, sourcecomment.StageProto, sourcecomment.NodeProtoField, "Record.name", "proto://rpc/record.proto#records.v1.Record.name", "string name = 1")
	projected.SourceDirective = refPointer(ent.Source)
	local := node(t, sourcecomment.StageProto, sourcecomment.NodeProtoField, "Record.summary", "proto://rpc/record.proto#records.v1.Record.summary", "string summary = 2")
	local.Facts = []sourcecomment.Directive{directive(t, `// @nexa label.zh-CN: "摘要"`, 8)}
	extended, diagnostics := sourcecomment.ExtendGraph(sourcecomment.StandardRegistry(), upstream, sourcecomment.BuildInput{
		Nodes: []sourcecomment.NodeInput{projected, local},
		Projections: []sourcecomment.ProjectionExpectation{{
			Downstream:              projected.Source,
			Upstream:                ent.Source,
			SemanticID:              projected.SemanticID,
			Kind:                    projected.Kind,
			ExpectedNativeCanonical: []byte("string name = 1"),
		}},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, id := range []sourcecomment.FactID{
		{SemanticID: "Record.name", Key: "label.zh-CN"},
		{SemanticID: "Record.summary", Key: "label.zh-CN"},
	} {
		if _, ok := extended.Fact(id); !ok {
			t.Errorf("fact %q missing", id.String())
		}
	}
}

func TestExtendGraphRejectsInheritedFactModification(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "Record.name", "ent://schema/record.go#Record.name", "string")
	ent.Facts = []sourcecomment.Directive{directive(t, `// @nexa label.zh-CN: "名称"`, 2)}
	upstream, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{ent}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}

	projected := node(t, sourcecomment.StageProto, sourcecomment.NodeProtoField, "Record.name", "proto://rpc/record.proto#records.v1.Record.name", "string name = 1")
	projected.SourceDirective = refPointer(ent.Source)
	projected.Facts = []sourcecomment.Directive{directive(t, `// @nexa label.zh-CN: "记录名称"`, 8)}
	_, diagnostics = sourcecomment.ExtendGraph(sourcecomment.StandardRegistry(), upstream, sourcecomment.BuildInput{
		Nodes: []sourcecomment.NodeInput{projected},
		Projections: []sourcecomment.ProjectionExpectation{{
			Downstream:              projected.Source,
			Upstream:                ent.Source,
			SemanticID:              projected.SemanticID,
			Kind:                    projected.Kind,
			ExpectedNativeCanonical: []byte("string name = 1"),
		}},
	})
	assertContainsCode(t, diagnostics, sourcecomment.CodeInheritedFactChanged)
	for _, item := range diagnostics {
		if item.Code == sourcecomment.CodeInheritedFactChanged && item.EarliestSource != ent.Source.String() {
			t.Fatalf("diagnostic = %#v", item)
		}
	}
}

func TestExtendGraphRejectsProjectionDeletion(t *testing.T) {
	ent := node(t, sourcecomment.StageEnt, sourcecomment.NodeField, "Record.name", "ent://schema/record.go#Record.name", "string")
	upstream, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: []sourcecomment.NodeInput{ent}})
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}

	downstream := mustRef(t, "proto://rpc/record.proto#records.v1.Record.name")
	_, diagnostics = sourcecomment.ExtendGraph(sourcecomment.StandardRegistry(), upstream, sourcecomment.BuildInput{
		Projections: []sourcecomment.ProjectionExpectation{{
			Downstream:              downstream,
			Upstream:                ent.Source,
			SemanticID:              ent.SemanticID,
			Kind:                    sourcecomment.NodeProtoField,
			ExpectedNativeCanonical: []byte("string name = 1"),
			Location:                sourcecomment.Location{File: downstream.Path(), Line: 1, Column: 1},
		}},
	})
	assertCodes(t, diagnostics, sourcecomment.CodeInheritedNodeChanged)
	if diagnostics[0].EarliestSource != ent.Source.String() || diagnostics[0].Expected != ent.SemanticID {
		t.Fatalf("diagnostic = %#v", diagnostics[0])
	}
}
