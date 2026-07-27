# Core IAM 契约

Core service source bundle 提供中性的 IAM 存储与 RPC 契约。Consumer 拥有身份
提供方配置、目录内容、产品权限和部署状态。Nexa 不预设 OIDC、Authentik、飞书或
其他具体身份产品。

## 身份与租户

- 身份账号是全局对象。非空外部身份按
  `(identity_source_code, external_subject)` 唯一；空外部身份不进入该部分唯一索引。
- 租户的 `code` 全局唯一。
- 租户成员绑定一个租户和一个全局身份账号，并按
  `(tenant_id, identity_account_id)` 唯一。
- 角色只属于一个租户，并按 `(tenant_id, code)` 唯一。

登录和 `ProvisionTenant` 使用稳定的 tenant code。进入 IAM 管理面后，认证上下文和
全部 tenant-scoped mutation 使用数据库中的 numeric tenant ID；tenant code 与 numeric
tenant ID 不得混用或由 adapter 静默猜测。

租户、成员和角色从 `version = 1` 开始。Application mutation 比较并递增该值，版本
不匹配时返回 `concurrent_write`。身份账号独立使用从 1 开始的
`credential_version`；密码重置必须递增该值，使旧凭据和 session 失效。

托管角色必须携带非空 `source_owner`、`source_key` 和 `source_digest`，人工角色不得
携带这些值。普通 IAM 命令修改托管角色时返回 `permission_denied`；保留最后一个启用
租户 owner 等状态不变量失败时返回 `failed_precondition`。只有拥有该来源的 application
目录同步路径可以替换托管定义。

人工成员角色授权与来源托管授权是两类独立记录。公开成员角色替换只更新人工授权，
永不删除托管授权。人工授权由显式 `TenantMemberRoleGrant` 建模，写入时必须同时携带
`tenant_id`、`tenant_member_id` 和 `role_id`；数据库复合外键保证成员和角色属于同一
租户。

## 菜单与权限

菜单、权限资源和权限动作是全局目录投影。每条记录携带稳定 source owner/key、
canonical source digest 和 enabled/disabled 状态。`source_digest` 必须是 canonical
catalog payload 的 SHA-256，不得使用原始文件字节、时间戳或写入顺序作为摘要。

权限动作通过稳定 `permission_resource_id` 外键关联资源。Owner/digest 一致性由
application 在同一个目录同步事务中校验；digest 不进入外键，因此来源升级可以按任一
语句顺序替换父子投影，不会产生中间态外键违约。角色菜单和角色权限授权携带角色的
numeric tenant ID，并通过复合角色外键拒绝跨租户关系。

目录同步仅属于 application，不暴露 Core RPC 或 HTTP route。Consumer 向 Core
application 提供目录；application 校验 source ownership 和 digest 后替换该来源拥有的
记录。`catalog_source_states` 独立保存每个 `source_id` 的 digest。空目录也必须写入该
状态，使相同输入的重复同步成为可证明的 no-op，而不是被误判为从未同步。

PostgreSQL consumer gate 的环境变量、容器和 CI 启动方式由 application owner 定义，
不属于 Core IAM 公共契约。

## 公开操作

首批管理面包含账号状态和密码重置、成员列表/状态/人工角色替换、租户创建/状态，以及
角色创建/更新/状态/权限/菜单操作。既有注册、登录、刷新、撤销、健康检查和鉴权消息的
wire number 与 route 保持不变。

账号禁用必须撤销活动 session，公开操作不提供保留 session 的开关。密码重置必须在同一
store operation 中递增 `credential_version` 并撤销活动 session，这些失效规则不可由
调用方关闭。

登录和刷新对租户、账号和租户成员执行相同的 enabled-state 检查。任一层被禁用时统一
投影为 `invalid_credentials` 和 HTTP 401，不向调用方泄露具体禁用层级。

本契约不暴露删除生命周期、租户 settings、身份提供方配置或目录同步。菜单与权限目录
记录是只读 generated CRUD projection，写入只发生在 application 同步路径。

新增 IAM 操作只使用稳定错误码 `invalid_input`、`not_found`、`conflict`、
`concurrent_write`、`permission_denied` 和 `failed_precondition`；`invalid_argument`
等旧别名不属于该管理契约。

## 所有权

- 人工事实源：service source bundle 中的 Core Proto message/options 和 Ent schema。
- 数据库投影：由 materialized consumer 持有的有序 SQL migration。
- 派生投影：generated RPC/API transport、CRUD artifact 和 FrontendIR。
- Consumer 事实：目录条目、角色模板、身份提供方配置、凭据、租户实例和产品文案。
