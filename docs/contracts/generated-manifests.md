# 生成清单契约

Artifact Manifest 与 API Manifest 是由权威 source 重建的 projection。它们记录生成输入、来源、输出和
ownership，但不是人工事实源，也不能作为输入反向修改 `.api`、Proto、Ent schema、Service Catalog
或其他 authoring surface。

## Contract 与 capability

Owner package 提供稳定的机器 contract：

| Contract | Public package | Machine schema | Canonical behavior |
| --- | --- | --- | --- |
| Artifact Manifest | `generation/artifact` | `artifact.Schema()` | `NewManifest` / `Parse` / `CanonicalJSON` |
| API Manifest | `generation/api` | `api.DocumentSchema()` | `NewManifest` / `Parse` / `CanonicalJSON` |
| Service Manifest | `generation/service` | `service.Schema()` | `New` / `Parse` / `CanonicalJSON` |
| Generation plan/result | `generation/transaction` | `transaction.PlanSchema()` / `transaction.ResultSchema()` | `Build` / `Check` / `Write` |

真实 source parser、versioned IR、plan、check、write 和 artifact lifecycle 属于 consumer 选择并编入的
generation capability。Contract package 不通过文件扫描注册命令，也不要求未选择的 capability 提供空
事实或空插件。某个二进制是否包含相应能力，以 `nexactl inspect --json` 为准。

Machine schema 与 owner package 同置。Consumer 通过 accessor 获取 schema；根目录 registry、文档副本
或手写 Go string 都不是 schema authoring surface。

## Provenance 与确定性

Manifest 使用 `provenance.SourceRef` 和 `provenance.Digest` 连接权威 source：

- whole-document ref 的 digest 来自 authored file 的精确 bytes；
- fragment ref 的 digest 来自领域 parser 定义的 canonical semantic bytes；
- Artifact Manifest 的 input digest 由 generator identity/version 与有序 source set 计算；
- API Manifest 的 source digest 由有序 source set 计算；
- canonical JSON 不包含生成时钟、随机值或 map iteration order，空集合编码为 `[]`。

API Manifest 的 `sources` 是节点来源的双向完整闭包，而不是文件级 allowlist。每个非内建 schema、每个
field 和每个 operation 都携带 `NodeProvenance`：`canonical` 恰好引用一个 owner-native 节点，
`derived` 引用一个或多个实际参与投影的 owner-native 节点。每个 ref 必须与 `sources` 中的
`SourceRef` 精确相等；相同 path 的 whole-document ref、父节点 ref 或相邻 fragment 都不能代替目标节点。
除合法的全空 Manifest 外，`sources` 中每一项也必须被某个节点 provenance 或 field origin 使用。

跨 source 的 field origin 使用独立的 `OriginBinding{Ref}`。Origin 只表达 owner 定义的 typed relation，
不重复 target category，也不能与 field 自身的 provenance ref 相同。`Manifest.Source(ref)` 提供精确、
不可变的来源查询；Manifest、IR、SDK model 和 frontend export 仍然只是只读 projection。

Generated API proxy 使用以下 owner-node source matrix：

| API node | Required owner sources |
| --- | --- |
| operation | 含 typed HTTP proxy option 的 Proto method；选中的 Service Catalog binding |
| operation request schema | 同一 Proto method；request message；选中的 Catalog binding |
| operation response schema | 同一 Proto method；response message；选中的 Catalog binding |
| request/response field | 同一 Proto method；精确 Proto field；选中的 Catalog binding |

Owner-native typed relation 只追加到其值实际改变的节点。Generated proxy 不创建 synthetic `.api` author
source，也不把 CompositionIR、API Manifest、generated source 或其他 projection 记录为 owner source。
Proto/API owner parser 负责构造 source；API Manifest contract 只验证 typed provenance、精确引用和闭包。

排序、digest 和 canonical bytes 相同，consumer 才能可靠区分重建、drift 与 ownership conflict。

## Artifact ownership

每个 artifact 记录稳定 id、repository-relative path、owner、内容 digest、source refs 和 stale policy。
这些字段表达投影归属，不授予任意写入或删除权限。

`delete-if-unmodified` 只表示 stale artifact 可以进入删除候选。执行删除前，selected capability 还必须
同时证明：

- 文件带有可识别的 generated marker/provenance；
- owner 与 artifact id 匹配；
- 当前内容 digest 与 manifest 记录的旧 digest 相同；
- source/plan 状态确认该 artifact 已 stale。

任一证明缺失或不匹配都产生 ownership conflict，并保持零写入。`retain` 永不授权自动删除；manifest
不提供 unconditional deletion policy。

## Serial staged publish lifecycle

受控生成以 canonical plan digest 连接只读决策与 repository write：

- `plan` 从精确 source snapshot、上一份 manifest 与 ownership probe 计算 change/conflict，不写文件；
- `check` 复用同一 plan/ownership kernel 报告 clean/drift/conflict，并返回对应 plan digest；
- `write` 必须接收同一 source snapshot 的 plan digest，source、旧 manifest 或 ownership 状态变化时拒绝写入；
- 同一 worktree 的 write 由调用方串行调度；每次 invocation 使用唯一 repository-scoped staging；
- candidate bytes、manifest、ownership 与 parser/type checker/compiler 检查在发布前完成；
- 发布前重新检查 source、旧 manifest 与所有受控文件，各文件以普通原子替换发布，manifest 最后发布；
- 多文件 batch 可以部分发布。失败只 best-effort 清理本次 staging，下次从当前 repository 重新 plan，
  不自动 Recover、重放或回滚旧 invocation。

Service Manifest 不委托外部代码生成器。它从所选 service 的 canonical contract source set 计算 contract
digest，并使用相同 check/write staged publish 维护 consumer-owned projection。

## Authoring 与消费方向

```text
authored owner nodes -> parser / typed metadata -> versioned IR -> generated artifact
          |                                                   |
          +--------- exact SourceRef / semantic Digest ------> manifest

manifest -> SDK / CLI / frontend / docs / read model (read-only)
```

Service Catalog 只表达 service-to-capability binding，不承载 API route、CRUD、field metadata 或 artifact
ownership。API Manifest 可以投影 `.api` authoring facts；Artifact Manifest 可以记录 API Manifest 本身作为
generated artifact，但两份 manifest 都不能成为新的人工入口。

业务事实归属与最近事实源规则见[业务事实契约](business-facts.md)，公共命令与 capability 的发现规则见
[CLI 机器协议](cli-machine-protocol.md)，完整生成链与命令矩阵见[受控生成契约](controlled-generation.md)。
