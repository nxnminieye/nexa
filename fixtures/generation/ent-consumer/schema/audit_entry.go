package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

type AuditEntry struct{ ent.Schema }

func (AuditEntry) Mixin() []ent.Mixin { return []ent.Mixin{SharedMixin{}} }

func (AuditEntry) Annotations() []entschema.Annotation {
	return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{
		Label:       localized("audit_entry.label", "审计记录", "Audit entry"),
		Description: localized("audit_entry.description", "操作审计记录", "Operation audit entry"),
		Identity:    nexaent.IdentityEntID,
		Scope:       nexaent.ScopeTenant,
	})}
}

func (AuditEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("actor").Annotations(nexaent.Field(nexaent.FieldMeta{
			Label:       localized("audit_entry.actor.label", "操作人", "Actor"),
			Description: localized("audit_entry.actor.description", "执行操作的主体", "Actor that performed the operation"),
			UIHint:      nexaent.UIHintMember,
			Visibility:  nexaent.VisibilityPublic,
		})),
	}
}
