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
	return []entschema.Annotation{
		schemaMeta("tenant", "租户", "Tenant", nexaent.ScopeGlobal),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDUpdate),
	}
}
func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").Annotations(publicField("tenant.code", "租户编码", "Tenant code", nexaent.UIHintText, nexaent.MutationNone)),
		field.String("name").Annotations(publicField("tenant.name", "租户名称", "Tenant name", nexaent.UIHintText, nexaent.MutationUpdate)),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(metadataField("tenant.status", "租户状态", "Tenant status", nexaent.UIHintSelect, nexaent.VisibilityPublic)),
		field.Int64("version").Default(1).Annotations(metadataField("tenant.version", "版本", "Version", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
	}
}
func (Tenant) Indexes() []ent.Index { return []ent.Index{index.Fields("code").Unique()} }

type IdentityAccount struct{ ent.Schema }

func (IdentityAccount) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("identity_account", "身份账号", "Identity account", nexaent.ScopeGlobal),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDUpdate),
	}
}
func (IdentityAccount) Fields() []ent.Field {
	return []ent.Field{
		field.String("identity_source_code").Default("").Annotations(metadataField("identity_account.source", "身份来源", "Identity source", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("external_subject").Default("").Annotations(metadataField("identity_account.subject", "外部主体", "External subject", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.String("username").Annotations(metadataField("identity_account.username", "用户名", "Username", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("email").Optional().Annotations(metadataField("identity_account.email", "邮箱", "Email", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("display_name").Optional().Annotations(metadataField("identity_account.display_name", "显示名称", "Display name", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(metadataField("identity_account.status", "账号状态", "Account status", nexaent.UIHintSelect, nexaent.VisibilityPublic)),
		field.Int64("credential_version").Default(1).Annotations(metadataField("identity_account.credential_version", "凭据版本", "Credential version", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
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
		field.Int64("version").Default(1).Annotations(metadataField("tenant_member.version", "版本", "Version", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
	}
}
func (TenantMember) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "identity_account_id").Unique()}
}
func (TenantMember) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("identity_account", IdentityAccount.Type).Field("identity_account_id").Unique().Required(),
		edge.From("manual_role_grants", TenantMemberRoleGrant.Type).Ref("member"),
		edge.From("managed_role_grants", ManagedTenantMemberRoleGrant.Type).Ref("member"),
	}
}

type Role struct{ ent.Schema }

func (Role) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("role", "角色", "Role", nexaent.ScopeTenant),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet, nexaent.CRUDCreate, nexaent.CRUDUpdate),
		entsql.Annotation{Checks: map[string]string{
			"role_managed_owner": "(managed = FALSE AND source_owner = '' AND source_key = '' AND source_digest = '') OR (managed = TRUE AND source_owner <> '' AND source_key <> '' AND source_digest <> '')",
		}},
	}
}
func (Role) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Annotations(internalCRUDField("role.tenant", "租户", "Tenant", "code")),
		field.String("code").Annotations(publicField("role.code", "角色编码", "Role code", nexaent.UIHintText, nexaent.MutationCreate)),
		field.String("name").Annotations(publicField("role.name", "角色名称", "Role name", nexaent.UIHintText, nexaent.MutationCreateUpdate)),
		field.String("description").Optional().Annotations(publicField("role.description", "角色说明", "Role description", nexaent.UIHintTextarea, nexaent.MutationCreateUpdate)),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(publicField("role.status", "角色状态", "Role status", nexaent.UIHintSelect, nexaent.MutationUpdate)),
		field.Bool("managed").Default(false).Annotations(publicField("role.managed", "托管角色", "Managed role", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_owner").Default("").Annotations(publicField("role.source_owner", "来源所有者", "Source owner", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_key").Default("").Annotations(publicField("role.source_key", "来源标识", "Source key", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_digest").Default("").Annotations(publicField("role.source_digest", "来源摘要", "Source digest", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.Int64("version").Default(1).Annotations(metadataField("role.version", "版本", "Version", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
	}
}
func (Role) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "code").Unique(),
		index.Fields("tenant_id", "source_owner", "source_key").Unique().Annotations(entsql.IndexWhere("managed = TRUE")),
	}
}
func (Role) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.From("manual_member_grants", TenantMemberRoleGrant.Type).Ref("role"),
		edge.To("permissions", Permission.Type).StorageKey(edge.Table("role_permissions"), edge.Columns("role_id", "permission_id")),
		edge.From("managed_member_grants", ManagedTenantMemberRoleGrant.Type).Ref("role"),
		edge.From("permission_grants", RolePermissionGrant.Type).Ref("role"),
		edge.From("menu_grants", RoleMenuGrant.Type).Ref("role"),
	}
}

type TenantMemberRoleGrant struct{ ent.Schema }

func (TenantMemberRoleGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("tenant_member_role_grant", "人工成员角色授权", "Manual member role grant", nexaent.ScopeTenant),
		entsql.Annotation{Table: "tenant_member_roles"},
	}
}
func (TenantMemberRoleGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Annotations(internalField("tenant_member_role_grant.tenant", "租户", "Tenant", "code")),
		field.Int("tenant_member_id").Annotations(internalField("tenant_member_role_grant.member", "租户成员", "Tenant member", "id")),
		field.Int("role_id").Annotations(internalField("tenant_member_role_grant.role", "角色", "Role", "code")),
	}
}
func (TenantMemberRoleGrant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "tenant_member_id", "role_id").Unique()}
}
func (TenantMemberRoleGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("member", TenantMember.Type).Field("tenant_member_id").Unique().Required(),
		edge.To("role", Role.Type).Field("role_id").Unique().Required(),
	}
}

