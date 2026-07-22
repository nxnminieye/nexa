# AGENTS.md

## 目的

本仓库是 `github.com/nxnminieye/nexa` 的公共实现与发布仓库。所有 agent 必须保持框架公共性、稳定接口和可验证行为，避免引入产品私有语义或只在单个业务仓成立的实现。

业务 contract、单一事实源和部署实例属于使用 Nexa 的 consumer repository。本仓库拥有版本化 schema、loader、parser、generator、validator、公共 package 和 CLI 行为，但不拥有任何业务事实实例。

## AI-native 原则

- AI-native 是本仓库的最高工程原则。
- CLI 是 agent、CI 和其他自动化消费者的可执行契约。公共命令优先提供非交互参数、稳定机器输出、结构化错误、operation id、明确 exit code 和能力自省。
- Skill 是 AI 的路由入口，负责定位 CLI 能力、组织审核和解释人工 gate；实际命令由当前已审核任务计划决定。Skill 不复制命令、schema 或业务事实，也不代替 CLI、测试和 release 证据。
- Framework skill 必须自包含，不 bundle、默认安装或依赖外部开发工作流。Consumer 可以自行选择工作流工具，但该选择不得成为 Nexa capability、构建、CI 或运行前提。
- Make target 和文档只能消费公开 CLI/schema，不得成为并行命令事实源。
- 当前二进制实际包含的命令、flag、schema、capability 和副作用以 `nexactl inspect --json` 为准。

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

- Framework skill 是 consumer-owned 的普通路由文本，不是命令授权文件，也不替代当前任务的人工审核。
- 处理 consumer 前先读取 consumer 的 `AGENTS.md`，再运行实际使用的 `nexactl inspect --json`。Inspection 用于发现当前二进制提供的命令、flag、owner 和副作用，并在执行前检查它们是否与计划一致；它不批准命令。
- 非内建命令由当前已审核的任务计划直接决定。计划必须写明准确命令、输入、affected files、允许的副作用、验证命令和回滚方式；不得从 capability presence、help、README、Make target 或历史执行记录自行扩大范围。
- Inspection 与计划不一致、输入不完整或副作用超出计划时停止执行并报告差异。Repository write 前重新确认 affected files 和副作用范围。
- 任何不直接服务 starter 或受控代码生成的新增框架能力，必须在设计、写计划或实现前交由用户审核。
- V0.1 未选择 runtime `nexa` CLI、Python SDK、Python artifacts、HTTP parity、governance/quality/deployment 扩展；不得探测缺失 binary、创建占位事实或把 companion skill 的安装解释为 capability 选择。
- 采用、升级和回滚的公开最终态入口见 `docs/adoption/`；设计来源只作解释性 provenance，Nexa 的 public contract、schema 和行为 gate 始终权威。

## 测试规则

- 单元测试必须验证业务逻辑、协议行为、生成产物行为、运行时状态转换或错误投影，不得作为源码文本门禁。
- 禁止新增只检查源码是否包含或不包含某段文本、某个函数位于哪个文件、文件行数上限、目录结构 allowlist、import 字符串、类型别名文本等“源码形态”测试。
- 生成产物测试必须解析、编译、执行产物或验证公开协议，不得仅搜索生成源码中的文本片段。
- 静态架构约束应使用 Go 编译器、类型系统、结构化 schema 校验或 AST/type-aware lint；lint 自身的测试必须验证输入与输出行为。
- 事实源门禁必须执行语义解析、引用解析、digest/drift 检查和生成行为验证；不得以源码字符串或目录形态断言替代契约行为。

## 验证

提交前至少运行与改动范围匹配的 Go 测试、生成验证和文档链接检查。所有完成、兼容和可发布声明必须以最新命令输出为依据。
