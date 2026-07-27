# AGENTS.md

## 目的

本仓库是 `github.com/nxnminieye/nexa` 的公共实现与发布仓库。所有 agent 必须保持框架公共性、稳定接口和可验证行为，避免引入产品私有语义或只在单个业务仓成立的实现。

业务 contract、单一事实源和部署实例属于使用 Nexa 的 consumer repository。本仓库拥有版本化 schema、loader、parser、generator、validator、公共 package 和 CLI 行为，但不拥有任何业务事实实例。

## AI-native 原则

- AI-native 是本仓库的最高工程原则。
- CLI 是 agent、CI 和其他自动化消费者的可执行契约。公共命令优先提供非交互参数、稳定机器输出、结构化错误、operation id、明确 exit code 和能力自省。
- Nexa Skill 是 AI 开发或采用 Nexa 的必选路由入口。Agent 读取目标仓 `AGENTS.md` 并确认 Nexa 版本后，必须先进入同版本的 `nexa-framework-router`，再按任务进入最窄的专项 Skill；不得绕过 Skill 直接猜测生成链、CLI 或公共契约。
- Skill 负责定位 owner、能力发现入口、实施顺序和验证边界；实际命令及副作用仍由当前任务约束和真实 CLI/schema 决定。Skill 不复制命令、schema 或业务事实，也不代替 CLI、测试和 release 证据。
- Nexa Skills 必须自包含，不依赖 Superpowers 或其他外部开发工作流。它们是 AI 工程接口，不是 Go module 构建、业务服务运行、CI 或部署的 runtime dependency。
- Nexa Skill 的人工事实源是根 `skills/`；仓库或 consumer 的 `.codex/skills/nexa-*` 由同版本 `nexactl skills sync` 重建，不作为第二份人工入口。同步只替换 Nexa 管理的 Skill，必须保留其他 Skill。
- Make target 和文档只能消费公开 CLI/schema，不得成为并行命令事实源。
- 当前二进制实际包含的命令、flag、schema、capability 和副作用以 `nexactl inspect --json` 为准。

## 阶段范围与停止规则

- 实现开始前必须明确当前阶段、写集和逐条验收条件。代码、文档和测试只有能直接证明其中一条验收条件时
  才能进入本阶段；“以后可能需要”“顺便更完整”或“担心覆盖率下降”都不是扩展范围的理由。
- Workspace 的最终完成定义只用于安排阶段顺序，不能下沉为每个中间阶段的验收条件。跨阶段能力由其 owner
  阶段验证，不得提前塞入当前阶段的实现或 E2E。
- 删除已废弃机制时，应删除只服务该机制的测试和文档；不得为了保持历史测试数量或覆盖形态，重新建设一个
  更大的替代流程。
- 每个新增或保留的测试必须能映射到当前阶段的一条明确验收条件。无法映射的测试应移到对应后续阶段，
  不得成为当前阶段的合并门禁。
- 当前阶段的验收条件全部通过后必须停止开发并进入下一阶段。任何额外加固、通用化、兼容扩展或跨阶段验证
  都必须作为单独任务重新确认范围，不能继续挂在当前阶段。

## 生成简化原则

本节是 Nexa 所有代码生成能力的最高优先级约束。现有 Skill、设计文档、历史分支或实现与本节冲突时，
一律以本节为准；冲突内容只能作为历史证据，不得继续集成或作为新实现的基础。

生成器只负责以下最小闭包：

1. 解析并校验版本化输入契约；
2. 校验声明的生成目录位于 consumer repository 内，且不指向 `.git` 或其大小写别名；
3. 以声明的 generated 目录为唯一 replacement unit，直接清空并重建整个目录；
4. 失败时返回非零结果和稳定错误，不掩盖已经发生的文件变化。

生成失败允许保留部分变更。生成器不负责恢复工作区；使用方通过 `git diff` 审阅实际变化，通过
`git restore` 恢复，并在同一 Git worktree 中决定是否接受生成结果。用户代码已经由 Git 管理，
不得在生成链中重复实现一套 staging、恢复、合并或 ownership 系统。

生成路径只保留必要的正确性检查：

- 所有输出必须位于当前 repository 边界内；
- `.git` 及其大小写别名永远禁止写入；
- generated 目录与显式声明的 consumer-owned extensions、hooks、slots 和 actions 目录必须分离；
- 生成器只能清空并重建声明的 generated 目录；声明目录之外的所有文件都必须因不在写集内而保持不变；
- 禁止扫描、分类或推断人工代码和 stale ownership，也不提供 file-set、action-list 或逐文件 ownership 模式；
- 首次写入前只执行普通路径检查：拒绝 repository lexical escape、越出声明 generated scope 的 traversal、
  `.git` 大小写别名、输出范围重叠、generated/extensions 重叠、exact/case-fold path collision，以及路径
  component 中的 symlink；这些检查不得演变为 FD/inode identity 或跨时间缓存跟踪。

