package entityload

import (
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"entgo.io/ent/entc/gen"
	"entgo.io/ent/entc/load"
	entfield "entgo.io/ent/schema/field"
	"github.com/google/uuid"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

func TestCRUDTypeQualificationAllowsNativeEntModelsWithoutChangingEntityIR(t *testing.T) {
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
		loadedCRUDField(t, entfield.Enum("enum_value").Values("enabled").Descriptor()),
	}
	schema := crudTypeSchema("Account", fields)
	document := projectedDocument(t, schema, allCRUDOperations(), nil)
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

	for _, field := range []*load.Field{
		crudJSONField("payload", "fixture.Payload", reflect.Struct, "Payload", "example.com/fixture"),
		crudJSONField("payload", "*fixture.Payload", reflect.Ptr, "Payload", "example.com/fixture"),
	} {
		variant := crudTypeSchema("Account", []*load.Field{field})
		projectedDocument(t, variant, []sourcecomment.CRUDOperation{sourcecomment.CRUDCreate}, nil)
	}
}

func TestCRUDTypeQualificationGoogleUUIDDoesNotDependOnReflectKind(t *testing.T) {
	info := customType(entfield.TypeUUID, "uuid.UUID", reflect.Array, "UUID", "github.com/google/uuid")
	schema := crudTypeSchema("Account", []*load.Field{crudTypeField("external_id", info)})
	projectedDocument(t, schema, []sourcecomment.CRUDOperation{sourcecomment.CRUDCreate}, nil)
}

func TestCRUDTypeQualificationRejectsParticipatingCustomGoTypes(t *testing.T) {
	tests := []struct {
		name       string
		info       *entfield.TypeInfo
		operations []sourcecomment.CRUDOperation
		meta       *sourcecomment.FieldFacts
	}{
		{name: "custom bool", info: loadedTypeInfo(t, entfield.Bool("value").GoType(qualificationBool(false)).Descriptor())},
		{name: "custom enum", info: loadedTypeInfo(t, entfield.Enum("value").Values("ready").GoType(qualificationEnum("")).Descriptor())},
		{name: "named scalar", info: loadedTypeInfo(t, entfield.String("value").GoType(qualificationString("")).Descriptor())},
		{name: "named time", info: loadedTypeInfo(t, entfield.Time("value").GoType(qualificationTime{}).Descriptor())},
		{name: "named bytes", info: loadedTypeInfo(t, entfield.Bytes("value").GoType(qualificationBytes{}).Descriptor())},
		{name: "non google uuid", info: loadedTypeInfo(t, entfield.UUID("value", qualificationUUID{}).Descriptor())},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field := crudTypeField("value", test.info)
			if test.info.Type == entfield.TypeEnum {
				field.Enums = []struct{ N, V string }{{N: "Ready", V: "ready"}}
			}
			schema := crudTypeSchema("Account", []*load.Field{field})
			assertCRUDQualificationError(t, []*load.Schema{schema}, map[string][]sourcecomment.CRUDOperation{"Account": {sourcecomment.CRUDCreate}}, nil, "/entities/0/fields/0/type")
		})
	}

	for _, test := range []struct {
		name      string
		operation sourcecomment.CRUDOperation
		mutation  sourcecomment.MutationPolicy
	}{{"create mutation despite read exclusion", sourcecomment.CRUDCreate, sourcecomment.MutationCreate}, {"update mutation despite read exclusion", sourcecomment.CRUDUpdate, sourcecomment.MutationUpdate}} {
		t.Run(test.name, func(t *testing.T) {
			field := crudTypeField("value", customType(entfield.TypeString, "fixture.Code", reflect.String, "Code", "example.com/fixture"))
			meta := excludedFieldFacts("account.value", test.mutation)
			schema := crudTypeSchema("Account", []*load.Field{field})
			assertCRUDQualificationError(t, []*load.Schema{schema}, map[string][]sourcecomment.CRUDOperation{"Account": {test.operation}}, map[string]sourcecomment.FieldFacts{"Account.value": meta}, "/entities/0/fields/0/type")
		})
	}
}

func TestCRUDTypeQualificationIsOperationSensitive(t *testing.T) {
	custom := customType(entfield.TypeString, "fixture.Code", reflect.String, "Code", "example.com/fixture")
	tests := []struct {
		name       string
		operations []sourcecomment.CRUDOperation
		meta       sourcecomment.FieldFacts
		immutable  bool
	}{
		{name: "zero crud", meta: noCRUDFieldFacts("account.value")},
		{name: "read excluded", operations: []sourcecomment.CRUDOperation{sourcecomment.CRUDList}, meta: excludedFieldFacts("account.value", sourcecomment.MutationNone)},
		{name: "immutable create only during update", operations: []sourcecomment.CRUDOperation{sourcecomment.CRUDUpdate}, meta: excludedFieldFacts("account.value", sourcecomment.MutationCreate), immutable: true},
		{name: "delete does not read ordinary fields", operations: []sourcecomment.CRUDOperation{sourcecomment.CRUDDelete}, meta: testFieldFacts("account.value")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			field := &load.Field{Name: "value", Info: custom, Immutable: test.immutable}
			schema := crudTypeSchema("Account", []*load.Field{field})
			projectedDocument(t, schema, test.operations, map[string]sourcecomment.FieldFacts{"Account.value": test.meta})
		})
	}
}

