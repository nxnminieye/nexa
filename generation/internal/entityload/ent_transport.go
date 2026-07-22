package entityload

import (
	"encoding/json"

	"entgo.io/ent/entc/gen"
	"github.com/nxnminieye/nexa/nexaent"
	nexamixin "github.com/nxnminieye/nexa/nexaent/mixin"
)

func decodeTenantAnnotation(annotations gen.Annotations) (bool, bool, error) {
	value, present := annotations[nexamixin.TenantAnnotationName]
	if !present {
		return false, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, true, err
	}
	if err := nexamixin.DecodeTenantAnnotation(encoded); err != nil {
		return false, true, err
	}
	return true, true, nil
}

func decodeSchemaAnnotation(annotations gen.Annotations) (nexaent.SchemaMeta, bool, error) {
	value, present := annotations[nexaent.SchemaAnnotationName]
	if !present {
		return nexaent.SchemaMeta{}, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		_, err = nexaent.DecodeSchema(nil)
		return nexaent.SchemaMeta{}, true, err
	}
	meta, err := nexaent.DecodeSchema(encoded)
	return meta, true, err
}

func decodeFieldAnnotation(annotations gen.Annotations) (nexaent.FieldMeta, bool, error) {
	value, present := annotations[nexaent.FieldAnnotationName]
	if !present {
		return nexaent.FieldMeta{}, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		_, err = nexaent.DecodeField(nil)
		return nexaent.FieldMeta{}, true, err
	}
	meta, err := nexaent.DecodeField(encoded)
	return meta, true, err
}

func decodeCRUDAnnotation(annotations gen.Annotations) (nexaent.CRUDSpec, bool, error) {
	value, present := annotations[nexaent.CRUDAnnotationName]
	if !present {
		return nexaent.CRUDSpec{}, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		_, err = nexaent.DecodeCRUD(nil)
		return nexaent.CRUDSpec{}, true, err
	}
	spec, err := nexaent.DecodeCRUD(encoded)
	return spec, true, err
}
