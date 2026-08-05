package entityload

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"entgo.io/ent/entc/gen"
	"entgo.io/ent/entc/load"
	entfield "entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/entmixin"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func TestProjectionSkipsNodeWithoutSourceFacts(t *testing.T) {
	legacy := &load.Schema{Name: "LegacyAccount", Pos: "schema/legacy_account.go:10", Fields: []*load.Field{{Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString}}}}
	unannotated := &load.Schema{Name: "Unannotated", Pos: "schema/unannotated.go:20", Fields: []*load.Field{{Name: "value", Info: &entfield.TypeInfo{Type: entfield.TypeString}}}}
	graph, err := gen.NewGraph(&gen.Config{}, legacy, unannotated)
	if err != nil {
		t.Fatal(err)
	}
	resolverCalls := 0
	projection, err := projectGraph(graph, sourcecomment.FactGraph{}, nil, func(string) (provenance.DomainSource, error) {
		resolverCalls++
		return provenance.DomainSource{}, errors.New("resolver must not be called for skipped nodes")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 0 || resolverCalls != 0 {
		t.Fatalf("projection = %#v, resolver calls = %d", projection.Entities, resolverCalls)
	}
}

func TestProjectGraphIncludesSchemaOnlyNode(t *testing.T) {
	node := &load.Schema{Name: "AuditEntry", Pos: "schema/audit_entry.go:10", Fields: []*load.Field{{Name: "actor", Info: &entfield.TypeInfo{Type: entfield.TypeString}}}}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, testFactGraph(t, []*load.Schema{node}, factOptions{}), nil, testSourceResolver(t, node))
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 1 || projection.Entities[0].Name != "AuditEntry" || projection.Entities[0].CRUD != nil {
		t.Fatalf("entities = %#v", projection.Entities)
	}
}

func TestProjectionRejectsCRUDWithoutSchemaFacts(t *testing.T) {
	node := &load.Schema{Name: "Account", Pos: "schema/account.go:10"}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	facts := testFactGraph(t, []*load.Schema{node}, factOptions{omitSchema: map[string]bool{"Account": true}, crud: map[string][]sourcecomment.CRUDOperation{"Account": {sourcecomment.CRUDList}}})
	_, err = projectGraph(graph, facts, nil, func(string) (provenance.DomainSource, error) {
		t.Fatal("resolver called before fact validation")
		return provenance.DomainSource{}, nil
	})
	if err == nil || err.Error() != "schema facts are required" {
		t.Fatalf("error = %v, want schema facts are required", err)
	}
}

func TestProjectGraphRequiresFieldFactsForSelectedSchema(t *testing.T) {
	node := &load.Schema{Name: "Account", Pos: "schema/account.go:10", Fields: []*load.Field{{Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString}}}}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	facts := testFactGraph(t, []*load.Schema{node}, factOptions{omitFields: map[string]bool{"Account.name": true}})
	_, err = projectGraph(graph, facts, nil, testSourceResolver(t, node))
	if err == nil || err.Error() != "fact Account.name:label.zh-CN is required" {
		t.Fatalf("error = %v, want missing field fact error", err)
	}
}

