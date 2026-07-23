# 业务事实

Nexa 为业务事实提供 typed value、strict parser、versioned schema 和 canonical projection。事实实例由
consumer repository author，框架只读取并投影；IR、manifest 和 generated source 不能反向成为事实源。

## Ownership

| 事实 | Authoring owner | Nexa public boundary |
| --- | --- | --- |
| Ent schema、field、CRUD intent、tenant scope | Consumer Ent schema | `nexaent` annotation 与 `nexaent/mixin` |
| RPC、message、method、transport metadata | Consumer Proto | `generation/protocol` compile 与 schema |
| Native HTTP route、type、field | Consumer `.api` | `generation/httpapi` loader 与 schema |
| Service topology 与 capability binding | Consumer Service Catalog | `project/servicecatalog` strict document |
| Cross-owner relation | Consumer 对应领域 relation document | Closed typed relation contract |
| Source manifest/profile/tree | Provider publisher | `sourceplugin` immutable contract |
| Generated manifest/source lock | 无人工 authoring surface | Owner package 从当前输入重建 |
| Runtime/deployment facts | Consumer runtime/ops | 不进入上述 framework facts |

## 最近事实源

同一语义只允许一个人工入口，优先使用最接近业务节点的正式表达：

1. 同一 Ent/Proto/`.api` 节点上的 typed metadata；
2. 同一领域 package/file 中的结构化声明；
3. 只保存跨 owner 关系的 closed typed relation；
4. 只表达服务拓扑与 capability binding 的 Service Catalog。

Relation 只引用原事实的 canonical `SourceRef` 和 `Digest`，不复制字段。Comment、目录名、generated source、
manifest 和 catalog generic extension 都不能覆盖更近的 typed fact。

## Ent typed facts

Ent schema 使用三个相互独立的 annotation：

- `nexaent.Schema(SchemaMeta)`：localized label/description、identity strategy 和 record scope；
- `nexaent.Field(FieldMeta)`：localized label/description、UI hint、reference、visibility 和可选 CRUD field policy；
- `nexaent.CRUD(operations...)`：schema 对闭合 CRUD operation set 的显式选择。

CRUD operation 只包含 `list`、`get`、`create`、`update`、`delete`。Annotation 缺失表示不选择；空、重复、
未知 operation 或重复同类 annotation 都是 typed error。Loader 只接受当前 public `nexaent` contract，
不读取 legacy annotation、comments、catalog binding 或字段名称作为 fallback。

Schema scope 是 `global` 或 `tenant`。Framework-owned `nexaent/mixin.Tenant` 提供 fixed `tenant_id` field：
required、positive、immutable、internal、read-only。使用 mixin 不会自行把 schema 改为 tenant scope；业务方
必须同时在 `SchemaMeta` 明确 `ScopeTenant`。反过来，普通同名字段也不能伪装成该 mixin。

Field visibility 与 CRUD policy 保持闭合：internal field 不允许 mutation policy，sensitive field 必须排除
read projection。物理 Ent edge display 与 logical business reference 是两种不同的 typed relation，不互相
fallback。

## Proto 与 HTTP facts

Proto 是 RPC/message/method 的 owner，并可通过 typed custom option 表达 HTTP proxy、authentication、
credential 和 RPC context binding。`.api` 是 Core native HTTP contract 的 owner。Composition 可以合并两类
projection，但不能把生成 route 写回 Proto 或 `.api`。

跨服务 context 在协议层保持明确的 wire type；需要 tenant context 时，CRUD projection 使用内部
`tenant_id` binding，不凭字段名猜测或向公开 item/mutation 暴露该字段。

## Service Catalog

Service Catalog 只拥有 service id、repository-relative root、dependency 和 service-to-capability binding。
Binding 由 closed id 与 version 组成，不携带 Ent、Proto、HTTP、deployment 或任意 config payload。

没有选择需要 catalog 的能力时，可以使用 `servicecatalog.Empty()`；显式 `services: []` 是有效空事实；
已请求文件但 source 缺失或内容空白则是错误。这三种状态不能互相替代。

## Canonical source

Owner parser 为可追踪节点产生 repository-relative `SourceRef` 和 semantic SHA-256 digest。Canonical bytes
忽略 JSON/YAML 格式、注释和字段顺序，但保留协议定义的语义。Consumer 可以通过 public accessor 读取防御
副本，不依赖 parser 内部状态。

## Machine schema

Schema 与 owner package 同置，常用 accessor 包括：

| Contract | Accessor |
| --- | --- |
| Ent schema/field/CRUD annotation | `nexaent.SchemaAnnotationSchema()`、`FieldAnnotationSchema()`、`CRUDAnnotationSchema()` |
| Tenant mixin marker | `mixin.TenantAnnotationSchema()` |
| Service Catalog | `servicecatalog.Schema()` |
| Entity/Protocol/HTTP/Composition IR | 对应 generation package 的 `Schema()` |
| Artifact/API/Service Manifest | 对应 manifest owner 的 schema accessor |
| Source Bundle/Source Lock | `sourceplugin.Schema()`、`lock.Schema()` |
| Quality Read Model | `readmodel.Schema()` |

精确字段以 accessor 返回的当前 schema 和 public Go type 为准。投影及写入行为见
[受控生成](controlled-generation.md)，manifest 语义见[生成清单](generated-manifests.md)。
