# Source Bundle 契约

Source Bundle 用于把版本化标准源码确定性地写入 consumer repository。它只处理源码发布、选择、物化、
差异、升级和来源关系，不参与服务运行。源码一旦物化就成为 consumer 的普通源码，可以自由修改；
provenance 只记录可重建的标准基线，不改变源码所有权。

## 所有权

| 对象 | Owner | Authoring surface | Derived projections |
| --- | --- | --- | --- |
| Bundle identity、文件清单、profile、依赖与验证 recipe | provider publisher | provider package 中的 typed `sourceplugin.Manifest` 或严格 Source Bundle document | canonical manifest、manifest digest、profile closure |
| 标准源码内容与文件 mode | provider publisher | provider package 的 embedded tree | immutable `sourceplugin.Tree`、tree digest、release cache entry |
| exact release、profile 与目标目录选择 | consumer | CLI invocation 或调用 `engine.NewSelection` 的 consumer composition | plan、check、provenance lock |
| materialized source | consumer | consumer repository 中的普通源码 | consumer build、test 和 release artifacts |
| provenance lock | 无人工 authoring surface；source engine 管理 consumer 中的生成实例 | exact release、profile closure、target 与 tracked baseline 的 canonical projection | status、diff、upgrade base 与 drift result |
| Build Plugin composition | consumer | consumer `nexactl` composition root 的显式 import 与 constructor | 编译后二进制中的 source capability 和七个命令 |
| runtime config、deployment instance、health state | consumer 对应领域 | consumer 的配置、部署与运行事实 | runtime render、deployment state 与 health read model |

Manifest、tree、release cache、lock、plan 和 command result 都不能反向成为 materialized source 的人工事实源。
业务源码修改只发生在 consumer 的源码节点；标准基线更新只通过新的不可变 release 进入三方比较。

## 对象关系

```text
provider package
  -> Provider { Manifest, Tree }
  -> exact release Ref
  -> one optional source Build Plugin adapter
  -> plan/check/write lifecycle
  -> materialized ordinary source + provenance lock
```

- `sourceplugin.Provider` 是一个不可变标准源码发布者，只暴露 `Manifest()` 与 `Tree()`。
- `plugins/nexactl/source` 是唯一的官方 source Build Plugin adapter。一个 adapter 可以组合零个、一个或多个
  provider，并统一提供一个 `source.bundle` capability 和七个命令。
- provider 自身不注册 CLI、不写 consumer repository，也不是运行时对象。
- materialized service 不再是 plugin。它以普通 Go/go-zero 服务的方式构建、测试、部署和启动。
- 未把 source adapter 传给 `host.New` 的 `nexactl` 仍保留 Host 的 `inspect` 与 `version`，但不存在 source
  capability 或 `source ...` 命令。

## Identity 与不可变发布

Source Bundle identity 由四个坐标组成：

| 字段 | 语义 |
| --- | --- |
| `providerId` | provider 的稳定 id |
| `modulePath` | 发布 provider 的 Go module path |
| `packagePath` | provider package 的完整 Go import path |
| `version` | 与 module path major 约束一致的 semantic version |

`release.Ref` 在四个坐标之外固定 `manifestDigest` 与 `treeDigest`。相同坐标只能解析到相同摘要；相同坐标
对应不同内容是 immutable release conflict，不能以缓存内容、注册顺序或本地文件覆盖。

一次 consumer 选择还包含 `profileId` 和 repository-relative `target`。`profileId` 选择 manifest 中的 named
profile closure；`target` 只决定 consumer 中的写入根，不进入 provider identity。provenance lock key 由
`providerId + target` 确定，并映射到 `.nexa/source/locks/` 下的 canonical lock path。

## Manifest、profile 与 tree

Source Bundle document 使用：

```text
apiVersion: nexa.dev/source-bundle/v1
kind: SourceBundle
```

Machine schema 由 `sourceplugin.Schema()` 返回。`sourceplugin.Parse` 对 JSON/YAML 执行严格解析，拒绝未知
字段、重复字段、尾随内容和不支持的 version/kind。Typed authoring 使用 `NewIdentity`、`NewManifest`、
`NewTree`、`LoadEmbeddedTree` 与 `NewProvider`；所有公开 accessor 返回不可变值或防御副本。

Manifest 只拥有以下事实：

- identity；
- 文件 path、size、digest 与 `0644`/`0755` mode；
- named profiles、profile dependencies 与 exact bundle requirements；
- `go-test`、`go-build` 验证 recipe。

Profile closure 以依赖优先顺序确定性展开，并对文件、bundle requirement 和验证 recipe 做封闭解析。
Bundle requirement 必须携带目标 provider 的完整 coordinates、profile 与两个摘要，不能使用浮动版本。

