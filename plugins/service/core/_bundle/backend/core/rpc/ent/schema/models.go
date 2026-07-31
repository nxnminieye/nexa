// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// @nexa label.zh-CN: "租户"
// @nexa label.en-US: "Tenant"
// @nexa description.zh-CN: "租户数据"
// @nexa description.en-US: "Tenant data"
// @nexa scope: "global"
// @nexa crud.operations: ["list","get","update"]
type Tenant struct{ ent.Schema }

func (Tenant) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租户编码"
		// @nexa label.en-US: "Tenant code"
		// @nexa description.zh-CN: "租户编码"
		// @nexa description.en-US: "Tenant code"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("code"),
		// @nexa label.zh-CN: "租户名称"
		// @nexa label.en-US: "Tenant name"
		// @nexa description.zh-CN: "租户名称"
		// @nexa description.en-US: "Tenant name"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "update"
		field.String("name"),
		// @nexa label.zh-CN: "租户状态"
		// @nexa label.en-US: "Tenant status"
		// @nexa description.zh-CN: "租户状态"
		// @nexa description.en-US: "Tenant status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		field.Enum("status").Values("enabled", "disabled").Default("enabled"),
		// @nexa label.zh-CN: "版本"
		// @nexa label.en-US: "Version"
		// @nexa description.zh-CN: "版本"
		// @nexa description.en-US: "Version"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.Int64("version").Default(1),
	}
}
func (Tenant) Indexes() []ent.Index { return []ent.Index{index.Fields("code").Unique()} }

// @nexa label.zh-CN: "身份账号"
// @nexa label.en-US: "Identity account"
// @nexa description.zh-CN: "身份账号数据"
// @nexa description.en-US: "Identity account data"
// @nexa scope: "global"
// @nexa crud.operations: ["list","get","update"]
type IdentityAccount struct{ ent.Schema }

func (IdentityAccount) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "身份来源"
		// @nexa label.en-US: "Identity source"
		// @nexa description.zh-CN: "身份来源"
		// @nexa description.en-US: "Identity source"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		field.String("identity_source_code").Default(""),
		// @nexa label.zh-CN: "外部主体"
		// @nexa label.en-US: "External subject"
		// @nexa description.zh-CN: "外部主体"
		// @nexa description.en-US: "External subject"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("external_subject").Default(""),
		// @nexa label.zh-CN: "用户名"
		// @nexa label.en-US: "Username"
		// @nexa description.zh-CN: "用户名"
		// @nexa description.en-US: "Username"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		field.String("username"),
		// @nexa label.zh-CN: "邮箱"
		// @nexa label.en-US: "Email"
		// @nexa description.zh-CN: "邮箱"
		// @nexa description.en-US: "Email"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		field.String("email").Optional(),
		// @nexa label.zh-CN: "显示名称"
		// @nexa label.en-US: "Display name"
		// @nexa description.zh-CN: "显示名称"
		// @nexa description.en-US: "Display name"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		field.String("display_name").Optional(),
		// @nexa label.zh-CN: "账号状态"
		// @nexa label.en-US: "Account status"
		// @nexa description.zh-CN: "账号状态"
		// @nexa description.en-US: "Account status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		field.Enum("status").Values("enabled", "disabled").Default("enabled"),
		// @nexa label.zh-CN: "凭据版本"
		// @nexa label.en-US: "Credential version"
		// @nexa description.zh-CN: "凭据版本"
		// @nexa description.en-US: "Credential version"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.Int64("credential_version").Default(1),
		// @nexa label.zh-CN: "密码摘要"
		// @nexa label.en-US: "Password hash"
		// @nexa description.zh-CN: "密码摘要"
		// @nexa description.en-US: "Password hash"
		// @nexa ui.control: "sensitive"
		// @nexa visibility: "sensitive"
		field.String("password_hash").Default("").Sensitive(),
	}
}
func (IdentityAccount) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("username").Unique(),
		index.Fields("identity_source_code", "external_subject").Unique().Annotations(entsql.IndexWhere("external_subject <> ''")),
	}
}

