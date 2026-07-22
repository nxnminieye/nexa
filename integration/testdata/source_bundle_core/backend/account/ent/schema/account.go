package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"

	"github.com/nxnminieye/nexa/nexaent"
)

type Account struct{ ent.Schema }

func (Account) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		nexaent.Schema(nexaent.SchemaMeta{
			Label:       localized("account.label", "账号", "Account"),
			Description: localized("account.description", "业务账号", "Business account"),
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
			Description: localized("account.name.description", "账号名称", "Account name"),
			UIHint:      nexaent.UIHintText,
			Visibility:  nexaent.VisibilityPublic,
			CRUD:        &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate},
		})),
	}
}

func localized(key, zhCN, enUS string) nexaent.LocalizedText {
	return nexaent.LocalizedText{Key: key, ZhCN: zhCN, EnUS: enUS}
}
