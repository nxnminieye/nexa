---
name: nexa-development-workflow
description: Use when a Nexa framework or consumer change needs planning, isolated implementation, review, verification, integration, delivery judgment, or an external repository write.
---

# Nexa Development Workflow

## 核心原则

本 skill 自包含定义 Nexa 的 AI-native 交付契约，不依赖特定外部工作流引擎。计划、实现、review、验证和外部写入必须针对可识别的同一变更，时间压力和沉没成本不能替代证据或授权。

## 执行链

1. 读取当前 consumer 仓库 `AGENTS.md`，确认当前 Skills 由 consumer 固定版本的 `nexactl skills sync`
   生成，再用 `$nexa-framework-router` 选择 Nexa 专项，并确认仓库自己的集成、保护分支和验证规则。
2. 在执行任务计划中的 CLI 自动化前，运行当前 consumer 实际使用的 `nexactl inspect --json`，且只接受
   `result.apiVersion=nexa.dev/cli-inspection/v1`。`inspect`/`version` 用于前置发现，不批准后续命令。
3. 在修改前明确目标、非目标、约束、验收标准和风险；形成包含写集、依赖、验证命令与回滚点的可执行计划。
4. 在隔离 worktree 和非保护分支实施；不得在用户有未保存改动的工作区或默认分支直接开发。
5. 可执行行为变更先建立会失败的行为测试或协议验证，再写最小实现。纯说明性文档可跳过测试先行，但仍需验证链接、命令和契约陈述。
6. 按计划拆分任务。仅在写集不重叠、接口与验收已冻结、且没有强顺序依赖时并行；主 agent 负责边界、审核和集成。
7. 每个可独立验证的任务依次完成两阶段 review：先审规格符合性，再审代码质量；规格未通过不得进入质量 review。
8. 修复 review 意见后，重新执行受影响的 review 阶段。所有修改结束后，在最终候选 commit 或仓库规定的本地集成结果上运行 fresh verification。
9. 完成判断必须核对最新命令、退出状态、候选 commit、覆盖范围和未覆盖项。旧提交、修改前结果或“之前通过”不是最新证据。

## 任务计划与 CLI 使用

- 本 skill 只组织流程。需要执行的命令由当前已审核的任务计划直接决定，skill、inspection、help、文档、Make target、capability presence 和历史成功都不能替代该计划。
- 计划必须明确准确命令、输入、affected files、允许的副作用、验证命令和回滚方式。运行 `nexactl inspect --json` 是为了发现实际 command path、owner、flags、schema、side effect 和 delegated tools，并检查计划与当前实现是否漂移。
- Inspection 不批准命令。它与计划不一致、输入不完整或副作用超出计划时停止执行并报告差异；repository write 前重新确认输入与写集。
- Runtime `nexa`、可发布的 Python SDK/wheel、HTTP parity、quality 和 deployment 在 V0.1 均为
  not-selected；未发布的 Python SDK runtime 已随旧 `sdk/api` descriptor contract 删除。不探测缺失 binary，
  不创建对应事实或占位目录。
- 任何不直接服务 starter 或受控代码生成的新增框架能力，必须在设计、写计划或实现前交用户审核。

## 集成与外部写入

- 只使用仓库已定义的本地集成分支或流程；不存在时不要创建或臆造。此时准确表述为“功能分支已验证”，不得宣称已集成。
- 本 Skill 不要求 consumer 安装任何外部 workflow。外部写入前必须遵守 consumer `AGENTS.md`、托管平台
  保护规则和当前用户对具体动作、目标及范围的授权；任一项无法确认时停止，不用另一工具旁路。
- 外部写入包括 push、创建或合并变更请求、tag、release、部署以及第三方系统写入。授权按动作、目标和范围生效；一种动作的授权不自动覆盖其他动作。
- 当前用户指令或仓库策略已明确授权同一动作和目标时，不重复询问。若用户保留最终审核、目标不明确或授权范围不覆盖该动作，停在外部写入前并提交候选 commit、review 结论和 fresh verification 摘要。
- 授权不能豁免 review 或验证。只有两阶段 review 已通过、最终候选已 fresh verification、仓库规定的本地集成已满足时，才执行获准的最小外部动作；禁止顺带 force push、merge、tag、release 或 deploy。

## 压力判断

例如：功能已投入数小时且截止时间临近，但用户要求最终 push 前审核。继续完成 review 和最终候选验证，报告准确 commit 与证据，然后停在 push 前；不得用 Draft、临时分支或“先推再审”绕过 gate。

| 诱因 | 结论 |
| --- | --- |
| “之前已经验证过” | 最终修改后的候选仍需 fresh verification。 |
| “只推 Draft，不算交付” | push 本身就是外部写入。 |
| “已经投入很多时间” | 沉没成本不是 review、证据或授权。 |
| “用户以前说过可以操作” | 仅在动作、目标和范围仍明确匹配时沿用；不要机械重复询问，也不要扩大授权。 |

## 收尾记录

交付判断至少记录：候选 commit、实现分支或集成结果、两阶段 review 结论、最新验证命令与结果、未覆盖项、已执行或待审核的外部动作。未完成某一门禁时精确报告当前阶段，不把 branch commit、review、push 或 CI 单独称为完成。

## 常见错误

- 把某个外部工作流工具写成 Nexa 前置依赖，或发明仓库分支名、托管平台和环境规则。
- review 后修改代码，却沿用旧 review 或旧测试结果。
- 把用户的实现授权误当成所有远端写入、合并或部署授权。
- 因可选 gate、证据或部署能力不存在而阻止普通代码的本地验证。