严禁为生成器增加以下机制。未来确需改变本边界时，必须先单独修改本节并完成治理评审，不能在实现中
增加例外：

- repository staging、scratch repository、generation sandbox 或仓库副本；
- 自动回滚、事务协议、两阶段发布、plan/check/write 状态机或恢复日志；
- ownership manifest、ownership digest、plan digest、冲突合并或 stale ownership 推断；
- 文件描述符身份链、缓存目录身份跟踪、私有构建缓存或 OS 级隔离；
- 自动 merge、diff3、`git apply`、Git staging、Git commit 或任何隐藏工作区变化的行为。

生成验收只围绕契约和输出行为：

1. 契约输入正确，非法输入在写入前失败；
2. 输出内容和目录符合预期；
3. 声明的 generated 目录被直接覆盖；
4. extensions 和其他人工代码不受影响；
5. 最小 generation consumer fixture 通过公开生成入口消费真实 typed contract，并产生预期源码；
6. fixture 预先提交期望生成物并从 clean tree 开始；第一次和第二次使用相同输入生成后 `git diff` 均为空，
   证明直接生成和重复生成不会引入额外变化；
7. 生成结果通过 format、compile 和与生成产物直接相关的定向 test。

DG1 的 E2E 到“真实 consumer 调用生成入口、得到预期源码并成功编译”为止。它明确不包含 Source Bundle
materialize/detach、Core 应用启动、HTTP/RPC health、IAM 或 session 流程、数据库、前端、容器、部署和
K8s；这些能力必须由各自后续阶段或 `example` 完整闭环验证，不能作为 DG1 的测试或合并门禁。

DG1 只建设满足上述验收的最小生成闭环。DG1 通过后立即进入 DG2，不得以“安全加固”、未来扩展、
通用 ownership 或事务完整性为由继续延长生成器建设。只有明确属于输入契约或输出正确性的代码才能
从历史实现中重新采用，并且必须在干净基线的独立 worktree 中重新证明。

## 事实源治理

- 同一事实必须归属于最接近其语法节点的权威源，放置优先级固定为：节点上的强类型 metadata、同一事实文件或 package 中的结构化声明、领域 owner 定义的强类型 relation document、只表达跨事实关系或服务拓扑的全局 catalog。更近层能够完整表达时，禁止另建远端配置或重复人工入口。
- Ent schema、type 和 field 事实属于 typed `SchemaMeta`、`FieldMeta` 与专用 `schema.Annotation`；Schema 级 CRUD 通过 annotation presence 和闭合 operations enum 显式选择，annotation 缺失即不生成 CRUD。label、desc、i18n、UIHint 和 source binding 同样属于对应 typed annotation，不使用 Go comment DSL 或 `map[string]any`。
- RPC、message 和 method 事实属于 Proto AST 与 custom options；HTTP route、type 和 field 事实属于 `.api` 正式 AST 与 typed metadata。GoDoc 可以说明代码，但不得成为机器消费的业务契约。
- Service Catalog 只拥有服务发现、服务拓扑和跨事实或跨服务 capability binding relation。Service Catalog v1 的 `CapabilityBinding` 仅包含 capability `id` 与 `apiVersion`；binding presence 只表达 service 到 capability contract 的关系，不保存 capability 本体或任何节点事实。前端专属交互事实属于对象 schema，部署实例属于部署事实，人工 gate 与审批属于其领域 review-decision 文件。
- Service Catalog v1 不提供通用 `configRef`、开放 extension 或 generic sidecar path，因为通用引用无法在语义上证明被引用内容遵守最近事实源所有权。真实关系横跨多个权威源或原生格式无法表达时，领域 owner 必须先定义封闭的 typed relation schema；relation document 只保存关系或决策，并为所有原事实携带规范的 canonical `SourceRef` 与 `Digest`。需要由 catalog 暴露的关系通过新的 catalog `apiVersion` 引入；不属于 catalog 的领域关系由 capability-specific typed binding contract 及其领域 loader 消费。两条路径都不得扩展 Service Catalog v1。
- Ent CRUD、label、desc、i18n、UIHint、source binding 和 Proto/API metadata 永远保留在各自最近权威源，不得复制到 Service Catalog 或 relation document。
- Artifact Manifest、IR、SDK model、frontend export、generated docs 和 read model 都是可重建 projection，不是人工事实源，也不得反向驱动权威源。
- 每个公共 contract 和生成链必须明确记录 `owner`、`authoring surface` 与 `derived projections`。发现 catalog、Go hardcode、comment、relation document 或 generated file 重复表达同一事实时，只保留最近的权威人工入口。
- 框架只提供公共 schema、typed annotation、parser、IR、generator 和 validator；业务 contract、业务 metadata 实例、关系决策、部署实例与生成产物均留在 consumer repository。

