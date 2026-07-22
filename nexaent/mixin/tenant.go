// Package mixin provides strict framework-owned Ent mixins.
package mixin

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	entmixin "entgo.io/ent/schema/mixin"
	"github.com/nxnminieye/nexa/nexaent"
)

// Tenant adds the immutable positive tenant ownership field.
type Tenant struct{ entmixin.Schema }

// Fields returns the fixed tenant ownership field.
func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").
			Positive().
			Immutable().
			Annotations(
				nexaent.Field(nexaent.FieldMeta{
					Label:       nexaent.LocalizedText{Key: "nexa.tenant_id.label", ZhCN: "租户 ID", EnUS: "Tenant ID"},
					Description: nexaent.LocalizedText{Key: "nexa.tenant_id.description", ZhCN: "记录所属租户的内部标识。", EnUS: "Internal identifier of the tenant that owns the record."},
					UIHint:      nexaent.UIHintReadonly,
					Visibility:  nexaent.VisibilityInternal,
				}),
				tenantAnnotation{},
			),
	}
}

var _ ent.Mixin = Tenant{}
