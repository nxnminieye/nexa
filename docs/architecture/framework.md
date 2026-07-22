# Nexa 框架架构

## 1. 仓库与事实所有权

Nexa 使用单一根 Go module：

```text
module github.com/nxnminieye/nexa
```

本仓库是公共实现与发布仓库。在职责边界上，本仓库拥有公共 package、机器协议、严格 schema、loader、parser、IR、generator、validator、官方代码组合模块和框架 skill；consumer repository 拥有业务 contract、业务事实源、生成产物、产品配置、部署实例和私有代码组合。

框架公共 API 是兼容承诺，不是业务事实源。业务事实不能因为被框架工具读取或生成投影而转移到本仓库。

### 1.1 最近事实源优先级

同一事实按以下顺序选择唯一人工入口：

1. 同一语法节点上的强类型 metadata；
2. 同一事实文件或 package 中的结构化声明；
3. 领域 owner 定义的强类型 relation document；
4. 只表达跨事实或跨服务 capability binding relation、服务发现与服务拓扑的全局 catalog。

Service Catalog v1 不提供通用 `configRef`、开放 extension 或 generic sidecar path。通用引用无法在语义上证明被引用内容遵守最近事实源所有权，因此不能作为 catalog 的扩展机制。真实关系横跨多个权威源或原生格式确实无法表达时，领域 owner 必须先定义封闭的 typed relation schema；relation document 只保存关系或决策，并为所有原事实携带规范的 canonical `SourceRef` 与 `Digest`。需要由 catalog 暴露的关系通过新的 catalog `apiVersion` 引入；不属于 catalog 的领域关系由 capability-specific typed binding contract 及其领域 loader 消费。两条路径都不得扩展 Service Catalog v1。

每个公共 contract 和生成链都必须明确记录：

- `owner`：谁拥有人工事实；
- `authoring surface`：事实在哪个 typed 节点、contract 或领域文件中编写；
- `derived projections`：哪些 IR、manifest、代码、SDK、export、文档或 read model 可由它重建。

当 catalog、Go hardcode、comment、relation document 或 generated file 重复表达同一事实时，只保留优先级最高的权威人工入口，其他对象只能消费或投影该事实。

### 1.2 领域所有权

| 领域事实 | Owner | Authoring surface | Derived projections |
| --- | --- | --- | --- |
| Ent schema、type、field，以及 label、desc、i18n、UIHint、source binding | consumer | Ent graph 中的 typed `SchemaMeta`、`FieldMeta` 或专用 `schema.Annotation` | `EntityIR`、生成协议、校验结果、前端与文档投影 |
| Ent Schema 级 CRUD intent | consumer | typed CRUD annotation presence 与闭合 operations enum | `CRUDProtocolIR`、Proto fragment 及其下游代码 |
| RPC、message、method | consumer | 服务 `desc/*.proto` 的 Proto AST 与 custom options | descriptor、RPC IR、proxy/client/SDK 投影 |
| HTTP route、type、field | consumer | Core API `desc/*.api` 的正式 AST 与 typed metadata | API IR、路由、类型、SDK 与文档投影 |
| 服务发现、跨事实或跨服务 capability binding relation、服务拓扑 | consumer | Service Catalog v1；`CapabilityBinding` 仅含 capability `id` 与 `apiVersion` | `CompositionIR`、静态 register composition 与服务文档投影 |
| 前端专属交互 | consumer | 对象 schema | 页面、表单、i18n bundle 与 frontend export |
| 部署环境与实例 | consumer | 部署事实文件 | render、diff、部署清单与状态 read model |
| 人工 gate 与审批 | consumer human owner | 对应领域的 review-decision 文件 | gate 状态、审核摘要与 release read model |
| Source Bundle manifest、profile 与标准 tree | provider publisher | provider package 的 typed manifest 与 embedded tree | exact release、cache entry 与 materialization plan |
| materialized source | consumer | consumer repository 中的普通源码 | consumer build、test 与 release artifacts |

