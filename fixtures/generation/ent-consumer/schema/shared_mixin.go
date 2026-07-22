package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
	"github.com/nxnminieye/nexa/nexaent"
)

type SharedMixin struct {
	mixin.Schema
	CRUD bool
}

func (m SharedMixin) Fields() []ent.Field {
	var policy *nexaent.CRUDFieldPolicy
	if m.CRUD {
		policy = &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: nexaent.MutationNone}
	}
	return []ent.Field{
		field.String("source").
			Default("system").
			Immutable().
			Annotations(nexaent.Field(nexaent.FieldMeta{
				Label:       localized("shared.source", "来源", "Source"),
				Description: localized("shared.source.description", "记录来源", "Record source"),
				UIHint:      nexaent.UIHintReadonly,
				Visibility:  nexaent.VisibilityPublic,
				CRUD:        policy,
			})),
	}
}

func localized(key, zhCN, enUS string) nexaent.LocalizedText {
	return nexaent.LocalizedText{Key: key, ZhCN: zhCN, EnUS: enUS}
}
