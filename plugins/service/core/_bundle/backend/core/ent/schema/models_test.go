package schema

import (
	"testing"

	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"github.com/nxnminieye/nexa/nexaent"
)

func TestModelsExposeValidTypedMetadata(t *testing.T) {
	models := []interface {
		Annotations() []entschema.Annotation
		Fields() []ent.Field
	}{Tenant{}, IdentityAccount{}, TenantMember{}, Role{}, Permission{}, AuthSession{}}
	for _, model := range models {
		if !validAnnotation(model.Annotations(), nexaent.SchemaAnnotationName) {
			t.Fatalf("%T has invalid schema metadata", model)
		}
		for _, value := range model.Fields() {
			descriptor := value.Descriptor()
			if descriptor.Err != nil || !validAnnotation(descriptor.Annotations, nexaent.FieldAnnotationName) {
				t.Fatalf("%T.%s has invalid field metadata: %v", model, descriptor.Name, descriptor.Err)
			}
		}
	}
	for _, model := range []interface{ Annotations() []entschema.Annotation }{IdentityAccount{}, TenantMember{}, AuthSession{}} {
		for _, annotation := range model.Annotations() {
			if annotation.Name() == nexaent.CRUDAnnotationName {
				t.Fatalf("%T unexpectedly opted in to CRUD", model)
			}
		}
	}
}

func validAnnotation(values []entschema.Annotation, name string) bool {
	for _, value := range values {
		annotation, ok := value.(nexaent.Annotation)
		if !ok || annotation.Name() != name {
			continue
		}
		if _, err := annotation.CanonicalJSON(); err == nil {
			return true
		}
	}
	return false
}
