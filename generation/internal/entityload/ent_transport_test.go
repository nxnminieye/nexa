package entityload

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"entgo.io/ent/entc"
	"entgo.io/ent/entc/gen"
	"entgo.io/ent/entc/load"
	entfield "entgo.io/ent/schema/field"
	"github.com/google/uuid"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/nexaent"
	nexamixin "github.com/nxnminieye/nexa/nexaent/mixin"
	"github.com/nxnminieye/nexa/provenance"
)

func TestProjectionRecognizesOnlyStrictTenantMarker(t *testing.T) {
	marker := transportValue(t, nexamixin.Tenant{}.Fields()[0].Descriptor().Annotations[1])
	got, present, err := decodeTenantAnnotation(gen.Annotations{nexamixin.TenantAnnotationName: marker})
	if err != nil || !present || !got {
		t.Fatalf("tenant annotation = %v, %v, %v", got, present, err)
	}
	got, present, err = decodeTenantAnnotation(gen.Annotations{"tenant_id": marker})
	if err != nil || present || got {
		t.Fatalf("unowned annotation = %v, %v, %v", got, present, err)
	}
}

func TestProjectionRejectsMalformedTenantMarker(t *testing.T) {
	annotations := gen.Annotations{nexamixin.TenantAnnotationName: map[string]any{
		"apiVersion": nexamixin.TenantAnnotationName,
		"kind":       "EntTenantField",
		"payload":    map[string]any{"enabled": true},
	}}
	if _, present, err := decodeTenantAnnotation(annotations); err == nil || !present {
		t.Fatalf("malformed tenant annotation = present %v, error %v", present, err)
	}
}

func TestTypedAnnotationTransportDecodesExactOwnersAndCRUDAbsence(t *testing.T) {
	schemaMeta := testSchemaMeta("account")
	fieldMeta := testFieldMeta("account.name")
	crudAnnotation := nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet)
	annotations := gen.Annotations{
		nexaent.SchemaAnnotationName: transportValue(t, nexaent.Schema(schemaMeta)),
		nexaent.FieldAnnotationName:  transportValue(t, nexaent.Field(fieldMeta)),
		nexaent.CRUDAnnotationName:   transportValue(t, crudAnnotation),
		"example.com/unrelated":      map[string]any{"ignored": true},
	}

	gotSchema, present, err := decodeSchemaAnnotation(annotations)
	if err != nil || !present || gotSchema != schemaMeta {
		t.Fatalf("schema annotation = %#v, %v, %v", gotSchema, present, err)
	}
	gotField, present, err := decodeFieldAnnotation(annotations)
	if err != nil || !present || gotField.Label != fieldMeta.Label || gotField.UIHint != fieldMeta.UIHint {
		t.Fatalf("field annotation = %#v, %v, %v", gotField, present, err)
	}
	gotCRUD, present, err := decodeCRUDAnnotation(annotations)
	if err != nil || !present || !equalOperations(gotCRUD.Operations(), []nexaent.CRUDOperation{nexaent.CRUDList, nexaent.CRUDGet}) {
		t.Fatalf("CRUD annotation = %#v, %v, %v", gotCRUD.Operations(), present, err)
	}
	if _, present, err := decodeCRUDAnnotation(gen.Annotations{}); err != nil || present {
		t.Fatalf("CRUD absence = %v, %v", present, err)
	}
}

func TestGenericUIHintsPreserveTypedEntTransportEquality(t *testing.T) {
	for _, hint := range []nexaent.UIHint{nexaent.UIHintLocale, nexaent.UIHintTimezone} {
		t.Run(string(hint), func(t *testing.T) {
			want := testFieldMeta("account.preference")
			want.UIHint = hint
			annotations := gen.Annotations{
				nexaent.FieldAnnotationName: transportValue(t, nexaent.Field(want)),
			}

			got, present, err := decodeFieldAnnotation(annotations)
			if err != nil || !present {
				t.Fatalf("field annotation = %#v, present %v, error %v", got, present, err)
			}
			if !reflect.DeepEqual(got, want) || got.UIHint != hint {
				t.Fatalf("typed field annotation = %#v, want exact %#v", got, want)
			}
		})
	}
}

