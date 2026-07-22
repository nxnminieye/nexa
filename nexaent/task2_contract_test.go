package nexaent

import (
	"encoding/json"
	"reflect"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestTask2FieldTransportReplacesRelation(t *testing.T) {
	old := []byte(`{"apiVersion":"nexa.dev/ent-field-meta/v1","kind":"EntFieldMeta","payload":{"label":{"key":"field.label","zhCN":"字段","enUS":"Field"},"description":{"key":"field.description","zhCN":"字段说明","enUS":"Field description"},"uiHint":"reference","relation":{"kind":"foreign-key","targetSchema":"Account","targetField":"id","displayField":"name"},"visibility":"internal"}}`)
	if _, err := DecodeField(old); err == nil {
		t.Fatal("legacy relation transport was accepted")
	}

	physical := []byte(`{"apiVersion":"nexa.dev/ent-field-meta/v1","kind":"EntFieldMeta","payload":{"label":{"key":"field.label","zhCN":"字段","enUS":"Field"},"description":{"key":"field.description","zhCN":"字段说明","enUS":"Field description"},"uiHint":"reference","physicalDisplay":{"field":"name"},"visibility":"internal"}}`)
	if _, err := DecodeField(physical); err != nil {
		t.Fatalf("physicalDisplay transport rejected: %v", err)
	}
	logical := []byte(`{"apiVersion":"nexa.dev/ent-field-meta/v1","kind":"EntFieldMeta","payload":{"label":{"key":"field.label","zhCN":"字段","enUS":"Field"},"description":{"key":"field.description","zhCN":"字段说明","enUS":"Field description"},"uiHint":"text","logicalReference":{"target":"Account","display":"name"},"visibility":"internal"}}`)
	if _, err := DecodeField(logical); err != nil {
		t.Fatalf("logicalReference transport rejected: %v", err)
	}
	both := []byte(`{"apiVersion":"nexa.dev/ent-field-meta/v1","kind":"EntFieldMeta","payload":{"label":{"key":"field.label","zhCN":"字段","enUS":"Field"},"description":{"key":"field.description","zhCN":"字段说明","enUS":"Field description"},"uiHint":"reference","physicalDisplay":{"field":"name"},"logicalReference":{"target":"Account","display":"name"},"visibility":"internal"}}`)
	if _, err := DecodeField(both); err == nil {
		t.Fatal("physicalDisplay and logicalReference were accepted together")
	}
}

func TestTask2FieldPublicSchemaMatchesReferenceExclusivity(t *testing.T) {
	var schemaDocument any
	if err := json.Unmarshal(FieldAnnotationSchema(), &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaID = "https://nexa.dev/schemas/nexaent/ent-field-meta-v1.schema.json"
	if err := compiler.AddResource(schemaID, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		t.Fatal(err)
	}
	base := `{"apiVersion":"nexa.dev/ent-field-meta/v1","kind":"EntFieldMeta","payload":{"label":{"key":"field.label","zhCN":"field","enUS":"Field"},"description":{"key":"field.description","zhCN":"description","enUS":"Description"},"uiHint":"reference","visibility":"internal"}}`
	for _, test := range []struct {
		name    string
		mutate  func(map[string]any)
		wantErr bool
	}{
		{name: "physical only", mutate: func(payload map[string]any) { payload["physicalDisplay"] = map[string]any{"field": "name"} }},
		{name: "logical only", mutate: func(payload map[string]any) {
			payload["logicalReference"] = map[string]any{"target": "Account", "display": "name"}
		}},
		{name: "both", wantErr: true, mutate: func(payload map[string]any) {
			payload["physicalDisplay"] = map[string]any{"field": "name"}
			payload["logicalReference"] = map[string]any{"target": "Account", "display": "name"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal([]byte(base), &document); err != nil {
				t.Fatal(err)
			}
			test.mutate(document["payload"].(map[string]any))
			err := compiled.Validate(document)
			if test.wantErr != (err != nil) {
				t.Fatalf("schema validation error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestTask2FieldReferencePointersAreDefensivelyCopied(t *testing.T) {
	fieldType := reflect.TypeOf(FieldMeta{})
	physicalField, ok := fieldType.FieldByName("PhysicalDisplay")
	if !ok {
		t.Fatal("FieldMeta.PhysicalDisplay is missing")
	}
	logicalField, ok := fieldType.FieldByName("LogicalReference")
	if !ok {
		t.Fatal("FieldMeta.LogicalReference is missing")
	}
	metaValue := validFieldMeta(t)
	meta := reflect.ValueOf(&metaValue).Elem()
	physical := reflect.New(physicalField.Type.Elem())
	physical.Elem().FieldByName("Field").SetString("name")
	logical := reflect.New(logicalField.Type.Elem())
	logical.Elem().FieldByName("Target").SetString("Account")
	logical.Elem().FieldByName("Display").SetString("name")
	meta.FieldByName("PhysicalDisplay").Set(physical)

	annotation := Field(meta.Interface().(FieldMeta))
	physical.Elem().FieldByName("Field").SetString("mutated")
	encoded, err := json.Marshal(annotation)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || reflect.ValueOf(annotation).IsZero() {
		t.Fatal("annotation is empty")
	}
	decoded, err := DecodeField(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got := reflect.ValueOf(decoded).FieldByName("PhysicalDisplay")
	if got.IsNil() || got.Elem().FieldByName("Field").String() != "name" {
		t.Fatalf("PhysicalDisplay aliased constructor input: %#v", decoded)
	}

	meta.FieldByName("PhysicalDisplay").Set(reflect.Zero(physicalField.Type))
	meta.FieldByName("LogicalReference").Set(logical)
	annotation = Field(meta.Interface().(FieldMeta))
	logical.Elem().FieldByName("Display").SetString("mutated")
	encoded, err = json.Marshal(annotation)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeField(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got = reflect.ValueOf(decoded).FieldByName("LogicalReference")
	if got.IsNil() || got.Elem().FieldByName("Display").String() != "name" {
		t.Fatalf("LogicalReference aliased constructor input: %#v", decoded)
	}
}