// @nexa label.zh-CN: "租户成员"
// @nexa label.en-US: "Tenant member"
// @nexa description.zh-CN: "租户成员数据"
// @nexa description.en-US: "Tenant member data"
// @nexa scope: "tenant"
type TenantMember struct{ ent.Schema }

func (TenantMember) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租户"
		// @nexa label.en-US: "Tenant"
		// @nexa description.zh-CN: "租户"
		// @nexa description.en-US: "Tenant"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Tenant","display":"code"}
		// @nexa visibility: "internal"
		field.Int("tenant_id"),
		// @nexa label.zh-CN: "身份账号"
		// @nexa label.en-US: "Identity account"
		// @nexa description.zh-CN: "身份账号"
		// @nexa description.en-US: "Identity account"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"IdentityAccount","display":"username"}
		// @nexa visibility: "internal"
		field.Int("identity_account_id"),
		// @nexa label.zh-CN: "成员状态"
		// @nexa label.en-US: "Member status"
		// @nexa description.zh-CN: "成员状态"
		// @nexa description.en-US: "Member status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		field.Enum("status").Values("enabled", "disabled").Default("enabled"),
		// @nexa label.zh-CN: "版本"
		// @nexa label.en-US: "Version"
		// @nexa description.zh-CN: "版本"
		// @nexa description.en-US: "Version"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.Int64("version").Default(1),
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

// @nexa label.zh-CN: "角色"
// @nexa label.en-US: "Role"
// @nexa description.zh-CN: "角色数据"
// @nexa description.en-US: "Role data"
// @nexa scope: "tenant"
// @nexa crud.operations: ["list","get","create","update"]
type Role struct{ ent.Schema }

func (Role) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Checks: map[string]string{
			"role_managed_owner": "(managed = FALSE AND source_owner = '' AND source_key = '' AND source_digest = '') OR (managed = TRUE AND source_owner <> '' AND source_key <> '' AND source_digest <> '')",
		}},
	}
}
func (Role) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租户"
		// @nexa label.en-US: "Tenant"
		// @nexa description.zh-CN: "租户"
		// @nexa description.en-US: "Tenant"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Tenant","display":"code"}
		// @nexa visibility: "internal"
		// @nexa crud.read: "exclude"
		// @nexa crud.mutation: "none"
		field.Int("tenant_id"),
		// @nexa label.zh-CN: "角色编码"
		// @nexa label.en-US: "Role code"
		// @nexa description.zh-CN: "角色编码"
		// @nexa description.en-US: "Role code"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create"
		field.String("code"),
		// @nexa label.zh-CN: "角色名称"
		// @nexa label.en-US: "Role name"
		// @nexa description.zh-CN: "角色名称"
		// @nexa description.en-US: "Role name"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create-update"
		field.String("name"),
		// @nexa label.zh-CN: "角色说明"
		// @nexa label.en-US: "Role description"
		// @nexa description.zh-CN: "角色说明"
		// @nexa description.en-US: "Role description"
		// @nexa ui.control: "textarea"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create-update"
		field.String("description").Optional(),
		// @nexa label.zh-CN: "角色状态"
		// @nexa label.en-US: "Role status"
		// @nexa description.zh-CN: "角色状态"
		// @nexa description.en-US: "Role status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "update"
		field.Enum("status").Values("enabled", "disabled").Default("enabled"),
		// @nexa label.zh-CN: "托管角色"
		// @nexa label.en-US: "Managed role"
		// @nexa description.zh-CN: "托管角色"
		// @nexa description.en-US: "Managed role"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.Bool("managed").Default(false),
		// @nexa label.zh-CN: "来源所有者"
		// @nexa label.en-US: "Source owner"
		// @nexa description.zh-CN: "来源所有者"
		// @nexa description.en-US: "Source owner"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_owner").Default(""),
		// @nexa label.zh-CN: "来源标识"
		// @nexa label.en-US: "Source key"
		// @nexa description.zh-CN: "来源标识"
		// @nexa description.en-US: "Source key"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_key").Default(""),
		// @nexa label.zh-CN: "来源摘要"
		// @nexa label.en-US: "Source digest"
		// @nexa description.zh-CN: "来源摘要"
		// @nexa description.en-US: "Source digest"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_digest").Default(""),
		// @nexa label.zh-CN: "版本"
		// @nexa label.en-US: "Version"
		// @nexa description.zh-CN: "版本"
		// @nexa description.en-US: "Version"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.Int64("version").Default(1),
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

