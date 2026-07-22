package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/nxnminieye/nexa/nexaent"
)

type Tenant struct{ ent.Schema }

func (Tenant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{schemaMeta("tenant", "租户", "Tenant", nexaent.ScopeGlobal)}
}
func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").Annotations(metadataField("tenant.code", "租户编码", "Tenant code", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("name").Annotations(metadataField("tenant.name", "租户名称", "Tenant name", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(metadataField("tenant.status", "租户状态", "Tenant status", nexaent.UIHintSelect, nexaent.VisibilityPublic)),
	}
}
func (Tenant) Indexes() []ent.Index { return []ent.Index{index.Fields("code").Unique()} }

type IdentityAccount struct{ ent.Schema }

func (IdentityAccount) Annotations() []entschema.Annotation {
	return []entschema.Annotation{schemaMeta("identity_account", "身份账号", "Identity account", nexaent.ScopeGlobal)}
}
func (IdentityAccount) Fields() []ent.Field {
	return []ent.Field{
		field.String("identity_source_code").Default("").Annotations(metadataField("identity_account.source", "身份来源", "Identity source", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("external_subject").Default("").Annotations(metadataField("identity_account.subject", "外部主体", "External subject", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.String("username").Annotations(metadataField("identity_account.username", "用户名", "Username", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("email").Optional().Annotations(metadataField("identity_account.email", "邮箱", "Email", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("display_name").Optional().Annotations(metadataField("identity_account.display_name", "显示名称", "Display name", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("password_hash").Default("").Sensitive().Annotations(sensitiveField("identity_account.password_hash", "密码摘要", "Password hash")),
	}
}
func (IdentityAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("username").Unique(),
		index.Fields("identity_source_code", "external_subject").Unique().Annotations(entsql.IndexWhere("external_subject <> ''")),
	}
}

type TenantMember struct{ ent.Schema }

func (TenantMember) Annotations() []entschema.Annotation {
	return []entschema.Annotation{schemaMeta("tenant_member", "租户成员", "Tenant member", nexaent.ScopeTenant)}
}
func (TenantMember) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Annotations(internalField("tenant_member.tenant", "租户", "Tenant", "code")),
		field.Int("identity_account_id").Annotations(internalField("tenant_member.account", "身份账号", "Identity account", "username")),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(metadataField("tenant_member.status", "成员状态", "Member status", nexaent.UIHintSelect, nexaent.VisibilityPublic)),
	}
}
func (TenantMember) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "identity_account_id").Unique()}
}
func (TenantMember) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("identity_account", IdentityAccount.Type).Field("identity_account_id").Unique().Required(),
		edge.To("roles", Role.Type).StorageKey(edge.Table("tenant_member_roles"), edge.Columns("tenant_member_id", "role_id")),
	}
}

type Role struct{ ent.Schema }

func (Role) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("role", "角色", "Role", nexaent.ScopeTenant),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDCreate, nexaent.CRUDUpdate, nexaent.CRUDDelete),
	}
}
func (Role) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Annotations(internalCRUDField("role.tenant", "租户", "Tenant", "code")),
		field.String("code").Annotations(publicField("role.code", "角色编码", "Role code", nexaent.UIHintText, nexaent.MutationCreate)),
		field.String("name").Annotations(publicField("role.name", "角色名称", "Role name", nexaent.UIHintText, nexaent.MutationCreateUpdate)),
	}
}
func (Role) Indexes() []ent.Index { return []ent.Index{index.Fields("tenant_id", "code").Unique()} }
func (Role) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.From("members", TenantMember.Type).Ref("roles"),
		edge.To("permissions", Permission.Type).StorageKey(edge.Table("role_permissions"), edge.Columns("role_id", "permission_id")),
	}
}

type Permission struct{ ent.Schema }

func (Permission) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("permission", "权限", "Permission", nexaent.ScopeGlobal),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet),
	}
}
func (Permission) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").Annotations(publicField("permission.code", "权限编码", "Permission code", nexaent.UIHintPermission, nexaent.MutationNone)),
		field.String("description").Optional().Annotations(publicField("permission.description", "权限说明", "Permission description", nexaent.UIHintTextarea, nexaent.MutationNone)),
	}
}
func (Permission) Indexes() []ent.Index { return []ent.Index{index.Fields("code").Unique()} }
func (Permission) Edges() []ent.Edge {
	return []ent.Edge{edge.From("roles", Role.Type).Ref("permissions")}
}

type AuthSession struct{ ent.Schema }

func (AuthSession) Annotations() []entschema.Annotation {
	return []entschema.Annotation{schemaMeta("auth_session", "认证会话", "Authentication session", nexaent.ScopeTenant)}
}
func (AuthSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("session_id").Annotations(metadataField("auth_session.id", "会话标识", "Session ID", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.Int("tenant_id").Annotations(internalField("auth_session.tenant", "租户", "Tenant", "code")),
		field.Int("identity_account_id").Annotations(internalField("auth_session.account", "身份账号", "Identity account", "username")),
		field.String("access_token_hash").Sensitive().Annotations(sensitiveField("auth_session.access_hash", "访问令牌摘要", "Access token hash")),
		field.String("refresh_token_hash").Sensitive().Annotations(sensitiveField("auth_session.refresh_hash", "刷新令牌摘要", "Refresh token hash")),
		field.Time("access_expires_at").Annotations(metadataField("auth_session.access_expiry", "访问过期时间", "Access expiry", nexaent.UIHintDatetime, nexaent.VisibilityPublic)),
		field.Time("refresh_expires_at").Annotations(metadataField("auth_session.refresh_expiry", "刷新过期时间", "Refresh expiry", nexaent.UIHintDatetime, nexaent.VisibilityPublic)),
		field.Bool("revoked").Default(false).Annotations(metadataField("auth_session.revoked", "已撤销", "Revoked", nexaent.UIHintSwitch, nexaent.VisibilityPublic)),
	}
}
func (AuthSession) Indexes() []ent.Index {
	return []ent.Index{index.Fields("session_id").Unique(), index.Fields("refresh_token_hash").Unique()}
}
func (AuthSession) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("identity_account", IdentityAccount.Type).Field("identity_account_id").Unique().Required(),
	}
}
