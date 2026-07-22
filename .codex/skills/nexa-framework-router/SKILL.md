---
name: nexa-framework-router
description: Use when Nexa framework or consumer work needs ownership classification, compiled capability discovery, or routing to the narrowest framework specialist before implementation.
---

# Nexa Framework Router

## 核心原则

先确认项目规则、真实能力和事实所有权，再选择专项。Router 只路由，不复制命令树、schema 或尚未实现的能力。

## 路由步骤

1. 首先读取当前 consumer 仓库根 `AGENTS.md`，再按其索引读取相关最终态文档。
2. 对 CLI 或自动化能力，执行当前 consumer 选择、实际编译并使用的 `nexactl inspect --json`。只支持
   `result.apiVersion=nexa.dev/cli-inspection/v1`；其他版本或无法读取的 inspection 都是结构化 gap，不得继续选择命令。
3. 分类事实：
   - framework public contract：公共 package、versioned schema、CLI 协议和生成内核；
   - consumer business facts：业务 contract、配置、生成产物、人工决策、证据和部署实例；
   - product-private facts：品牌、域名、产品 profile、客户规则和私有默认值。
4. 选择最窄专项；任务跨边界时可组合，但不得把产品私有事实带入公共框架。

| 任务信号 | 专项 |
| --- | --- |
| CLI、SDK、机器 envelope、错误或 exit code | `$nexa-ai-first-cli` |
| 事实源、IR、生成物、manifest 或 ownership | `$nexa-controlled-generation` |
| 计划、隔离开发、review、验证或外部写入 | `$nexa-development-workflow` |
| CRUD/Kafka/logging/gRPC access/OTel runtime package 或 Minimum Runtime 可选链接 | 读取 `docs/contracts/runtime-packages.md` 后按对应 public package 行为与 external consumer gate 执行 |

例如，新增 RPC proxy endpoint 属于 consumer 业务事实与受控生成，路由到 `$nexa-controlled-generation`；命令细节仍以自省结果为准。

## 命令发现与计划执行

- 当前已审核的任务计划直接决定需要执行的命令。Router 和其他 skill 只帮助定位专项，不携带命令批准清单。
- `nexactl inspect --json` 用于发现实际 command path、owner、flags、schema、side effect 和 delegated tools，并检查它们是否与计划一致；inspection 不批准命令，也不能代替任务计划。
- 计划必须明确准确命令、输入、affected files、允许的副作用、验证和回滚。Inspection 与计划不一致、输入不完整或副作用越界时停止执行并报告差异，不猜测命令，不手改生成物。
- Repository write 前重新确认计划、输入和 affected files。任何不直接服务 starter 或受控代码生成的新增框架能力，都必须先交用户审核。

V0.1 明确 not-selected：runtime `nexa`、Python SDK/artifact、HTTP parity、governance、quality 和 deployment。
不得探测缺失的 runtime/SDK binary，不得为这些面创建事实文件、占位目录或空插件，也不得手改生成物。

## 缺失能力

未出现在自省结果中的能力视为未编入。Inspection 版本不支持、计划命令不存在、输入不完整或副作用越界时报告结构化 gap。以上情况均不臆造命令，不手改生成物，
也不创建空插件、空目录或占位事实。requirements/work/UserOperation、human gate、TestSpec/evidence、deployment
instance 和 frontend 全部缺失仍是合法的 Minimum Runtime。

Runtime 能力不通过 CLI 自省声明运行时实例。判断是否选择某个 runtime package，必须查看 consumer 的
Go import、constructor composition 与编译依赖结果；根 `go.mod` 中存在 requirement 不等于最终二进制已链接或构造该能力。

## 常见错误

- 用 `--help`、文档或 Make target 代替结构化自省。
- 把框架可读取的业务事实误判为框架所有。
- 因可选能力缺失而阻止后端构建或要求占位模块。
