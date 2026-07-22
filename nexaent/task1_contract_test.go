package nexaent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFieldMetaTask1Contract(t *testing.T) {
	t.Run("iconify is the only icon UI hint", func(t *testing.T) {
		meta := validFieldMeta(t)
		meta.UIHint = UIHint("iconify")
		if _, err := Field(meta).CanonicalJSON(); err != nil {
			t.Fatalf("iconify rejected: %v", err)
		}
		meta.UIHint = UIHint("icon")
		if _, err := Field(meta).CanonicalJSON(); err == nil {
			t.Fatal("legacy icon accepted")
		}
	})

	t.Run("crud is optional and round trips when present", func(t *testing.T) {
		withoutCRUD := []byte(fieldEnvelope(`{"label":{"key":"l","zhCN":"l","enUS":"l"},"description":{"key":"d","zhCN":"d","enUS":"d"},"uiHint":"text","visibility":"public"}`))
		decoded, err := DecodeField(withoutCRUD)
		if err != nil {
			t.Fatalf("DecodeField() without crud error = %v", err)
		}
		if crudPresent(decoded) {
			t.Fatalf("CRUD = %#v, want absent", decoded.CRUD)
		}

		policy := &CRUDFieldPolicy{Read: ReadInclude, Mutation: MutationCreateUpdate}
		meta := validFieldMeta(t)
		setCRUDPolicy(&meta, policy)
		transport, err := json.Marshal(Field(meta))
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := DecodeField(transport)
		if err != nil || !reflect.DeepEqual(crudPolicy(roundTrip), policy) {
			t.Fatalf("CRUD round trip = %#v, %v", roundTrip.CRUD, err)
		}
	})

	t.Run("legacy sourceBinding is unknown", func(t *testing.T) {
		legacy := []byte(fieldEnvelope(`{"label":{"key":"l","zhCN":"l","enUS":"l"},"description":{"key":"d","zhCN":"d","enUS":"d"},"uiHint":"text","sourceBinding":{"kind":"canonical","ref":"repo:a","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"visibility":"public","crud":{"read":"include","mutation":"create-update"}}`))
		_, err := DecodeField(legacy)
		assertTypedError(t, err, "annotation_invalid", "document_unknown_field", "/payload/sourceBinding", FieldAnnotationName)
	})

	t.Run("crud pointer is defensively copied", func(t *testing.T) {
		policy := &CRUDFieldPolicy{Read: ReadInclude, Mutation: MutationCreateUpdate}
		meta := validFieldMeta(t)
		setCRUDPolicy(&meta, policy)
		annotation := Field(meta)
		want, err := annotation.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		policy.Read = ReadExclude
		got, err := annotation.CanonicalJSON()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("constructor retained CRUD pointer: %s, %v; want %s", got, err, want)
		}
	})
}

func TestFieldTransportRejectsCaseVariantMembers(t *testing.T) {
	valid := fieldEnvelope(validFieldPayload())
	tests := []struct {
		name    string
		data    string
		pointer string
	}{
		{name: "kind", data: strings.Replace(valid, `"kind"`, `"Kind"`, 1), pointer: "/Kind"},
		{name: "payload", data: strings.Replace(valid, `"payload"`, `"Payload"`, 1), pointer: "/Payload"},
		{name: "nested UI hint", data: strings.Replace(valid, `"uiHint"`, `"UIHint"`, 1), pointer: "/payload/UIHint"},
		{name: "nested UI hint wrong type", data: strings.Replace(valid, `"uiHint":"text"`, `"UIHint":1`, 1), pointer: "/payload/UIHint"},
		{name: "removed relation", data: fieldEnvelope(fieldPayloadWith(`"relation":{"Kind":"foreign-key","targetSchema":"Account","targetField":"id","displayField":"name"},`)), pointer: "/payload/relation"},
		{name: "nested crud", data: strings.Replace(valid, `"read"`, `"Read"`, 1), pointer: "/payload/crud/Read"},
		{name: "case variant double write", data: strings.TrimSuffix(valid, "}") + `,"Payload":` + validFieldPayload() + `}`, pointer: "/Payload"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeField([]byte(test.data))
			assertTypedError(t, err, "annotation_invalid", "document_unknown_field", test.pointer, FieldAnnotationName)
		})
	}
}

func setCRUDPolicy(meta *FieldMeta, policy *CRUDFieldPolicy) {
	field := reflect.ValueOf(meta).Elem().FieldByName("CRUD")
	if field.Kind() == reflect.Pointer {
		field.Set(reflect.ValueOf(policy))
		return
	}
	field.Set(reflect.ValueOf(*policy))
}

func crudPolicy(meta FieldMeta) *CRUDFieldPolicy {
	field := reflect.ValueOf(meta).FieldByName("CRUD")
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return nil
		}
		policy := field.Interface().(*CRUDFieldPolicy)
		return policy
	}
	policy := field.Interface().(CRUDFieldPolicy)
	return &policy
}

func crudPresent(meta FieldMeta) bool {
	field := reflect.ValueOf(meta).FieldByName("CRUD")
	return field.Kind() != reflect.Pointer || !field.IsNil()
}
