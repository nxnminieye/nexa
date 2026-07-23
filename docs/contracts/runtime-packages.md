# Runtime 公共契约

Nexa 的 runtime packages 是可独立导入的公共 Go package。它们提供中性的值对象、端口、状态机与适配器，不拥有业务配置、服务实例、broker、gRPC server、OpenTelemetry provider 或 exporter。

框架不提供 runtime umbrella package。Consumer 只导入实际使用的 package，并在自己的 composition root 中构造依赖。

## 所有权矩阵

| 事实或能力 | Owner | Authoring surface | Nexa projection |
| --- | --- | --- | --- |
| CRUD JSON 业务值与分页边界 | consumer | 调用点的 typed Go value、API/存储 contract 与 caller policy | `JSONObject`、`WindowPolicy`、`Window` |
| Kafka topic、group、subscription、handler 与 retry 决策 | consumer | 服务 package 的 typed composition 与运行配置 | `Subscription`、`Record`、`Batch`、`Failure`、`Manager` 状态 |
| Kafka broker 地址与 franz client options | consumer | adapter composition root 的 typed options | franz reader/producer factory |
| S3 bucket、object key、content metadata 与 body | consumer | 服务 package 的 typed composition 与调用点 | `ObjectRef`、`PutRequest`、`ObjectInfo`、read/write results |
| AWS config、credentials、endpoint 与 path-style 选择 | consumer | adapter composition root 的 `ClientOptions` | AWS SDK v2 S3 client |
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

## `runtime/s3`

S3 core 定义 transport-neutral 的不可变 `ObjectRef`、`Reader`、`Writer`、`Inspector`、`Deleter`、组合这些能力的 `Store`、`PutRequest`、`ObjectInfo` 与 read/write result。`ObjectInfo` 仅返回 object metadata，不包含 body；content length 必须非负，metadata 在构造和读取边界均使用防御性副本。写入 body 必须是 `io.ReadSeeker`，由 caller 持有且 package 和 adapter 永不关闭；读取成功返回的 `io.ReadCloser` 归 caller 所有，caller 必须关闭；非 EOF read error 与 close error 只投影稳定错误，不暴露 provider 原始文本。content type、metadata、ETag、version id 与 last-modified 均为可选值；零 optional 不产生框架默认值。独立 `Presigner` 为 upload、download、head、delete 四种单对象能力接受 object ref 与正 expiration，只有 upload 额外接受可选 content type；结果防御性公开 URL string 与签名要求的 headers，Core API 不代理或发起真实 object 操作。不提供 list、分片、回调或业务策略。

`runtime/s3/aws` 是可选 AWS SDK for Go v2 adapter。`NewClient` 要求 consumer 显式提供非空、合法 UTF-8、无首尾空白或控制字符的 region，只校验并冻结 `ClientOptions`、构造 SDK client，不解析 consumer-owned credentials，也不进行网络 I/O。构造时复制 caller config 并清除其中可覆盖 S3 endpoint/region 或 request URL 的 global endpoint resolver、base endpoint、config sources、API options、HTTP interceptors 与 service options；caller 仍拥有且继续提供 credentials、HTTP client 与 retry 等非路由配置，原 config 不被修改。允许的自定义 endpoint 必须显式通过 `ClientOptions.Endpoint` 提供，且必须是无 userinfo、query、fragment、非根 path、端口范围为 1-65535 的绝对 HTTP(S) base URL；bucket 只通过 `ObjectRef` 提供，不能编码在 endpoint path 中。AWS presign expiration 必须是 1 秒到 7 天范围内的整秒，并将 SDK 返回的 signed headers 一并投影给 caller。

明文 HTTP 仅在 `AllowInsecureHTTP` 显式启用时接受。安全模式支持 SDK 默认或显式 `*awshttp.BuildableClient`，以及 Transport 为空或为非 nil concrete `*http.Transport` 的 caller `*http.Client`；adapter 保留 SDK resolved transport/timeout，或复制 caller client，并在每次 RoundTrip 前校验 URL scheme。只允许同 scheme、同 host 的 307/308 redirect，其他 redirect 不继续发送；HTTPS 到 HTTP 的 307/308 不会产生第二次 RoundTrip。typed-nil client/transport 会被拒绝，任意 `sdkaws.HTTPClient` 或自定义 RoundTripper 因无法约束内部发送行为也会在安全模式构造阶段被拒绝。`AllowInsecureHTTP=true` 时可以显式采用这些通用 client，但 adapter 只能检查交给它的初始 request；client 内部的自发请求、路由或 redirect 属于 consumer 的信任边界。兼容 S3 的服务可通过 `Endpoint` 与 `UsePathStyle` 显式组合，adapter 使用已验证的 `BaseEndpoint`，不使用 deprecated endpoint resolver 或 ambient service endpoint。Get/Head 的 transport/provider failure 按 read failure 安全投影，Put/Delete 按 write failure 安全投影；Head 的 HTTP 404 稳定匹配 not found，Delete 成功不要求 provider result，context cancel/deadline 保持稳定匹配。

## `runtime/observability/logging`

logging package 直接扩展标准 `log/slog`，不提供 logger facade、全局 logger 或后端实现。`ContextFields` 按 request、trace、span、tenant、member、session 的稳定顺序输出合法非空字段；trace/span 使用小写非零十六进制格式，其他标识执行 UTF-8、字节长度、Unicode control 与边界空白校验。

redacting handler 对每个 resolved non-group leaf 调用一次 consumer `Redactor`，遵守 `ReplaceAttr` 的 group 路径与 inline group 语义。drop、replace、空 group、`WithAttrs`/`WithGroup` 顺序均保留；redactor panic 投影为安全 typed error且不提交该 record。Handler 不规定 JSON、采样、存储或 exporter。

## `runtime/observability/rpcaccess`

`UnaryServerInterceptor` 保留现有 `(interceptor, error)` 构造签名，但 logger 与 extractor 都是可选依赖。logger 为空或未启用 Info 时直接调用 handler，不做 extraction；启用后在 unary handler 正常返回时记录一条 `slog.LevelInfo` access record，属性顺序为 method、canonical gRPC code、duration、可选 extraction-failed marker、稳定 context attrs。Duration 使用 `time.Now` 覆盖 extraction 与 handler。Interceptor 原样返回 response/error；extractor 为空时按 no-op 处理，extractor error 或 panic 只产生安全布尔 marker，不泄露错误文本或 partial context。Handler panic 原样传播且不产生 completed access record。

`runtime/observability/rpcaccess/otel` 是可选 adapter，只读取 context 中已存在且有效的 local/remote `SpanContext`。它不创建 span、不安装 provider、不管理 exporter，也不改变 span 生命周期。

## 可选组合

“0 optional”表示 Minimum Runtime：

- 不 import 可选 runtime package；
- 编译产物不 link franz-go、AWS SDK、gRPC、OpenTelemetry 或 go-zero package；
- composition root 不构造其 factory、interceptor、provider 或 exporter；
- 启动、健康检查和停止不需要 broker、S3 endpoint、gRPC server、配置目录、凭据或 exporter。

它不表示根 `go.mod` 的 module graph 中没有这些依赖。Go module requirement 只支持同一 module 内各公共 package 的构建；最终二进制的实际能力由 consumer import 与 constructor composition 决定。

Runtime 行为门禁为 `make runtime`。该目标运行 package tests 和独立 external consumer/Minimum Runtime 编译执行证据，不访问 broker、数据库或 exporter。