// @nexa label.zh-CN: "人工成员角色授权"
// @nexa label.en-US: "Manual member role grant"
// @nexa description.zh-CN: "人工成员角色授权数据"
// @nexa description.en-US: "Manual member role grant data"
// @nexa scope: "tenant"
type TenantMemberRoleGrant struct{ ent.Schema }

func (TenantMemberRoleGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Table: "tenant_member_roles"},
	}
}
func (TenantMemberRoleGrant) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租户"
		// @nexa label.en-US: "Tenant"
		// @nexa description.zh-CN: "租户"
		// @nexa description.en-US: "Tenant"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Tenant","display":"code"}
		// @nexa visibility: "internal"
		field.Int("tenant_id"),
		// @nexa label.zh-CN: "租户成员"
		// @nexa label.en-US: "Tenant member"
		// @nexa description.zh-CN: "租户成员"
		// @nexa description.en-US: "Tenant member"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"TenantMember","display":"id"}
		// @nexa visibility: "internal"
		field.Int("tenant_member_id"),
		// @nexa label.zh-CN: "角色"
		// @nexa label.en-US: "Role"
		// @nexa description.zh-CN: "角色"
		// @nexa description.en-US: "Role"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Role","display":"code"}
		// @nexa visibility: "internal"
		field.Int("role_id"),
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

// @nexa label.zh-CN: "托管成员角色授权"
// @nexa label.en-US: "Managed member role grant"
// @nexa description.zh-CN: "托管成员角色授权数据"
// @nexa description.en-US: "Managed member role grant data"
// @nexa scope: "tenant"
type ManagedTenantMemberRoleGrant struct{ ent.Schema }

func (ManagedTenantMemberRoleGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Table: "managed_tenant_member_roles", Checks: map[string]string{
			"managed_member_role_owner": "source_owner <> '' AND source_digest <> ''",
		}},
	}
}
func (ManagedTenantMemberRoleGrant) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租户"
		// @nexa label.en-US: "Tenant"
		// @nexa description.zh-CN: "租户"
		// @nexa description.en-US: "Tenant"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Tenant","display":"code"}
		// @nexa visibility: "internal"
		field.Int("tenant_id"),
		// @nexa label.zh-CN: "租户成员"
		// @nexa label.en-US: "Tenant member"
		// @nexa description.zh-CN: "租户成员"
		// @nexa description.en-US: "Tenant member"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"TenantMember","display":"id"}
		// @nexa visibility: "internal"
		field.Int("tenant_member_id"),
		// @nexa label.zh-CN: "角色"
		// @nexa label.en-US: "Role"
		// @nexa description.zh-CN: "角色"
		// @nexa description.en-US: "Role"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Role","display":"code"}
		// @nexa visibility: "internal"
		field.Int("role_id"),
		// @nexa label.zh-CN: "来源所有者"
		// @nexa label.en-US: "Source owner"
		// @nexa description.zh-CN: "来源所有者"
		// @nexa description.en-US: "Source owner"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.String("source_owner"),
		// @nexa label.zh-CN: "来源摘要"
		// @nexa label.en-US: "Source digest"
		// @nexa description.zh-CN: "来源摘要"
		// @nexa description.en-US: "Source digest"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.String("source_digest"),
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

// @nexa label.zh-CN: "菜单"
// @nexa label.en-US: "Menu"
// @nexa description.zh-CN: "菜单数据"
// @nexa description.en-US: "Menu data"
// @nexa scope: "global"
// @nexa crud.operations: ["list","get"]
type Menu struct{ ent.Schema }