Service Catalog v1 中的 binding presence 只表达 service 到 capability contract 的跨事实关系，不拥有 capability 本体或任何节点事实。Ent CRUD、label、desc、i18n、UIHint、source binding，以及 Proto/API metadata 永远保留在各自最近权威源，不得复制到 Service Catalog 或 relation document。Go comments 只用于人类说明，不参与 typed metadata、IR 或生成选择；metadata 结构不得使用 `map[string]any`。

公共使用规则与 machine schema accessor 见[业务事实契约](../contracts/business-facts.md)。

Artifact Manifest、所有 IR、SDK model、frontend export、generated docs 和 read model 都是 projection。它们可以记录 provenance、digest、ownership 和产物状态，但不得成为人工编辑入口，也不得反向覆盖其权威源。

Artifact/API Manifest 的 provenance、determinism、ownership 与 stale policy 见[生成清单契约](../contracts/generated-manifests.md)。
typed facts 到 IR、普通源码与 serial staged publish 的完整链路见
[受控生成契约](../contracts/controlled-generation.md)。
Source Bundle 的 provider、identity、resolver/cache、provenance 与无锁串行发布见
[Source Bundle 契约](../contracts/source-bundles.md)。

## 2. AI-native 控制面

Nexa 按以下单向关系组织自动化：

```text
business facts -> CLI / SDK / generators -> artifacts and evidence
                         ^
                         |
                   skills orchestrate
```

- CLI 定义稳定机器协议、非交互执行、能力发现和错误投影。
- Skill 识别任务、选择 CLI、组织人工审核，但不复制命令或业务事实。
- Framework skill 自包含，不要求安装外部开发工作流 plugin。Consumer 自选的工作流工具只影响其本地执行方式，不属于 Nexa capability、构建、CI 或运行依赖。
- Make target 可以提供人类友好别名，但只能调用公开 CLI。
- `nexactl inspect --json` 是编译组成、capability、命令、flag、schema 和副作用的唯一发现入口。

## 3. 代码组合对象

| 对象 | 分发方式 | 业务仓是否获得源码副本 | 最终所有者 | 运行时插件身份 |
| --- | --- | --- | --- | --- |
| Nexa Core | Go module dependency | 否 | Nexa | 否 |
| Core Application Source Baseline | Source Bundle 物化 | 是 | 业务仓 | 否 |
| Service Source Plugin | Source Bundle 物化 | 是 | 业务仓 | 否 |
| Nexactl Build Plugin | Go package dependency | 否 | 官方包归 Nexa，私有包归业务仓 | 否 |
| Business Service | 业务仓普通源码 | 已位于业务仓 | 业务仓 | 否 |

“插件”只表达两种代码组合方式：确定性物化标准源码，或把工程命令通过显式 Go import 和 constructor 编译进 `nexactl`。插件不参与服务发现、进程管理、部署调度、远程安装、动态加载、热替换或运行时路由。

普通业务微服务不是插件。`job` 等跨业务复用的标准服务可以由 Service Source Plugin 提供；物化后仍是业务仓中的普通微服务。
Source Provider、一个可选 source Build Plugin adapter 与 materialized ordinary service 是三个独立对象；
只有最后一个进入业务运行。Source capability 不拥有 runtime config、deployment instance 或 health state。

## 4. 规范 package 边界

以下拓扑定义职责与依赖边界，不是某个 release 的能力清单。release 实际公开的 Go package 以 module 内容为准，CLI 能力以 `nexactl inspect --json` 为准。

```text
github.com/nxnminieye/nexa
  runtime/...                 public runtime contracts
  sdk/...                     generic transport and error model
  sourceplugin/...            Source Bundle contracts
  nexactl/
    host/...                  CLI composition and machine protocol
    plugin/...                Build Plugin public contract
  plugins/
    service/...               official source providers
    nexactl/...               official build-time command modules
  cmd/nexactl/...             reference composition root
  internal/...                non-public implementation
```

- Core 不依赖官方插件或业务私有插件。
- 官方插件和私有插件只依赖公开 contract。
- composition root 同时依赖 host 和所选插件，并决定二进制实际能力。
- `internal` 不属于兼容面，consumer repository 不得 import。
- 公开 DTO 在公开 package 中定义，通过 adapter 与内部模型转换，不转发内部类型别名。
- Runtime package 的 owner、公共行为、adapter 副作用边界与 optional-link 定义见
  [Runtime 公共契约](../contracts/runtime-packages.md)。

