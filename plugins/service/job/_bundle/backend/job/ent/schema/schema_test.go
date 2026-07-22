package schema

import (
	"testing"

	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"github.com/nxnminieye/nexa/nexaent"
)

func TestJobSchemasExposeOnlyValidTypedMetadata(t *testing.T) {
	schemas := []interface {
		Annotations() []entschema.Annotation
		Fields() []ent.Field
	}{Schedule{}, Run{}, Lease{}}
	for _, candidate := range schemas {
		annotations := candidate.Annotations()
		if len(annotations) != 1 || annotations[0].Name() != nexaent.SchemaAnnotationName {
			t.Fatalf("schema annotations = %#v", annotations)
		}
		assertCanonicalAnnotation(t, annotations[0])
		for _, candidateField := range candidate.Fields() {
			descriptor := candidateField.Descriptor()
			if descriptor.Err != nil {
				t.Fatalf("field %q descriptor: %v", descriptor.Name, descriptor.Err)
			}
			if len(descriptor.Annotations) != 1 || descriptor.Annotations[0].Name() != nexaent.FieldAnnotationName {
				t.Fatalf("field %q annotations = %#v", descriptor.Name, descriptor.Annotations)
			}
			assertCanonicalAnnotation(t, descriptor.Annotations[0])
		}
	}
}

func assertCanonicalAnnotation(t *testing.T, value entschema.Annotation) {
	t.Helper()
	typed, ok := value.(nexaent.Annotation)
	if !ok {
		t.Fatalf("annotation %q is not typed Nexa metadata", value.Name())
	}
	canonical, err := typed.CanonicalJSON()
	if err != nil || len(canonical) == 0 {
		t.Fatalf("annotation %q canonical=%q err=%v", value.Name(), canonical, err)
	}
}