func (Menu) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Checks: map[string]string{"menu_source_owner": "source_owner <> '' AND source_key <> '' AND source_digest <> ''"}},
	}
}
func (Menu) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "来源所有者"
		// @nexa label.en-US: "Source owner"
		// @nexa description.zh-CN: "来源所有者"
		// @nexa description.en-US: "Source owner"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_owner"),
		// @nexa label.zh-CN: "来源标识"
		// @nexa label.en-US: "Source key"
		// @nexa description.zh-CN: "来源标识"
		// @nexa description.en-US: "Source key"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_key"),
		// @nexa label.zh-CN: "来源摘要"
		// @nexa label.en-US: "Source digest"
		// @nexa description.zh-CN: "来源摘要"
		// @nexa description.en-US: "Source digest"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_digest"),
		// @nexa label.zh-CN: "菜单编码"
		// @nexa label.en-US: "Menu code"
		// @nexa description.zh-CN: "菜单编码"
		// @nexa description.en-US: "Menu code"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("code"),
		// @nexa label.zh-CN: "父菜单"
		// @nexa label.en-US: "Parent menu"
		// @nexa description.zh-CN: "父菜单"
		// @nexa description.en-US: "Parent menu"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("parent_code").Default(""),
		// @nexa label.zh-CN: "菜单名称"
		// @nexa label.en-US: "Menu name"
		// @nexa description.zh-CN: "菜单名称"
		// @nexa description.en-US: "Menu name"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("name"),
		// @nexa label.zh-CN: "菜单路径"
		// @nexa label.en-US: "Menu path"
		// @nexa description.zh-CN: "菜单路径"
		// @nexa description.en-US: "Menu path"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("path").Default(""),
		// @nexa label.zh-CN: "菜单组件"
		// @nexa label.en-US: "Menu component"
		// @nexa description.zh-CN: "菜单组件"
		// @nexa description.en-US: "Menu component"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("component").Default(""),
		// @nexa label.zh-CN: "菜单图标"
		// @nexa label.en-US: "Menu icon"
		// @nexa description.zh-CN: "菜单图标"
		// @nexa description.en-US: "Menu icon"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("icon").Default(""),
		// @nexa label.zh-CN: "菜单排序"
		// @nexa label.en-US: "Menu sort order"
		// @nexa description.zh-CN: "菜单排序"
		// @nexa description.en-US: "Menu sort order"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.Int("sort_order").Default(0),
		// @nexa label.zh-CN: "菜单状态"
		// @nexa label.en-US: "Menu status"
		// @nexa description.zh-CN: "菜单状态"
		// @nexa description.en-US: "Menu status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.Enum("status").Values("enabled", "disabled").Default("enabled"),
	}
}
func (Menu) Indexes() []ent.Index {
	return []ent.Index{index.Fields("code").Unique(), index.Fields("source_owner", "source_key").Unique()}
}
func (Menu) Edges() []ent.Edge {
	return []ent.Edge{edge.From("role_grants", RoleMenuGrant.Type).Ref("menu")}
}

// @nexa label.zh-CN: "权限资源"
// @nexa label.en-US: "Permission resource"
// @nexa description.zh-CN: "权限资源数据"
// @nexa description.en-US: "Permission resource data"
// @nexa scope: "global"
// @nexa crud.operations: ["list","get"]
type PermissionResource struct{ ent.Schema }

