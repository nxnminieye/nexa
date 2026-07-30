---
name: nexa-controlled-generation
description: Use when modifying or reviewing Nexa typed generation facts, direct RPC/API/frontend generation, FrontendIR, generated scopes, extension scopes, or replacement behavior.
---

# Nexa Controlled Generation

## 核心原则

consumer repository 拥有人工业务事实、生成源码和扩展源码；框架拥有 typed fact contract、严格校验、
direct generation adapter 和 replace-tree 路径约束。生成物是可重建投影，不是人工事实源。

业务事实遵守“最接近事实源优先”：RPC、message 和 method 事实来自 Proto AST 与 typed options；HTTP route、
type 和 field 事实来自 `.api` AST 与 typed metadata。不得从 generated artifact、inspection、文档或全局 catalog
反推并覆盖这些事实。

## 发现与计划

先读取当前仓库 `AGENTS.md` 和已审核任务计划，再运行当前 consumer 实际使用的：

```bash
nexactl inspect --json
```

只接受 `result.apiVersion=nexa.dev/cli-inspection/v1`。Inspection 用于核对 command path、owner、flags、
side effect 和 delegated tool，不批准命令。计划必须明确准确命令、输入、affected files、允许副作用、验证和
Git 恢复方式。Inspection 与计划不一致、输入不完整或副作用越界时停止，不猜测命令、不创建占位事实、
不手改生成物。

官方 generation plugin 提供三个 public command 和三个 capability：

- `generation rpc generate` 对应 `generation.rpc`；
- `generation api generate` 对应 `generation.api`；
- `generation frontend generate` 对应 `generation.frontend`。

Ent、CRUD、service manifest、plan/check/write、plan digest、ownership/staging/sandbox/transaction 都不属于
该 plugin 的公开生成面。不存在的能力不得通过历史命令、Make target 或文档补回。

## ProjectProvider

consumer 的 `ProjectProvider` 返回 `ServiceProject`。每个被选择的 RPC/API/frontend target 必须显式提供：

- 已解析的 typed Proto、API document 或 canonical FrontendIR；
- 与 provider descriptor 完全一致的 delegated tool identity；
- 唯一 generated scope；
- consumer-owned extensions、hooks、slots 或 actions scopes。
- RPC/API 可选的准确 user-logic 初始文件；缺失时创建一次，已存在时默认跳过；
- frontend target 的 exact frontend source lock digest。

Provider 只定位和组合事实，不复制节点 metadata。delegated tool 是 consumer 明确选择的受信任本地进程，
直接在 consumer repository 写入；Nexa 不提供 OS sandbox，也不推导 tool 的业务写集。

## Replace-Tree

每次 generate 在工具执行前清空并重建整个声明 generated scope。没有 file-set、action list、stale ownership
扫描或历史 manifest。声明的 RPC/API user-logic 文件缺失时创建、已存在时默认跳过且字节不变；只有公开的
`--overwrite-logic` 参数为 true 时覆盖这些准确目标。Frontend command 不接受 overwrite flag，也不创建
user logic。其他 generated scope 之外的内容因不在写集内保持不变。

首次写入前必须完成普通路径检查：

- repository lexical boundary 和 traversal；
- `.git` 及其大小写别名；
- generated/extensions overlap；
- user-logic 与 generated/extensions overlap，以及 user-logic exact/case-fold collision；
- exact 与 case-fold scope collision；
- 已存在路径 component 中的 symlink。

路径校验失败必须在清空前返回非零。工具启动后失败同样返回非零，但已发生的删除或写入保留；生成器不回滚。
使用方通过 `git diff` 审阅，通过 `git restore` 恢复。

## 验证

验证必须覆盖：

1. typed Proto/API/PageSpec 输入可由正式 parser 读取；
2. 非法路径在首次写入前失败；
3. stale generated tree 被完整替换；
4. extensions 和其他人工源码字节不变；
5. 缺失 user-logic 被创建，已有 user-logic 默认字节不变，显式 overwrite 只覆盖声明目标；
6. delegated tool 失败返回非零并保留 partial change；
7. fixture 预提交期望生成物，从 clean Git tree 开始，第一次和第二次相同生成后 `git diff` 均为空；
8. 空 PageSpec 集仍调用 renderer，成功后 generated scope 存在且为空；
9. 生成 Proto/API/Go/TypeScript/Vue 通过 parser、format、compile/typecheck、unit 和真实 external-consumer E2E。

禁止用源码字符串、文件位置、目录 allowlist 或行数门禁代替公开协议和生成行为验证。

## 常见错误

- 把 generated 文件或 inspection 当人工业务事实。
- 为了 stale 清理重新引入 ownership manifest 或 transaction。
- 在工具失败后自动恢复 generated tree，掩盖真实 Git diff。
- 因历史 Ent/CRUD/service-manifest 命令存在过而推断当前 plugin 仍提供它们。
