package entityload

import (
	"reflect"
	"testing"

	"entgo.io/ent/entc/gen"
	"entgo.io/ent/entc/load"
	entfield "entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
)

func TestTask2ProjectsCompiledEntEdgeFacts(t *testing.T) {
	graph, member := task2GraphWithBoundEdge(t)
	account := task2Schema(graph, "Account")
	projection, err := projectGraph(graph, testFactGraph(t, []*load.Schema{account, member}, factOptions{}), nil, testSourceResolver(t, account, member))
	if err != nil {
		t.Fatal(err)
	}
	entity := projection.Entities[1]
	value := reflect.ValueOf(entity)
	edges := value.FieldByName("Edges")
	if !edges.IsValid() {
		t.Fatal("EntityProjection.Edges is missing")
	}
	if edges.Len() != 1 {
		t.Fatalf("edges length = %d, want 1", edges.Len())
	}
	edge := edges.Index(0)
	want := map[string]any{
		"Name": "account", "TargetEntityID": "schema:Account", "Direction": "to",
		"BoundFieldID": "schema:Member/field:account_id", "Optional": false, "Unique": true,
	}
	for name, expected := range want {
		field := reflect.Indirect(edge).FieldByName(name)
		if !field.IsValid() || !reflect.DeepEqual(field.Interface(), expected) {
			t.Fatalf("edge %s = %#v, want %#v", name, field, expected)
		}
	}
}

func TestTask2ProjectsInversePairByBoundFieldSemanticOwner(t *testing.T) {
	graph, account, member := task2GraphWithInverseBoundEdge(t)
	projection, err := projectGraph(graph, testFactGraph(t, []*load.Schema{account, member}, factOptions{}), nil, testSourceResolver(t, account, member))
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 2 {
		t.Fatalf("entities = %d, want 2", len(projection.Entities))
	}
	accountEdge := projection.Entities[0].Edges[0]
	memberEdge := projection.Entities[1].Edges[0]
	wantBound := "schema:Member/field:account_id"
	if accountEdge.Name != "members" || accountEdge.Direction != "from" || accountEdge.TargetEntityID != "schema:Member" || accountEdge.BoundFieldID != wantBound {
		t.Fatalf("Account.members = %#v, Member.account = %#v; want from edge bound by target Member field", accountEdge, memberEdge)
	}
	if memberEdge.Name != "account" || memberEdge.Direction != "to" || memberEdge.TargetEntityID != "schema:Account" || memberEdge.BoundFieldID != wantBound {
		t.Fatalf("Member.account = %#v, want to edge bound by current Member field", memberEdge)
	}
}

func TestTask2RejectsUnsupportedOrUnclosedEntEdges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gen.Graph)
	}{
		{"through", func(graph *gen.Graph) { graph.Nodes[1].Edges[0].Through = graph.Nodes[0] }},
		{"immutable", func(graph *gen.Graph) { graph.Nodes[1].Edges[0].Immutable = true }},
		{"custom annotation", func(graph *gen.Graph) {
			graph.Nodes[1].Edges[0].Annotations = gen.Annotations{"custom": map[string]any{"enabled": true}}
		}},
		{"inverse not closed", func(graph *gen.Graph) { graph.Nodes[1].Edges[0].Inverse = "missing"; graph.Nodes[1].Edges[0].Ref = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph, member := task2GraphWithBoundEdge(t)
			account := task2Schema(graph, "Account")
			test.mutate(graph)
			if _, err := projectGraph(graph, testFactGraph(t, []*load.Schema{account, member}, factOptions{}), nil, testSourceResolver(t, account, member)); err == nil {
				t.Fatal("unsupported or unclosed edge was silently accepted")
			}
		})
	}
}

func TestCRUDProjectionIgnoresRelationshipStructureOutsideGeneratedContract(t *testing.T) {
	graph, member := task2GraphWithBoundEdge(t)
	account := task2Schema(graph, "Account")
	for _, node := range graph.Nodes {
		if node.Name == "Member" {
			node.Edges[0].Immutable = true
		}
	}
	facts := testFactGraph(t, []*load.Schema{account, member}, factOptions{
		crud: map[string][]sourcecomment.CRUDOperation{"Member": {sourcecomment.CRUDList}},
	})
	projection, err := projectCRUDGraph(graph, facts, nil, testSourceResolver(t, account, member))
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 1 || projection.Entities[0].Name != "Member" || len(projection.Entities[0].Edges) != 0 {
		t.Fatalf("CRUD projection = %#v", projection.Entities)
	}
}

func TestTask2RejectsEdgeTargetOutsideTypedProjection(t *testing.T) {
	graph, member := task2GraphWithBoundEdge(t)
	account := task2Schema(graph, "Account")
	for _, node := range graph.Nodes {
		if node.Name == "Account" {
			node.Annotations = nil
		}
	}
	facts := testFactGraph(t, []*load.Schema{account, member}, factOptions{omitSchema: map[string]bool{"Account": true}})
	if _, err := projectGraph(graph, facts, nil, testSourceResolver(t, account, member)); err == nil {
		t.Fatal("edge target outside typed projection was accepted")
	}
}

func task2GraphWithBoundEdge(t *testing.T) (*gen.Graph, *load.Schema) {
	t.Helper()
	account := &load.Schema{
		Name: "Account", Pos: "schema/account.go:10",
		Fields: []*load.Field{{Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString}}},
	}
	member := &load.Schema{
		Name: "Member", Pos: "schema/member.go:10",
		Fields: []*load.Field{{Name: "account_id", Info: &entfield.TypeInfo{Type: entfield.TypeInt}}},
		Edges:  []*load.Edge{{Name: "account", Type: "Account", Field: "account_id", Unique: true, Required: true}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, account, member)
	if err != nil {
		t.Fatal(err)
	}
	return graph, member
}

func task2GraphWithInverseBoundEdge(t *testing.T) (*gen.Graph, *load.Schema, *load.Schema) {
	t.Helper()
	account := &load.Schema{
		Name: "Account", Pos: "schema/account.go:10",
		Fields: []*load.Field{{Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString}}},
		Edges:  []*load.Edge{{Name: "members", Type: "Member"}},
	}
	member := &load.Schema{
		Name: "Member", Pos: "schema/member.go:10",
		Fields: []*load.Field{{Name: "account_id", Info: &entfield.TypeInfo{Type: entfield.TypeInt}}},
		Edges:  []*load.Edge{{Name: "account", Type: "Account", RefName: "members", Inverse: true, Field: "account_id", Unique: true, Required: true}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, account, member)
	if err != nil {
		t.Fatal(err)
	}
	return graph, account, member
}

func task2Schema(graph *gen.Graph, name string) *load.Schema {
	for _, node := range graph.Nodes {
		if node.Name == name {
			fields := make([]*load.Field, 0, len(node.Fields))
			for _, field := range node.Fields {
				fields = append(fields, &load.Field{Name: field.Name, Info: field.Type})
			}
			return &load.Schema{Name: name, Pos: node.Pos(), Fields: fields}
		}
	}
	return nil
}
