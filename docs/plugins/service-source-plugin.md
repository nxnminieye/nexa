# Service Source Plugin

Service Source Plugin 是发布标准服务源码的 `sourceplugin.Provider`。它提供不可变 manifest、named profiles
和封闭 source tree；consumer 通过可选 source Build Plugin 把选定 profile 物化到自己的 repository。

完整 identity、schema、resolver/cache、安全树、provenance、七命令、三方升级、serial staged publish 和错误契约见
[Source Bundle 契约](../contracts/source-bundles.md)。本文只定义标准服务的产品边界。

官方 Core、Job 与 Quality package 的选择边界和 profile closure 见
[标准服务 Source Bundles](standard-service-source-bundles.md)。V0.1 reference composition 只包含 Core，端到端
参考路径选择 backend；是否已经编入 exact Provider/profile 仍以当前 `nexactl inspect --json` 为准。其他
Provider 只能由 consumer 在自己的构建入口中显式构造。

## Provider、adapter 与普通服务

| 对象 | 形态 | Owner | 是否参与运行 |
| --- | --- | --- | --- |
| Service Source Provider | 实现 `sourceplugin.Provider` 的 Go package | publisher | 否 |
| Source Build Plugin adapter | `plugins/nexactl/source` 的一个编译期实例 | Nexa contract；consumer 选择组合 | 否 |
| Materialized service | consumer repository 中的普通源码 | consumer | 作为普通服务运行 |

一个 source adapter 可以同时接收多个 provider，但只注册一个 `source.bundle` capability 和同一组七命令。
Provider 不各自注册命令，不通过 `init()` 或 registry 自发现。未组合 adapter 时，provider 不产生 repository
副作用，consumer binary 也没有 source capability。

普通业务微服务不是插件。`job` 等具有跨业务复用价值的标准服务可以由 Service Source Provider 发布；
物化后与业务自行编写的服务没有构建、部署或运行差异。

## 物化后的所有权

源码成功写入 consumer repository 后：

- consumer 取得完整源码所有权；
- 可以修改 API、logic、schema、database change、测试、配置结构和目录组织；
- 使用普通 Go/go-zero toolchain 构建、测试、部署和启动；
- 不实现 plugin lifecycle、动态 load、start hook 或运行时 registry；
- 服务启动不读取 source manifest、release cache、provenance lock 或 source adapter；
- `detach` 只解除 provenance，保留源码及其全部 consumer 修改。

Provenance lock 记录 exact release、profile closure、target 与 tracked standard baseline，支持 status、diff 和
三方升级。它不是 ownership claim，也不禁止 local delta；consumer source 永远不是 provider tree 的只读镜像。

## Profile 边界

每个标准服务至少提供可独立选择的 `backend` profile。该 profile closure 必须包含其声明的完整源码和验证
recipe，并能在独立 consumer module 中构建和测试。其他 profile 只能通过 manifest 中的显式依赖组合，
不能依靠目录存在、隐式环境或运行时探测。

标准服务可以携带自身中性的 Proto、Ent schema、代码、测试和配置类型。物化后的这些节点就是 consumer
中的最近业务事实源；publisher 不再拥有 consumer 对它们的修改。服务发现和跨服务 binding 仍由 consumer
Service Catalog 拥有，不复制到 Source Bundle manifest。

## 不属于 Source Plugin 的对象

Service Source Plugin、source adapter 和 Source Bundle contract 都不拥有或操作：

- runtime config value、环境变量和 secret；
- Helm/Kubernetes value、deployment instance、调度和发布状态；
- readiness/liveness endpoint 配置或 health state；
- 服务发现、运行地址、remote installation 和进程生命周期；
- requirements、work、UserOperation、人工 gate、TestSpec 或 evidence。

这些对象可以全部缺席而不影响 provider package 的构建。consumer 选择运行 materialized service 时，再由
相应领域的最近事实源提供该服务所需的运行和部署事实。

## 验证

Service Source Provider 的测试验证公开行为：

- manifest 与 tree 能通过 owner schema、constructor 和 digest 校验；
- `backend` profile 在真实临时 consumer module 中完成构建、测试和需要的进程行为；
- 相同 exact release 与 profile 产生相同 tree 和 digest；
- materialize/upgrade 的 plan conflict 与 staging validation 失败保持零写入；发布中途失败允许保留已经发布的
  文件，并要求从当前 repository 重新 plan；
- detach 后 materialized service 仍可独立构建和运行。

Detach 只解除 provenance 并保留 consumer-owned source。Detach 成功后，consumer 可以另行删除 source
tool、Provider/cache 与 Source Build Plugin composition；后续 generation 直接读取 consumer native facts。
生成完成后删除 generation tool 也不影响普通 service 的 compile、start 或 runtime behavior。

测试不得用源码字符串、import 文本、文件位置、目录 allowlist 或行数替代 manifest、产物、协议和运行行为。