type ManagedTenantMemberRoleGrant struct{ ent.Schema }

func (ManagedTenantMemberRoleGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("managed_tenant_member_role_grant", "托管成员角色授权", "Managed member role grant", nexaent.ScopeTenant),
		entsql.Annotation{Table: "managed_tenant_member_roles", Checks: map[string]string{
			"managed_member_role_owner": "source_owner <> '' AND source_digest <> ''",
		}},
	}
}
func (ManagedTenantMemberRoleGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Annotations(internalField("managed_tenant_member_role_grant.tenant", "租户", "Tenant", "code")),
		field.Int("tenant_member_id").Annotations(internalField("managed_tenant_member_role_grant.member", "租户成员", "Tenant member", "id")),
		field.Int("role_id").Annotations(internalField("managed_tenant_member_role_grant.role", "角色", "Role", "code")),
		field.String("source_owner").Annotations(metadataField("managed_tenant_member_role_grant.source_owner", "来源所有者", "Source owner", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
		field.String("source_digest").Annotations(metadataField("managed_tenant_member_role_grant.source_digest", "来源摘要", "Source digest", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
	}
}
func (ManagedTenantMemberRoleGrant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "tenant_member_id", "role_id", "source_owner").Unique()}
}
func (ManagedTenantMemberRoleGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("member", TenantMember.Type).Field("tenant_member_id").Unique().Required(),
		edge.To("role", Role.Type).Field("role_id").Unique().Required(),
	}
}

type Menu struct{ ent.Schema }

