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
		Label:       localized("audit.label", "Audit entry", "Audit entry"),
		Description: localized("audit.description", "Audit record", "Audit record"),
		Identity:    nexaent.IdentityEntID,
		Scope:       nexaent.ScopeTenant,
	})}
}

func (AuditEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("actor").Annotations(nexaent.Field(nexaent.FieldMeta{
			Label:       localized("audit.actor.label", "Actor", "Actor"),
			Description: localized("audit.actor.description", "Operation actor", "Operation actor"),
			UIHint:      nexaent.UIHintMember,
			Visibility:  nexaent.VisibilityPublic,
		})),
	}
}
