package entityload

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"entgo.io/ent/entc/gen"
	entfield "entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/generation/entmixin"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func TestParseEntFactGraphBindsASTNodes(t *testing.T) {
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import "entgo.io/ent/schema/field"

// @nexa label.zh-CN: "记录"
// @nexa label.en-US: "Record"
// @nexa description.zh-CN: "记录数据"
// @nexa description.en-US: "Record data"
// @nexa crud.operations: ["delete","list","create","get","update"]
// @nexa scope: "global"
type Record struct{}

func (Record) Fields() []any {
	return []any{
		// @nexa label.zh-CN: "名称"
		// @nexa label.en-US: "Name"
		// @nexa description.zh-CN: "记录名称"
		// @nexa description.en-US: "Record name"
		// @nexa ui.control: "text"
		// @nexa ui.reference: {"target":"Record","display":"id"}
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create-update"
		field.String("name"),
	}
}

`
	path, err := provenance.ParseDomainSource("backend/records/ent/schema/record.go")
	if err != nil {
		t.Fatal(err)
	}
	field := &gen.Field{Name: "name", Type: &entfield.TypeInfo{Type: entfield.TypeString}}
	graph := &gen.Graph{Nodes: []*gen.Type{{Name: "Record", ID: &gen.Field{Name: "id", Type: &entfield.TypeInfo{Type: entfield.TypeInt}}, Fields: []*gen.Field{field}}}}
	facts, diagnostics, err := parseEntFactGraph(graph, []entCommentSource{{path: path, data: []byte(source)}})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("parseEntFactGraph error=%v diagnostics=%#v", err, diagnostics)
	}
	assertEntFact(t, facts, "Record", "scope", "global")
	assertEntFact(t, facts, "Record.name", "ui.control", "text")
	operations, ok := facts.Fact(sourcecomment.FactID{SemanticID: "Record", Key: "crud.operations"})
	if !ok {
		t.Fatal("CRUD operations fact is missing")
	}
	items, ok := operations.Value().Elements()
	if !ok || len(items) != 5 {
		t.Fatalf("CRUD operations = %#v", operations.Value())
	}
	for index, expected := range []string{"list", "get", "create", "update", "delete"} {
		actual, _ := items[index].String()
		if actual != expected {
			t.Fatalf("operation[%d] = %q, want %q", index, actual, expected)
		}
	}
}

func TestParseEntFactGraphProjectsAnnotatedMixinFieldWithoutBuilderCall(t *testing.T) {
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

// @nexa label.zh-CN: "记录"
// @nexa label.en-US: "Record"
// @nexa description.zh-CN: "记录数据"
// @nexa description.en-US: "Record data"
// @nexa scope: "tenant"
type Record struct{}
`
	path, err := provenance.ParseDomainSource("backend/records/ent/schema/record.go")
	if err != nil {
		t.Fatal(err)
	}
	annotation := entmixin.Tenant{}.Fields()[0].Descriptor().Annotations[0]
	encoded, err := json.Marshal(annotation)
	if err != nil {
		t.Fatal(err)
	}
	var annotationMap map[string]any
	if err := json.Unmarshal(encoded, &annotationMap); err != nil {
		t.Fatal(err)
	}
	tenantField := &gen.Field{Name: "tenant_id", Type: &entfield.TypeInfo{Type: entfield.TypeInt}, Immutable: true, Annotations: map[string]any{entmixin.FieldAnnotationName: annotationMap}}
	graph := &gen.Graph{Nodes: []*gen.Type{{Name: "Record", ID: &gen.Field{Name: "id", Type: &entfield.TypeInfo{Type: entfield.TypeInt}}, Fields: []*gen.Field{tenantField}}}}
	facts, diagnostics, err := parseEntFactGraph(graph, []entCommentSource{{path: path, data: []byte(source)}})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("parseEntFactGraph error=%v diagnostics=%#v", err, diagnostics)
	}
	fieldFacts, err := facts.FieldFacts("Record.tenant_id")
	if err != nil {
		t.Fatal(err)
	}
	if fieldFacts.Label.ZhCN != "租户 ID" || fieldFacts.Label.EnUS != "Tenant ID" || fieldFacts.Visibility != sourcecomment.VisibilityInternal || fieldFacts.Control != sourcecomment.UIControlReadonly {
		t.Fatalf("mixin field facts = %#v", fieldFacts)
	}
	if fieldFacts.CRUD != nil {
		t.Fatalf("mixin CRUD facts = %#v", fieldFacts.CRUD)
	}
	native, err := canonicalEntField(tenantField, false)
	if err != nil || !bytes.Contains(native, []byte(`"isTenantField":true`)) {
		t.Fatalf("mixin native field = %s, error=%v", native, err)
	}
}