func TestProjectGraphUsesEntDescriptorsAndExplicitCRUDOnly(t *testing.T) {
	account := &load.Schema{Name: "Account", Pos: "schema/account.go:10", Fields: []*load.Field{
		{Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString}, Optional: true},
		{Name: "state", Info: &entfield.TypeInfo{Type: entfield.TypeEnum}, Enums: []struct{ N, V string }{{N: "Active", V: "active"}, {N: "Disabled", V: "disabled"}}, Immutable: true, Default: true},
	}}
	audit := &load.Schema{Name: "AuditEntry", Pos: "schema/audit_entry.go:20", Fields: []*load.Field{{Name: "actor", Info: &entfield.TypeInfo{Type: entfield.TypeString}}}}
	graph, err := gen.NewGraph(&gen.Config{}, account, audit)
	if err != nil {
		t.Fatal(err)
	}
	moduleRef, err := provenance.RepositoryRef("go.mod", "")
	if err != nil {
		t.Fatal(err)
	}
	moduleSources := []provenance.Source{{Ref: moduleRef, Digest: provenance.SHA256([]byte("module consumer"))}}
	facts := testFactGraph(t, []*load.Schema{account, audit}, factOptions{
		crud:       map[string][]sourcecomment.CRUDOperation{"Account": {sourcecomment.CRUDList, sourcecomment.CRUDGet}},
		fieldFacts: map[string]sourcecomment.FieldFacts{"Account.state": immutableFieldFacts("account.state")},
	})
	projection, err := projectGraph(graph, facts, moduleSources, testSourceResolver(t, account, audit))
	if err != nil {
		t.Fatal(err)
	}
	document, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	entities := document.Entities()
	if len(entities) != 2 || entities[0].Name() != "Account" || entities[1].Name() != "AuditEntry" {
		t.Fatalf("entities = %#v", entities)
	}
	crud, present := entities[0].CRUD()
	if !present || !equalOperations(crud.Operations(), []sourcecomment.CRUDOperation{sourcecomment.CRUDList, sourcecomment.CRUDGet}) {
		t.Fatalf("Account CRUD = %#v, %v", crud.Operations(), present)
	}
	if _, present := entities[1].CRUD(); present {
		t.Fatal("source fact absence became CRUD opt-in")
	}
	state, ok := entities[0].Field("schema:Account/field:state")
	if !ok || state.Type() != "enum" || !state.Immutable() || !state.HasDefault() || len(state.EnumValues()) != 2 {
		t.Fatalf("state projection = %#v, %v", state, ok)
	}
	if got := document.ExecutionModuleSources(); len(got) != 1 || got[0] != moduleSources[0] {
		t.Fatalf("execution module sources = %#v", got)
	}
}

func TestProjectionDoesNotInferTenantFromFieldNameOrScope(t *testing.T) {
	node := &load.Schema{Name: "Account", Pos: "schema/account.go:10", Fields: []*load.Field{{Name: "tenant_id", Info: &entfield.TypeInfo{Type: entfield.TypeInt}, Immutable: true}}}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, testFactGraph(t, []*load.Schema{node}, factOptions{}), nil, testSourceResolver(t, node))
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 1 || len(projection.Entities[0].Fields) != 1 || projection.Entities[0].Fields[0].IsTenantField {
		t.Fatalf("projection = %#v", projection)
	}
}

func TestProjectionRecognizesAnnotatedTenantField(t *testing.T) {
	annotation := entmixin.Tenant{}.Fields()[0].Descriptor().Annotations[0]
	encoded, err := json.Marshal(annotation)
	if err != nil {
		t.Fatal(err)
	}
	var annotationMap map[string]any
	if err := json.Unmarshal(encoded, &annotationMap); err != nil {
		t.Fatal(err)
	}
	node := &load.Schema{Name: "Account", Pos: "schema/account.go:10", Fields: []*load.Field{{Name: "tenant_id", Info: &entfield.TypeInfo{Type: entfield.TypeInt}, Immutable: true, Annotations: map[string]any{entmixin.FieldAnnotationName: annotationMap}}}}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, testFactGraph(t, []*load.Schema{node}, factOptions{}), nil, testSourceResolver(t, node))
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 1 || len(projection.Entities[0].Fields) != 1 || !projection.Entities[0].Fields[0].IsTenantField {
		t.Fatalf("projection = %#v", projection)
	}
}

