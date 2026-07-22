# 受控生成契约

Nexa 受控生成把 consumer repository 中的强类型业务事实投影为可编译、可追溯的普通源码。
consumer 拥有事实、项目关系、生成产物和最终发布；Nexa 只拥有 typed annotation、strict parser、
versioned IR、generator、serial staged publish、validator 和 CLI 协议。IR、manifest 与 generated source 都不是
人工事实源。

## 1. 所有权与完整链路

| 生成链 | Owner | Authoring surface | Derived projections | Public contract | Behavior gate |
| --- | --- | --- | --- | --- | --- |
| Ent Go | consumer Ent schema；生成语义归 Ent | Ent schema Go types | Ent generated Go | `generation/toolchain` 的 Ent 委托边界 | 真实 consumer module 中调用固定版本 Ent，并验证进程结果 |
| CRUD Proto | consumer Ent schema | `nexaent.CRUD(operations...)` typed annotation | `EntityIR`、`CRUDProtocolIR`、Proto fragment、compatibility lock | `nexaent`、`generation/entity`、`generation/crudproto` | annotation presence/absence、闭合枚举、Proto parse 与 compatibility check |
| RPC Go | consumer Proto | Proto AST、descriptor 与 custom options | `ProtocolIR`、RPC Go 普通源码、Artifact Manifest | `generation/protocol`、`generation/rpcgo` | Proto compile、委托工具协议、generated Go 编译 |
| Core API | consumer Proto、Core `.api` 与 Service Catalog | Proto proxy metadata、`.api` AST、service-to-capability binding 与拓扑 | `ProtocolIR`、APIIR、`CompositionIR`、API Manifest、proxy/client/mapper/error adapter/logic/register 普通源码 | `generation/httpapi`、`generation/composition`、`generation/api`、`generation/apigo` | source closure、route/type collision、API parse、generated Go 编译与 runtime contract 校验 |
| Service Manifest | consumer service contracts | 该 service 的 catalog/module source，以及按 service kind 纳入的 Proto、API 与 Composition owner nodes | Service Manifest | `generation/service` | 精确 contract source set、digest 重算与 check/write drift |
| 生成发布 | consumer repository | 上述权威源与上一份 Artifact Manifest | plan、result、invocation staging、Artifact Manifest | `generation/artifact`、`generation/transaction` | plan digest、ownership probe、staging validation、发布前 drift、逐文件原子替换 |

所有链路固定遵循：

```text
owner facts -> strict load -> validate -> versioned IR -> deterministic plan
            -> check or stage -> parse/typecheck/compile verify
            -> pre-publish drift check -> accepted per-file write
            -> structured result + generated manifest last
```

每个 source 由 owner parser 投影 canonical `SourceRef` 与 `Digest`。Project provider 只解析 consumer 的
项目关系、入口路径和受控工具选择，不复制 Ent、Proto、API 或 catalog 中的节点事实。

## 2. Ent 委托与 CRUD 是两条链

`nexactl gen ent` 只委托 Ent 官方工具链生成 Ent Go。它负责 consumer module 定位、版本固定、输入与写集
隔离、进程执行和统一错误投影，不重实现 Ent 语义，也不把 Ent generated Go 变成 Nexa-owned artifact。

Nexa CRUD 生成只读取 Ent graph 中的 typed annotation：

```text
nexaent.CRUD(operations...)
  -> EntityIR
  -> CRUDProtocolIR
  -> Proto fragment
```

- annotation presence 加非空 closed operations 是唯一 opt-in；annotation absence 即不生成 CRUD。
- operations 只允许 `create`、`get`、`list`、`update`、`delete` 的公开闭合枚举。
- `SchemaMeta`、`FieldMeta`、label、desc、i18n、`UIHint` 与 source binding 同样属于对应 typed
  annotation；Go comments 只用于人类说明。
- Service Catalog、project provider、Artifact Manifest 和 Ent generated Go 都不参与 CRUD 选择。

## 3. Business API Composition

Business API Composition 合并三类互不替代的 consumer facts：

| Owner | Authoring surface | Composition 使用方式 |
| --- | --- | --- |
| service topology | Service Catalog | 选择 service、dependency 与 service-to-capability binding |
| RPC contract | 服务 Proto | 提供 RPC/message/method 与 typed HTTP proxy metadata |
| native HTTP contract | Core `.api` | 提供 Core 原生 route、type 与 field metadata |

生成链先从 Proto 构建 `ProtocolIR`，从 Core `.api` 构建 native APIIR，再由 catalog binding 与拓扑构建
`CompositionIR`。Composition 产生 generated APIIR，与 native APIIR 经过结构化 merge 后投影为 API Manifest
和业务 Core API 普通源码。proxy、client、mapper、error adapter、logic 与 register composition 都在 consumer
中静态编译；它们不是运行时插件，也不会进入 Nexa module。

