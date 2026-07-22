# Nexa

## v0.1.0-alpha.1

`v0.1.0-alpha.1` 是 Nexa 公共 Go module 的 Alpha 检查点，用于试用已验证的公共契约并建立 consumer
集成反馈。它不是 RC，不代表完整 V0.1，也不表示任何产品或业务系统已经完成采用。

### 安装与最小入口

```bash
go get github.com/nxnminieye/nexa@v0.1.0-alpha.1
```

参考 `nexactl` 的编译组成可以通过稳定的机器入口读取：

```bash
GOWORK=off go run github.com/nxnminieye/nexa/cmd/nexactl@v0.1.0-alpha.1 inspect --json
```

`cmd/nexactl` 是 reference composition，不是所有 consumer 的固定命令集合。业务仓必须对自己实际编译的
`nexactl` 再执行 `inspect --json`，并且只使用 inspection 中真实存在的 command、flag、schema、delegated
tool 和 side effect。

### 当前 Alpha 覆盖

- starter/scaffold 所需的公共构件与参考证据，包括 [Framework Minimum fixture](fixtures/consumers/framework-minimum/)、
  [backend-only consumer fixture](integration/testdata/adoption/backend-only/) 和可选的
  [Core Source Bundle](plugins/service/core/)；这些是框架入口与测试闭包，不是一键业务采用器。
- `nexaent.Schema`、`nexaent.Field`、`nexaent.CRUD` 等 typed Ent 事实，以及对应 strict schema、loader
  和验证行为。
- 从 consumer-owned typed facts 到 versioned IR 的受控生成，以及当前 `generation crud-proto`、
  `generation rpc`、`generation api` 的 plan/check/write staged publish。
- `nexactl inspect --json` 的机器协议、显式 Build Plugin composition，以及可独立成立的 Framework Minimum、
  Core 和 backend-only consumer。
- 可选的 Service Source Plugin 与公共 runtime packages；是否进入某个二进制或运行实例，仍由 consumer
  的 import、link、constructor composition 和 inspection 决定。

### 不在本 Alpha 承诺内

- 统一 CRUD logic CLI 尚未纳入本 Alpha；当前受控入口仍是 `generation crud-proto`。
- 完整 consumer 采用、文档、集成与 release 收口尚未承诺；本版本不是 RC 或完整 V0.1。
- Python SDK、Python artifacts 和 runtime `nexa` CLI 不属于本 Alpha 支持面；reference `nexactl` 中存在的
  asset/schema projection 不等于 Python SDK 已发布或受支持。
- 产品私有规则、部署实例和业务仓采用结果不由本公共 module 交付，也不在本版本声明为完成。

业务 contract、配置、typed facts、物化源码、生成源码和部署实例继续保存在 consumer repository。这里的
插件只表示源码选择或显式 Go import/constructor 驱动的构建期组合，不提供运行时动态发现、`.so` 加载、
服务生命周期管理或热替换。

Nexa 是面向业务系统的 AI-native Go 框架与工程工具集，canonical Go module path 为：

```text
github.com/nxnminieye/nexa
```

CLI 是可执行契约，skill 是 AI 路由入口。命令、flag、schema、capability 和副作用由编译后的 CLI 自省；Make target、skill 和文档只消费这些契约，不维护第二份命令事实。

Nexa 的 framework skills 自包含，不绑定外部开发工作流。业务仓可以自行选择工作流工具，但该选择不进入 Nexa 的构建、CI、capability 或运行依赖。

本仓库的公共能力按三类对象组织：

- Nexa Core：稳定公共契约、SDK、生成内核和跨服务运行时基础能力。
- Service Source Plugin：可选择写入业务仓的标准服务源码；物化后完全归业务仓所有。
- Nexactl Build Plugin：通过显式 Go import 和 constructor 在编译期组合的工程命令模块。

这里的“插件”只用于源码选择和构建期代码组合，不表示运行时发现、动态加载、服务生命周期管理或热替换。

## 文档

- [文档索引](docs/README.md)
- [架构文档索引](docs/architecture/README.md)
- [框架架构](docs/architecture/framework.md)
- [设计影响](docs/architecture/design-influences.md)
- [Consumer 闭包](docs/adoption/consumers.md)
- [Skill 分发与路由](docs/adoption/skills.md)
- [升级与回滚](docs/adoption/upgrade-and-rollback.md)
- [CLI 机器协议](docs/contracts/cli-machine-protocol.md)
- [业务事实契约](docs/contracts/business-facts.md)
- [受控生成契约](docs/contracts/controlled-generation.md)
- [生成清单契约](docs/contracts/generated-manifests.md)
- [Source Bundle 契约](docs/contracts/source-bundles.md)
- [Quality Read Model 契约](docs/contracts/quality-read-model.md)
- [Runtime 公共契约](docs/contracts/runtime-packages.md)
- [Service Source Plugin](docs/plugins/service-source-plugin.md)
- [标准服务 Source Bundles](docs/plugins/standard-service-source-bundles.md)
- [Nexactl Build Plugin](docs/plugins/nexactl-build-plugin.md)

## 仓库边界

- 本仓库是公共实现与发布仓库，不是业务事实源。
- 业务 contract、`services.yaml`、服务 Proto、Core API desc、产品配置、质量事实和部署实例保留在 consumer repository。
- 业务代码只依赖公开 package，不依赖本仓库的 `internal` package。
- 普通业务微服务不是插件。
- 标准服务源码可以通过 Service Source Plugin 分发，但物化后与业务自行编写的微服务没有运行时差异。
- Source Bundle 只拥有标准源码发布、exact selection、provenance 与 repository-scoped serial staged publish；不拥有 runtime config、deployment instance 或 health state。
- 业务 Core API 可以拥有由业务事实生成的 proxy 普通源码；本 module 只提供中性的 parser、IR、generator 和 kernel。
- Framework Minimum 不选择 Source Bundle。V0.1 reference composition 只包含
  `core-application@v0.1.0`，端到端参考路径选择 `backend` profile；consumer 必须先从当前
  `nexactl inspect --json` 读取 exact Provider/profile，`source.bundle` capability 本身不证明该 release
  已编入。Job 与 Quality 保持 consumer 可选 package，不是 Minimum/Core 依赖。

受控生成的主链固定为 typed owner facts 到 versioned IR，再通过 plan/check/write staged publish 产出并验证
consumer-owned 普通源码。Ent CRUD 使用 `typed annotation -> EntityIR -> CRUDProtocolIR -> Proto`；
`nexactl gen ent` 仅委托 Ent 官方生成。所有命令在使用前都以 consumer 实际二进制的
`nexactl inspect --json` 为准。

标准服务源码的公开链路是 `materialize -> generate -> compile -> run`。Detach 后 consumer-owned native
facts 与普通服务源码不依赖 Provider、source tool 或 generation tool；完整边界见
[标准服务 Source Bundles](docs/plugins/standard-service-source-bundles.md)。

公共 runtime 能力按 package 独立选择：`runtime/crud`、`runtime/kafka`、`runtime/kafka/franz`、
`runtime/observability/logging`、`runtime/observability/rpcaccess` 与可选 OTel extractor 不通过 umbrella
package 或运行时插件组合。未 import、未 link、未构造这些能力的 Minimum Runtime 不需要 broker、gRPC
server、provider、exporter、配置目录或凭据。完整边界见 [Runtime 公共契约](docs/contracts/runtime-packages.md)。