func TestEntityLoadProjectsSensitivePolicyConflictIntoDomainTuple(t *testing.T) {
	schema := &load.Schema{Name: "SecretRecord", Pos: "schema/secret_record.go:10", Fields: []*load.Field{{Name: "secret", Info: &entfield.TypeInfo{Type: entfield.TypeString}, Sensitive: true}}}
	graph, err := gen.NewGraph(&gen.Config{}, schema)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, testFactGraph(t, []*load.Schema{schema}, factOptions{}), nil, testSourceResolver(t, schema))
	if err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("schema")
	if err != nil {
		t.Fatal(err)
	}
	_, err = adoptProjection(projection, testFactGraph(t, []*load.Schema{schema}, factOptions{}), source)
	owner, ok := err.(*entity.Error)
	if !ok || owner.Code() != "entity_ir_invalid" || owner.Reason() != "policy_conflict" || owner.Pointer() != "/entities/0/fields/0/fieldFacts/crud" || owner.Source() != source.String() {
		t.Fatalf("load error = %T %#v", err, err)
	}
}

type factOptions struct {
	omitSchema map[string]bool
	omitFields map[string]bool
	crud       map[string][]sourcecomment.CRUDOperation
	fieldFacts map[string]sourcecomment.FieldFacts
}

func testFactGraph(t *testing.T, schemas []*load.Schema, options factOptions) sourcecomment.FactGraph {
	t.Helper()
	var nodes []sourcecomment.NodeInput
	for _, schema := range schemas {
		file := strings.Split(schema.Pos, ":")[0]
		if !options.omitSchema[schema.Name] {
			target := testFactTarget(t, sourcecomment.NodeSchema, schema.Name, file, schema.Name)
			meta := testSchemaFacts(strings.ToLower(schema.Name))
			nodes = append(nodes, sourcecomment.NodeInput{SemanticID: schema.Name, Kind: sourcecomment.NodeSchema, Stage: sourcecomment.StageEnt, Source: target.Source, Location: sourcecomment.Location{File: file, Line: 1}, NativeCanonical: []byte("schema:" + schema.Name), Facts: schemaDirectives(t, target, meta, options.crud[schema.Name])})
		} else if operations := options.crud[schema.Name]; len(operations) > 0 {
			target := testFactTarget(t, sourcecomment.NodeSchema, schema.Name, file, schema.Name)
			nodes = append(nodes, sourcecomment.NodeInput{SemanticID: schema.Name, Kind: sourcecomment.NodeSchema, Stage: sourcecomment.StageEnt, Source: target.Source, Location: sourcecomment.Location{File: file, Line: 1}, NativeCanonical: []byte("schema:" + schema.Name), Facts: []sourcecomment.Directive{testDirective(t, target, "crud.operations", operations)}})
		}
		for _, field := range schema.Fields {
			semanticID := schema.Name + "." + field.Name
			if options.omitFields[semanticID] {
				continue
			}
			target := testFactTarget(t, sourcecomment.NodeField, semanticID, file, semanticID)
			meta, ok := options.fieldFacts[semanticID]
			if !ok {
				meta = testFieldFacts(strings.ToLower(strings.ReplaceAll(semanticID, ".", "_")))
				if len(options.crud[schema.Name]) == 0 {
					meta.CRUD = nil
				}
			}
			nodes = append(nodes, sourcecomment.NodeInput{SemanticID: semanticID, Kind: sourcecomment.NodeField, Stage: sourcecomment.StageEnt, Source: target.Source, Location: sourcecomment.Location{File: file, Line: 2}, NativeCanonical: []byte("field:" + semanticID), Facts: fieldDirectives(t, target, meta)})
		}
	}
	graph, diagnostics := sourcecomment.BuildGraph(sourcecomment.StandardRegistry(), sourcecomment.BuildInput{Nodes: nodes})
	if len(diagnostics) != 0 {
		t.Fatalf("build test fact graph: %#v", diagnostics)
	}
	return graph
}

func testFactTarget(t *testing.T, kind sourcecomment.NodeKind, semanticID, file, symbol string) sourcecomment.Target {
	t.Helper()
	ref, err := sourcecomment.ParseSourceRef("ent://" + file + "#" + symbol)
	if err != nil {
		t.Fatal(err)
	}
	return sourcecomment.Target{SemanticID: semanticID, Kind: kind, Stage: sourcecomment.StageEnt, Source: ref}
}

