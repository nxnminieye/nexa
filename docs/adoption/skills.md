# Skill 分发与路由

Framework skill 是可审计的 AI 路由材料。它解释如何发现 public contract、组织审核和验证执行结果，但不复制 CLI command schema，不创造 capability，也不代替任务计划、测试或 release evidence。

## Skill asset

- Asset manifest 对每个 skill 文件记录 repository-relative path 与 SHA-256；zip member 顺序、mode 和时间戳确定，解包必须拒绝绝对路径和 traversal。
- Materialize 后的 skill 是 consumer-owned 普通文本，不依赖远端服务才能阅读。
- Provenance lock 只参与 status、diff 和 upgrade。它不在日常路由时远程取回 skill，也不授权任何命令。
- Detach 只删除来源管理关系，保留 materialized skill。后续修改由 consumer 审核和维护。
- Skill、binary、inspection apiVersion 或精确版本不兼容时 fail closed，不尝试用 help、README、Make target 或路径猜测恢复。

## Inspect-first 路由

每次处理 consumer 时先读取 consumer `AGENTS.md`，然后用以下命令读取当前实现信息：

```bash
nexactl inspect --json
nexactl version --json
```

`inspect` 是当前二进制 command、flag、schema、side effect、plugin 和 capability 的发现入口。`version` 报告 binary identity。两者都不批准命令；当前已审核的任务计划直接决定执行内容。

执行任务计划中的命令时按以下顺序完成：

1. strict decode inspection envelope，并要求受支持的 inspection `apiVersion`；
2. 读取已审核任务计划中的准确命令、输入、affected files、允许的副作用、验证和回滚步骤；
3. 用 inspection 核对 command path、owner、flags、schema、side effect 和 delegated tools；
4. repository write 前重新确认输入和 affected files，之后调用计划中的命令；
5. 任何缺失或漂移都返回结构化 gap 并停止执行。

Capability presence 只说明某个 owner 声明了兼容能力，不能替代任务计划或自行扩大执行范围。

## Materialize、detach 与 companion skill

- Materialize 从精确版本的 asset 建立 consumer-owned 文本与 provenance；同一选择可确定性重建。
- Consumer 可以编辑 materialized skill；status/diff 显示与来源基线的差异，upgrade 使用 old/local/new 三方语义，不能静默覆盖本地修改。
- Detach 后 skill 继续存在并可使用，但不再有来源 status/diff/upgrade 关系。
- Companion skill 可以随 asset 安装；未绑定已实现责任时，它不得选择 nonbuiltin command，也不得创建 requirements、evidence、frontend 或 deployment 事实。
- 外部工作流不是 Framework skill 的依赖。Skill 只执行 consumer 当前已审核的任务计划。
- 任何不直接服务 starter 或受控代码生成的新增框架能力，必须先交用户审核。

首个 RC 明确不选择 runtime `nexa` CLI、Python SDK、Python artifacts 和 HTTP parity。Skill 可以说明职责边界，但不得写入或探测这些命令路径，不得把缺席 binary、空目录或 placeholder 解释为已安装能力。
