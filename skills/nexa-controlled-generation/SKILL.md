---
name: nexa-controlled-generation
description: Use when modifying or reviewing Nexa business fact sources, Generation IR, code generation, artifact manifests, generated/manual ownership, drift, stale cleanup, Business API Composition, or migration generation boundaries.
---

# Nexa Controlled Generation

## 核心原则

consumer repository 拥有人工业务事实和生成源码；框架拥有 strict loader、versioned IR、generator 与 validator。IR、manifest 和 generated artifact 都是可重建投影，不是第二份人工事实源。

业务事实遵守“最接近事实源优先”的放置顺序：

1. 优先使用原生节点上的强类型 metadata；
2. 其次使用同一事实文件或 package 中的结构化声明；
3. 只有关系跨越多个权威源，或原生格式无法表达时，才由领域 owner 定义封闭的 typed relation schema；relation document 只保存 relation 或 decision，并为所有原事实携带规范的 canonical `SourceRef` 与 `Digest`；
4. 全局 catalog 只表达服务发现、跨事实或跨服务 capability binding relation 和服务拓扑。Service Catalog v1 的 `CapabilityBinding` 仅包含 capability `id` 与 `apiVersion`，其 presence 只表达 service 到 capability contract 的关系，不保存 capability 本体或节点事实。

Service Catalog v1 不提供通用 `configRef`、开放 extension 或 generic sidecar path；通用引用无法在语义上证明最近事实源所有权。新的跨源 relation 只能在领域 typed relation schema 已定义后引入：需要由 catalog 暴露的关系使用新的 catalog `apiVersion`；其他领域关系使用 capability-specific typed binding contract 及其领域 loader。两条路径都不得扩展 Service Catalog v1。Ent CRUD、label、desc、i18n、UIHint、source binding 必须留在 typed Ent annotation，Proto/API metadata 必须留在各自 contract；它们不得复制到 relation document 或全局 catalog。Generated manifest、IR、SDK、export 和生成文档都是可重建投影。

## 发现入口与任务计划

先读取当前 consumer 仓库根 `AGENTS.md` 和当前已审核的任务计划。执行受控生成前，对 consumer 实际使用的二进制运行：

```bash
nexactl inspect --json
```

只支持 `result.apiVersion=nexa.dev/cli-inspection/v1`。Inspection 用于发现当前 command path、owner、flags、schema、side effect 和 delegated tools，并检查计划与实现是否漂移；它不批准命令，也不替代任务计划。

计划必须明确准确命令、参数来源、输入文件、affected files、允许的副作用、预期输出、验证命令和回滚步骤。不得从 capability presence、`--help`、Makefile、文档或历史执行记录自行选择额外命令，也不得在 skill 中复制第二套 flag/schema。

Inspection 与计划不一致、输入不完整或副作用超出计划时，输出结构化 gap 并停止执行，不构造额外 argv、不创建 WorkRoot、空插件、事实/占位目录或手工生成物。每个 repository write 前重新确认输入与 affected files。

对于 Service Source + generation 链，inspect 后的顺序固定为：

1. 只考虑当前已审核任务计划列出且 inspect 中真实存在的 `source` 与 generation command；
2. 按计划通过 Source adapter 对显式 `provider@version/profile` 执行只读 plan，再 materialize；
3. materialize 成功后，把 consumer-owned typed Ent、Proto、`.api` 和 manual Go 作为权威输入；
4. 每个 generation plan/check/write 都按计划重新确认输入、affected files 和副作用，再用 parse/typecheck/compile/execute 验证；
5. 不手改 generated artifact，不从 Provider manifest、source lock、IR、read model 或文档重建人工事实。

Job、Quality 或业务私有 Provider 缺席是合法状态，不能用 package presence 或文档中的 Provider 列表推断可用性。

V0.1 的 runtime `nexa`、可发布的 Python SDK/wheel、HTTP parity、quality 和 deployment 明确
not-selected。Reference `nexactl` 中的 governance validation、Skill sync 与 `sdk-python-assets` 工程命令
不等于这些 runtime 能力已被 consumer 选择。不得探测缺失 binary，不得创建这些能力的事实文件、占位
目录或无条件投影，也不得手改生成物。

