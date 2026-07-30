---
name: nexa-ai-first-cli
description: Use when implementing or reviewing Nexa runtime/admin or repository CLI commands, SDK-backed automation, machine JSON, error projection, operation IDs, exit codes, or stdout/stderr behavior.
---

# Nexa AI-First CLI

## 核心原则

CLI 是可执行机器契约。先发现实际能力，再复用公共 SDK 和 `cli/protocol`；不要在 skill、命令包装层或业务场景中发明第二套协议。

## 实施顺序

1. 读取当前 consumer 仓库根 `AGENTS.md` 和相关最终态契约，再运行 consumer 选择并实际编译的
   `nexactl inspect --json`。只支持 `result.apiVersion=nexa.dev/cli-inspection/v1`，不得用 `--help` 替代自省。
2. 当前已审核的任务计划直接决定需要调用的命令。Inspection 用于发现该命令实际存在的 path、owner、flag、输入输出 schema 和副作用，并检查实现漂移；skill 和 inspection 都不批准命令。
3. 计划必须明确命令、输入、affected files、允许的副作用、验证和回滚。Inspection 与计划不一致、输入不完整或副作用越界时，报告结构化 contract gap 并停止执行，不猜测 command/flag/schema，也不创建占位实现。
4. Automation command 必须显式传入 inspection 声明的必要 flag、文件或 stdin；不得提示、确认、打开浏览器或依赖 TTY。
5. HTTP runtime command 未来必须通过已公开的 typed SDK/transport 调用服务；CLI 只解析参数、调用 SDK、投影结果。当前 V0.1 未选择 runtime `nexa` 与 HTTP parity，不探测缺失 runtime binary，也不以临时 HTTP 实现补位。

## 任务计划与命令检查

- 任务计划必须由人审核并列出准确命令、参数来源、输入文件、affected files、允许的副作用、预期输出、验证命令和回滚步骤。
- 执行前使用 inspection 核对计划中的 command path、owner、flags、schema、side effect 和 delegated tools。Inspection 只提供当前实现信息，不作为批准记录，也不根据 capability presence 自动选择命令。
- Repository write 前重新确认计划仍适用、输入未漂移且写集仍在 affected files 内。发现差异时停止并更新计划后重新审核。
- 新的 CLI 能力如果不直接服务 starter 或受控代码生成，必须先交用户审核，再进入设计和实现。

Runtime `nexa`、可发布的 Python SDK/wheel、HTTP parity、quality 和 deployment 在 V0.1 均为 not-selected。
未发布的 Python SDK runtime 已随旧 `sdk/api` descriptor contract 删除。不得探测缺失 binary，不得创建事实文件、占位
目录或空插件，不得手改 generated artifact。

## 机器协议

- 使用 `nexa.dev/cli-envelope/v1`：顶层只有 `apiVersion`、`ok`、`operationId`，以及互斥的 `result` 或 `error`。
- validation、permission、transport 与 business failure 是 failure kind，不是 `protocol.Category` 名。任何 literal error JSON/schema 或 failure 到 category/exit 的映射都必须读取实际 `cli/protocol` 与已实现 typed SDK；无法读取时只报告 contract gap 并列出所需协议，绝不 sketch partial error object。用稳定 `code`、`domain`、`details` 保留区别，只通过既有 `protocol.Category` 和 `protocol.ExitStatus` 投影；禁止伪造 mapping table、category、顶层字段或 exit taxonomy。
- `operationId` 是 Host 为本地 CLI invocation 生成的关联 id，不接受调用方覆盖。server request id 属于单次远端请求，idempotency key 属于可重试变更的 API 输入；只按 command/SDK schema 承载，三者不得互相代替。
- automation 显式使用 `--json`。普通成功和失败都只在 stdout 输出一个完整 envelope；stderr 仅承载稳定 diagnostic，不重复结果。调用方同时检查 envelope 与 process exit code，不解析 `message`。

## 二进制边界

| 对象 | 职责 |
| --- | --- |
| `nexa` runtime/admin CLI + SDK | HTTP、数据库、远端系统和运行时状态操作 |
| `nexactl` Build Plugin | repository 内 `none`、`repository-read`、`repository-write` 工程操作 |

`nexactl skills sync` 是 repository-write 工程命令：它只把当前二进制内嵌的 Nexa Skills 同步到目标仓，
不下载网络内容、不解释 Git tag，也不授权后续写入。

不得把远端调用或运行时状态变更包装成 `nexactl` repository command。

## 验证

先重新读取 consumer `AGENTS.md`、已审核任务计划与实际 inspection v1，核对准确命令、输入、affected files 和允许的副作用。对计划内能力，用真实 subprocess 执行编译后的 CLI；只有未来显式选择 HTTP
runtime 后，才用 fake HTTP server 或公共 transport fixture 覆盖服务端行为。解析公开 envelope，核对 exit、
stdout/stderr、operation id、request id 与 idempotency 行为。测试公共行为和错误投影，不检查源码文本、函数
位置或 import 字符串。

## 常见错误

- 看到业务需求就先命名 command/flag，而未检查实际自省。
- 把 request id 或 idempotency key 添加为 envelope 顶层字段。
- 按 HTTP status 临时发明 category/exit，或让 CLI 重写服务端业务判断。
