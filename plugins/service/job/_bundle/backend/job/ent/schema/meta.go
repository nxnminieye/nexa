package schema

import "github.com/nxnminieye/nexa/nexaent"

func localized(key, zhCN, enUS string) nexaent.LocalizedText {
	return nexaent.LocalizedText{Key: key, ZhCN: zhCN, EnUS: enUS}
}

func schemaMeta(key, zhCN, enUS string) nexaent.SchemaMeta {
	return nexaent.SchemaMeta{
		Label:       localized("job."+key+".label", zhCN, enUS),
		Description: localized("job."+key+".description", zhCN, enUS),
		Identity:    nexaent.IdentityEntID,
		Scope:       nexaent.ScopeGlobal,
	}
}

func fieldMeta(key, zhCN, enUS string, hint nexaent.UIHint, visibility nexaent.FieldVisibility) nexaent.Annotation {
	return nexaent.Field(nexaent.FieldMeta{
		Label:       localized("job."+key+".label", zhCN, enUS),
		Description: localized("job."+key+".description", zhCN, enUS),
		UIHint:      hint,
		Visibility:  visibility,
	})
}