func testDirective(t *testing.T, target sourcecomment.Target, key string, value any) sourcecomment.Directive {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	directive, selected, diagnostic := sourcecomment.ParseLine(sourcecomment.Line{Text: "// @nexa " + key + ": " + string(encoded), CommentPrefix: "//", Location: sourcecomment.Location{File: target.Source.Path(), Line: 1, Column: 1}, Target: &target})
	if diagnostic != nil || !selected {
		t.Fatalf("parse %s: selected=%v diagnostic=%#v", key, selected, diagnostic)
	}
	return directive
}

func schemaDirectives(t *testing.T, target sourcecomment.Target, meta sourcecomment.SchemaFacts, operations []sourcecomment.CRUDOperation) []sourcecomment.Directive {
	values := []struct {
		key   string
		value any
	}{{"label.zh-CN", meta.Label.ZhCN}, {"label.en-US", meta.Label.EnUS}, {"description.zh-CN", meta.Description.ZhCN}, {"description.en-US", meta.Description.EnUS}, {"scope", meta.Scope}}
	result := make([]sourcecomment.Directive, 0, len(values)+1)
	for _, value := range values {
		result = append(result, testDirective(t, target, value.key, value.value))
	}
	if len(operations) > 0 {
		result = append(result, testDirective(t, target, "crud.operations", operations))
	}
	return result
}

func fieldDirectives(t *testing.T, target sourcecomment.Target, meta sourcecomment.FieldFacts) []sourcecomment.Directive {
	values := []struct {
		key   string
		value any
	}{{"label.zh-CN", meta.Label.ZhCN}, {"label.en-US", meta.Label.EnUS}, {"description.zh-CN", meta.Description.ZhCN}, {"description.en-US", meta.Description.EnUS}, {"ui.control", meta.Control}, {"visibility", meta.Visibility}}
	result := make([]sourcecomment.Directive, 0, len(values)+2)
	for _, value := range values {
		result = append(result, testDirective(t, target, value.key, value.value))
	}
	if meta.CRUD != nil {
		result = append(result, testDirective(t, target, "crud.read", meta.CRUD.Read), testDirective(t, target, "crud.mutation", meta.CRUD.Mutation))
	}
	return result
}

func testSourceResolver(t *testing.T, schemas ...*load.Schema) sourceResolver {
	t.Helper()
	files := make(map[string]string, len(schemas))
	for _, schema := range schemas {
		files[schema.Pos] = strings.Split(schema.Pos, ":")[0]
	}
	return func(position string) (provenance.DomainSource, error) {
		file, ok := files[position]
		if !ok {
			t.Fatalf("unexpected position %q", position)
		}
		return provenance.ParseDomainSource(file)
	}
}

func testSchemaFacts(prefix string) sourcecomment.SchemaFacts {
	return sourcecomment.SchemaFacts{Label: sourcecomment.LocalizedText{Key: prefix + ".label", ZhCN: prefix, EnUS: prefix}, Description: sourcecomment.LocalizedText{Key: prefix + ".description", ZhCN: prefix + " desc", EnUS: prefix + " desc"}, Scope: sourcecomment.ScopeTenant}
}

func testFieldFacts(prefix string) sourcecomment.FieldFacts {
	return sourcecomment.FieldFacts{Label: sourcecomment.LocalizedText{Key: prefix + ".label", ZhCN: prefix, EnUS: prefix}, Description: sourcecomment.LocalizedText{Key: prefix + ".description", ZhCN: prefix + " desc", EnUS: prefix + " desc"}, Control: sourcecomment.UIControlText, Visibility: sourcecomment.VisibilityPublic, CRUD: &sourcecomment.CRUDFieldPolicy{Read: sourcecomment.ReadInclude, Mutation: sourcecomment.MutationCreateUpdate}}
}

func immutableFieldFacts(prefix string) sourcecomment.FieldFacts {
	meta := testFieldFacts(prefix)
	meta.Control = sourcecomment.UIControlSelect
	meta.CRUD.Mutation = sourcecomment.MutationCreate
	return meta
}

func equalOperations(left, right []sourcecomment.CRUDOperation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
