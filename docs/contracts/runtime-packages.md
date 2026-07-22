# Runtime 公共契约

Nexa 的 runtime packages 是可独立导入的公共 Go package。它们提供中性的值对象、端口、状态机与适配器，不拥有业务配置、服务实例、broker、gRPC server、OpenTelemetry provider 或 exporter。

框架不提供 runtime umbrella package。Consumer 只导入实际使用的 package，并在自己的 composition root 中构造依赖。

## 所有权矩阵

| 事实或能力 | Owner | Authoring surface | Nexa projection |
| --- | --- | --- | --- |
| CRUD JSON 业务值与分页边界 | consumer | 调用点的 typed Go value、API/存储 contract 与 caller policy | `JSONObject`、`WindowPolicy`、`Window` |
| Kafka topic、group、subscription、handler 与 retry 决策 | consumer | 服务 package 的 typed composition 与运行配置 | `Subscription`、`Record`、`Batch`、`Failure`、`Manager` 状态 |
| Kafka broker 地址与 franz client options | consumer | adapter composition root 的 typed options | franz reader/producer factory |
| request、trace、tenant、member 与 session 标识 | consumer | 请求上下文和认证/追踪 adapter | 有序 `slog.Attr` |
| 日志 redaction 策略与日志后端 | consumer | logging composition root | redacting `slog.Handler` |
| gRPC unary method、status code 与耗时 | Nexa adapter | gRPC interceptor 调用边界 | access log attrs |
| OpenTelemetry SpanContext | consumer-selected tracing stack | 已存在的 `context.Context` | trace/span access attrs |
| broker、gRPC server、provider、exporter、凭据与部署实例 | consumer | 运行配置与部署事实 | 不由本 module 保存或创建 |

## `runtime/crud`

`JSONObject` 使用 Go 标准库 `encoding/json` 解析，只接受 JSON object，并拒绝 `null`、非 object 根、原始非法 UTF-8、尾随输入以及超过 1 MiB 的输入。重复键采用标准库的 last-wins 语义，JSON 转义中的未配对 surrogate 也沿用标准库的替换语义；框架不再维护第二套 JSON parser、depth/node 计数器或自定义 canonical Unicode 协议。成功值通过标准库重新编码为稳定 JSON，并为 `database/sql` 提供严格的 `Scanner`/`Valuer` 行为；零值和转换失败返回稳定 typed error。

`WindowPolicy` 由 caller 显式提供 `MinLimit`、`MaxLimit` 与 `MaxOffset`。框架不定义业务默认分页策略；`Check` 只在该 caller policy 内产生不可变 `Window`。

## `runtime/kafka`

Kafka core 定义 transport-neutral 的 `Record`、`Message`、`Batch`、`Subscription`、reader/producer ports、stage-aware retry policy 与 `Manager`。Header 是有序序列：重复 key 合法，`nil` 与非 nil 空值保持区别，输入输出均采用防御性副本。

`Manager` 构造不打开资源或启动 goroutine。`Start` 为每个 subscription 打开一个 reader 和一个 worker；`Close` 取消启动或运行中的工作、关闭全部 reader 以解阻 Poll，并等待 worker 收口。并发 `Start` 返回冲突，重复或并发 `Close(ctx)` 等待同一关闭结果。消费采用显式 Poll、Handle、Commit 边界；同一阶段的连续失败以递增 attempt 交给 caller retry policy，成功后重置相应重试序列。该生命周期只保证普通取消、重试与 at-least-once 行为，不提供 admission token、revocation winner 或精确竞态证明协议。

`runtime/kafka/franz` 是可选 franz-go adapter。`NewReaderFactory` 与 `NewProducerFactory` 只校验并冻结 options，不连接 broker；client 生命周期分别从 reader factory `Open` 和 producer factory `Open` 开始。Kafka core 不依赖 franz-go、go-zero 或 package-global logger。

## `runtime/observability/logging`

logging package 直接扩展标准 `log/slog`，不提供 logger facade、全局 logger 或后端实现。`ContextFields` 按 request、trace、span、tenant、member、session 的稳定顺序输出合法非空字段；trace/span 使用小写非零十六进制格式，其他标识执行 UTF-8、字节长度、Unicode control 与边界空白校验。

redacting handler 对每个 resolved non-group leaf 调用一次 consumer `Redactor`，遵守 `ReplaceAttr` 的 group 路径与 inline group 语义。drop、replace、空 group、`WithAttrs`/`WithGroup` 顺序均保留；redactor panic 投影为安全 typed error且不提交该 record。Handler 不规定 JSON、采样、存储或 exporter。

## `runtime/observability/rpcaccess`

`UnaryServerInterceptor` 保留现有 `(interceptor, error)` 构造签名，但 logger 与 extractor 都是可选依赖。logger 为空或未启用 Info 时直接调用 handler，不做 extraction；启用后在 unary handler 正常返回时记录一条 `slog.LevelInfo` access record，属性顺序为 method、canonical gRPC code、duration、可选 extraction-failed marker、稳定 context attrs。Duration 使用 `time.Now` 覆盖 extraction 与 handler。Interceptor 原样返回 response/error；extractor 为空时按 no-op 处理，extractor error 或 panic 只产生安全布尔 marker，不泄露错误文本或 partial context。Handler panic 原样传播且不产生 completed access record。

`runtime/observability/rpcaccess/otel` 是可选 adapter，只读取 context 中已存在且有效的 local/remote `SpanContext`。它不创建 span、不安装 provider、不管理 exporter，也不改变 span 生命周期。

## 可选组合

“0 optional”表示 Minimum Runtime：

- 不 import 可选 runtime package；
- 编译产物不 link franz-go、gRPC、OpenTelemetry 或 go-zero package；
- composition root 不构造其 factory、interceptor、provider 或 exporter；
- 启动、健康检查和停止不需要 broker、gRPC server、配置目录、凭据或 exporter。

它不表示根 `go.mod` 的 module graph 中没有这些依赖。Go module requirement 只支持同一 module 内各公共 package 的构建；最终二进制的实际能力由 consumer import 与 constructor composition 决定。

Runtime 行为门禁为 `make runtime`。该目标运行 package tests 和独立 external consumer/Minimum Runtime 编译执行证据，不访问 broker、数据库或 exporter。