空 Service Catalog、没有 proxy binding 或没有 generated operation 都是合法输入。生成链不得为缺席能力
创建占位 route、空插件或伪造 source。

## 4. CLI 发现与 12 命令矩阵

任何自动化在选择命令、flag、schema、delegated tool 或 side effect 前，必须先对 consumer 实际编译的
二进制执行：

```bash
nexactl inspect --json
```

下表定义官方 generation Build Plugin 的命令族。Consumer 可以不编入该插件或只在自己的 composition
中提供 project provider；因此下表不能代替 `inspect`，调用方也不得从本文推断 flag 或 schema。

| # | Command path | Lifecycle | Delegated tool role |
| --- | --- | --- | --- |
| 1 | `gen ent` | Ent 官方委托 | `ent-generate` |
| 2 | `generation crud-proto plan` | 只读 plan | `ent-crud` |
| 3 | `generation crud-proto check` | 只读 drift check | `ent-crud` |
| 4 | `generation crud-proto write` | 接受 plan 后写入 | `ent-crud` |
| 5 | `generation rpc plan` | 只读 plan | `rpc-go` |
| 6 | `generation rpc check` | 只读 drift check | `rpc-go` |
| 7 | `generation rpc write` | 接受 plan 后写入 | `rpc-go` |
| 8 | `generation api plan` | 只读 plan | `api-go` |
| 9 | `generation api check` | 只读 drift check | `api-go` |
| 10 | `generation api write` | 接受 plan 后写入 | `api-go` |
| 11 | `generation service-manifest check` | 只读 drift check 并返回 plan digest | 无外部委托工具 |
| 12 | `generation service-manifest write` | 接受 plan 后写入 | 无外部委托工具 |

`check` 不修改 repository。`write` 只接受同一 source snapshot 计算出的 plan digest；输入发生变化时必须
重新 plan/check。CRUD compatibility lock 是否需要额外接受，也只由实际 `inspect` 返回的命令 schema 决定。

## 5. ProjectProvider 与 ProviderTool

consumer 在自己的 `nexactl` composition root 中显式实现 `ProjectProvider`。`ProviderDescriptor` 提供稳定
provider identity、semantic version 和 `ProviderTool`；`Resolve` 返回服务入口、Core service relation 与
每个服务实际选择的 `toolchain.Tool`。

`ProviderTool` 把一个可自省的 `DelegatedToolSpec` 绑定到一个闭合 `ToolRole`：

| Role | 允许服务的命令族 | 约束 |
| --- | --- | --- |
| `ent-generate` | `gen ent` | 只允许 Ent 官方生成委托 |
| `ent-crud` | CRUD Proto plan/check/write | 只允许读取 EntityIR 协议并产出 CRUD artifacts |
| `rpc-go` | RPC plan/check/write | 只允许读取 ProtocolIR 协议并产出 RPC Go artifacts |
| `api-go` | API plan/check/write | 只允许读取 merged APIIR/Composition context 并产出 Core API artifacts |

同一 tool id/version 只有在 provider descriptor 的对应 role 中声明，且与 service 选择的
`toolchain.Tool` 一致时才能执行。角色不能跨命令族复用。Delegated tool 的 id、version、inputs 与 writes
由 plugin spec 投影到 `inspect`；可执行文件、参数、环境规则和 repository/scratch scope 由 consumer
provider 的 typed Go 配置解析。Service Manifest 由 Nexa 的结构化 contract projection 生成，不需要
外部 tool role。

Project provider 是构建期项目 adapter，不是运行时插件、全局 registry 或业务事实袋。Consumer 私有
provider 留在自己的仓库，并且不得 import Nexa `internal` package。

## 6. Serial staged publish 与 ownership

- `plan` 输出 canonical source set、generator identity、目标 artifacts、create/update/delete/conflict 和
  plan digest；不写 repository。
- `check` 对当前 repository 与上一份 manifest 执行相同 ownership probe，并以结构化状态报告 drift。
- `write` 由调用方保证同一 worktree 串行；同一 worktree 并发 generation 明确 unsupported。
- 每次 write 使用同文件系统内的唯一 staging，并在发布前完成 candidate bytes、manifest、ownership probe
  与必要 parser/typecheck/compile 验证。
- 首次发布前重新检查 accepted plan digest、source snapshot、旧 manifest 和当前受控文件；每个文件发布点
  再核对自己的 precondition。
- generated artifact 只有在 generated marker、artifact id、generator id、input digest 与当前内容 digest
  全部匹配时才能更新或删除；manual artifact 永不被生成器覆盖或删除。
- create/update/delete 使用普通单文件原子 publish，manifest 最后发布；多文件 batch 不承诺原子性。
- 失败报告真实错误并 best-effort 只清理本次 staging；已经发布的文件不自动回滚。下次必须从当前
  repository 重新 plan，不读取、恢复或重放旧 invocation。
