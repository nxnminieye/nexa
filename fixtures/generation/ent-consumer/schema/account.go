package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

type Account struct{ ent.Schema }

func (Account) Mixin() []ent.Mixin { return []ent.Mixin{SharedMixin{CRUD: true}} }

func (Account) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		nexaent.Schema(nexaent.SchemaMeta{
			Label:       localized("account.label", "账户", "Account"),
			Description: localized("account.description", "账户记录", "Account record"),
			Identity:    nexaent.IdentityEntID,
			Scope:       nexaent.ScopeTenant,
		}),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDCreate),
	}
}

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Annotations(nexaent.Field(nexaent.FieldMeta{
			Label:       localized("account.name.label", "名称", "Name"),
			Description: localized("account.name.description", "账户名称", "Account name"),
			UIHint:      nexaent.UIHintText,
			Visibility:  nexaent.VisibilityPublic,
			CRUD:        &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate},
		})),
		field.Enum("state").Values("active", "disabled").Default("active").Annotations(nexaent.Field(nexaent.FieldMeta{
			Label:       localized("account.state.label", "状态", "State"),
			Description: localized("account.state.description", "账户状态", "Account state"),
			UIHint:      nexaent.UIHintSelect,
			Visibility:  nexaent.VisibilityPublic,
			CRUD:        &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate},
		})),
	}
}
