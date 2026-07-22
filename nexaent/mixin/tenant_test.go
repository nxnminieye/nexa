package mixin

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

func TestTenantMixinRequiredPositiveImmutableWithoutEdge(t *testing.T) {
	mixin := Tenant{}
	fields := mixin.Fields()
	if len(fields) != 1 {
		t.Fatalf("Fields() count = %d, want 1", len(fields))
	}
	descriptor := fields[0].Descriptor()
	if descriptor.Err != nil {
		t.Fatalf("tenant field descriptor error = %v", descriptor.Err)
	}
	if descriptor.Name != "tenant_id" || descriptor.Info.Type != field.TypeInt || descriptor.Optional || descriptor.Nillable || !descriptor.Immutable {
		t.Fatalf("tenant field descriptor = %#v", descriptor)
	}
	if len(descriptor.Validators) != 1 {
		t.Fatalf("tenant validators = %d, want 1", len(descriptor.Validators))
	}
	validator, ok := descriptor.Validators[0].(func(int) error)
	if !ok || validator(1) != nil || validator(0) == nil || validator(-1) == nil {
		t.Fatal("tenant validator does not enforce a positive int")
	}
	if edges := mixin.Edges(); len(edges) != 0 {
		t.Fatalf("Edges() = %#v, want none", edges)
	}
}

func TestTenantMixinCarriesFixedInternalFieldMeta(t *testing.T) {
	descriptor := Tenant{}.Fields()[0].Descriptor()
	annotations := annotationMap(t, descriptor.Annotations)
	encoded, err := json.Marshal(annotations[nexaent.FieldAnnotationName])
	if err != nil {
		t.Fatal(err)
	}
	got, err := nexaent.DecodeField(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := nexaent.FieldMeta{
		Label:       nexaent.LocalizedText{Key: "nexa.tenant_id.label", ZhCN: "租户 ID", EnUS: "Tenant ID"},
		Description: nexaent.LocalizedText{Key: "nexa.tenant_id.description", ZhCN: "记录所属租户的内部标识。", EnUS: "Internal identifier of the tenant that owns the record."},
		UIHint:      nexaent.UIHintReadonly, Visibility: nexaent.VisibilityInternal,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tenant FieldMeta = %#v, want %#v", got, want)
	}
	marker, ok := annotations[TenantAnnotationName]
	if !ok {
		t.Fatal("tenant marker annotation missing")
	}
	markerJSON, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeTenantAnnotation(markerJSON); err != nil {
		t.Fatalf("DecodeTenantAnnotation() error = %v", err)
	}
}

func TestTenantAnnotationSchemaReturnsDefensiveCopy(t *testing.T) {
	first := TenantAnnotationSchema()
	second := TenantAnnotationSchema()
	if len(first) == 0 || !bytes.Equal(first, second) {
		t.Fatal("tenant annotation schema is empty or unstable")
	}
	first[0] ^= 0xff
	if bytes.Equal(first, TenantAnnotationSchema()) {
		t.Fatal("TenantAnnotationSchema retained caller mutation")
	}
}

func TestDecodeTenantAnnotationRejectsNonEmptyUnknownDuplicateAndTrailing(t *testing.T) {
	valid := []byte(`{"apiVersion":"nexa.dev/ent-tenant-field/v1","kind":"EntTenantField","payload":{}}`)
	if err := DecodeTenantAnnotation(valid); err != nil {
		t.Fatalf("valid marker rejected: %v", err)
	}
	invalid := [][]byte{
		[]byte(`{"apiVersion":"nexa.dev/ent-tenant-field/v1","kind":"EntTenantField","payload":{"enabled":true}}`),
		[]byte(`{"apiVersion":"nexa.dev/ent-tenant-field/v1","kind":"EntTenantField","payload":{},"unknown":true}`),
		[]byte(`{"apiVersion":"nexa.dev/ent-tenant-field/v1","kind":"EntTenantField","payload":{},"payload":{}}`),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
	}
	for index, input := range invalid {
		if err := DecodeTenantAnnotation(input); err == nil {
			t.Fatalf("invalid marker %d accepted: %s", index, input)
		}
	}
	duplicate := tenantAnnotation{}.Merge(tenantAnnotation{})
	encoded, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeTenantAnnotation(encoded); err == nil {
		t.Fatalf("duplicate marker accepted: %s", encoded)
	}
}

func annotationMap(t *testing.T, annotations []schema.Annotation) map[string]schema.Annotation {
	t.Helper()
	result := make(map[string]schema.Annotation, len(annotations))
	for _, annotation := range annotations {
		if _, duplicate := result[annotation.Name()]; duplicate {
			t.Fatalf("duplicate annotation %q", annotation.Name())
		}
		result[annotation.Name()] = annotation
	}
	return result
}
