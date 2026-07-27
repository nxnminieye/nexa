# Skill 路由

Nexa Skill 是 AI 开发或采用 Nexa 的必选入口。它负责把任务路由到正确 owner、公共契约和验证入口；它不
创建 capability、不批准副作用，也不替代源码、schema、CLI inspection 或测试结果。

## 必选顺序

1. 读取 consumer 的 `AGENTS.md` 和当前任务约束；
2. 确认 Nexa module 的精确版本或当前源码 commit；
3. 对包含同步命令的版本运行 `nexactl version --json`，核对 CLI 报告的真实 Nexa module 版本；
4. 使用同版本 `nexactl skills sync --repo-root <absolute-repo> --json` 同步 Nexa Skills；
5. 在任何 Nexa 专项操作前读取同步后的 `nexa-framework-router`；
6. 由 router 按任务选择最窄专项，跨领域时才组合多个专项；
7. 对 consumer 实际二进制执行 `nexactl inspect --json`；
8. 用 public source/type、schema、inspection 与测试核对计划、输入、affected files 和验证；
9. 写入前再次确认目标、ownership 和副作用范围，执行后保留当前结果证据。

当前专项包括：

| 任务 | 专项 Skill |
| --- | --- |
| facts、typed document、direct generation、generated/extensions scopes | `nexa-controlled-generation` |
| CLI、machine envelope、error、exit code、stdout/stderr | `nexa-ai-first-cli` |
| 计划、隔离实现、review、验证与外部写入边界 | `nexa-development-workflow` |

不要求每次加载全部专项，但禁止绕过 router 直接从旧文档、命令名称或历史输出推断执行方式。

## Skill 的权限边界

Skill 中出现的命令名称只是路由上下文。Skill 和 inspection 都不授权 repository write、push、发布、部署或
第三方系统变更；授权仍来自当前任务和 consumer 规则。命令缺失、版本不兼容、输入不完整或副作用越界时
必须停止，不能用 help、README、路径猜测或旧版本输出恢复执行。

Nexa Skills 自包含，不依赖任何外部开发工作流。Consumer 可以自行选择通用工作流工具，但该选择不是 Nexa
采用、构建、CI 或运行的前提，也不能替代 Nexa Skill 路由。

## 同版本与同步

Nexa Skill 的版本化资产位于 module 的 `skills/`，与源码一起编入同版本 `nexactl`。同步命令把这些资产写到
consumer 的 `.codex/skills/nexa-*`：每次完整收敛到当前 embedded set，替换当前目录、删除已经退役的
`nexa-*` 目录，同时保留 consumer 自己维护的非 `nexa-*` Skill。该投影不使用网络下载、Git tag 解析、
manifest、lock 或历史 merge。

同步在写入前验证全部待替换或删除的 Nexa Skill 路径。同步失败意味着目标可能仍是旧版本，或在普通文件
写入失败时只完成了部分更新；不得继续使用其中的 Nexa Skills。修复错误后重新运行同版本同步并确认成功。

已发布版本的 module、docs、CLI 和 Skills 必须来自同一个 tag；未发布源码工作则使用同一个 commit。当前
分支文档不能解释较旧 tag，另一版本的本地 Skill 也不能操作当前 module。同步后如需审查变化，直接使用
consumer 自己的 Git diff；平台不替用户决定是否接受版本变化。

`v0.1.0-alpha.1` 早于 `skills sync`，不能从当前文档反推该 tag 已有此命令。后续包含该命令的版本通过
`version --json` 报告 `go run ...@version` 实际解析到的 Nexa module 版本。