func TestCRUDTypeQualificationAllowsEntExactModelsWithoutChangingEntityIR(t *testing.T) {
	fields := []*load.Field{
		loadedCRUDField(t, entfield.Bool("bool_value").Descriptor()),
		loadedCRUDField(t, entfield.Bytes("bytes_value").Descriptor()),
		loadedCRUDField(t, entfield.Float32("float32_value").Descriptor()),
		loadedCRUDField(t, entfield.Float("float64_value").Descriptor()),
		loadedCRUDField(t, entfield.Int16("int16_value").Descriptor()),
		loadedCRUDField(t, entfield.Int32("int32_value").Descriptor()),
		loadedCRUDField(t, entfield.Int64("int64_value").Descriptor()),
		loadedCRUDField(t, entfield.Int8("int8_value").Descriptor()),
		loadedCRUDField(t, entfield.Int("int_value").Descriptor()),
		loadedCRUDField(t, entfield.String("string_value").Descriptor()),
		loadedCRUDField(t, entfield.Time("time_value").Descriptor()),
		loadedCRUDField(t, entfield.Uint16("uint16_value").Descriptor()),
		loadedCRUDField(t, entfield.Uint32("uint32_value").Descriptor()),
		loadedCRUDField(t, entfield.Uint64("uint64_value").Descriptor()),
		loadedCRUDField(t, entfield.Uint8("uint8_value").Descriptor()),
		loadedCRUDField(t, entfield.Uint("uint_value").Descriptor()),
		loadedCRUDField(t, entfield.UUID("uuid_value", uuid.UUID{}).Descriptor()),
		loadedCRUDField(t, entfield.Any("json_any").Descriptor()),
		loadedCRUDField(t, entfield.JSON("json_array", [3]string{}).Descriptor()),
		loadedCRUDField(t, entfield.Floats("json_floats").Descriptor()),
		loadedCRUDField(t, entfield.Ints("json_ints").Descriptor()),
		loadedCRUDField(t, entfield.JSON("json_map", map[string]string{}).Descriptor()),
		loadedCRUDField(t, entfield.JSON("json_pointer", &qualificationPayload{}).Descriptor()),
		loadedCRUDField(t, entfield.JSON("json_raw", json.RawMessage{}).Descriptor()),
		loadedCRUDField(t, entfield.Strings("json_strings").Descriptor()),
		loadedCRUDField(t, entfield.JSON("json_struct", qualificationPayload{}).Descriptor()),
	}
	fields = append(fields, loadedCRUDField(t, entfield.Enum("enum_value").Values("enabled").Descriptor()))
	schema := crudTypeSchema(t, "Account", fields, nexaent.AllCRUDOperations())

	graph, err := gen.NewGraph(&gen.Config{}, schema)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, nil, testSourceResolver(t, schema))
	if err != nil {
		t.Fatal(err)
	}
	document, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	if document.APIVersion() != entity.APIVersion {
		t.Fatalf("api version = %q, want %q", document.APIVersion(), entity.APIVersion)
	}
	account := document.Entities()[0]
	wantScalars := map[string]string{
		"int8_value": "int64", "int16_value": "int64", "int32_value": "int64", "int_value": "int64", "int64_value": "int64",
		"uint8_value": "uint64", "uint16_value": "uint64", "uint32_value": "uint64", "uint_value": "uint64", "uint64_value": "uint64",
		"bool_value": "bool", "string_value": "string", "float32_value": "float", "float64_value": "double",
		"bytes_value": "bytes", "time_value": "timestamp", "enum_value": "enum", "uuid_value": "uuid",
		"json_any": "json", "json_array": "json", "json_floats": "json", "json_ints": "json", "json_map": "json",
		"json_pointer": "json", "json_raw": "json", "json_strings": "json", "json_struct": "json",
	}
	for name, want := range wantScalars {
		field, ok := account.Field("schema:Account/field:" + name)
		if !ok || field.Type() != want {
			t.Fatalf("field %s = %#v, want scalar %q", name, field, want)
		}
	}

	variant := crudTypeSchema(t, "Account", []*load.Field{
		crudJSONField(t, "payload", "fixture.Payload", reflect.Struct, "Payload", "example.com/fixture"),
	}, []nexaent.CRUDOperation{nexaent.CRUDCreate})
	variant2 := crudTypeSchema(t, "Account", []*load.Field{
		crudJSONField(t, "payload", "map[string]any", reflect.Map, "", ""),
	}, []nexaent.CRUDOperation{nexaent.CRUDCreate})
	first := projectedDocument(t, variant)
	second := projectedDocument(t, variant2)
	if !reflect.DeepEqual(first.CanonicalJSON(), second.CanonicalJSON()) {
		t.Fatal("exact JSON model leaked into EntityIR canonical bytes")
	}
}