- Artifact Manifest、API Manifest 与 Service Manifest 记录 provenance 和 projection 状态，但不承载
  CRUD、route、field 或 topology 配置。

完整 manifest 规则见[生成清单契约](generated-manifests.md)。

## 7. Optional composition

generation、frontend、requirements/work/UserOperation、human gate、TestSpec/evidence、deployment 和其他
Build Plugin 都是编译期可选能力。缺席时：

- `inspect` 不返回对应 capability 和 command；
- consumer 不需要空目录、空事实、空 provider 或占位 artifact；
- Core 与普通业务后端仍可构建和运行；
- 调用不存在的命令由 host 投影稳定的 `command_not_found`，不得用空 handler 模拟已安装能力。

## 8. `wails3-plugin-platform` 设计影响边界

[wails3-plugin-platform](https://github.com/nxnminieye/wails3-plugin-platform) 只作为公共平台设计影响的
对照，不是 Nexa contract 或业务事实源。Nexa 保留适合 Go module、CLI 和 AI automation 的实践，并按
编译期代码组合与受控生成边界重新定型。

### 8.1 直接借鉴

| 持续有效的设计影响 | Owner | Authoring surface | Projection | Nexa public contract | Behavior gate |
| --- | --- | --- | --- | --- | --- |
| 一个 Go module release tag 锚定代码、contracts、docs 与 framework skills | Nexa release owner | root module 与同一 tag 下的仓库资产 | module archive、release docs、skill distribution | canonical module path 与 semantic version | hermetic module archive、external consumer build/test |
| CLI 是薄入口；inspect 结构化暴露组成，doctor/selftest 由所选 capability 按需提供 | host 与各 capability owner | immutable plugin spec、typed command handler | CLI inspection/envelope/diagnostic evidence | `nexactl/plugin`、`nexactl/host`、`cli/protocol` | Host Inspect/Execute 与已编入命令的 subprocess tests |
| 公共 facade/contract 与 `internal` 实现分层 | 各 Nexa domain owner | public DTO、constructor、interface 与 schema accessor | consumer adapters | versioned public Go packages | external consumer compile isolation |
| 中性 reference consumer 与能力验证矩阵 | Nexa framework owner | `fixtures/consumers` 和行为测试 fixtures | release/readiness evidence | public Go API 与 CLI protocol | `GOWORK=off` consumer tests 和 generation integration closure |
| 渐进式文档索引与明确的“不做”边界 | Nexa documentation owner | `README.md`、`docs/README.md`、分域 contract | AI/reader context | versioned public docs | link validation、skill validation 与 contract behavior tests |

### 8.2 调整后借鉴

| 持续有效的设计影响 | Owner | Authoring surface | Projection | Nexa public contract | Behavior gate |
| --- | --- | --- | --- | --- | --- |
| AI action、inspect、evidence、approval 分离 | CLI 协议归 Nexa；业务 evidence/decision 归 consumer human owner | plugin spec、typed fact 与可选 review-decision contract | envelope、plan/result、evidence/read model | stable category/exit code/side effect/plan digest | inspect/execute、serial staged publish 与 optional host composition tests |
| recipe/Make 作为可重复入口 | 使用该入口的 repository owner | Make target 或 consumer recipe | 命令执行摘要 | 只消费 `inspect` 发现的 CLI 或公开 Go test contract | target 真实执行；不得复制 flag/schema 或成为第二事实源 |
| starter/asset distribution 映射为 Source Bundle | bundle 发布者拥有发布 bundle；consumer 拥有物化源码 | versioned Source Bundle contract | materialized service/source baseline 与 provenance | `sourceplugin` public contract | deterministic materialize、ownership 与 consumer build tests |

### 8.3 明确不借鉴

| 不进入 Nexa 的平台能力 | Owner | Authoring surface | Nexa projection | Nexa public contract | Behavior gate |
| --- | --- | --- | --- | --- | --- |
| runtime plugin catalog、market、install、lifecycle、hot load | 不属于 Nexa plugin model | 无 Nexa authoring surface；业务服务仍是普通源码 | 仅允许 Service Catalog 投影静态 topology/CompositionIR | `project/servicecatalog` 与编译期 composition | Minimum Runtime、host composition 与 generated static code tests |
| remote gateway、token、debug bus 与远程 support session | consumer runtime/ops 自行拥有 | consumer runtime/deployment facts | 不进入 generation IR 或 `nexactl` | `nexactl` 无此运行时 contract | plugin side-effect 校验拒绝 runtime/remote mutation |
| Wails UI shell、browser bridge 与 portable host | consumer application 或其 UI platform 自行拥有 | consumer UI/runtime facts | 无 Nexa Core projection | 无 Nexa public UI contract | 无 frontend 的 consumer fixture 仍可构建运行 |

这些边界保证 Nexa 可以借鉴成熟公共平台的版本、CLI、公共 API 和验证纪律，而不把编译期 Build Plugin
重新解释成运行时插件系统。
