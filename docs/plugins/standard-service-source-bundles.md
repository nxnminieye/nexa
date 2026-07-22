# 标准服务 Source Bundles

标准服务 Source Bundle 发布可选择的中立服务源码。Provider 属于构建工具，materialized source 属于
consumer；物化后的服务是普通静态源码，不具有运行时插件身份。

## 组合边界

Framework Minimum 不选择任何 Source Bundle，也不创建 Core、Job、Quality 或其他服务占位。它可以独立
编译、启动并提供自身健康能力，不需要 catalog、source lock、前端、质量生产链、人工 gate、测试证据、
部署实例或远端凭据。

Core Application 是 Framework Minimum 之上的显式选择。V0.1 reference composition 只包含
`core-application@v0.1.0`，其 profile closure 如下表；端到端参考路径选择 `backend`。Exact Provider/profile
必须由当前 `nexactl inspect --json` 直接暴露，不能从 `source.bundle` capability 或下表反推。没有全局
Provider 列表、隐式默认选择或运行时发现。Job 与 Quality 是可由 consumer 显式构造的独立 package，不进入
V0.1 reference composition，也不是 Framework Minimum 或 Core 的依赖。

| Provider package | Release identity | Profiles | 公共边界 |
| --- | --- | --- | --- |
| `plugins/service/core` | `core-application@v0.1.0` | `backend`、`identity-oidc`、`frontend`、`full` | Core API/RPC、local auth、session、tenant membership 与 RBAC 的中立源码 |
| `plugins/service/job` | `job-service@v0.1.0` | `backend` | scheduler、run、retention、task port 与 health 的中立源码 |
| `plugins/service/quality` | `quality-runtime@v0.1.0` | `backend`、`frontend`、`full` | 只读 Quality API、可注入 projection source 与独立前端源码 |

Provider 是否真实存在、当前二进制是否包含其 exact release/profile 及 Source 命令的 flags/schema，必须
通过该 consumer 编译出的 `nexactl inspect --json` 查询。文档表格不是 capability discovery 接口；若 exact
Provider/profile 缺席，必须报告 capability gap，不得尝试 materialize。

## Core authored-only profiles

`core-application/backend` 只包含 authored source：typed Ent schema、人工 Proto/`.api`、人工 Go、版本化
数据库变更、行为测试和中立构造边界。它不包含 Ent generated Go、CRUD Proto fragment、聚合 Proto/API、
API/RPC Go、proxy/client/mapper/register Go、Artifact/API/Service Manifest、frontend export 或 provenance lock。

Profile closure 固定为：

- `backend` 独立闭合，不选择 frontend 或外部身份 Provider；
- `identity-oidc` 显式依赖 `backend`，只增加通用 OIDC adapter source；
- `frontend` 与 backend 独立，只包含人工对象 schema、locale 与 page source；
- `full` 组合 `backend` 与 `frontend`，不会隐式选择 `identity-oidc`。

Local auth 不通过外部身份 Provider。`IdentityProvider` 是 materialized Core 在运行时由 consumer 构造的
认证端口；issuer、client、secret、redirect、group mapping 和实例生命周期都由 consumer 拥有。
`sourceplugin.Provider` 则是发布 immutable source tree 的 BundleProvider，只在 source tool 构造时存在。
两者没有共同接口、registry、constructor 或 lifecycle。

| Object | 存在阶段 | Owner | 职责 |
| --- | --- | --- | --- |
| BundleProvider (`sourceplugin.Provider`) | source tool 构建与执行期 | provider publisher | 发布 immutable manifest、profile 与 source tree |
| IdentityProvider (`coreapp.IdentityProvider`) | materialized Core 运行期 | consumer composition | 对接显式选择的外部身份系统并返回规范身份 |

Provider package presence 不创建任何运行时实例。Core 的 store、clock、token/password port 与可选
IdentityProvider，Job 的 task 实现，以及 Quality 的 projection source、server 和监听地址都由 consumer
显式构造。Runtime config、secret、health state 与 deployment instance 同样留在 consumer 的最近事实源。

## 从源码到运行

链路方向固定为：

```text
inspect actual nexactl
  -> source plan/materialize selected provider@version/profile
  -> consumer edits and owns typed Ent / Proto / .api / manual Go
  -> generation plan/check/write from those native owner facts
  -> parse/typecheck/compile generated and manual source
  -> construct consumer runtime instances and run ordinary services
```

Schema 级 CRUD 只由 typed `nexaent.CRUD(operations...)` annotation presence 显式选择；annotation 缺失就
不生成 CRUD。生成器读取 consumer Ent graph，经 `EntityIR -> CRUDProtocolIR` 生成 Proto fragment。
`nexactl gen ent` 只委托 Ent 官方生成，不提供 CRUD 选择语义。

Source Bundle manifest/tree 和 source ownership snapshot 不参与生成事实重建。materialize 成功后，consumer 可以修改
native facts，再由其 owner parser/generator 生成投影；不能手工编辑 generated artifact 绕过正式链路。

## 删除工具后的独立性

Consumer 完成 materialize 后，可以 detach 并删除 source tool、Provider package/cache、Source Build Plugin
组合和 source ownership snapshot。已物化的源码仍由 consumer 保存；不存在需要重放的 transaction state。

Generation tool 与 source tool 相互独立：generation 只读取 consumer-owned catalog、typed Ent、Proto 与
`.api` facts，不读取 Provider 或 source lock。生成完成后也可以删除 generation tool；consumer service
继续依赖稳定的 Nexa public contracts，保持 compile、start、health 与业务行为。

## 可选能力缺席

requirements、work、UserOperation、人工 gate、TestSpec、evidence、quality producer、frontend、deployment、
observability instance 和业务私有工具均是独立选择。它们全部缺席时，Framework Minimum 与 Core backend
仍可构建和运行；缺席不会创建空插件、空事实、空目录或占位生成物。

Quality backend 只消费 [`quality/readmodel`](../contracts/quality-read-model.md) 的只读 snapshot；它不生产
requirement、test、evidence 或 freeze 事实。Job 的具体 task 实现及 Quality 的 projection source 都由
consumer 构造。选择任一 package 不会隐式选择另一个 package。

完整 materialize、upgrade、detach、serial staged publish 和 provenance 语义见
[Source Bundle 契约](../contracts/source-bundles.md)。
