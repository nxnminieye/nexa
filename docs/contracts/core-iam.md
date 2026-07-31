# Core IAM 契约

`core-application` 是同一版本化 Source Bundle 中的 Core RPC 与 Core API
Starter。物化后固定形成两个运行进程：`Core API -> Core RPC -> PostgreSQL`；
API 不得在 RPC 不可用时嵌入或 fallback 到本地 IAM 实现。默认物化目录为
`backend/core/rpc/**` 与 `backend/core/api/**`。

本阶段只冻结中性 Core IAM 闭包。具体身份提供方、租户域名解析、浏览器授权、
工具会话、PAT、service account、tenant settings、Casbin/Redis watcher 以及
产品 RPC 不属于该 Starter。

Core service source bundle 提供中性的 IAM 存储与 RPC 契约。Consumer 拥有身份
提供方配置、目录内容、产品权限和部署状态。Nexa 不预设 OIDC、Authentik、飞书或
其他具体身份产品。

## 抽取基线与本段验收

本段行为 donor 固定为 PDCL `origin/main` exact commit
`e6b07aadfac96da29b6245a4c3b1cb29d5077e51`。只提炼本地登录、session、tenant、
用户、角色、菜单、权限、bootstrap、目录同步和稳定错误投影；Casdoor、OIDC、
Authentik、tenant domain、浏览器授权、PAT、service account、Redis/Casbin 多实例
传播及产品 RPC 仍由 consumer 持有，不以兼容层进入 Core。

本段 Nexa 写集只包含 `plugins/service/core/**`、`cmd/core-transport-gen/**`、直接相关
的 Core integration fixture/test 和本契约。验收命令为：

```sh
GOWORK=off go test ./plugins/service/core/... -count=1
GOWORK=off go test ./cmd/core-transport-gen ./generation/sourcecomment ./generation/protocol ./generation/httpapi ./sourceplugin/engine -count=1
GOWORK=off go test ./integration -run '^(TestCoreIAMTransportGeneration|TestSourceBundleCore|TestOfficialSourceReference)' -count=1
NEXA_CORE_IAM_TEST_DSN='<postgres-dsn>' GOWORK=off go test ./integration -run '^TestCoreIAMPostgresConsumer$' -count=1
```

前三项可以在无数据库环境完成；最后一项必须使用 fresh、可写且健康的 PostgreSQL，
不得用 mock、跳过结果或仅端口连通代替。

## 身份与租户

- 身份账号是全局对象。非空外部身份按
  `(identity_source_code, external_subject)` 唯一；空外部身份不进入该部分唯一索引。
- 租户的 `code` 全局唯一。
- 租户成员绑定一个租户和一个全局身份账号，并按
  `(tenant_id, identity_account_id)` 唯一。
- 角色只属于一个租户，并按 `(tenant_id, code)` 唯一。

登录和 `ProvisionTenant` 使用稳定的 tenant code。首个 consumer 由自己的配置提供
登录 tenant code，不把 tenant code 加入公开登录 DTO；后续 consumer 可以在自己的
middleware/provider 中解析域名。进入 IAM 管理面后，认证上下文和
全部 tenant-scoped mutation 使用数据库中的 numeric tenant ID；tenant code 与 numeric
tenant ID 不得混用或由 adapter 静默猜测。

公开登录 DTO 只包含用户名和密码。登录所需 tenant code 由 consumer 拥有的全局请求上下文或
部署配置确定；撤销 DTO 为空，待撤销 session ID 只来自已经认证的 `AccessPrincipal`。两者都不
作为逐 operation 字段重新进入 DTO，也不允许客户端覆盖。

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

目录同步仅属于 application/bootstrap，不暴露 Core RPC 或 HTTP route。Consumer 向 Core
application 提供目录；application 校验 source ownership 和 digest 后替换该来源拥有的
记录。`catalog_source_states` 独立保存每个 `source_id` 的 digest。空目录也必须写入该
状态，使相同输入的重复同步成为可证明的 no-op，而不是被误判为从未同步。

PostgreSQL consumer gate 的环境变量、容器和 CI 启动方式由 application owner 定义，
不属于 Core IAM 公共契约。

## 公开操作

首批管理面包含：

- 身份账号 list/get、状态更新和密码重置；
- 租户成员 list/get、状态更新和人工角色替换；
- 租户 list/get、创建、显示名称更新和状态更新；
- 角色 list/get、创建、更新、状态、权限和菜单操作；
- 菜单与权限目录 list/get。

List 使用统一 `keyword/status/limit/offset`，默认 limit 为 50、最大 200，并返回 `total/items`。成员和角色
查询严格使用认证上下文中的 numeric tenant ID；角色 readback 返回 permission/menu codes，成员 readback
区分人工角色授权并保留账户 username、email、display name、identity source 与 external subject。既有注册、
登录、刷新、撤销、健康检查和鉴权消息的 wire number 与 route 保持不变。

账号禁用必须撤销活动 session，公开操作不提供保留 session 的开关。密码重置必须在同一
store operation 中递增 `credential_version` 并撤销活动 session，这些失效规则不可由
调用方关闭。

登录和刷新对租户、账号和租户成员执行相同的 enabled-state 检查。任一层被禁用时统一
投影为 `invalid_credentials` 和 HTTP 401，不向调用方泄露具体禁用层级。

## Access Principal

受保护请求通过独立的 `AccessAuthenticator` 将不透明 access token 解析为当前 `AccessPrincipal`。框架只把
原始 token 的 SHA-256 摘要和同一次 `Clock.Now()` 传给 `AccessSessionStore`；不裁剪、记录或返回原始 token
及摘要。空 token、未知、过期、撤销、租户/账号/成员禁用或三元组错配统一返回 `invalid_credentials`，取消
返回 `canceled`，真实存储故障返回不携带底层细节的 `store_failure`。

PostgreSQL store 以单条 statement 从同一 snapshot 读取 session identity、人工与托管角色并集、角色绑定的
enabled permission action/resource，以及角色绑定的 enabled menu 和 enabled 祖先。角色、权限和菜单 code 均
按字典序去重；菜单递归显式防环，且 `MenuCodes` 只用于导航投影，不作为 API 授权依据。HTTP adapter 必须以
principal 的 numeric tenant ID 作为服务调用上下文；请求显式携带的 header、path 或 body tenant 只能用于
一致性校验，不匹配返回 403。路由权限缺失同样返回 403，认证失败保持 401。

本契约不暴露删除生命周期、租户 settings、身份资料更新、身份提供方配置或目录同步。菜单与权限目录
记录是只读 projection，写入只发生在 application 同步路径。

新增 IAM 操作只使用稳定错误码 `invalid_input`、`not_found`、`conflict`、
`concurrent_write`、`permission_denied` 和 `failed_precondition`；`invalid_argument`
等旧别名不属于该管理契约。

## 所有权

- 人工事实源：service source bundle 中的 Core Proto message 与 `@nexa` source-comment facts，以及 Ent schema。
- 运行拓扑事实：`core-application` Starter 的 API/RPC source 与 consumer 配置；不由
  Source Provider 读取或拥有 consumer 的 DSN、端口、凭据和部署值。
- 数据库投影：由 materialized consumer 持有的有序 SQL migration。
- 派生投影：generated RPC/API transport、CRUD artifact 和 FrontendIR。
- Consumer 事实：目录条目、角色模板、身份提供方配置、凭据、租户实例和产品文案。
