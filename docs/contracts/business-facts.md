# 业务事实契约

Nexa 提供版本化 schema、不可变 value object、strict parser、validator 和 canonical protocol。使用
Nexa 的 consumer repository 负责业务事实实例、关系决策和生成投影；公共框架不会接管这些人工入口。

## 所有权

| 对象 | Authoring owner | Nexa 提供的公共边界 | Derived projection |
| --- | --- | --- | --- |
| 服务发现、拓扑、跨事实或跨服务 capability binding | consumer repository | `project/servicecatalog` schema、loader、parser、validator | composition IR、静态注册与服务文档 |
| Ent schema、type、field 和 CRUD intent | consumer Ent schema | typed annotation contract 与生成能力边界 | entity/CRUD IR、协议与代码 |
| RPC、message、method | consumer Proto | descriptor/custom option parser contract | RPC IR、client、proxy 和 SDK model |
| HTTP route、type、field | consumer API contract | API parser/validator contract | API Manifest、路由、SDK 和文档 |
| Artifact/API Manifest | 无人工 authoring surface；consumer 持有生成实例 | `generation/artifact`、`generation/api` immutable contract；selected generation capability 管理生成生命周期 | 可重建的生成清单 |
| Runtime BuildInfo | consumer composition identity 与 Go build settings | `runtime/buildinfo` resolver 和 output schema | 只读 runtime identity projection |
| Source Bundle manifest、profile 与 tree | provider publisher | `sourceplugin` typed contract、strict parser 与 immutable Provider | exact release、cache entry 与 source plan |
| Source provenance lock | 无人工 authoring surface；consumer 持有生成实例 | `sourceplugin/lock` key、schema、derive 与 verify | status、diff 与三方升级基线 |
| Quality read-model wire | Nexa `quality/readmodel`；snapshot instance 由 consumer 持有 | public typed constructor、strict schema 与 canonical parser | 只读 requirement coverage snapshot/API/frontend view |

生成投影不能反向覆盖 authoring surface。更完整的 ownership 和依赖方向见
[框架架构](../architecture/framework.md)，manifest lifecycle 见[生成清单契约](generated-manifests.md)。

## Service Catalog v1

Service Catalog v1 只拥有：

- service id 与 repository-relative source root；
- service dependency；
- service 到 capability contract 的 closed binding，其中 binding 只有 `id` 与 `apiVersion`。

Capability identity 使用 `<namespace>/<name>`，版本使用同一 identity 前缀的
`<namespace>/<name>/v<positive-integer>`；例如 API proxy capability 是
`nexa.dev/generation-api-proxy` 与 `nexa.dev/generation-api-proxy/v1`。ID 与版本前缀不一致是非法 binding。

Catalog parser 为每个 service topology node 和 capability binding node 派生 canonical
`SourceRef + semantic Digest`，不向 authored catalog 增加来源字段。Service digest 只拥有
`id/root/dependsOn`，binding digest 只拥有 service id 与 binding id/version；JSON/YAML 表达、字段顺序、
注释和 source location 不进入 digest。`Service.Source()`、`CapabilityBinding.Source()`、
`Catalog.Sources()` 与 `Catalog.Source(ref)` 暴露防御性、精确的 owner projection；
`Service.CanonicalSourceJSON()` 与 `CapabilityBinding.CanonicalSourceJSON()` 返回 digest 对应 bytes 的防御副本。
节点 fragment 分别是 `service:<id>` 与
`service:<id>/binding:<capability-id>@<capability-api-version>`，并统一通过 `provenance.RepositoryRef`
生成 canonical reference。

节点 semantic bytes 使用 RFC 8785 JSON Canonicalization Scheme，UTF-8 编码且不带换行。Object member
按 JCS 的 UTF-16 code unit 顺序排序，字符串使用 JCS escaping，不执行 Unicode normalization；
`dependsOn` 在编码前按 service id 排序，空集合固定为 `[]`。两个 versioned envelope 精确为：

```json
{"apiVersion":"nexa.dev/service-node/v1","dependsOn":["foundation"],"id":"sample","root":"backend/sample"}
{"apiVersion":"nexa.dev/capability-binding-node/v1","capabilityApiVersion":"nexa.dev/generation-api-proxy/v1","id":"nexa.dev/generation-api-proxy","serviceId":"sample"}
```

实现对上述无换行 bytes 计算 SHA-256。Consumer 可以仅使用公开 service/binding values 和本段 envelope
独立复算 canonical bytes 与 digest，不依赖 Catalog parser 的内部状态。

它不拥有 Ent、Proto、HTTP API、数据库、部署、前端、CLI 或 capability 本体，也不提供 generic
`configRef`、开放 extension 或 sidecar path。Binding presence 只表达关系，不能携带或复制节点事实。

### Empty、显式空与缺失 source

- composition 没有选择任何需要 catalog 的 capability 时，使用 `servicecatalog.Empty()`，不读取文件；
- 已选择的 loader 显式请求 catalog 而 source 不存在时，返回 `fact_source_missing`；
- 空白文件非法；包含 `services: []` 的 strict document 是有效、显式的空事实。

这三种状态不能互相替代。Minimum Runtime 不需要空目录、占位 catalog 或可选 capability。

## 最近事实源

