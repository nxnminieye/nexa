package entmixin

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	entschemamixin "entgo.io/ent/schema/mixin"
)

// Tenant adds the immutable positive tenant ownership field.
type Tenant struct{ entschemamixin.Schema }

// Fields returns the standard tenant ownership field.
func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").
			Positive().
			Immutable().
			Comment("Internal identifier of the tenant that owns the record.").
			Annotations(standardField(ProfileTenant)),
	}
}

// TimeAuditMixin adds standard creation and update timestamps.
type TimeAuditMixin struct{ entschemamixin.Schema }

// Fields returns the standard creation and update timestamp fields.
func (TimeAuditMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("Time when the record was created.").
			Annotations(standardField(ProfileCreatedAt)),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment("Time when the record was last updated.").
			Annotations(standardField(ProfileUpdatedAt)),
	}
}

// SortMixin adds the standard integer sort field.
type SortMixin struct{ entschemamixin.Schema }

// Fields returns the standard sort field.
func (SortMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Int("sort").
			Default(0).
			Comment("Sort value for the record.").
			Annotations(standardField(ProfileSort)),
	}
}

// StatusMixin adds the standard enabled-state field.
type StatusMixin struct{ entschemamixin.Schema }

// Fields returns the standard status field.
func (StatusMixin) Fields() []ent.Field {
	return []ent.Field{
		field.String("status").
			Default("enabled").
			Comment("Enablement status of the record.").
			Annotations(standardField(ProfileStatus)),
	}
}

// SoftDeleteMixin adds the nullable soft-deletion timestamp.
type SoftDeleteMixin struct{ entschemamixin.Schema }

// Fields returns the standard soft-deletion field.
func (SoftDeleteMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("deleted_at").
			Optional().
			Nillable().
			Comment("Time when the record was soft-deleted.").
			Annotations(standardField(ProfileDeletedAt)),
	}
}

var (
	_ ent.Mixin = Tenant{}
	_ ent.Mixin = TimeAuditMixin{}
	_ ent.Mixin = SortMixin{}
	_ ent.Mixin = StatusMixin{}
	_ ent.Mixin = SoftDeleteMixin{}
)
