package nexaent

import "entgo.io/ent/schema"

// Annotation is an immutable Ent annotation with a semantic JSON projection.
// Implementations returned by this package marshal as Ent transport envelopes;
// only CanonicalJSON returns bytes suitable for semantic digests.
type Annotation interface {
	schema.Annotation
	schema.Merger
	// CanonicalJSON returns fresh RFC 8785 JCS bytes for a valid normal value.
	// Invalid and duplicate sentinels return a typed error and no bytes.
	CanonicalJSON() ([]byte, error)
}

type annotationOwner uint8

const (
	ownerSchema annotationOwner = iota
	ownerField
	ownerCRUD
)

func (o annotationOwner) name() string {
	switch o {
	case ownerField:
		return FieldAnnotationName
	case ownerCRUD:
		return CRUDAnnotationName
	default:
		return SchemaAnnotationName
	}
}

func (o annotationOwner) kind() string {
	switch o {
	case ownerField:
		return FieldAnnotationKind
	case ownerCRUD:
		return CRUDAnnotationKind
	default:
		return SchemaAnnotationKind
	}
}

type annotationValue struct {
	owner      annotationOwner
	schemaMeta SchemaMeta
	fieldMeta  FieldMeta
	crudSpec   CRUDSpec
	failure    *Error
	duplicate  bool
}

var duplicateAnnotations = [...]Annotation{
	&annotationValue{owner: ownerSchema, duplicate: true},
	&annotationValue{owner: ownerField, duplicate: true},
	&annotationValue{owner: ownerCRUD, duplicate: true},
}

// Schema constructs schema metadata. Invalid input becomes a payload-free
// invalid sentinel so Ent transport can preserve the typed failure.
func Schema(meta SchemaMeta) Annotation {
	if err := validateSchemaMeta(ownerSchema, meta); err != nil {
		return &annotationValue{owner: ownerSchema, failure: err}
	}
	return &annotationValue{owner: ownerSchema, schemaMeta: meta}
}

// Field constructs field metadata and deep-copies optional pointer fields.
// Invalid input becomes a payload-free invalid sentinel.
func Field(meta FieldMeta) Annotation {
	normalized, err := normalizeFieldMeta(ownerField, meta)
	if err != nil {
		return &annotationValue{owner: ownerField, failure: err}
	}
	return &annotationValue{owner: ownerField, fieldMeta: normalized}
}

// CRUD constructs the annotation whose presence opts a schema in to exactly
// the supplied operations; omitting the annotation means no CRUD opt-in.
// Operations must be non-empty and unique, and valid input is stored in
// canonical operation order. Invalid input becomes a payload-free sentinel.
func CRUD(operations ...CRUDOperation) Annotation {
	copied := append([]CRUDOperation(nil), operations...)
	canonical, err := validateCRUDOperations(ownerCRUD, copied)
	if err != nil {
		return &annotationValue{owner: ownerCRUD, failure: err}
	}
	return &annotationValue{owner: ownerCRUD, crudSpec: CRUDSpec{operations: canonical}}
}

// AllCRUDOperations returns a fresh slice containing every operation in
// canonical list/get/create/update/delete order.
func AllCRUDOperations() []CRUDOperation {
	return []CRUDOperation{CRUDList, CRUDGet, CRUDCreate, CRUDUpdate, CRUDDelete}
}

func (a *annotationValue) Name() string {
	if a == nil {
		return SchemaAnnotationName
	}
	return a.owner.name()
}

func (a *annotationValue) Merge(schema.Annotation) schema.Annotation {
	if a == nil {
		return duplicateAnnotations[ownerSchema]
	}
	return duplicateAnnotations[a.owner]
}

var _ Annotation = (*annotationValue)(nil)