Tree 必须与 manifest 文件集合、mode、size 和 digest 完全一致。路径使用 portable relative path，拒绝绝对
路径、`.`/`..`、控制字符、非规范 Unicode、大小写折叠冲突、文件/目录前缀冲突和未声明文件。Tree limits
对文件数量、单文件大小和总字节数提供显式上界；tree digest 由排序后的文件事实和内容确定。

Source Bundle contract 不拥有端口、运行地址、环境变量、secret、runtime config、部署清单、健康检查或
health state。标准源码可以定义消费这些业务事实的普通代码，但事实实例仍必须放在 consumer 中最接近其
语法节点的 typed config、deployment contract 或 runtime state owner。

## Public Go API

| Package | Public boundary |
| --- | --- |
| `sourceplugin` | `Schema`、`Parse`、typed manifest/tree constructors、`Provider`、`SnapshotProvider` |
| `sourceplugin/release` | `Ref`、`FromProvider`、`ExactResolver`、`DirectoryCache` 与显式 limits |
| `sourceplugin/lock` | `Key`、`Schema`、`Parse`、`Derive`、`Verify`、`Snapshot` 与 `VerifiedLock` |
| `sourceplugin/engine` | `Selection`、requests/results、`Engine` 七项生命周期行为、merge/executor/toolchain interfaces |
| `plugins/nexactl/source` | `New(Options, ...Provider)`、`CapabilityID`、`CapabilityVersion` 与 Build Plugin adapter |

Consumer 只依赖这些公开 package，不依赖 Nexa `internal` package。Source adapter 的 cache、limits、merge
driver、executor 与 Go toolchain 通过 `Options` 显式注入；不存在 package-global repo root、隐式环境或
可变 registry。

## Resolver 与 cache

`release.ExactResolver` 只按完整 `Ref` 解析：

1. 优先匹配 constructor 注入的 provider snapshots；
2. 同坐标不同摘要立即返回 conflict；
3. 静态 provider 未命中时才读取显式注入的 `DirectoryCache`；
4. cache miss 返回 unavailable，不执行网络发现或浮动版本选择；
5. cache 命中后重新构造 provider 并验证完整 Ref。

Directory cache 是本地、内容固定、只增不改的 release store。读取和发布都受 manifest/tree/path limits
约束；目录遍历不跟随 symlink，拒绝非普通文件、mode 漂移、额外或缺失 entry、摘要不符和路径替换。
发布先写私有 staging，完成文件与目录同步后再原子发布；并发发布者必须验证既有 winner，而不是覆盖。
Reference `nexactl` 在组合时把操作系统标准用户 cache 下的稳定目录显式传入 Source adapter；隔离测试或
受控运行可通过绝对、规范的 `NEXA_SOURCE_CACHE` 覆盖该 operational path。进程级
HOME/temp/GOPATH/build/merge 工作目录清理不得删除 release store，后续 binary 才能从 provenance lock
解析未再静态链接的旧 release 并执行三方 upgrade。

## Repository state 与安全树

Engine 只在调用方显式给出的绝对 repository root 下工作。Selection target 和 lock 中所有 tracked path
均为 portable repository-relative path。仓库检查区分 `absent`、`regular`、`directory`、`symlink` 与
`other`，不把 symlink 当作目标文件跟随，也不跨出 target 或 `.nexa/source` 的 engine-owned control
paths。

Managed state 是闭合集合：

| State | 含义 |
| --- | --- |
| `not-managed` | 不存在对应 canonical provenance lock |
| `managed-clean` | consumer 文件状态与 lock 中的标准基线一致 |
| `managed-modified` | 存在 added、modified、deleted、mode-changed 或 type-changed delta |

`status` 投影状态与 delta；`diff` 投影基线到当前 consumer tree 的变化。两者只读，不清理遗留 staging，
也不推断历史 invocation 状态。

## 七命令

命令是否存在、实际 flags、input/output schema 和 side effect 必须由 consumer binary 的
`nexactl inspect --json` 发现。官方 source adapter 提供以下闭合集合：

| Command | Input | Side effect | Result |
| --- | --- | --- | --- |
| `source plan` | exact release、profile、target、repo root | `repository-read` | `SourcePlan` |
| `source materialize` | plan input，可选 expected plan digest | `repository-write` | `SourceResult` |
| `source status` | provider、target、repo root | `repository-read` | `SourceStatus` |
| `source diff` | provider、target、repo root | `repository-read` | `SourceDiff` |
| `source upgrade` | exact release、profile、target、repo root，可选 expected plan digest | `repository-write` | `SourceResult` |
| `source detach` | provider、target、repo root | `repository-write` | `SourceResult` |
| `source check` | exact release、profile、target、repo root | `repository-read` | `SourceCheck` |

`plan` 只计算 deterministic change/conflict set。`check` 同时返回当前 status 和目标 plan。
`materialize` 建立初始管理关系，并允许同一 selection 的幂等 noop；已经管理的 target 变更标准基线必须
使用 `upgrade`。调用方提供
`expected-plan-digest` 时，write 必须重新计算并精确匹配，避免 plan 与写入之间的状态漂移。
`detach` 只删除 exact provenance lock，保留所有 consumer source。