func TestParseEntFactGraphRejectsNonAdjacentAndDuplicateFacts(t *testing.T) {
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import "entgo.io/ent/schema/field"

// @nexa scope: "global"

type Record struct{}

func (Record) Fields() []any {
	return []any{
		// @nexa visibility: "public"
		// @nexa visibility: "internal"
		field.String("name"),
	}
}
`
	path, _ := provenance.ParseDomainSource("backend/records/ent/schema/record.go")
	graph := &gen.Graph{Nodes: []*gen.Type{{Name: "Record", ID: &gen.Field{Name: "id", Type: &entfield.TypeInfo{Type: entfield.TypeInt}}, Fields: []*gen.Field{{Name: "name", Type: &entfield.TypeInfo{Type: entfield.TypeString}}}}}}
	_, diagnostics, err := parseEntFactGraph(graph, []entCommentSource{{path: path, data: []byte(source)}})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[sourcecomment.Code]bool{}
	for _, diagnostic := range diagnostics {
		codes[diagnostic.Code] = true
	}
	if !codes[sourcecomment.CodeInvalidTarget] || !codes[sourcecomment.CodeDuplicateFact] {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestParseEntFactGraphRejectsSourceForgery(t *testing.T) {
	source := `// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

// @nexa $source: "proto://backend/records/record.proto#Record"
type Record struct{}
`
	path, _ := provenance.ParseDomainSource("backend/records/ent/schema/record.go")
	graph := &gen.Graph{Nodes: []*gen.Type{{Name: "Record", ID: &gen.Field{Name: "id", Type: &entfield.TypeInfo{Type: entfield.TypeInt}}}}}
	_, diagnostics, err := parseEntFactGraph(graph, []entCommentSource{{path: path, data: []byte(source)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != sourcecomment.CodeSourceMismatch {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestEntFieldCallRequiresASTFieldBuilder(t *testing.T) {
	set := tokenFileForTest(t, `package schema
func Fields() { fake.String("name") }
`)
	var callName string
	astInspectCalls(set.file, func(call *ast.CallExpr) {
		if name, ok := entFieldCall(call); ok {
			callName = name
		}
	})
	if callName != "" {
		t.Fatalf("non-Ent builder bound as %q", callName)
	}
}

type parsedGoForTest struct{ file *ast.File }

func tokenFileForTest(t *testing.T, source string) parsedGoForTest {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "sample.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return parsedGoForTest{file: file}
}

func astInspectCalls(file *ast.File, visit func(*ast.CallExpr)) {
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			visit(call)
		}
		return true
	})
}

func assertEntFact(t *testing.T, graph sourcecomment.FactGraph, semanticID, key, expected string) {
	t.Helper()
	fact, ok := graph.Fact(sourcecomment.FactID{SemanticID: semanticID, Key: key})
	if !ok {
		t.Fatalf("fact %s:%s is missing", semanticID, key)
	}
	actual, ok := fact.Value().String()
	if !ok || actual != expected {
		t.Fatalf("fact %s:%s = %q, want %q", semanticID, key, actual, expected)
	}
}
