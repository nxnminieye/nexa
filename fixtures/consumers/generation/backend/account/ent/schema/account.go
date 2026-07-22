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
			Label:       localized("account.label", "Account", "Account"),
			Description: localized("account.description", "Account record", "Account record"),
			Identity:    nexaent.IdentityEntID,
			Scope:       nexaent.ScopeTenant,
		}),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDCreate),
	}
}

func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Annotations(nexaent.Field(nexaent.FieldMeta{
			Label:       localized("account.name.label", "Name", "Name"),
			Description: localized("account.name.description", "Account name", "Account name"),
			UIHint:      nexaent.UIHintText,
			Visibility:  nexaent.VisibilityPublic,
			CRUD:        &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationCreateUpdate},
		})),
	}
}

func localized(key, zhCN, enUS string) nexaent.LocalizedText {
	return nexaent.LocalizedText{Key: key, ZhCN: zhCN, EnUS: enUS}
}
