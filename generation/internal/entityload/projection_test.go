package entityload

import (
	"errors"
	"strings"
	"testing"

	"entgo.io/ent/entc/gen"
	"entgo.io/ent/entc/load"
	entfield "entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entipc"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

func TestProjectionSkipsNodeWithoutSchemaMetaOrCRUD(t *testing.T) {
	legacy := &load.Schema{
		Name: "LegacyAccount", Pos: "schema/legacy_account.go:10",
		Annotations: map[string]any{
			"NexaSchemaMeta": map[string]any{"identity": "ent-id"},
		},
		Fields: []*load.Field{{
			Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString},
			Annotations: map[string]any{"NexaFieldMeta": map[string]any{"uiHint": "text"}},
		}},
	}
	unannotated := &load.Schema{
		Name: "Unannotated", Pos: "schema/unannotated.go:20",
		Fields: []*load.Field{{Name: "value", Info: &entfield.TypeInfo{Type: entfield.TypeString}}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, legacy, unannotated)
	if err != nil {
		t.Fatal(err)
	}
	resolverCalls := 0
	projection, err := projectGraph(graph, nil, func(string) (provenance.DomainSource, error) {
		resolverCalls++
		return provenance.DomainSource{}, errors.New("resolver must not be called for skipped nodes")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 0 {
		t.Fatalf("entities = %#v, want none", projection.Entities)
	}
	if resolverCalls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolverCalls)
	}
}

func TestProjectGraphIncludesSchemaOnlyNode(t *testing.T) {
	node := &load.Schema{
		Name: "AuditEntry", Pos: "schema/audit_entry.go:10",
		Annotations: typedAnnotations(t, testSchemaMeta("audit_entry"), nil),
		Fields: []*load.Field{{
			Name: "actor", Info: &entfield.TypeInfo{Type: entfield.TypeString},
			Annotations: fieldAnnotations(t, testFieldMeta("audit_entry.actor")),
		}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, nil, testSourceResolver(t, node))
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 1 || projection.Entities[0].Name != "AuditEntry" {
		t.Fatalf("entities = %#v", projection.Entities)
	}
	if projection.Entities[0].CRUD != nil {
		t.Fatalf("CRUD = %#v, want nil", projection.Entities[0].CRUD)
	}
}

func TestProjectionRejectsCRUDWithoutSchemaMeta(t *testing.T) {
	node := &load.Schema{
		Name: "Account", Pos: "schema/account.go:10",
		Annotations: map[string]any{
			nexaent.CRUDAnnotationName: transportValue(t, nexaent.CRUD(nexaent.CRUDList)),
		},
	}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectGraph(graph, nil, func(string) (provenance.DomainSource, error) {
		t.Fatal("resolver called before selection validation")
		return provenance.DomainSource{}, nil
	})
	if err == nil || err.Error() != "schema metadata is required" {
		t.Fatalf("error = %v, want schema metadata is required", err)
	}
}

func TestProjectGraphRequiresFieldMetaForSelectedSchema(t *testing.T) {
	node := &load.Schema{
		Name: "Account", Pos: "schema/account.go:10",
		Annotations: typedAnnotations(t, testSchemaMeta("account"), nil),
		Fields:      []*load.Field{{Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString}}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectGraph(graph, nil, testSourceResolver(t, node))
	if err == nil || err.Error() != "field metadata is required" {
		t.Fatalf("error = %v, want field metadata is required", err)
	}
}

func TestProjectGraphRejectsMalformedCurrentSelectionAnnotation(t *testing.T) {
	node := &load.Schema{
		Name: "Account", Pos: "schema/account.go:10",
		Annotations: map[string]any{
			nexaent.SchemaAnnotationName: map[string]any{"apiVersion": nexaent.SchemaAnnotationName},
		},
	}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectGraph(graph, nil, func(string) (provenance.DomainSource, error) {
		t.Fatal("resolver called before annotation validation")
		return provenance.DomainSource{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), nexaent.SchemaAnnotationName) {
		t.Fatalf("error = %v, want malformed current schema annotation error", err)
	}
}

func TestProjectGraphUsesTypedDescriptorsAndExplicitCRUDOnly(t *testing.T) {
	account := &load.Schema{
		Name: "Account", Pos: "schema/account.go:10",
		Annotations: typedAnnotations(t, testSchemaMeta("account"), []nexaent.CRUDOperation{nexaent.CRUDList, nexaent.CRUDGet}),
		Fields: []*load.Field{
			{Name: "name", Info: &entfield.TypeInfo{Type: entfield.TypeString}, Optional: true, Annotations: fieldAnnotations(t, testFieldMeta("account.name"))},
			{Name: "state", Info: &entfield.TypeInfo{Type: entfield.TypeEnum}, Enums: []struct{ N, V string }{{N: "Active", V: "active"}, {N: "Disabled", V: "disabled"}}, Immutable: true, Default: true, Annotations: fieldAnnotations(t, immutableFieldMeta("account.state"))},
		},
	}
	audit := &load.Schema{
		Name: "AuditEntry", Pos: "schema/audit_entry.go:20",
		Annotations: typedAnnotations(t, testSchemaMeta("audit_entry"), nil),
		Fields:      []*load.Field{{Name: "actor", Info: &entfield.TypeInfo{Type: entfield.TypeString}, Annotations: fieldAnnotations(t, task2NoCRUDFieldMeta("audit_entry.actor"))}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, account, audit)
	if err != nil {
		t.Fatal(err)
	}
	moduleRef, err := provenance.RepositoryRef("go.mod", "")
	if err != nil {
		t.Fatal(err)
	}
	moduleSources := []provenance.Source{{Ref: moduleRef, Digest: provenance.SHA256([]byte("module consumer"))}}
	projection, err := projectGraph(graph, moduleSources, func(position string) (provenance.DomainSource, error) {
		files := map[string]string{
			"schema/account.go:10":     "schema/account.go",
			"schema/audit_entry.go:20": "schema/audit_entry.go",
		}
		return provenance.ParseDomainSource(files[position])
	})
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
	accountEntity := entities[0]
	crud, present := accountEntity.CRUD()
	if !present || !equalOperations(crud.Operations(), []nexaent.CRUDOperation{nexaent.CRUDList, nexaent.CRUDGet}) {
		t.Fatalf("Account CRUD = %#v, %v", crud.Operations(), present)
	}
	if _, present := entities[1].CRUD(); present {
		t.Fatal("annotation absence became CRUD opt-in")
	}
	state, ok := accountEntity.Field("schema:Account/field:state")
	if !ok || state.Type() != "enum" || !state.Immutable() || !state.HasDefault() || len(state.EnumValues()) != 2 {
		t.Fatalf("state projection = %#v, %v", state, ok)
	}
	if got := document.ExecutionModuleSources(); len(got) != 1 || got[0] != moduleSources[0] {
		t.Fatalf("execution module sources = %#v", got)
	}
}

func TestProjectionDoesNotInferTenantFromFieldNameOrScope(t *testing.T) {
	node := &load.Schema{
		Name: "Account", Pos: "schema/account.go:10",
		Annotations: typedAnnotations(t, testSchemaMeta("account"), nil),
		Fields: []*load.Field{{
			Name: "tenant_id", Info: &entfield.TypeInfo{Type: entfield.TypeInt}, Immutable: true,
			Annotations: fieldAnnotations(t, testFieldMeta("account.tenant_id")),
		}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, node)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, nil, testSourceResolver(t, node))
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Entities) != 1 || len(projection.Entities[0].Fields) != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	if projection.Entities[0].Fields[0].IsTenantField {
		t.Fatal("tenant marker inferred from field name or schema scope")
	}
}

func TestEntityLoadProjectsInvalidEntityIRIntoClosedHelperDomainTuple(t *testing.T) {
	schema := &load.Schema{
		Name: "SecretRecord", Pos: "schema/secret_record.go:10", Annotations: typedAnnotations(t, testSchemaMeta("secret_record"), nil),
		Fields: []*load.Field{{
			Name: "secret", Info: &entfield.TypeInfo{Type: entfield.TypeString}, Sensitive: true,
			Annotations: fieldAnnotations(t, task2NoCRUDFieldMeta("secret_record.secret")),
		}},
	}
	graph, err := gen.NewGraph(&gen.Config{}, schema)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, nil, testSourceResolver(t, schema))
	if err != nil {
		t.Fatal(err)
	}
	source, err := provenance.ParseDomainSource("schema")
	if err != nil {
		t.Fatal(err)
	}
	_, err = adoptProjection(projection, source)
	owner, ok := err.(*entity.Error)
	if !ok || owner.Code() != "entity_ir_invalid" || owner.Reason() != "policy_conflict" || owner.Pointer() != "/entities/0/fields/0/fieldMeta/payload/crud" || owner.Source() != source.String() {
		t.Fatalf("load error = %T %#v", err, err)
	}
	_, recognized, transportErr := entipc.ResultFromDomainError(err)
	if transportErr != nil || !recognized {
		t.Fatalf("helper transport = recognized %v, error %v", recognized, transportErr)
	}
}

func typedAnnotations(t *testing.T, meta nexaent.SchemaMeta, operations []nexaent.CRUDOperation) map[string]any {
	t.Helper()
	result := map[string]any{nexaent.SchemaAnnotationName: transportValue(t, nexaent.Schema(meta))}
	if operations != nil {
		result[nexaent.CRUDAnnotationName] = transportValue(t, nexaent.CRUD(operations...))
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

func fieldAnnotations(t *testing.T, meta nexaent.FieldMeta) map[string]any {
	t.Helper()
	return map[string]any{nexaent.FieldAnnotationName: transportValue(t, nexaent.Field(meta))}
}

func testSchemaMeta(prefix string) nexaent.SchemaMeta {
	return nexaent.SchemaMeta{
		Label:       nexaent.LocalizedText{Key: prefix + ".label", ZhCN: prefix, EnUS: prefix},
		Description: nexaent.LocalizedText{Key: prefix + ".description", ZhCN: prefix + " desc", EnUS: prefix + " desc"},
		Identity:    nexaent.IdentityEntID, Scope: nexaent.ScopeTenant,
	}
}

func testFieldMeta(prefix string) nexaent.FieldMeta {
	return nexaent.FieldMeta{
		Label:       nexaent.LocalizedText{Key: prefix + ".label", ZhCN: prefix, EnUS: prefix},
		Description: nexaent.LocalizedText{Key: prefix + ".description", ZhCN: prefix + " desc", EnUS: prefix + " desc"},
		UIHint:      nexaent.UIHintText, Visibility: nexaent.VisibilityPublic,
		CRUD: &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate},
	}
}

func immutableFieldMeta(prefix string) nexaent.FieldMeta {
	meta := testFieldMeta(prefix)
	meta.UIHint = nexaent.UIHintSelect
	meta.CRUD.Mutation = nexaent.MutationCreate
	return meta
}
