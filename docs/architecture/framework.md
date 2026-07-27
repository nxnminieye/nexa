# Nexa 当前架构

Nexa 是一个公共 Go module，由事实契约、生成内核、标准源码发布、build-time CLI 组合和可选 runtime
packages 组成。Consumer repository 是业务事实、普通源码和运行实例的最终 owner。

## 层次与依赖方向

```text
consumer authoring facts
       |
       v
public typed contracts and strict loaders
       |
       v
versioned IR and deterministic generation
       |
       v
consumer-owned ordinary source and manifests

optional source provider -> materialized consumer source -> same fact/generation path
optional runtime package --------------------------------> consumer runtime
```

依赖只沿箭头向下。IR、manifest、CLI inspection 和生成源码是 projection，不能反向改写 Ent、Proto、
`.api` 或 service topology 等 authoring facts。

## Facts 与 contract

领域事实尽量留在最近 owner：

- Ent schema 使用 `nexaent` typed annotations 表达 schema、field、CRUD 和 tenant scope；
- Proto 拥有 RPC/message/method 及其 typed transport metadata；
- `.api` 拥有 native HTTP route/type/field；
- Service Catalog 只表达服务拓扑和 service-to-capability binding；
- manifest 和 source lock 没有人工 authoring surface，由 owner package 从当前输入重建。

每个 machine document 由同一 public package 拥有 value、strict parser、schema accessor 和 canonical
projection。具体 ownership 见[业务事实](../contracts/business-facts.md)。

## IR 与 generation

当前 official generation 把 typed RPC/API owner facts 投影为 Protocol、HTTP API 和 Composition IR，再通过
受信任 direct tool 形成普通源码。Project provider 只定位 consumer 入口、声明 generated/extensions scopes
并绑定工具，不复制业务事实。

直接生成、replace-tree、generated/extensions 路径隔离与失败语义由
[受控生成](../contracts/controlled-generation.md)统一说明。架构层不维护命令或 flag 清单。

## Source 与 starter

`sourceplugin.Provider` 发布 immutable manifest、profile 和 source tree。Consumer 通过可选 source adapter
选择并物化源码；物化完成后，源码和其中的 native facts 归 consumer，服务按普通 Go/go-zero 工程构建和
运行。Provider、cache、lock 和 source CLI 不进入业务服务生命周期。

标准源码组成与移交见[标准服务 starter](../starters/standard-services.md)，release/lock/upgrade 语义见
[Source Bundle](../contracts/source-bundles.md)。

## Build-time composition

`nexactl/host` 通过显式 constructor 组合实现 `nexactl/plugin` contract 的 plugin。Plugin 在编译期决定，
Host 从实际组合生成 inspection；不存在 `init` registry、blank-import discovery、动态 `.so` 或运行时目录
扫描。

Reference `cmd/nexactl` 只是一个组合实例。Consumer 可以构建自己的 composition root，实际能力只能由
对应二进制的 `inspect --json` 证明。完整规则见
[Nexactl Build Plugin](../plugins/nexactl-build-plugin.md)。

## Runtime packages

`runtime/*` 提供按 package 独立选择的 transport-neutral contract 和 adapter。Import、link 和 constructor
决定某项能力是否进入 consumer；没有 umbrella runtime，也没有全局 plugin registry。未选择 Kafka、OTel
或其他 adapter 时，consumer 不需要其 broker、provider、exporter 或配置。

Quality Read Model 同样是可选只读 projection，不是 requirement、test 或人工 gate 的 owner。

## Consumer boundary

Consumer 负责最终 module pin、facts、生成选择、业务代码、数据库迁移、部署和运行验证。Framework 的
build/test 只能证明公共契约和中立 fixture，不能证明某个业务系统已经采用或上线。采用时应按
[验证矩阵](../adoption/verification-matrix.md)逐层建立证据。