## 5. 可选 Build Plugin

Nexactl Build Plugin 按职责分为以下独立类别：

| 类别 | 工程职责 | 业务事实所有者 |
| --- | --- | --- |
| generation | 事实解析、IR、代码生成和 drift 检查 | consumer repository |
| migration | repository schema change 的 plan、diff、render、lint、check | consumer repository |
| frontend | schema、i18n、页面与客户端投影 | consumer repository |
| source | Source Bundle plan、check、物化、状态、差异、升级和 detach | provider 拥有发布 bundle；consumer 拥有副本与 provenance projection |
| requirements | 需求、work、UserOperation 的结构化处理 | consumer repository |
| gate | 人工 review/decision 的状态与投影 | consumer repository 的人工决策 |
| evidence | TestSpec、runner、trace、evidence 和 freeze | consumer repository |
| governance | 文档、仓库、package 和发布治理 | 相应仓库 |
| deployment | 中性部署 schema、render、validate 和 diff | consumer repository 的环境与实例 |

这些类别是编译期选择，不是运行时插件。未编入的能力不注册空命令；调用不存在的命令得到稳定的 `command_not_found`。

## 6. Minimum Runtime

Minimum Runtime 必须在以下能力全部缺席时仍可构建、启动并提供基础后端能力：

- requirements、work 和 UserOperation；
- human gate；
- TestSpec、runner、trace、evidence 和 freeze；
- deployment 与 observability instance；
- frontend。

缺席这些能力时不需要空目录、占位事实、空插件、Node、Kubernetes 或远端凭据。可选插件的依赖只约束“选择它时的组合是否合法”，不能反向成为 Core runtime 依赖。

Framework Minimum 不选择任何 Source Bundle，也不隐式创建 Core。Core Application 由 consumer 显式选择；
V0.1 reference composition 只包含 `core-application@v0.1.0`，端到端参考路径选择 `backend` profile。这个
exact Provider/profile 必须由当前 `nexactl inspect --json` 直接暴露后才能选择；只有 `source.bundle`
capability 不足以证明 release 已编入。Job 与 Quality Provider package 保持显式可选，不是 V0.1 reference
composition 或 Minimum/Core 的启动前提。

Core backend profile 只发布 authored source。Materialize 后，consumer-owned typed Ent、Proto、`.api` 与
manual Go 是 generation 的直接输入；Provider manifest/tree 和 source lock 不是生成事实源。链路、profile
closure 和 detach independence 见[标准服务 Source Bundles](../plugins/standard-service-source-bundles.md)。

Runtime package 同样按 Go import 与 constructor 显式选择。“0 optional”以最终程序未 import、未 link、
未构造 franz、gRPC、OpenTelemetry、go-zero 等可选能力为准，不以根 module graph 中是否存在 requirement
判断。该 Minimum Runtime 不需要 broker、gRPC server、provider、exporter、配置目录或凭据。

## 7. Business API Composition

consumer Core API 可以包含业务 RPC proxy 的生成源码。这些文件是业务仓中的普通静态源码，与 Core API 一起编译和部署；它们不是框架运行时插件，也不进入公共 module。

Business API Composition 有三个互不替代的业务事实源：

| 事实源 | 决定内容 |
| --- | --- |
| `services.yaml` | 服务发现、跨事实或跨服务 capability binding relation 与服务拓扑 |
| 服务 `desc/*.proto` | RPC、message、method 与 HTTP proxy metadata |
| Core API `desc/*.api` | Core 原生 HTTP contract |

框架只拥有 strict loader、parser、`CompositionIR`、generator、validator、mapping/error kernel。业务仓拥有生成的 API fragment、service client、mapper、error adapter、proxy logic 和静态 register composition。

生成物不是事实源，必须从上述业务 contract 重建。服务运行地址、环境 values 和 deployment instance 是运行时配置，也不是生成事实源。复杂业务编排应由业务手写 Core API 表达，不扩张透明 proxy contract。

Composition 的完整 owner、IR、static projection 与 behavior gate 见
[受控生成契约](../contracts/controlled-generation.md)。

