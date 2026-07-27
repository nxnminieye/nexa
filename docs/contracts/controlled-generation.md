# 受控生成

Nexa 把 consumer-owned typed facts 投影为可编译的普通源码。Consumer 拥有输入事实、生成选择、生成源码、
扩展源码和最终发布；framework 拥有 typed fact contract、direct generation adapter、路径校验和稳定错误投影。

## 当前公开能力

任何自动化先对 consumer 实际二进制执行：

```bash
nexactl inspect --json
```

Official generation plugin 当前提供三个 public command：

- `generation rpc generate`，capability 为 `generation.rpc`；
- `generation api generate`，capability 为 `generation.api`；
- `generation frontend generate`，capability 为 `generation.frontend`。

Reference CLI 没有 consumer `ProjectProvider`，因此只能展示命令 contract；执行时会稳定返回 provider
unavailable。Ent、CRUD、Service Manifest、plan/check/write、plan digest、ownership manifest 和 staging 不属于
当前 official generation plugin 的公开能力。仓库中存在历史 package 不代表它已接入 CLI 或支持面。

## 主链

```text
consumer typed Proto/API/PageSpec facts
  -> strict parse and validation
  -> canonical Proto/API document or FrontendIR
  -> validate generated/extensions/user-logic paths
  -> clear and recreate the declared generated scope
  -> run the consumer-selected tool in the consumer repository
  -> RPC/API only: create, skip, or explicitly overwrite declared user logic
  -> parse, format, compile, test and review Git diff
```

`ProjectProvider` 为选中 service 返回 typed RPC/API/FrontendIR document、与 provider descriptor 一致的 delegated tool、
唯一 generated scope、显式的 consumer-owned extensions、hooks、slots 或 actions scopes，以及可选的准确
RPC/API user-logic 初始文件。Frontend target 还必须提供 exact frontend source lock digest。Provider 只定位和
组合事实，不复制 Proto/API 节点 metadata。PageSpec、FrontendIR 与 renderer request 的完整语义见
[前端生成契约](frontend-generation.md)。

## Replace-tree

整个声明的 generated scope 是唯一 replacement unit。每次 generate 在启动 delegated tool 前清空并重建该
目录；不维护 file-set、action list、previous manifest、stale ownership 或逐文件 merge。声明的 RPC/API
user-logic 文件是 create-once 输出：缺失时创建，已存在时默认跳过且字节不变，只有显式
`--overwrite-logic` 才覆盖准确目标。Frontend command 不接受该 flag，也不创建 user logic。

首次写入前拒绝：

- repository escape 和 traversal；
- `.git` 及其大小写别名；
- generated/extensions overlap 或 case-fold collision；
- user-logic 与 generated/extensions overlap 或 user-logic exact/case-fold collision；
- 已存在路径 component 中的 symlink。

Extensions 和未声明人工源码位于 generated scope 之外，因不在写集内保持不变。Generator 不扫描或推断人工
ownership；overwrite 也不会扩展到声明目标以外。

## Delegated tool

Delegated tool 是 consumer 明确选择的受信任本地进程。Framework 将 canonical typed facts 或
`FrontendRenderRequest` 写入 stdin，并在
consumer repository 中直接执行 version-pinned tool，同时通过
`--generated-scope <validated-relative-scope>` 传入唯一输出根。Nexa 不提供 OS sandbox、repository staging、
私有构建 cache 或自动 rollback。

路径或输入 contract 失败必须在清空 generated scope 前返回非零。Tool probe 或执行开始后的失败同样返回
非零，但已发生的删除和写入保留。使用方通过 `git diff` 审阅，通过 `git restore` 恢复，不由 generator 隐藏
或合并工作区变化。

## 验证

完成条件只围绕 contract 和输出：

1. typed Proto/API/PageSpec 输入由正式 parser 读取；
2. 输出位于声明 generated scope，stale tree 被完整替换；
3. extensions 和其他人工源码字节不变；
4. 缺失 user-logic 被创建，已有 user-logic 默认字节不变，显式 overwrite 只覆盖声明目标；
5. delegated tool 非零退出保留 partial change；
6. 预提交期望生成物的 clean fixture 第一次和第二次生成后 `git diff` 都为空；
7. 空 FrontendIR 仍替换输出树并调用 renderer，成功后输出根存在且为空；
8. generated Proto/API/Go/TypeScript/Vue 通过 parser、format、compile/typecheck、unit test 和 external-consumer E2E。

Artifact/API/Service Manifest package 的独立数据结构见[生成清单](generated-manifests.md)。这些 package 的
存在不表示当前 direct generation command 会创建或消费 manifest。