func TestCRUDTypeQualificationGoogleUUIDDoesNotDependOnReflectKind(t *testing.T) {
	info := loadedTypeInfo(t, entfield.UUID("value", uuid.UUID{}).Descriptor())
	info.RType.Kind = reflect.String
	if !crudExactTypeSupported(info) {
		t.Fatal("Google UUID qualification depended on reflect.Kind instead of Ent type identity")
	}
}

func TestProjectLoadedGraphTransportsOperationSensitiveTypeErrorFromEntGraph(t *testing.T) {
	frameworkRoot := qualificationFrameworkRoot(t)
	testdataRoot := filepath.Join(frameworkRoot, "generation", "internal", "entityload", "testdata")
	if err := os.Mkdir(testdataRoot, 0o700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(testdataRoot) })
	fixtureRoot, err := os.MkdirTemp(testdataRoot, "crud-type-qualification-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureRoot) })
	schemaRoot := filepath.Join(fixtureRoot, "schema")
	writeQualificationFile(t, filepath.Join(schemaRoot, "account.go"), `package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

type Code string
type Account struct{ ent.Schema }

func (Account) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		nexaent.Schema(nexaent.SchemaMeta{
			Label: nexaent.LocalizedText{Key: "account.label", ZhCN: "Account", EnUS: "Account"},
			Description: nexaent.LocalizedText{Key: "account.description", ZhCN: "Account", EnUS: "Account"},
			Identity: nexaent.IdentityEntID,
			Scope: nexaent.ScopeGlobal,
		}),
		nexaent.CRUD(nexaent.CRUDCreate),
	}
}

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").GoType(Code("")).Annotations(nexaent.Field(nexaent.FieldMeta{
			Label: nexaent.LocalizedText{Key: "account.code.label", ZhCN: "Code", EnUS: "Code"},
			Description: nexaent.LocalizedText{Key: "account.code.description", ZhCN: "Code", EnUS: "Code"},
			UIHint: nexaent.UIHintText,
			Visibility: nexaent.VisibilityPublic,
			CRUD: &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationCreate},
		})),
	}
}
`)
	relative, err := filepath.Rel(frameworkRoot, schemaRoot)
	if err != nil {
		t.Fatal(err)
	}
	schemaSource, err := provenance.ParseDomainSource(filepath.ToSlash(relative))
	if err != nil {
		t.Fatal(err)
	}
	graph, err := entc.LoadGraph("github.com/nxnminieye/nexa/"+schemaSource.String(), &gen.Config{
		Target: filepath.Join(t.TempDir(), "generated"), BuildFlags: []string{"-mod=readonly"},
	})
	if err != nil {
		t.Fatalf("load explicit testdata schema: %v", err)
	}
	schemaFile, err := provenance.ParseDomainSource(schemaSource.String() + "/account.go")
	if err != nil {
		t.Fatal(err)
	}
	expectedFile := filepath.Join(schemaRoot, "account.go")
	_, err = projectLoadedGraph(graph, nil, func(position string) (provenance.DomainSource, error) {
		filename, err := sourceFilename(position)
		if err != nil {
			return provenance.DomainSource{}, err
		}
		if !filepath.IsAbs(filename) {
			filename = filepath.Join(frameworkRoot, filename)
		}
		actual, err := filepath.EvalSymlinks(filename)
		if err != nil || filepath.Clean(actual) != expectedFile {
			return provenance.DomainSource{}, fmt.Errorf("unexpected schema position %q", position)
		}
		return schemaFile, nil
	}, schemaSource)
	owner, ok := err.(*entity.Error)
	if !ok || owner.Code() != "entity_ir_invalid" || owner.Reason() != "field_type_unsupported" || owner.Pointer() != "/entities/0/fields/0/type" || owner.Source() != schemaSource.String() {
		t.Fatalf("loader tuple = %T %#v, want entity_ir_invalid/field_type_unsupported at %s from %s", err, err, "/entities/0/fields/0/type", schemaSource.String())
	}
}