func (Menu) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("menu", "菜单", "Menu", nexaent.ScopeGlobal),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet),
		entsql.Annotation{Checks: map[string]string{"menu_source_owner": "source_owner <> '' AND source_key <> '' AND source_digest <> ''"}},
	}
}
func (Menu) Fields() []ent.Field {
	return []ent.Field{
		field.String("source_owner").Annotations(publicField("menu.source_owner", "来源所有者", "Source owner", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_key").Annotations(publicField("menu.source_key", "来源标识", "Source key", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_digest").Annotations(publicField("menu.source_digest", "来源摘要", "Source digest", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("code").Annotations(publicField("menu.code", "菜单编码", "Menu code", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("parent_code").Default("").Annotations(publicField("menu.parent_code", "父菜单", "Parent menu", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("name").Annotations(publicField("menu.name", "菜单名称", "Menu name", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("path").Default("").Annotations(publicField("menu.path", "菜单路径", "Menu path", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("component").Default("").Annotations(publicField("menu.component", "菜单组件", "Menu component", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("icon").Default("").Annotations(publicField("menu.icon", "菜单图标", "Menu icon", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.Int("sort_order").Default(0).Annotations(publicField("menu.sort_order", "菜单排序", "Menu sort order", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(publicField("menu.status", "菜单状态", "Menu status", nexaent.UIHintSelect, nexaent.MutationNone)),
	}
}
func (Menu) Indexes() []ent.Index {
	return []ent.Index{index.Fields("code").Unique(), index.Fields("source_owner", "source_key").Unique()}
}
func (Menu) Edges() []ent.Edge {
	return []ent.Edge{edge.From("role_grants", RoleMenuGrant.Type).Ref("menu")}
}

type PermissionResource struct{ ent.Schema }

func (PermissionResource) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("permission_resource", "权限资源", "Permission resource", nexaent.ScopeGlobal),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet),
		entsql.Annotation{Checks: map[string]string{"permission_resource_source_owner": "source_owner <> '' AND source_key <> '' AND source_digest <> ''"}},
	}
}
func (PermissionResource) Fields() []ent.Field {
	return []ent.Field{
		field.String("source_owner").Annotations(publicField("permission_resource.source_owner", "来源所有者", "Source owner", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_key").Annotations(publicField("permission_resource.source_key", "来源标识", "Source key", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_digest").Annotations(publicField("permission_resource.source_digest", "来源摘要", "Source digest", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("code").Annotations(publicField("permission_resource.code", "资源编码", "Resource code", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("name").Annotations(publicField("permission_resource.name", "资源名称", "Resource name", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("description").Optional().Annotations(publicField("permission_resource.description", "资源说明", "Resource description", nexaent.UIHintTextarea, nexaent.MutationNone)),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(publicField("permission_resource.status", "资源状态", "Resource status", nexaent.UIHintSelect, nexaent.MutationNone)),
	}
}
func (PermissionResource) Indexes() []ent.Index {
	return []ent.Index{index.Fields("code").Unique(), index.Fields("source_owner", "source_key").Unique()}
}
func (PermissionResource) Edges() []ent.Edge {
	return []ent.Edge{edge.From("actions", PermissionAction.Type).Ref("resource")}
}

type PermissionAction struct{ ent.Schema }

func (PermissionAction) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("permission_action", "权限动作", "Permission action", nexaent.ScopeGlobal),
		nexaent.CRUD(nexaent.CRUDList, nexaent.CRUDGet),
		entsql.Annotation{Checks: map[string]string{"permission_action_source_owner": "source_owner <> '' AND source_key <> '' AND source_digest <> ''"}},
	}
}
func (PermissionAction) Fields() []ent.Field {
	return []ent.Field{
		field.String("source_owner").Annotations(publicField("permission_action.source_owner", "来源所有者", "Source owner", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_key").Annotations(publicField("permission_action.source_key", "来源标识", "Source key", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("source_digest").Annotations(publicField("permission_action.source_digest", "来源摘要", "Source digest", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.Int("permission_resource_id").Annotations(internalField("permission_action.resource", "权限资源", "Permission resource", "code")),
		field.String("code").Annotations(publicField("permission_action.code", "动作编码", "Action code", nexaent.UIHintPermission, nexaent.MutationNone)),
		field.String("name").Annotations(publicField("permission_action.name", "动作名称", "Action name", nexaent.UIHintReadonly, nexaent.MutationNone)),
		field.String("description").Optional().Annotations(publicField("permission_action.description", "动作说明", "Action description", nexaent.UIHintTextarea, nexaent.MutationNone)),
		field.Enum("status").Values("enabled", "disabled").Default("enabled").Annotations(publicField("permission_action.status", "动作状态", "Action status", nexaent.UIHintSelect, nexaent.MutationNone)),
	}
}
func (PermissionAction) Indexes() []ent.Index {
	return []ent.Index{index.Fields("code").Unique(), index.Fields("source_owner", "source_key").Unique()}
}
func (PermissionAction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("resource", PermissionResource.Type).Field("permission_resource_id").Unique().Required(),
		edge.From("role_grants", RolePermissionGrant.Type).Ref("permission_action"),
	}
}

type RolePermissionGrant struct{ ent.Schema }

func (RolePermissionGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("role_permission_grant", "角色权限授权", "Role permission grant", nexaent.ScopeTenant),
		entsql.Annotation{Table: "role_permission_actions"},
	}
}
func (RolePermissionGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Annotations(internalField("role_permission_grant.tenant", "租户", "Tenant", "code")),
		field.Int("role_id").Annotations(internalField("role_permission_grant.role", "角色", "Role", "code")),
		field.Int("permission_action_id").Annotations(internalField("role_permission_grant.permission_action", "权限动作", "Permission action", "code")),
	}
}
func (RolePermissionGrant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "role_id", "permission_action_id").Unique()}
}
func (RolePermissionGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("role", Role.Type).Field("role_id").Unique().Required(),
		edge.To("permission_action", PermissionAction.Type).Field("permission_action_id").Unique().Required(),
	}
}

type RoleMenuGrant struct{ ent.Schema }

func (RoleMenuGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		schemaMeta("role_menu_grant", "角色菜单授权", "Role menu grant", nexaent.ScopeTenant),
		entsql.Annotation{Table: "role_menus"},
	}
}
func (RoleMenuGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Annotations(internalField("role_menu_grant.tenant", "租户", "Tenant", "code")),
		field.Int("role_id").Annotations(internalField("role_menu_grant.role", "角色", "Role", "code")),
		field.Int("menu_id").Annotations(internalField("role_menu_grant.menu", "菜单", "Menu", "code")),
	}
}
func (RoleMenuGrant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "role_id", "menu_id").Unique()}
}
func (RoleMenuGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("role", Role.Type).Field("role_id").Unique().Required(),
		edge.To("menu", Menu.Type).Field("menu_id").Unique().Required(),
	}
}

type CatalogSourceState struct{ ent.Schema }

func (CatalogSourceState) Annotations() []entschema.Annotation {
	return []entschema.Annotation{schemaMeta("catalog_source_state", "目录来源状态", "Catalog source state", nexaent.ScopeGlobal)}
}
func (CatalogSourceState) Fields() []ent.Field {
	return []ent.Field{
		field.String("source_id").Annotations(metadataField("catalog_source_state.source_id", "来源标识", "Source ID", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
		field.String("source_digest").Annotations(metadataField("catalog_source_state.source_digest", "来源摘要", "Source digest", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
	}
}
func (CatalogSourceState) Indexes() []ent.Index {
	return []ent.Index{index.Fields("source_id").Unique()}
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
