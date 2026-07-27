# 标准服务 Starter

Nexa 可以通过 Service Source Provider 发布标准服务源码。Starter 的目标是给 consumer 一份可构建、可测试、
可以继续生成和修改的普通工程起点，不是创建受框架控制的运行时服务。

## 对象与 ownership

| 对象 | Owner | 生命周期 |
| --- | --- | --- |
| Source manifest、profile、immutable tree | Provider publisher | 随 source release 发布 |
| Source adapter 与 release cache | 工程工具 composition | 只在 plan/materialize/upgrade 时使用 |
| Materialized source 与 native facts | Consumer | 物化后由业务修改、构建和发布 |
| Provenance lock | Source engine 生成，consumer 持有 | 支持 status、diff、upgrade 和 detach |
| Runtime config、migration apply、deployment | Consumer 对应领域 | 不进入 Source Provider |

Materialized service 使用普通 Go/go-zero 分层。它不实现 plugin start/stop hook，不在运行时读取 Provider、
release cache 或 source lock。

## 选择与物化

先对 consumer 实际编译的 `nexactl` 执行：

```bash
nexactl inspect --json
```

只有 inspection 中存在 `source.bundle` 和 source commands 时，才可以继续。可选 release、profile、manifest
digest 和 tree digest 从各 source command 的 input schema extension 读取，不从本文复制。

执行顺序是：

1. `source plan` 只读计算目标变化和冲突；
2. 审核 selection、target、digests、plan digest 和 affected files；
3. `source materialize` 使用同一选择写入；
4. 在 materialized source 上运行其 manifest 声明的验证 recipe；
5. 对当前 binary 明确支持的 typed facts 运行所选 generation；
6. 用 consumer 自己的 build/test 验证最终工程。

当前 alpha 的 reference composition 包含可选 Core source provider。它用于证明 source-to-consumer 闭包，
不要求所有 consumer 采用 Core，也不代表目录中其他 Provider 已编入某个二进制。

## Tenant 与 generation 切入点

多租户 schema 可以直接组合 `nexaent/mixin.Tenant`。该 mixin 提供 required、positive、immutable 的
`tenant_id` Ent field 和固定 internal metadata；业务 schema 仍需用 `nexaent.Schema` 明确选择
`ScopeTenant`，两者缺一都不能凭名称自动启用隔离。

当前官方 generation plugin 只公开 typed RPC 和 HTTP API generation。是否可用、命令 schema 和 delegated
tool 必须以 consumer binary 的 fresh inspection 为准。生成时 consumer 显式声明 generated 与 extensions
scopes；工具直接清空并重建 generated 目录，extensions 中的业务 logic、hooks 和其他人工源码保持不变。

官方 generation plugin 不公开 Ent、CRUD、Service Manifest、manual create-once 或 overwrite 契约。标准
CRUD 的业务 contract 和非空 logic 必须由对应领域 owner 冻结并保留，不能由 starter 或 generator 生成空
实现替代。生成后由 consumer 审阅 Git diff，失败时允许保留部分变更并返回非零；再次生成必须无新 diff。

## Detach

`source detach` 只移除 provenance relationship，保留 materialized source 和全部 consumer 修改。Detach 后：

- Provider、cache 和 source adapter 可以从该 consumer 工具组合中移除；
- native facts 和普通源码继续由 consumer 维护；
- generation 仍直接读取这些 native facts；
- build、test 和 runtime 不依赖 source tool。

Source release、lock、升级和冲突的完整语义见[Source Bundle](../contracts/source-bundles.md)。