func TestCRUDTypeQualificationRejectsParticipatingCustomGoTypes(t *testing.T) {
	tests := []struct {
		name       string
		info       *entfield.TypeInfo
		operations []nexaent.CRUDOperation
		meta       nexaent.FieldMeta
	}{
		{name: "custom bool", info: loadedTypeInfo(t, entfield.Bool("value").GoType(qualificationBool(false)).Descriptor())},
		{name: "custom enum", info: loadedTypeInfo(t, entfield.Enum("value").Values("ready").GoType(qualificationEnum("")).Descriptor())},
		{name: "named scalar", info: loadedTypeInfo(t, entfield.String("value").GoType(qualificationString("")).Descriptor())},
		{name: "named time", info: loadedTypeInfo(t, entfield.Time("value").GoType(qualificationTime{}).Descriptor())},
		{name: "named bytes", info: loadedTypeInfo(t, entfield.Bytes("value").GoType(qualificationBytes{}).Descriptor())},
		{name: "non google uuid", info: loadedTypeInfo(t, entfield.UUID("value", qualificationUUID{}).Descriptor())},
		{name: "create mutation despite read exclusion", info: customType(entfield.TypeString, "fixture.Code", reflect.String, "Code", "example.com/fixture"), operations: []nexaent.CRUDOperation{nexaent.CRUDCreate}, meta: excludedFieldMeta("account.value", nexaent.MutationCreate)},
		{name: "update mutation despite read exclusion", info: customType(entfield.TypeString, "fixture.Code", reflect.String, "Code", "example.com/fixture"), operations: []nexaent.CRUDOperation{nexaent.CRUDUpdate}, meta: excludedFieldMeta("account.value", nexaent.MutationUpdate)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field := crudTypeField(t, "value", test.info)
			if test.meta.CRUD != nil {
				field.Annotations = fieldAnnotations(t, test.meta)
			}
			if test.info.Type == entfield.TypeEnum {
				field.Enums = []struct{ N, V string }{{N: "Ready", V: "ready"}}
			}
			operations := test.operations
			if operations == nil {
				operations = []nexaent.CRUDOperation{nexaent.CRUDCreate}
			}
			schema := crudTypeSchema(t, "Account", []*load.Field{field}, operations)
			assertCRUDQualificationError(t, schema, "/entities/0/fields/0/type")
		})
	}
}

func TestCRUDTypeQualificationIsOperationSensitive(t *testing.T) {
	custom := customType(entfield.TypeString, "fixture.Code", reflect.String, "Code", "example.com/fixture")
	tests := []struct {
		name       string
		operations []nexaent.CRUDOperation
		meta       nexaent.FieldMeta
		immutable  bool
	}{
		{name: "zero crud", operations: nil, meta: task2NoCRUDFieldMeta("account.value")},
		{name: "read excluded", operations: []nexaent.CRUDOperation{nexaent.CRUDList}, meta: excludedFieldMeta("account.value", nexaent.MutationNone)},
		{name: "immutable create only during update", operations: []nexaent.CRUDOperation{nexaent.CRUDUpdate}, meta: excludedFieldMeta("account.value", nexaent.MutationCreate), immutable: true},
		{name: "delete does not read ordinary fields", operations: []nexaent.CRUDOperation{nexaent.CRUDDelete}, meta: testFieldMeta("account.value")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field := &load.Field{Name: "value", Info: custom, Immutable: test.immutable, Annotations: fieldAnnotations(t, test.meta)}
			schema := crudTypeSchema(t, "Account", []*load.Field{field}, test.operations)
			graph, err := gen.NewGraph(&gen.Config{}, schema)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := projectGraph(graph, nil, testSourceResolver(t, schema)); err != nil {
				t.Fatalf("unused custom type was rejected: %v", err)
			}
		})
	}
}

