package schema

import "github.com/nxnminieye/nexa/nexaent"

func localized(key, zhCN, enUS string) nexaent.LocalizedText {
	return nexaent.LocalizedText{Key: key, ZhCN: zhCN, EnUS: enUS}
}

func schemaMeta(key, zhCN, enUS string, scope nexaent.RecordScope) nexaent.Annotation {
	return nexaent.Schema(nexaent.SchemaMeta{
		Label:       localized("core."+key+".label", zhCN, enUS),
		Description: localized("core."+key+".description", zhCN+"数据", enUS+" data"),
		Identity:    nexaent.IdentityEntID,
		Scope:       scope,
	})
}

func publicField(key, zhCN, enUS string, hint nexaent.UIHint, mutation nexaent.MutationPolicy) nexaent.Annotation {
	return nexaent.Field(nexaent.FieldMeta{
		Label:       localized("core."+key+".label", zhCN, enUS),
		Description: localized("core."+key+".description", zhCN, enUS),
		UIHint:      hint,
		Visibility:  nexaent.VisibilityPublic,
		CRUD:        &nexaent.CRUDFieldPolicy{Read: nexaent.ReadInclude, Mutation: mutation},
	})
}

func metadataField(key, zhCN, enUS string, hint nexaent.UIHint, visibility nexaent.FieldVisibility) nexaent.Annotation {
	return nexaent.Field(nexaent.FieldMeta{
		Label:       localized("core."+key+".label", zhCN, enUS),
		Description: localized("core."+key+".description", zhCN, enUS),
		UIHint:      hint,
		Visibility:  visibility,
	})
}

func internalField(key, zhCN, enUS, display string) nexaent.Annotation {
	return nexaent.Field(nexaent.FieldMeta{Label: localized("core."+key+".label", zhCN, enUS), Description: localized("core."+key+".description", zhCN, enUS), UIHint: nexaent.UIHintReference, PhysicalDisplay: &nexaent.PhysicalDisplay{Field: display}, Visibility: nexaent.VisibilityInternal})
}

func internalCRUDField(key, zhCN, enUS, display string) nexaent.Annotation {
	return nexaent.Field(nexaent.FieldMeta{Label: localized("core."+key+".label", zhCN, enUS), Description: localized("core."+key+".description", zhCN, enUS), UIHint: nexaent.UIHintReference, PhysicalDisplay: &nexaent.PhysicalDisplay{Field: display}, Visibility: nexaent.VisibilityInternal, CRUD: &nexaent.CRUDFieldPolicy{Read: nexaent.ReadExclude, Mutation: nexaent.MutationNone}})
}

func sensitiveField(key, zhCN, enUS string) nexaent.Annotation {
	return nexaent.Field(nexaent.FieldMeta{
		Label:       localized("core."+key+".label", zhCN, enUS),
		Description: localized("core."+key+".description", zhCN, enUS),
		UIHint:      nexaent.UIHintSensitive,
		Visibility:  nexaent.VisibilitySensitive,
	})
}