## 三方升级与冲突

升级固定使用：

```text
old base = provenance lock 指向的标准 tree
local    = consumer 当前 tree
new base = 目标 exact release tree
```

Engine 对每个 path 比较 file type、mode、size、digest 和必要的内容，产生确定排序的 add、replace、delete、
preserve 或 conflict。`local == old` 时接受 new base 的单边 add/delete、type、mode、binary 或内容变化；
`new == old` 时保留 local delta；`local == new` 时视为 converged。只有 local 与 new 都相对 old 变化且结果
不一致时，type/mode/binary 与 delete/modify 组合才成为结构化 conflict。文本双方修改通过显式
`MergeDriver` 执行 diff3；`NewGitMergeDriver` 只接受绝对、可执行的 Git 路径和受控 temp root，并使用
固定 argv。无法干净合并时不静默覆盖 consumer 修改，也不提供 force overwrite。

同一路径的 file 与 directory 类型演进在 plan 阶段返回 `ConflictType`。框架不为该演进建立额外恢复
状态；consumer 必须先明确调整 source layout，再生成新的 plan。

## 验证与无锁串行发布

Write 在 repository 内 engine-owned control path 中准备与 target 隔离的 preview tree，并按 profile closure
执行结构化验证。`GoToolchain` 固定
Go executable、working root 与闭合环境；`Executor` 只执行完整 `Execution`，不继承调用进程环境。
Go recipe 使用 `GOWORK=off`、`GOENV=off`、`GOPROXY=off`、`GOSUMDB=off`、`GOTOOLCHAIN=local`，并在
隔离 HOME 中先执行 `go telemetry off`；随后只在隔离 preview module 中允许 Go 更新临时 module graph。
验证失败属于 external error，source tree 与 ownership snapshot
保持原状；consumer 在 materialize 后仍自行执行最终 module tidy 与 readonly build/test。

同一 worktree 的 source write 由 agent/CLI 调度串行执行；并发 source writer 明确 unsupported。每次
invocation 只拥有自己的唯一同文件系统 staging。执行顺序为：

1. 在 staging 中构造完整 preview 与新的 ownership snapshot；
2. 在该 preview 上完成 profile validation；
3. 发布前重新验证 repository snapshot、old ownership snapshot 与 accepted plan digest；
4. 每个受控 path 在发布点再次核对 precondition，并用普通 create/rename/delete 发布；
5. 验证最终 target digest 后，最后原子发布新的 ownership snapshot；detach 只移除对应 snapshot；
6. best-effort 只清理本次 staging。

多文件 batch 不承诺 all-or-nothing；错误可能发生在部分文件已发布之后。此时不会根据旧记录自动恢复。
遗留 staging 可忽略或由人工显式清理；下一次从当前 repository 重新 plan。Ownership snapshot 只在最终
target digest 通过后发布，因此它是 source provenance 基线，不是互斥锁、恢复日志或执行授权。

## 错误投影

各 owner package 返回封闭 typed error：

- `sourceplugin.Error`：manifest、profile、tree 与 provider contract；
- `release.Error`：Ref、resolver 与 cache；
- `lock.Error`：key、canonical lock、derive 与 verify；
- `engine.Error`：request、state、plan、merge、validation 与 serial publish。既有稳定 error code/stage
  可以保留历史命名，但不表示仍存在 transaction lock 或 Recover。

Source adapter 将 owner error 投影到 CLI 的 `input`、`conflict`、`unavailable`、`external`、`canceled` 或
`internal` category，并保留稳定 code、stage、reason、JSON pointer 和安全的 source location。未知 error、
panic、外部工具原始错误、绝对路径和 source bytes 不进入 stdout envelope；stdout、stderr、operation id 与
exit code 遵守[CLI 机器协议](cli-machine-protocol.md)。

## 最近事实源规则

- Bundle 自身事实放在 provider manifest 与 tree，不另建全局 source catalog。
- Consumer 的 release/profile/target 选择放在调用 source lifecycle 的 typed invocation，不复制进 Service
  Catalog、runtime config 或 deployment facts。
- Runtime config、部署实例和 health facts 放在各自领域 owner；source lock 只记录来源与基线。
- Materialized source 是 consumer 的人工源码入口；lock、cache、plan、result 和 docs 都只是 projection。
- 只有事实真实横跨多个权威源且原格式无法表达时，才由对应领域定义 closed typed relation，并用 canonical
  `SourceRef` 与 `Digest` 指向原事实。

Service Source Plugin 的业务所有权与普通服务边界见
[Service Source Plugin](../plugins/service-source-plugin.md)，编译期组合规则见
[Nexactl Build Plugin](../plugins/nexactl-build-plugin.md)。