func TestCRUDTypeQualificationUsesIdentityPointer(t *testing.T) {
	id := crudTypeField(t, "id", customType(entfield.TypeString, "fixture.AccountID", reflect.String, "AccountID", "example.com/fixture"))
	id.Annotations = fieldAnnotations(t, identityFieldMeta("account.id"))
	schema := crudTypeSchema(t, "Account", []*load.Field{id}, []nexaent.CRUDOperation{nexaent.CRUDDelete})
	assertCRUDQualificationError(t, schema, "/entities/0/identity")

	withoutCRUD := crudTypeSchema(t, "Account", []*load.Field{id}, nil)
	graph, err := gen.NewGraph(&gen.Config{}, withoutCRUD)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectGraph(graph, nil, testSourceResolver(t, withoutCRUD)); err != nil {
		t.Fatalf("zero CRUD custom identity was rejected: %v", err)
	}
}

func TestCRUDTypeQualificationExemptsTenantExactModel(t *testing.T) {
	descriptor := nexamixin.Tenant{}.Fields()[0].Descriptor()
	annotations := make(map[string]any, len(descriptor.Annotations))
	for _, annotation := range descriptor.Annotations {
		annotations[annotation.Name()] = transportValue(t, annotation)
	}
	field := &load.Field{
		Name: "tenant_id", Info: customType(entfield.TypeInt, "fixture.TenantID", reflect.Int, "TenantID", "example.com/fixture"),
		Immutable: true, Annotations: annotations,
	}
	schema := crudTypeSchema(t, "Account", []*load.Field{field}, nexaent.AllCRUDOperations())
	graph, err := gen.NewGraph(&gen.Config{}, schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectGraph(graph, nil, testSourceResolver(t, schema)); err != nil {
		t.Fatalf("tenant exact model was qualified even though CRUD uses fixed context: %v", err)
	}
}

func TestCRUDTypeQualificationPointersFollowProjectionOrder(t *testing.T) {
	alpha := func() *load.Schema {
		schema := crudTypeSchema(t, "Alpha", []*load.Field{loadedCRUDField(t, entfield.String("name").Descriptor())}, []nexaent.CRUDOperation{nexaent.CRUDCreate})
		schema.Pos = "schema/alpha.go:10"
		return schema
	}

	t.Run("field", func(t *testing.T) {
		zeta := crudTypeSchema(t, "Zeta", []*load.Field{
			loadedCRUDField(t, entfield.String("a_supported").Descriptor()),
			crudTypeField(t, "z_custom", customType(entfield.TypeString, "fixture.Code", reflect.String, "Code", "example.com/fixture")),
		}, []nexaent.CRUDOperation{nexaent.CRUDCreate})
		zeta.Pos = "schema/zeta.go:10"
		assertCRUDQualificationErrorForSchemas(t, []*load.Schema{zeta, alpha()}, "/entities/1/fields/1/type")
	})

	t.Run("identity", func(t *testing.T) {
		id := crudTypeField(t, "id", customType(entfield.TypeString, "fixture.AccountID", reflect.String, "AccountID", "example.com/fixture"))
		id.Annotations = fieldAnnotations(t, identityFieldMeta("zeta.id"))
		zeta := crudTypeSchema(t, "Zeta", []*load.Field{id}, []nexaent.CRUDOperation{nexaent.CRUDDelete})
		zeta.Pos = "schema/zeta.go:10"
		assertCRUDQualificationErrorForSchemas(t, []*load.Schema{zeta, alpha()}, "/entities/1/identity")
	})
}

func crudTypeSchema(t *testing.T, name string, fields []*load.Field, operations []nexaent.CRUDOperation) *load.Schema {
	t.Helper()
	return &load.Schema{Name: name, Pos: "schema/account.go:10", Annotations: typedAnnotations(t, testSchemaMeta("account"), operations), Fields: fields}
}

func crudTypeField(t *testing.T, name string, info *entfield.TypeInfo) *load.Field {
	t.Helper()
	return &load.Field{Name: name, Info: info, Annotations: fieldAnnotations(t, testFieldMeta("account."+name))}
}

func loadedCRUDField(t *testing.T, descriptor *entfield.Descriptor) *load.Field {
	t.Helper()
	field, err := load.NewField(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	field.Annotations = fieldAnnotations(t, testFieldMeta("account."+field.Name))
	return field
}

func loadedTypeInfo(t *testing.T, descriptor *entfield.Descriptor) *entfield.TypeInfo {
	t.Helper()
	field, err := load.NewField(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	return field.Info
}

func crudJSONField(t *testing.T, name, ident string, kind reflect.Kind, typeName, pkgPath string) *load.Field {
	t.Helper()
	return crudTypeField(t, name, customType(entfield.TypeJSON, ident, kind, typeName, pkgPath))
}

func customType(kind entfield.Type, ident string, reflectKind reflect.Kind, typeName, pkgPath string) *entfield.TypeInfo {
	return &entfield.TypeInfo{Type: kind, Ident: ident, PkgPath: pkgPath, RType: &entfield.RType{Name: typeName, Ident: ident, Kind: reflectKind, PkgPath: pkgPath}}
}

type qualificationPayload struct{ Value string }
type qualificationBool bool
type qualificationEnum string
type qualificationString string
type qualificationTime time.Time
type qualificationBytes []byte
type qualificationUUID [16]byte

func (qualificationEnum) Values() []string {
	return []string{"ready"}
}

func (value qualificationUUID) Value() (driver.Value, error) {
	return value[:], nil
}

func (qualificationUUID) Scan(any) error { return nil }

func qualificationFrameworkRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	return canonicalQualificationDirectory(t, filepath.Join(filepath.Dir(file), "../../.."))
}

func canonicalQualificationDirectory(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(canonical)
}

func writeQualificationFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func excludedFieldMeta(prefix string, mutation nexaent.MutationPolicy) nexaent.FieldMeta {
	meta := testFieldMeta(prefix)
	meta.CRUD.Read = nexaent.ReadExclude
	meta.CRUD.Mutation = mutation
	return meta
}

func identityFieldMeta(prefix string) nexaent.FieldMeta {
	meta := testFieldMeta(prefix)
	meta.UIHint = nexaent.UIHintReadonly
	meta.CRUD.Mutation = nexaent.MutationNone
	return meta
}

func projectedDocument(t *testing.T, schema *load.Schema) entityvalue.Document {
	t.Helper()
	graph, err := gen.NewGraph(&gen.Config{}, schema)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := projectGraph(graph, nil, testSourceResolver(t, schema))
	if err != nil {
		t.Fatal(err)
	}
	document, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func assertCRUDQualificationError(t *testing.T, schema *load.Schema, pointer string) {
	t.Helper()
	assertCRUDQualificationErrorForSchemas(t, []*load.Schema{schema}, pointer)
}

func assertCRUDQualificationErrorForSchemas(t *testing.T, schemas []*load.Schema, pointer string) {
	t.Helper()
	graph, err := gen.NewGraph(&gen.Config{}, schemas...)
	if err != nil {
		t.Fatal(err)
	}
	_, err = projectGraph(graph, nil, testSourceResolver(t, schemas...))
	source, sourceErr := provenance.ParseDomainSource("schema")
	if sourceErr != nil {
		t.Fatal(sourceErr)
	}
	err = entity.AdoptLoadedDocumentError(err, source)
	owner, ok := err.(*entity.Error)
	if !ok || owner.Code() != "entity_ir_invalid" || owner.Reason() != "field_type_unsupported" || owner.Pointer() != pointer || owner.Source() != source.String() {
		t.Fatalf("qualification error = %T %#v, want entity_ir_invalid/field_type_unsupported at %s", err, err, pointer)
	}
}

func transportValue(t *testing.T, annotation any) any {
	t.Helper()
	encoded, err := json.Marshal(annotation)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func equalOperations(left, right []nexaent.CRUDOperation) bool {
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
