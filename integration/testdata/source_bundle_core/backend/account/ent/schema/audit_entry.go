package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"

	"github.com/nxnminieye/nexa/nexaent"
)

type AuditEntry struct{ ent.Schema }

func (AuditEntry) Annotations() []entschema.Annotation {
	return []entschema.Annotation{nexaent.Schema(nexaent.SchemaMeta{
		Label:       localized("audit.label", "审计记录", "Audit entry"),
		Description: localized("audit.description", "审计记录", "Audit entry"),
		Identity:    nexaent.IdentityEntID,
		Scope:       nexaent.ScopeTenant,
	})}
}

func (AuditEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("actor").Annotations(nexaent.Field(nexaent.FieldMeta{
			Label:       localized("audit.actor.label", "操作人", "Actor"),
			Description: localized("audit.actor.description", "操作人", "Operation actor"),
			UIHint:      nexaent.UIHintMember,
			Visibility:  nexaent.VisibilityPublic,
		})),
	}
}