func (PermissionResource) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Checks: map[string]string{"permission_resource_source_owner": "source_owner <> '' AND source_key <> '' AND source_digest <> ''"}},
	}
}
func (PermissionResource) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "来源所有者"
		// @nexa label.en-US: "Source owner"
		// @nexa description.zh-CN: "来源所有者"
		// @nexa description.en-US: "Source owner"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_owner"),
		// @nexa label.zh-CN: "来源标识"
		// @nexa label.en-US: "Source key"
		// @nexa description.zh-CN: "来源标识"
		// @nexa description.en-US: "Source key"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_key"),
		// @nexa label.zh-CN: "来源摘要"
		// @nexa label.en-US: "Source digest"
		// @nexa description.zh-CN: "来源摘要"
		// @nexa description.en-US: "Source digest"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_digest"),
		// @nexa label.zh-CN: "资源编码"
		// @nexa label.en-US: "Resource code"
		// @nexa description.zh-CN: "资源编码"
		// @nexa description.en-US: "Resource code"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("code"),
		// @nexa label.zh-CN: "资源名称"
		// @nexa label.en-US: "Resource name"
		// @nexa description.zh-CN: "资源名称"
		// @nexa description.en-US: "Resource name"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("name"),
		// @nexa label.zh-CN: "资源说明"
		// @nexa label.en-US: "Resource description"
		// @nexa description.zh-CN: "资源说明"
		// @nexa description.en-US: "Resource description"
		// @nexa ui.control: "textarea"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("description").Optional(),
		// @nexa label.zh-CN: "资源状态"
		// @nexa label.en-US: "Resource status"
		// @nexa description.zh-CN: "资源状态"
		// @nexa description.en-US: "Resource status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.Enum("status").Values("enabled", "disabled").Default("enabled"),
	}
}
func (PermissionResource) Indexes() []ent.Index {
	return []ent.Index{index.Fields("code").Unique(), index.Fields("source_owner", "source_key").Unique()}
}
func (PermissionResource) Edges() []ent.Edge {
	return []ent.Edge{edge.From("actions", PermissionAction.Type).Ref("resource")}
}

// @nexa label.zh-CN: "权限动作"
// @nexa label.en-US: "Permission action"
// @nexa description.zh-CN: "权限动作数据"
// @nexa description.en-US: "Permission action data"
// @nexa scope: "global"
// @nexa crud.operations: ["list","get"]
type PermissionAction struct{ ent.Schema }

func (PermissionAction) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Checks: map[string]string{"permission_action_source_owner": "source_owner <> '' AND source_key <> '' AND source_digest <> ''"}},
	}
}
func (PermissionAction) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "来源所有者"
		// @nexa label.en-US: "Source owner"
		// @nexa description.zh-CN: "来源所有者"
		// @nexa description.en-US: "Source owner"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_owner"),
		// @nexa label.zh-CN: "来源标识"
		// @nexa label.en-US: "Source key"
		// @nexa description.zh-CN: "来源标识"
		// @nexa description.en-US: "Source key"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_key"),
		// @nexa label.zh-CN: "来源摘要"
		// @nexa label.en-US: "Source digest"
		// @nexa description.zh-CN: "来源摘要"
		// @nexa description.en-US: "Source digest"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("source_digest"),
		// @nexa label.zh-CN: "权限资源"
		// @nexa label.en-US: "Permission resource"
		// @nexa description.zh-CN: "权限资源"
		// @nexa description.en-US: "Permission resource"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"PermissionResource","display":"code"}
		// @nexa visibility: "internal"
		field.Int("permission_resource_id"),
		// @nexa label.zh-CN: "动作编码"
		// @nexa label.en-US: "Action code"
		// @nexa description.zh-CN: "动作编码"
		// @nexa description.en-US: "Action code"
		// @nexa ui.control: "permission"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("code"),
		// @nexa label.zh-CN: "动作名称"
		// @nexa label.en-US: "Action name"
		// @nexa description.zh-CN: "动作名称"
		// @nexa description.en-US: "Action name"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("name"),
		// @nexa label.zh-CN: "动作说明"
		// @nexa label.en-US: "Action description"
		// @nexa description.zh-CN: "动作说明"
		// @nexa description.en-US: "Action description"
		// @nexa ui.control: "textarea"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("description").Optional(),
		// @nexa label.zh-CN: "动作状态"
		// @nexa label.en-US: "Action status"
		// @nexa description.zh-CN: "动作状态"
		// @nexa description.en-US: "Action status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.Enum("status").Values("enabled", "disabled").Default("enabled"),
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