同一事实只能有一个人工入口，优先级固定为：

1. 同一语法节点上的 typed metadata；
2. 同一事实文件或 package 中的结构化声明；
3. 领域 owner 定义的 closed typed relation document；
4. 只表达跨事实关系、服务发现与拓扑的全局 catalog。

Typed relation 只用于事实真实横跨多个权威源，或原格式无法表达该关系的情况。Relation document 只
保存关系或决策，并以 canonical `SourceRef` 与 `Digest` 指向每个原事实；它不能复制 Ent、Proto、API
或其他 source 字段。需要 catalog 暴露的新关系必须通过新的 catalog `apiVersion` 表达；领域专属关系
由 capability-specific typed contract 及其 loader 消费，二者都不扩展 Service Catalog v1。

## Ent 责任边界

Ent toolchain delegation 与 typed Ent annotation 驱动的 CRUD projection 是两项独立责任：

- delegation 只定位并调用 consumer 声明的 Ent toolchain，Ent schema 与 generated Go 仍归 consumer；
- CRUD projection 只读取 typed `schema.Annotation`；annotation presence 加非空 closed operations 是唯一
  opt-in，annotation 缺失即不生成 CRUD；
- Go comments、catalog binding 或 generic reference 都不参与 CRUD 选择。

公开文档只描述这些职责。某个二进制的实际命令、capability、schema 与 side effect 以
`nexactl inspect --json` 为准。

## 生成投影方向

业务事实只沿 owner 到 projection 的方向流动：

| Owner facts | Versioned projection | Generated output |
| --- | --- | --- |
| Ent typed `SchemaMeta` / `FieldMeta` / CRUD annotation | `EntityIR`、`CRUDProtocolIR` | 可选 CRUD Proto fragment |
| 服务 Proto AST/custom options | `ProtocolIR` | RPC Go、proxy inputs 与 SDK model |
| Core `.api` AST/typed metadata | native APIIR | Core native API contract projection |
| Service Catalog binding/topology + ProtocolIR + native APIIR | `CompositionIR`、merged APIIR | API Manifest 与静态业务 proxy 普通源码 |
| 上述 service contract owner nodes | canonical contract source set | Service Manifest |

Project provider 只定位这些 authoring surface 并绑定受控 toolchain，不拥有节点事实。Artifact/API/Service
Manifest 只记录 provenance、digest 与 projection 状态，也不能反向成为配置入口。完整链路、CLI 命令族
与 serial staged publish ownership 见[受控生成契约](controlled-generation.md)。

Source Bundle manifest/tree 是 provider 发布事实，materialized source 是 consumer 普通源码，provenance
lock 是可重建 projection；三者不能互相替代。Source Bundle 不拥有 consumer runtime config、deployment
instance 或 health state。完整 identity、resolver/cache、七命令与 serial staged publish 见
[Source Bundle 契约](source-bundles.md)。

标准 Core、Job 与 Quality source package 的 authored-only inventory、profile closure、reference composition 与
detach independence 见[标准服务 Source Bundles](../plugins/standard-service-source-bundles.md)。V0.1 reference
composition 只包含 Core，端到端参考路径选择 backend，并要求当前自省结果直接暴露 exact Provider/profile；
Provider package presence 或 `source.bundle` capability 都不创建运行时 service instance，也不能替代该发现门禁。

## 可选业务域

Role、Menu 和 ProductProfile 不是基础公共事实袋。它们由选择相应能力的 consumer contract 拥有；没有
这些事实时，Core 与业务后端仍可构建和运行。Frontend、requirements、gate、evidence、deployment 和
observability instance 同样不能反向成为 Minimum Runtime 的构建或启动前提。

Quality read model 是关系投影，不是 requirement、test、evidence 或 freeze 的人工事实源。其 owner、strict
schema、canonical digest、empty semantics 与可选消费边界见
[Quality Read Model 契约](quality-read-model.md)。Requirements、work、UserOperation、人工 gate、TestSpec、
evidence、quality producer、frontend 与 deployment 可以全部缺席，Core backend 仍可构建和运行。

## Machine schema access

Document schema 与 owner package 同置并由 accessor 返回防御副本：

| Contract | Public accessor |
| --- | --- |
| Service Catalog | `servicecatalog.Schema()` |
| Artifact Manifest | `artifact.Schema()` |
| API Manifest | `api.DocumentSchema()` |
| Ent EntityIR | `entity.Schema()` |
| CRUDProtocolIR / compatibility lock | `crudproto.IRSchema()` / `crudproto.LockSchema()` |
| ProtocolIR | `protocol.Schema()` |
| APIIR | `httpapi.Schema()` |
| CompositionIR | `composition.Schema()` |
| Generation plan / result | `transaction.PlanSchema()` / `transaction.ResultSchema()` |
| Service Manifest | `service.Schema()` |
| BuildInfo | `buildinfo.Schema()` |
| Source Bundle | `sourceplugin.Schema()` |
| Source provenance lock | `lock.Schema()` |
| Quality read model | `readmodel.Schema()` |

`provenance` 提供 Digest、SourceRef、Source 与 TreeDigest value object，不定义 document schema。Consumer
直接使用 owner accessor；文档和全局 registry 不复制 schema。