## 发现与事实源

先读取项目 `AGENTS.md` 和已审核任务计划，再执行上面的发现入口。Inspection 中存在 generation command family 只说明当前实现提供该命令；执行范围仍以计划为准。不要根据框架参考二进制、文档表格或另一个 consumer 推断当前命令。
委托上游生成器的 toolchain adapter 不是 Nexa 自有 Generation IR/generator。

任何不直接服务 starter 或受控代码生成的新增框架能力，必须在设计、写计划或实现前交用户审核。

Business API Composition 有三个不可互换的人工事实源：

| 事实源 | 唯一职责 |
| --- | --- |
| `services.yaml` | 服务发现、跨事实或跨服务 capability binding relation 与服务拓扑 |
| 服务 `desc/*.proto` | RPC、message、method 与 HTTP proxy metadata |
| Core API `desc/*.api` | Core 原生 HTTP contract |

新增 RPC proxy method 通常修改服务 Proto；只有服务发现、binding relation 或拓扑变化才修改 `services.yaml`，只有 Core 原生 HTTP contract 变化才修改人工 `.api`。不要增加第四份 exposure 事实。业务 logic 和测试是 consumer-owned manual code，但不是 Generation IR 的替代输入。

## Ent 边界与标准 CRUD

`Ent Schema -> Ent Generated Go` 属于 Ent 上游工具链和 consumer 的常规 Go 构建，不是 Nexa 自有 Generation IR 或 generator 的职责。`nexactl gen ent` 必须保留，但其身份是 Ent toolchain adapter：负责从业务 descriptor 定位服务、调用业务声明且版本固定的 Ent 生成入口，并把进程结果投影为统一的结构化 CLI 结果。它不得重实现 Ent 生成语义、把 Ent generated Go 宣称为 Nexa 产物，或把该命令描述成 Nexa generator。

该 adapter 可以作为可选 Build Plugin 编入业务 `nexactl`，introspection 必须标明其 delegated tool、输入、写集和副作用。schema 和 generated Go 属于 consumer；生成引擎属于 Ent；Nexa 只拥有 adapter contract、调用编排与错误投影。

Nexa 只在已选择的 Nexa capability 确实需要时，把业务 Ent schema 解析为只读 `EntityIR`。其中标准 CRUD 协议生成遵守：

- Schema 级 CRUD 意图的唯一事实源是业务 Ent schema graph 中的自定义、强类型 `schema.Annotation`。它属于与 `SchemaMeta` 一致的 typed annotation 体系，公开构造形式为 `nexaent.CRUD(operations...)`。
- `nexaent.CRUD(...)` annotation 出现即表示该 schema 显式选择 CRUD；annotation 缺失即不生成任何 CRUD contract。不设置冗余的 `Enabled: true`，也不接受独立 YAML、`configRef`、无类型 `map[string]any` 或其他并行配置。
- `operations` 使用 Nexa 公开 package 定义的闭合枚举，分别表达 create、get、list、update 和 delete。每个 schema 必须显式声明它需要的操作集；空集合、重复操作或枚举外值必须在构建 `EntityIR` 前失败。
- CRUD generator 直接从 Ent schema graph 读取 typed annotation，链路固定为 `Ent annotation -> EntityIR -> CRUDProtocolIR -> Proto Fragment`。它不读取 Ent generated Go，也不得把 `nexactl gen ent` 的委托执行结果当作 CRUD 事实源。
- Go comments 只服务人类阅读和文档，不参与 CRUD 选择、操作集计算或 IR 构建。
- Service Catalog 只记录服务拓扑与跨事实或跨服务 capability binding relation，不拥有 capability 本体或 Schema 级事实；Artifact Manifest 只记录输入 provenance、生成器和受控产物，不承载 CRUD 配置。
- 非 CRUD schema 可由业务正常使用 Ent client，并通过人工 Proto/logic 暴露命令、查询或 read model；不得因为存在 Ent schema 就推断公共 CRUD。
- 删除 `nexaent.CRUD(...)` annotation 或缩减其 operations 时，`plan/check/write` 必须识别并受控清理对应 stale Proto fragment；RPC Go 生成只消费实际存在的可选 CRUD fragments。