## 8. 受控生成链

所有生成链遵守同一生命周期：

```text
load facts -> validate -> build versioned IR -> plan -> check or write -> verify -> emit result
```

- 业务事实保留在 consumer repository。
- IR 是框架定义的结构化投影，不是第二份人工事实源。
- generated artifact 由生成器管理内容，但保存在业务仓并参与业务发布。
- manual/generated ownership 必须由 contract 或 artifact manifest 表达，不能只依赖目录形态。
- check 只读；write 限定写集，在唯一 staging 内完成 parse、compile 或 execute 验证，发布前复核输入与
  受控文件，再逐文件原子替换；同一 worktree 的调用由上层串行调度。
- Migration Build Plugin 只处理 repository 内的 plan、diff、render、lint 和 check，不执行数据库 `apply` 或 `status`。运行时数据库操作由 consumer-selected runtime administration or private ops CLI 负责。
- 每条链的 loader 必须从其声明的 `authoring surface` 读取事实，并将 `owner`、输入 `SourceRef`/`Digest` 和 `derived projections` 投影到结构化协议或 manifest；生成产物不得作为上游输入回写事实。
- 事实源门禁必须覆盖语义解析、引用解析、digest/drift 和可观察生成行为。禁止用源码字符串、文件位置或目录形态测试代替 typed contract、parser、validator 或产物行为验证。

框架对受控生成链的职责止于 schema、typed annotation、parser、versioned IR、generator 和 validator。业务实例、业务关系决策、生成源码与部署事实始终由 consumer repository 持有。

Ent 生成委托与 CRUD projection 是两条独立链：`nexactl gen ent` 只调用 Ent 官方生成器；CRUD 固定从
typed annotation 经 `EntityIR -> CRUDProtocolIR` 生成 Proto。RPC、API、Service Manifest 与生成发布的
owner、authoring surface、12 命令矩阵、`ProviderTool` role 和 optional composition 统一定义在
[受控生成契约](../contracts/controlled-generation.md)。命令存在性、flag、schema、delegated tool 与 side
effect 仍必须由 consumer 实际 `nexactl inspect --json` 发现。

## 9. Schema、错误与副作用

跨 package 或跨仓 descriptor 使用明确的 `apiVersion`。封闭 schema 拒绝未知字段、重复字段和尾随内容；兼容变化通过新 schema version 表达，不通过静默忽略维持假兼容。

CLI envelope、错误分类、operation id、stdout/stderr 和自省结构由 [CLI 机器协议](../contracts/cli-machine-protocol.md)定义。

Nexactl Build Plugin 只声明 `none`、`repository-read` 或 `repository-write`。远端 API、业务数据库和运行时状态变更不属于 `nexactl`。

## 10. 版本与发布不变量

Core、官方 Build Plugin、官方 Source Bundle 和机器 schema 由根 module semantic version tag 锚定。只有独立发布节奏、依赖隔离或下载体积形成明确需求时才拆分 module。

正式 release 必须满足：

- `GOWORK=off` 时可以在干净环境完成构建和测试。
- 不依赖本地 `replace`、业务仓路径或未提交生成物。
- 不包含产品名称、客户配置、产品域名、凭据或环境私有值。
- 公共 API、schema、生成资产和 CLI 协议具备相应兼容说明。

Go module release tag 同时锚定公共代码、contracts、docs 与 framework skills。Nexa 对公共平台实践的
直接借鉴、调整借鉴和明确不借鉴边界见[受控生成契约](../contracts/controlled-generation.md)。

## 11. 测试不变量

- 单元测试验证行为、协议、生成产物可用性、状态转换和错误投影。
- 生成代码通过 parser、type checker、编译器或真实执行验证。
- 架构边界通过编译、类型检查、schema lint 或 AST/type-aware lint 验证。
- 中性 consumer fixture 验证公共 package 引用、私有 `nexactl` 组合和标准服务源码物化。
- Minimum Runtime fixture 真实缺少 frontend、requirements、gate、evidence 和 deployment 目录。
- 不使用源码字符串、import 文本、文件位置、目录 allowlist 或行数作为行为正确性的替代证据。