## 文档规则

- 仓库内文档只描述 Nexa 的最终架构、稳定契约、公开使用方式和持续有效的工程规则。
- 一次性实施步骤、仓库差异、临时状态、调研过程和执行记录不得提交到本仓库。
- 架构、插件和公共契约发生变化时，必须同步更新 `docs/README.md` 及对应分域文档。
- 文档不得把设计中的命令、package 或 schema 描述成已经通过验证的实现；实现状态以代码、测试、`nexactl inspect --json` 和 release 证据为准。

## 框架边界

- canonical Go module path 是 `github.com/nxnminieye/nexa`。
- Service Source Plugin 只提供标准源码。源码写入业务仓后归业务仓所有，可以自由修改、重构或解除来源追踪。
- Source Bundle manifest/tree 属于 provider publisher，materialized source 属于 consumer，provenance lock 只是可重建 projection；三者不得互相充当人工事实源。
- Source Plugin 不拥有 runtime config、deployment instance、health state 或服务生命周期；相关事实必须保留在 consumer 对应领域的最近事实源。
- 普通业务微服务不是插件；通过源码插件获得的服务也不具有运行时插件身份。
- Nexactl Build Plugin 只通过显式 Go import 和 constructor 在编译期组合。
- 禁止使用 Go `plugin`、动态 `.so`、运行时目录扫描、blank import、`init()` 全局注册或可变 package-global registry 组合插件。
- 公共 package 不得依赖产品私有 package；业务消费者不得依赖本仓库 `internal` package。
- `nexactl` 只处理代码仓和工程事实。数据库、远端系统和运行时状态变更属于 `nexa admin` 或业务私有 ops CLI。

## Consumer 采用与 Skill 路由

- AI 处理 Nexa framework 或 consumer 前，必须先读取目标仓 `AGENTS.md`，确认 consumer 固定的 Nexa module/ref，再用同版本 `nexactl skills sync --repo-root <absolute-repo> --json` 同步 Nexa Skills；随后先读取 `nexa-framework-router`，任务相关专项 Skill 按 router 结果选择，不要求无关专项同时加载。
- Framework skill 是 consumer-owned 的普通路由文本，不是命令授权文件，也不替代当前任务中的用户判断。Skill 版本无法确认或与 module/ref 不一致时，先报告漂移，不沿用旧 Skill 猜测当前能力。
- 完成 Skill 路由后，再运行 consumer 实际使用的 `nexactl inspect --json`。Inspection 用于发现当前二进制提供的命令、flag、owner 和副作用，并在执行前检查它们是否与计划一致；它不批准命令。
- 非内建命令由当前已审核的任务计划直接决定。计划必须写明准确命令、输入、affected files、允许的副作用、验证命令和回滚方式；不得从 capability presence、help、README、Make target 或历史执行记录自行扩大范围。
- Inspection 与计划不一致、输入不完整或副作用超出计划时停止执行并报告差异。Repository write 前重新确认 affected files 和副作用范围。
- 任何不直接服务 starter 或受控代码生成的新增框架能力，必须在设计、写计划或实现前交由用户审核。
- V0.1 未选择 runtime `nexa` CLI、可发布的 Python SDK/wheel、HTTP parity、quality 或 deployment 扩展。Reference `nexactl` 当前会编入 governance validation、Skill sync 与 `sdk-python-assets` 工程命令；这些命令存在不等于 Python SDK 已发布，也不要求 consumer 选择对应 runtime 能力。不得探测缺失 runtime binary、创建占位事实或把 Skill 同步解释为 capability 选择。
- 采用、升级和回滚的公开最终态入口见 `docs/adoption/`；设计来源只作解释性 provenance，Nexa 的 public contract、schema 和行为 gate 始终权威。

## 测试规则

- 单元测试必须验证业务逻辑、协议行为、生成产物行为、运行时状态转换或错误投影，不得作为源码文本门禁。
- 禁止新增只检查源码是否包含或不包含某段文本、某个函数位于哪个文件、文件行数上限、目录结构 allowlist、import 字符串、类型别名文本等“源码形态”测试。
- 生成产物测试必须解析、编译、执行产物或验证公开协议，不得仅搜索生成源码中的文本片段。
- 静态架构约束应使用 Go 编译器、类型系统、结构化 schema 校验或 AST/type-aware lint；lint 自身的测试必须验证输入与输出行为。
- 事实源门禁必须执行语义解析、引用解析、digest/drift 检查和生成行为验证；不得以源码字符串或目录形态断言替代契约行为。

## 验证

提交前至少运行与改动范围匹配的 Go 测试、生成验证和文档链接检查。所有完成、兼容和可发布声明必须以最新命令输出为依据。
