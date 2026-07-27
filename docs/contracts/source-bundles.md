# Source Bundle

Source Bundle 发布可复现的标准源码。Provider 拥有 immutable manifest/tree；consumer 选择 release/profile
并物化后，目标源码归 consumer。Source 工具不参与 materialized service 的 runtime lifecycle。

## 对象

| 对象 | Owner | 用途 |
| --- | --- | --- |
| Manifest、profile、validation recipe | Provider publisher | 描述 closed source release |
| Immutable source tree | Provider publisher | 提供与 manifest digest 一致的 bytes/mode |
| Exact release resolver/cache | Source composition | 解析并缓存明确版本，不扫描远端 registry |
| Source Lock | Source engine 生成，consumer 持有 | 记录 selection、target 和 tracked baseline |
| Materialized source | Consumer | 普通源码、native facts、业务修改和发布 |

Manifest、tree、lock 和 materialized source 不能互相替代。

## Provider 与 release

`sourceplugin.Provider` 只暴露 immutable `Manifest()` 与 `Tree()`。Manifest 使用 closed provider identity、
module/package path、semantic version、file inventory、profiles、requirements 和 validation recipe；tree 中的
每个 path、mode、size 和 digest 必须与 manifest 匹配。

Exact resolver 只接受明确编入 composition 的 Provider release。Directory cache 按 exact identity/digest
保存验证后的 tree；缺失版本、digest mismatch 或无效 cache entry 保留真实错误，不 fallback 到相似目录或
host source tree。

## Selection 与 Source Lock

一次 selection 包含 provider、version、profile、target、manifest digest 和 tree digest。Profile closure
按依赖顺序闭合并拒绝 cycle、重复冲突和非法 requirement。Source Lock 的 key 由 provider + target 决定，
记录 exact selection 与已发布 standard baseline。

Lock 是可重建 provenance projection，不是文件 ownership 权威，也不阻止 consumer 修改源码。它与 CRUD
Proto compatibility lock、进程锁和事务日志是不同对象。

## CLI lifecycle

Source adapter 向实际二进制提供一个 `source.bundle` capability。当前 command family 为：

- `source plan` / `check`：只读计算候选或验证 selection；
- `source materialize`：首次发布选定 tree；
- `source status` / `diff`：比较 lock baseline 与当前 consumer source；
- `source upgrade`：计算并发布 old/local/new 三方结果；
- `source detach`：删除 provenance relationship，保留源码。

精确 flag、schema、release 和 side effect 必须从当前 `nexactl inspect --json` 读取，本文不复制清单。

## Materialize 与 publish

Plan 先检查 local collision、unsafe path、selection 和 target。完整候选在 invocation-local staging 中通过
manifest validation recipe，再在每个发布点检查 current file 未漂移并按文件写入。Source generation 在同一
worktree 由调用方串行执行，不提供跨进程 transaction lock、WAL 或 Recover。

失败报告真实阶段和原因。若发布中途失败，已经写入的普通文件保持当前状态；下一次结合当前 files 和 lock
重新 plan，不回放旧过程。

## Upgrade

Upgrade 对每个 tracked path 比较：

```text
old provider baseline + consumer local source + new provider baseline
```

只有 provider changed 而 local unchanged 时可采用 new；只有 local changed 时保留 local；双方相同变化采用
该结果；不同变化进入 merge/conflict。Conflict 不由目录 allowlist 或文本猜测自动批准。Clean result 仍需
通过新 manifest recipe、当前 binary inspection、直接 generation、重复生成无 diff 和 consumer build/test。

## Detach 与 ownership

Detach 成功后，Source Lock 被移除，materialized source 和 consumer 修改保留。Consumer 可以删除 source
adapter、Provider 和 cache；native Ent/Proto/`.api` facts、generated output、build 和 runtime 不依赖这些工具。

标准服务移交和 generation 衔接见[标准服务 starter](../starters/standard-services.md)。

## 路径与安全

Manifest/tree/target/lock path 必须是 canonical repository-relative path，拒绝 absolute path、traversal、
symlink escape、control root、case-fold collision、duplicate file 和 unsupported mode。解包、cache 和 staging
只操作 invocation 已创建或明确选择的 root，不扫描或清理 host 其他 cache。

Source error 通过 typed class/stage/reason 投影到 CLI machine envelope，不泄露任意文件内容或不受控外部
错误文本。
