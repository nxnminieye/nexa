# AI 读取真实契约

AI 处理 Nexa 或 consumer 任务时，先读取目标仓 `AGENTS.md` 并确认所用 Nexa module/ref，再用同版本
`nexactl skills sync` 更新仓库内的 Nexa Skills。随后读取 `nexa-framework-router`，再按任务选择专项 Skill。
Router 是 Nexa 专项操作的必选入口，不能跳过。

Skill 不是命令清单，也不证明某项能力已经编入当前二进制。真正的执行依据来自：

- 当前 consumer 的 `AGENTS.md` 和任务约束；
- 当前版本的 public Go source/type 与 versioned schema；
- 实际使用的 `nexactl inspect --json` 与 `version --json`；
- 当前 consumer composition、测试和生成差异。

因此，文档提到一个命令、仓库包含一个 package，或另一个 consumer 曾经成功，都不能代替当前环境的能力
发现。Inspection 中缺失的命令视为不可用；schema 或版本无法匹配时应停止并报告差异，不能从名称、help
或旧输出猜测参数。

Nexa Skill 负责路由和上下文，不授权 repository write、部署或其他副作用，也不替代人工判断与验证证据。

继续阅读：[Skill 路由](../adoption/skills.md)和 [稳定边界](../developer/stable-surface.md)。