## ProjectProvider 与工具角色

consumer 的 generation Build Plugin 通过显式 `ProjectProvider` 解析 project relation、service authoring
surface 与被选择的 `toolchain.Tool`。Provider 不是业务事实源；它只定位 Ent、Proto、API 与 catalog
owner，并且不能复制这些节点的 metadata。

运行委托工具前，同时检查：

1. inspect 中该 command 声明了 delegated tool；
2. provider descriptor 用 `ProviderTool` 把 tool id/version 绑定到该 command family 的闭合 role；
3. service resolve 结果选择的 `toolchain.Tool` 与 descriptor 一致；
4. 实际输入、写集、环境和 repository/scratch scope 满足 inspect 与 tool contract。

role 不能跨 Ent generation、CRUD、RPC Go 或 API Go 命令族复用。Service Manifest 由 Nexa 的 typed
contract projection 生成，不应虚构 delegated tool。

## 受控链路

```text
strict load -> validate -> versioned Generation IR -> plan -> check or write
            -> staging parse/typecheck/compile/execute verify
            -> pre-publish drift check -> per-file atomic publish -> structured result
```

- `plan` 只读计算 create/update/delete/conflict；`check` 只读检测 drift；`write` 由调用方保证同一 worktree
  串行，在唯一 staging 验证成功且发布前 drift 复核通过后逐文件发布，manifest 最后发布。
- Artifact manifest 记录 generator/version、input digest 与 provenance，以及 artifact id、owned path、owner、digest 和 stale policy；它本身仍是生成投影。
- generated/manual ownership 必须由 contract/manifest 表达。只有 generated 标记、manifest owner 和 provenance 一致时才能自动删除 stale artifact；人工修改、未知 owner、共享引用或 digest 冲突必须失败。
- 业务 proxy fragment、client、mapper、error adapter、logic 和 register projection 留在 consumer Core API 静态编译，不进入框架公共 module。

Business API Composition 必须按 `Service Catalog + ProtocolIR + native APIIR -> CompositionIR -> merged
APIIR -> static consumer source` 执行。空 catalog、没有 proxy binding 或可选 capability 缺席都是合法
状态；不要生成 placeholder operation。

## 可选与运行时边界

requirements/work/UserOperation、human gate、TestSpec/evidence、deployment 和 frontend 可以全部缺失；不要生成占位事实、空目录或无条件下游投影。仓库内 migration plan/diff/render/lint/check 可以属于 Build Plugin；数据库 `apply/status` 必须进入 `nexa admin migration` 或业务私有 ops CLI，不得由 `nexactl` 执行。

`sourceplugin.Provider` 是 source tool 构造期的 BundleProvider；materialized Core `IdentityProvider` 是
consumer runtime 的认证端口。不得复用它们的 interface、registry、constructor 或 lifecycle。标准链路是
`materialize -> generate -> compile/run`：detach 和删除 source tool 后，generation 只消费 consumer-owned
native facts；删除 generation tool 后，普通服务仍保持 build/start/runtime behavior。

Quality read model 是可重建的只读 relation projection。Quality runtime、producer、frontend、人工 gate、
TestSpec、evidence 与 deployment 分别显式选择；任何一个缺席都不能反向阻断 Framework Minimum 或 Core。

## 验证

先重新运行 `nexactl inspect --json`，确认仍为支持的 inspection v1，并与已审核计划中的命令、输入、affected files 和允许副作用核对。行为测试必须
覆盖确定性重建、missing/changed/stale/conflict drift、受控清理、manual protection、partial publish 后 fresh
plan。失败只 best-effort 清理本次 staging，不自动回滚已发布文件。
生成 Proto/API/Go/SDK 后使用对应 parser、type checker、编译器或真实执行验证；重复生成应无 diff。
禁止用源码文本、import、文件位置、目录 allowlist 或行数门禁代替产物行为。

## 常见错误

- 把 generated `.api`、IR 或 manifest 当人工事实源。
- 把 `nexactl gen ent` 描述为 Nexa generator，或把 Ent generated Go 记为 Nexa artifact。
- 因 generation capability 缺失而手补生成物。
- 为 backend 变更无条件引入质量、前端、部署或数据库运行时链路。
