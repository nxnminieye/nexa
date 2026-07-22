package crudbuild

import (
	"bytes"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
)

func RebuildPlanFromSnapshot(snapshot entity.Snapshot, spec Spec) (Plan, error) {
	projection := entityvalue.Projection{Entities: make([]entityvalue.EntityProjection, 0, len(snapshot.Entities()))}
	for _, item := range snapshot.Entities() {
		identity := item.Identity()
		projected := entityvalue.EntityProjection{
			Name: item.Name(), SourceRef: item.SourceRef(), Meta: item.Meta(),
			Identity: entityvalue.IdentityProjection{Kind: string(identity.Kind()), Name: identity.Name(), Type: string(identity.Type())},
			Fields:   make([]entityvalue.FieldProjection, 0, len(item.Fields())),
		}
		if value, present := item.CRUD(); present {
			crud := value
			projected.CRUD = &crud
		}
		for _, field := range item.Fields() {
			enumValues := field.EnumValues()
			projectedEnums := make([]entityvalue.EnumValue, len(enumValues))
			for index, value := range enumValues {
				projectedEnums[index] = entityvalue.EnumValue{Name: value.Name, Value: value.Value}
			}
			projected.Fields = append(projected.Fields, entityvalue.FieldProjection{
				Name: field.Name(), SourceRef: field.SourceRef(), Type: string(field.Type()), EnumValues: projectedEnums,
				Optional: field.Optional(), Nillable: field.Nillable(), Immutable: field.Immutable(), HasDefault: field.HasDefault(),
				Sensitive: field.Sensitive(), IsIdentity: field.IsIdentity(), IsTenantField: field.IsTenantField(), Meta: field.Meta(),
			})
		}
		projection.Entities = append(projection.Entities, projected)
	}
	value, err := entityvalue.NewDocument(projection)
	if err != nil {
		return Plan{}, buildError("document_state_invalid", "/entities")
	}
	document, err := entity.AdoptLoadedDocument(value)
	if err != nil {
		return Plan{}, buildError("document_state_invalid", "/entities")
	}
	canonical, err := entity.CanonicalJSON(document)
	if err != nil {
		return Plan{}, buildError("canonical_invalid", "/document")
	}
	snapshotCanonical, err := snapshot.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, snapshotCanonical) {
		return Plan{}, buildError("canonical_invalid", "/document")
	}
	return BuildPlan(document, spec)
}
