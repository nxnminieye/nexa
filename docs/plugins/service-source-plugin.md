# Service Source Plugin

Service Source Plugin 是发布标准服务源码的 `sourceplugin.Provider` package。它拥有 immutable manifest、
named profile、source tree 和 validation recipe，不拥有 consumer 运行实例。

## 三个对象

| 对象 | 形态 | 是否参与业务运行 |
| --- | --- | --- |
| Service Source Provider | 实现 `sourceplugin.Provider` 的 Go package | 否 |
| Source adapter | 显式组合进 `nexactl` 的 build-time plugin | 否 |
| Materialized service | Consumer repository 中的普通源码 | 是，按普通服务运行 |

一个 adapter 可以组合多个 Provider，但它们共同使用统一 source capability/command。Provider 不使用全局
registry 自发现，也不为每个服务创建 runtime plugin。

## Provider 责任

Provider 必须保证 manifest 与 tree 一致，profile closure 闭合，path/mode/digest 安全，并为选定 profile
声明可执行 validation recipe。Provider package 的 presence 不代表它已经编入某个 binary；实际选择从
`nexactl inspect --json` 读取。

Source release、cache、lock、materialize、upgrade 和 detach 由
[Source Bundle](../contracts/source-bundles.md)统一定义，本文不复制其生命周期。

## 物化后的责任

源码进入 consumer 后：

- Consumer 可以修改 schema、API、logic、migration、test 和 config shape；
- native Ent/Proto/`.api` facts 成为 consumer 的最近事实源；
- 后续 generation 直接读取这些 native facts；
- build、test、deploy 和 runtime 不读取 Provider、cache 或 Source Lock；
- detach 只解除 provenance，不删除 source。

Standard starter 的采用顺序和 tenant/CRUD 切入点见
[标准服务 starter](../starters/standard-services.md)。

## 明确不拥有

Provider 和 source adapter 不拥有 runtime config value、secret、database apply、deployment instance、health
state、服务发现地址、requirements、人工 gate 或 evidence。普通业务微服务也不因来自 starter 而变成插件。