func TestCRUDTypeQualificationUsesNativeIdentityPointer(t *testing.T) {
	id := crudTypeField("id", customType(entfield.TypeString, "fixture.AccountID", reflect.String, "AccountID", "example.com/fixture"))
	schema := crudTypeSchema("Account", []*load.Field{id})
	assertCRUDQualificationError(t, []*load.Schema{schema}, map[string][]sourcecomment.CRUDOperation{"Account": {sourcecomment.CRUDDelete}}, map[string]sourcecomment.FieldFacts{"Account.id": identityFieldFacts("account.id")}, "/entities/0/identity")

	document := projectedDocument(t, schema, nil, map[string]sourcecomment.FieldFacts{"Account.id": identityFieldFacts("account.id")})
	identity := document.Entities()[0].Identity()
	if identity.Kind() != "field" || identity.Name() != "id" || identity.Type() != "string" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestCRUDTypeQualificationPointersFollowProjectionOrder(t *testing.T) {
	alpha := crudTypeSchema("Alpha", []*load.Field{loadedCRUDField(t, entfield.String("name").Descriptor())})
	alpha.Pos = "schema/alpha.go:10"
	zeta := crudTypeSchema("Zeta", []*load.Field{loadedCRUDField(t, entfield.String("a_supported").Descriptor()), crudTypeField("z_custom", customType(entfield.TypeString, "fixture.Code", reflect.String, "Code", "example.com/fixture"))})
	zeta.Pos = "schema/zeta.go:10"
	assertCRUDQualificationError(t, []*load.Schema{zeta, alpha}, map[string][]sourcecomment.CRUDOperation{"Alpha": {sourcecomment.CRUDCreate}, "Zeta": {sourcecomment.CRUDCreate}}, nil, "/entities/1/fields/1/type")
}

func crudTypeSchema(name string, fields []*load.Field) *load.Schema {
	return &load.Schema{Name: name, Pos: "schema/account.go:10", Fields: fields}
}

func crudTypeField(name string, info *entfield.TypeInfo) *load.Field {
	return &load.Field{Name: name, Info: info}
}

func loadedCRUDField(t *testing.T, descriptor *entfield.Descriptor) *load.Field {
	t.Helper()
	field, err := load.NewField(descriptor)
	if err != nil {
		t.Fatal(err)
	}
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

func crudJSONField(name, ident string, kind reflect.Kind, typeName, pkgPath string) *load.Field {
	return crudTypeField(name, customType(entfield.TypeJSON, ident, kind, typeName, pkgPath))
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

func (qualificationEnum) Values() []string                   { return []string{"ready"} }
func (value qualificationUUID) Value() (driver.Value, error) { return value[:], nil }
func (qualificationUUID) Scan(any) error                     { return nil }

func excludedFieldFacts(prefix string, mutation sourcecomment.MutationPolicy) sourcecomment.FieldFacts {
	meta := testFieldFacts(prefix)
	meta.CRUD.Read = sourcecomment.ReadExclude
	meta.CRUD.Mutation = mutation
	return meta
}

func noCRUDFieldFacts(prefix string) sourcecomment.FieldFacts {
	meta := testFieldFacts(prefix)
	meta.CRUD = nil
	return meta
}

func identityFieldFacts(prefix string) sourcecomment.FieldFacts {
	meta := testFieldFacts(prefix)
	meta.Control = sourcecomment.UIControlReadonly
	meta.CRUD = nil
	return meta
}

func allCRUDOperations() []sourcecomment.CRUDOperation {
	return []sourcecomment.CRUDOperation{sourcecomment.CRUDList, sourcecomment.CRUDGet, sourcecomment.CRUDCreate, sourcecomment.CRUDUpdate, sourcecomment.CRUDDelete}
}

func projectedDocument(t *testing.T, schema *load.Schema, operations []sourcecomment.CRUDOperation, fields map[string]sourcecomment.FieldFacts) entityvalue.Document {
	t.Helper()
	graph, err := gen.NewGraph(&gen.Config{}, schema)
	if err != nil {
		t.Fatal(err)
	}
	crud := map[string][]sourcecomment.CRUDOperation{}
	if len(operations) > 0 {
		crud[schema.Name] = operations
	}
	facts := testFactGraph(t, []*load.Schema{schema}, factOptions{crud: crud, fieldFacts: fields})
	projection, err := projectGraph(graph, facts, nil, testSourceResolver(t, schema))
	if err != nil {
		t.Fatal(err)
	}
	document, err := entityvalue.NewDocument(projection)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func assertCRUDQualificationError(t *testing.T, schemas []*load.Schema, operations map[string][]sourcecomment.CRUDOperation, fields map[string]sourcecomment.FieldFacts, pointer string) {
	t.Helper()
	graph, err := gen.NewGraph(&gen.Config{}, schemas...)
	if err != nil {
		t.Fatal(err)
	}
	facts := testFactGraph(t, schemas, factOptions{crud: operations, fieldFacts: fields})
	_, err = projectGraph(graph, facts, nil, testSourceResolver(t, schemas...))
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
