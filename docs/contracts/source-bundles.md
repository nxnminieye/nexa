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

Go validation 在 staging 中使用独立的 consumer module 视图。若 target 自带根 `go.mod`，它必须是单一、
自包含的 module；否则 engine 只复制 consumer 根 `go.mod` 和可选 `go.sum` 到 staging repository root，
不复制其他 consumer 源码，也不执行 `go.work use`。Provider profile 声明的 Go module requirement 必须在
该 `go.mod` 中存在精确版本的 `require` directive；`indirect` 可以接受，但这只验证声明本身，不声称最终
MVS selection 被固定。

Staging 会删除 consumer `go.mod` 中的全部 `replace`。只有 `go.work` 中与当前 selected provider module
匹配、带精确旧版本且指向无 symlink 的本地 module root 的单条 version-specific `replace`，才会在校验前
投影为 absolute local replace；该旧版本必须对齐 consumer `go.mod` 的 module requirement，而不是
SourceBundle release version。wildcard、竞争项、版本不一致、module path 不一致和 consumer `go.mod`
直接替换 selected provider 都 fail closed。其他 `use` 和 `replace` 不进入 staging。Go 校验继续使用
`GOWORK=off`、`GOPROXY=off`、caller 显式提供的 `GOMODCACHE`，以及隔离的 HOME、TMPDIR、GOPATH 和
GOCACHE；未预热的外部依赖会以校验失败返回，不会访问网络。

只有 target 不自带根 `go.mod`、采用 consumer root module context 时，engine 才在 preview staging 建立后、
Go invocation 前记录 consumer 根 `go.mod`、`go.sum`、`go.work` 的 absent/present、类型和精确 bytes，并在
任何 target 或 Source Lock 写入前复核。自包含 target 不读取 consumer root module metadata。校验期间只允许
staging 副本被 Go 更新；metadata 漂移返回 conflict，真实 consumer metadata 在成功和失败路径都不由 engine
修改，staging metadata 也永不发布。

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
