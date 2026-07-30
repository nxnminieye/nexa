# 业务事实

Nexa 使用 `nexa.dev/source-comment/v1`、语言原生 AST/descriptor 和 typed FactGraph 读取业务事实。
事实实例由 consumer repository author，框架只读取并投影；IR、manifest、generated source 和 runtime
descriptor 不能反向成为事实源。

## Ownership

| 事实 | Authoring owner | Nexa public boundary |
| --- | --- | --- |
| Ent schema/field 原生结构与补充事实 | Consumer Ent source | Go AST/entc source adapter + Source Comment registry |
| RPC/message/method 原生结构与补充事实 | Consumer Proto | Proto AST/descriptor adapter + Source Comment registry |
| Native HTTP route/type/field | Consumer `.api` | `.api` AST adapter + Source Comment registry |
| UI-only page facts | Consumer YAML frontend source | `nexa.dev/frontend-source/v1` adapter |
| Service topology 与 capability binding | Consumer Service Catalog | `project/servicecatalog` strict document |
| Cross-owner relation | Consumer 对应领域 relation document | Closed typed relation contract |
| Source manifest/profile/tree | Provider publisher | `sourceplugin` immutable contract |
| Projection/source lock | 无人工 authoring surface | Owner compiler 从当前输入重建 |
| Runtime/deployment facts | Consumer runtime/ops | 不进入 framework facts |

## 第一事实源

一个 FactID 只能有一个第一事实源。存在 Ent 时，Ent 可表达的事实必须在 Ent；没有 Ent 时 Proto 可以
成为起点；原生 HTTP 可以从 `.api` 开始；UI-only 事实从 frontend source 开始。下游只能增加本阶段新
出现的 node/fact，不得覆盖、删除或重复声明上游事实。

Ent、Proto、`.api` 和 TypeScript 已由原生语法表达的结构不写进注释。补充事实统一使用严格
`@nexa`，先进入闭合集合 registry 和 typed FactGraph。未知 key、错误类型、非法枚举、错误 target、
misplaced fact、伪造 `$source` 和 semantic collision 都在写入前失败。

完整 carrier、registry、source reference、projection lock 和诊断见
[统一事实注释协议 v1](source-comment.md)。

## Ent facts

Ent 字段类型、optional/nillable/default/unique/validator/index/edge 继续由 Ent 原生 schema 拥有。
label/description、CRUD selection、scope/visibility 和 UI presentation 使用紧邻 AST node 的 `@nexa`
directive。`nexaent`、`SchemaMeta`、`FieldMeta`、CRUD annotation 和 legacy reader 不再属于公共或内部
authoring surface。

tenant field 的结构仍由 consumer 的 Ent schema/mixin 明确表达；`scope: "tenant"` 是安全相关补充
事实。compiler 必须验证二者一致，不能按字段名猜测 tenant boundary，也不能把内部 tenant context 暴露为
公开 mutation field。

field visibility 与 CRUD facts 使用 registry 的封闭值。`ui.reference` 在 compiler 中解析为 typed
`NodeRef + FieldRef`；原始对象或字符串路径不得进入 renderer/runtime。

## Proto 与 HTTP facts

Proto 原生拥有 message、field type/number、service、RPC 和标准 Proto option。RPC proxy 的
`http.method`、`http.path`、`auth` 与 `permission` 使用 method 上的 `@nexa` 补充事实；request/response
type 由 RPC 签名拥有，operation id 由 fully-qualified RPC identity 确定性生成。Nexa-specific custom
option、request/response/context/error projection 不再存在。

`.api` 原生拥有 type、field、operation、method 与 route。Proto 投影生成的 `.api` 结构只携带
`$source`，不重复 `http.*` comment。原生 HTTP operation 可以在 `.api` 上声明 `auth`/`permission`。
HTTP wire 行为由 [HTTP Convention v1](http-convention.md) 固定，不允许逐字段 alias 或 mapping。

## Frontend facts

frontend source 只建立 page semantic node，并声明 `ui.entity`、route/menu、page size 和真实 extension
component 等 UI-only facts。标准 CRUD operation/type/validation/pagination/client method 均从前序
FactGraph 与 Convention 推导，不再通过 PageSpec、operation binding 或 JSON metadata 重复 author。

FrontendIR 是页面引用的 typed closure，只是可重建 projection。Vben renderer 不解析 comment、YAML、
动态 fact map 或 runtime page descriptor。

## Service Catalog

Service Catalog 只拥有 service id、repository-relative root、dependency 和 service-to-capability binding。
Binding 由 closed id 与 version 组成，不携带 Ent、Proto、HTTP、frontend、deployment 或任意 config
payload。Relation 只引用原事实的 canonical source identity，不复制字段。

## Canonical source

每个 source adapter 为语义节点产生 repository-relative SourceRef 和 semantic digest。格式化、普通注释
和无关顺序不改变 native/fact canonical value。`nexa.dev/source-projection-lock/v1` 只保存上一轮 inherited
node/fact 的 first source 与 canonical digest，用于区分删除和上游新增；它不是 authoring source、
ownership manifest、兼容映射或 runtime 输入。

## Machine boundary

Source Comment contract、registry、FactGraph 和 diagnostics 位于 `generation/sourcecomment`。Entity、Protocol、
HTTP 和 FrontendIR owner 只暴露经过 adapter/FactGraph 校验的 typed projection。精确可执行能力以当前
`nexactl inspect --json`、public Go API、schema 和行为测试为准。