// @nexa label.zh-CN: "角色权限授权"
// @nexa label.en-US: "Role permission grant"
// @nexa description.zh-CN: "角色权限授权数据"
// @nexa description.en-US: "Role permission grant data"
// @nexa scope: "tenant"
type RolePermissionGrant struct{ ent.Schema }

func (RolePermissionGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Table: "role_permission_actions"},
	}
}
func (RolePermissionGrant) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租户"
		// @nexa label.en-US: "Tenant"
		// @nexa description.zh-CN: "租户"
		// @nexa description.en-US: "Tenant"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Tenant","display":"code"}
		// @nexa visibility: "internal"
		field.Int("tenant_id"),
		// @nexa label.zh-CN: "角色"
		// @nexa label.en-US: "Role"
		// @nexa description.zh-CN: "角色"
		// @nexa description.en-US: "Role"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Role","display":"code"}
		// @nexa visibility: "internal"
		field.Int("role_id"),
		// @nexa label.zh-CN: "权限动作"
		// @nexa label.en-US: "Permission action"
		// @nexa description.zh-CN: "权限动作"
		// @nexa description.en-US: "Permission action"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"PermissionAction","display":"code"}
		// @nexa visibility: "internal"
		field.Int("permission_action_id"),
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

// @nexa label.zh-CN: "角色菜单授权"
// @nexa label.en-US: "Role menu grant"
// @nexa description.zh-CN: "角色菜单授权数据"
// @nexa description.en-US: "Role menu grant data"
// @nexa scope: "tenant"
type RoleMenuGrant struct{ ent.Schema }

func (RoleMenuGrant) Annotations() []entschema.Annotation {
	return []entschema.Annotation{
		entsql.Annotation{Table: "role_menus"},
	}
}
func (RoleMenuGrant) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "租户"
		// @nexa label.en-US: "Tenant"
		// @nexa description.zh-CN: "租户"
		// @nexa description.en-US: "Tenant"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Tenant","display":"code"}
		// @nexa visibility: "internal"
		field.Int("tenant_id"),
		// @nexa label.zh-CN: "角色"
		// @nexa label.en-US: "Role"
		// @nexa description.zh-CN: "角色"
		// @nexa description.en-US: "Role"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Role","display":"code"}
		// @nexa visibility: "internal"
		field.Int("role_id"),
		// @nexa label.zh-CN: "菜单"
		// @nexa label.en-US: "Menu"
		// @nexa description.zh-CN: "菜单"
		// @nexa description.en-US: "Menu"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Menu","display":"code"}
		// @nexa visibility: "internal"
		field.Int("menu_id"),
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

// @nexa label.zh-CN: "目录来源状态"
// @nexa label.en-US: "Catalog source state"
// @nexa description.zh-CN: "目录来源状态数据"
// @nexa description.en-US: "Catalog source state data"
// @nexa scope: "global"
type CatalogSourceState struct{ ent.Schema }

func (CatalogSourceState) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "来源标识"
		// @nexa label.en-US: "Source ID"
		// @nexa description.zh-CN: "来源标识"
		// @nexa description.en-US: "Source ID"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.String("source_id"),
		// @nexa label.zh-CN: "来源摘要"
		// @nexa label.en-US: "Source digest"
		// @nexa description.zh-CN: "来源摘要"
		// @nexa description.en-US: "Source digest"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "internal"
		field.String("source_digest"),
	}
}
func (CatalogSourceState) Indexes() []ent.Index {
	return []ent.Index{index.Fields("source_id").Unique()}
}

// @nexa label.zh-CN: "权限"
// @nexa label.en-US: "Permission"
// @nexa description.zh-CN: "权限数据"
// @nexa description.en-US: "Permission data"
// @nexa scope: "global"
// @nexa crud.operations: ["list","get"]
type Permission struct{ ent.Schema }

