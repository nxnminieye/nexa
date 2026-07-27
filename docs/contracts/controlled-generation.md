# 受控生成

Nexa 把 consumer-owned typed facts 投影为可编译的普通源码。Consumer 拥有输入事实、生成选择、生成源码、
扩展源码和最终发布；framework 拥有 typed fact contract、direct generation adapter、路径校验和稳定错误投影。

## 当前公开能力

任何自动化先对 consumer 实际二进制执行：

```bash
nexactl inspect --json
```

Official generation plugin 当前只提供两个 public command：

- `generation rpc generate`，capability 为 `generation.rpc`；
- `generation api generate`，capability 为 `generation.api`。

Reference CLI 没有 consumer `ProjectProvider`，因此只能展示命令 contract；执行时会稳定返回 provider
unavailable。Ent、CRUD、Service Manifest、plan/check/write、plan digest、ownership manifest 和 staging 不属于
当前 official generation plugin 的公开能力。仓库中存在历史 package 不代表它已接入 CLI 或支持面。

## 主链

```text
consumer typed facts
  -> strict parse and validation
  -> canonical Proto/API document
  -> validate generated/extensions scopes
  -> clear and recreate the declared generated scope
  -> run the consumer-selected tool in the consumer repository
  -> parse, format, compile, test and review Git diff
```

`ProjectProvider` 为选中 service 返回 typed RPC/API document、与 provider descriptor 一致的 delegated tool、
唯一 generated scope，以及显式的 consumer-owned extensions、hooks、slots 或 actions scopes。Provider 只定位和
组合事实，不复制 Proto/API 节点 metadata。

## Replace-tree

整个声明的 generated scope 是唯一 replacement unit。每次 generate 在启动 delegated tool 前清空并重建该
目录；不维护 file-set、action list、previous manifest、stale ownership 或逐文件 merge。

首次写入前拒绝：

- repository escape 和 traversal；
- `.git` 及其大小写别名；
- generated/extensions overlap 或 case-fold collision；
- 已存在路径 component 中的 symlink。

Extensions 和其他人工源码位于 generated scope 之外，因不在写集内保持不变。Generator 不扫描或推断人工
ownership。

## Delegated tool

Delegated tool 是 consumer 明确选择的受信任本地进程。Framework 将 canonical typed facts 写入 stdin，并在
consumer repository 中直接执行 version-pinned tool。Nexa 不提供 OS sandbox、repository staging、私有构建
cache 或自动 rollback。

路径或输入 contract 失败必须在清空 generated scope 前返回非零。Tool probe 或执行开始后的失败同样返回
非零，但已发生的删除和写入保留。使用方通过 `git diff` 审阅，通过 `git restore` 恢复，不由 generator 隐藏
或合并工作区变化。

## 验证

完成条件只围绕 contract 和输出：

1. typed Proto/API 输入由正式 parser 读取；
2. 输出位于声明 generated scope，stale tree 被完整替换；
3. extensions 和其他人工源码字节不变；
4. delegated tool 非零退出保留 partial change；
5. 预提交期望生成物的 clean fixture 第一次和第二次生成后 `git diff` 都为空；
6. generated Proto/API/Go 通过 parser、format、compile、unit test 和 external-consumer E2E。

Artifact/API/Service Manifest package 的独立数据结构见[生成清单](generated-manifests.md)。这些 package 的
存在不表示当前 direct generation command 会创建或消费 manifest。
