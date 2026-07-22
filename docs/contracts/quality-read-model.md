# Quality Read Model 契约

`quality/readmodel` 是 requirement coverage 只读投影的公共 wire/schema owner。Snapshot instance 由
consumer 拥有；requirements、tests、evidence、freeze decision 和 gate decision 的领域 owner 仍保留各自
事实，不能把 read model 反向作为人工 authoring surface。

## Owner 与投影

| Object | Owner | Authoring surface | Derived projection |
| --- | --- | --- | --- |
| Quality read-model wire/schema | Nexa `quality/readmodel` | public Go types、constructor、strict JSON Schema | canonical JSON 与 digest |
| Snapshot instance | consumer quality composition | producer 解析各领域 owner facts 后传入的 typed `SnapshotSpec` | read-only API/frontend view |
| Requirement/test/evidence/freeze facts | consumer 对应领域 | canonical `SourceRef` 指向的领域事实文件 | `RequirementCoverage` relation row |

Read model 只保存关系投影与显示所需的稳定状态，不复制原事实内容。每条 relation 必须使用可解析的
canonical `SourceRef`；重复或 unresolved reference 会返回稳定 typed error。

## Public contract

Document identity 固定为：

- `apiVersion: nexa.dev/quality-read-model/v1`
- `kind: QualityReadModel`

Snapshot 顶层字段为 `sourceProfile`、`readModelScope`、`revision` 和 `requirements`。每条
`RequirementCoverage` 包含 requirement ref、title、status、test refs、evidence refs、freeze refs、
freeze status 与 gap codes。

`FreezeStatus` 是闭合集合：`none`、`frozen`、`changed`。`none` 不允许 freeze ref；`frozen` 与 `changed`
必须携带至少一个 freeze ref。Requirements、refs 与 gap codes 由 constructor canonical sort，accessor
返回防御副本。

公开 API：

- `NewSnapshot(SnapshotSpec)` 构造并校验 typed snapshot；
- `Empty()` 返回合法、显式、canonical 的空 projection；
- `Parse(source, bytes)` 执行 strict parse、schema 与 relation 校验；
- `CanonicalJSON(snapshot)` 返回 RFC 8785 canonical JSON；
- `Snapshot.Digest()` 返回 canonical bytes 的 SHA-256 digest；
- `Schema()` 返回 JSON Schema 的防御副本。

未知字段、trailing input、错误 document identity、非法 freeze relation、重复或 unresolved ref 均投影为
`*readmodel.Error`，稳定 code 为 `quality_read_model_invalid`，并携带 reason、source 和 JSON pointer。

## Empty 与可选消费

`Empty()` 表示 producer 明确提供空 projection，不表示缺失 source 或无效 document。Quality runtime 可以
在没有 producer 时服务 canonical empty snapshot；配置了 producer 而读取失败时必须返回 source error，
不能伪装成 empty success。

Quality runtime、quality producer、frontend、requirements、人工 gate、TestSpec、evidence 与 deployment
可以分别缺席。`quality/readmodel` package 不启动服务、不读取仓库、不批准 gate、不生成 evidence，也不
要求上述模块存在。Framework Minimum 与 Core backend 不依赖该 package 的实例。

## Consumer rules

1. Producer 从各领域最近事实源解析 relation，并构造 `SnapshotSpec`。
2. API 或 frontend 只消费 canonical snapshot，不扩展、复制或独立版本化 wire schema。
3. `sourceProfile` 与 `readModelScope` 必须由 consumer composition 显式提供，避免混合不同 snapshot scope。
4. Read model、generated frontend export 和文档都是可重建 projection，不得反向修改 requirement、test、
   evidence 或 freeze owner facts。
5. 结构与行为以 `Schema()`、strict parser、constructor 和 package tests 为准，不以源码文本断言为准。