func (Permission) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "权限编码"
		// @nexa label.en-US: "Permission code"
		// @nexa description.zh-CN: "权限编码"
		// @nexa description.en-US: "Permission code"
		// @nexa ui.control: "permission"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("code"),
		// @nexa label.zh-CN: "权限说明"
		// @nexa label.en-US: "Permission description"
		// @nexa description.zh-CN: "权限说明"
		// @nexa description.en-US: "Permission description"
		// @nexa ui.control: "textarea"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "none"
		field.String("description").Optional(),
	}
}
func (Permission) Indexes() []ent.Index { return []ent.Index{index.Fields("code").Unique()} }
func (Permission) Edges() []ent.Edge {
	return []ent.Edge{edge.From("roles", Role.Type).Ref("permissions")}
}

// @nexa label.zh-CN: "认证会话"
// @nexa label.en-US: "Authentication session"
// @nexa description.zh-CN: "认证会话数据"
// @nexa description.en-US: "Authentication session data"
// @nexa scope: "tenant"
type AuthSession struct{ ent.Schema }

func (AuthSession) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "会话标识"
		// @nexa label.en-US: "Session ID"
		// @nexa description.zh-CN: "会话标识"
		// @nexa description.en-US: "Session ID"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("session_id"),
		// @nexa label.zh-CN: "租户"
		// @nexa label.en-US: "Tenant"
		// @nexa description.zh-CN: "租户"
		// @nexa description.en-US: "Tenant"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"Tenant","display":"code"}
		// @nexa visibility: "internal"
		field.Int("tenant_id"),
		// @nexa label.zh-CN: "身份账号"
		// @nexa label.en-US: "Identity account"
		// @nexa description.zh-CN: "身份账号"
		// @nexa description.en-US: "Identity account"
		// @nexa ui.control: "reference"
		// @nexa ui.reference: {"target":"IdentityAccount","display":"username"}
		// @nexa visibility: "internal"
		field.Int("identity_account_id"),
		// @nexa label.zh-CN: "访问令牌摘要"
		// @nexa label.en-US: "Access token hash"
		// @nexa description.zh-CN: "访问令牌摘要"
		// @nexa description.en-US: "Access token hash"
		// @nexa ui.control: "sensitive"
		// @nexa visibility: "sensitive"
		field.String("access_token_hash").Sensitive(),
		// @nexa label.zh-CN: "刷新令牌摘要"
		// @nexa label.en-US: "Refresh token hash"
		// @nexa description.zh-CN: "刷新令牌摘要"
		// @nexa description.en-US: "Refresh token hash"
		// @nexa ui.control: "sensitive"
		// @nexa visibility: "sensitive"
		field.String("refresh_token_hash").Sensitive(),
		// @nexa label.zh-CN: "访问过期时间"
		// @nexa label.en-US: "Access expiry"
		// @nexa description.zh-CN: "访问过期时间"
		// @nexa description.en-US: "Access expiry"
		// @nexa ui.control: "datetime"
		// @nexa visibility: "public"
		field.Time("access_expires_at"),
		// @nexa label.zh-CN: "刷新过期时间"
		// @nexa label.en-US: "Refresh expiry"
		// @nexa description.zh-CN: "刷新过期时间"
		// @nexa description.en-US: "Refresh expiry"
		// @nexa ui.control: "datetime"
		// @nexa visibility: "public"
		field.Time("refresh_expires_at"),
		// @nexa label.zh-CN: "已撤销"
		// @nexa label.en-US: "Revoked"
		// @nexa description.zh-CN: "已撤销"
		// @nexa description.en-US: "Revoked"
		// @nexa ui.control: "switch"
		// @nexa visibility: "public"
		field.Bool("revoked").Default(false),
	}
}
func (AuthSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("session_id").Unique(),
		index.Fields("access_token_hash").Unique(),
		index.Fields("refresh_token_hash").Unique(),
	}
}
func (AuthSession) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tenant", Tenant.Type).Field("tenant_id").Unique().Required(),
		edge.To("identity_account", IdentityAccount.Type).Field("identity_account_id").Unique().Required(),
	}
}
