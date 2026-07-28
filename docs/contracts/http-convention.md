# HTTP Convention v1

`nexa.dev/http-convention/v1` 是 Nexa 拥有的 JSON HTTP API 唯一约定。HTTP authoring 和业务事实只来自
consumer 的 `.api`；Proto 只拥有 RPC 事实，并可经 composition 生成 `.api`。本契约不定义 HTTPAPIIR，也不允许
把编译器中间状态序列化、跨进程传递或嵌入 FrontendIR。

V1 是一次性治理边界，不提供 legacy reader、大小写 alias、fallback、response decoder 或逐字段兼容 map。不符合
本契约的现有 API 必须在 consumer 中一次性修改并重新生成。

## 命名与路由

- JSON object、query、path、`.api` member、PageSpec、FrontendIR 和 TypeScript 字段全部使用 exact lowerCamelCase。
- Go/Proto 标识只经 `httpconvention.CanonicalName` 做一次确定性转换；Pascal、initialism 和 snake_case 不能形成第二个 wire 名。
- 外部 API 基路径固定为 `/api`，开发 proxy 必须原样保留此前缀。
- literal path segment 使用 lower-kebab-case；资源命名约定使用复数，但 validator 不通过简单的 `s` 后缀猜测资源语义；模板变量与 request field 同名且为 lowerCamelCase。
- 禁止显式 wire rename 或 alias。

## 请求

字段位置只由 method 与 route 推导：

| 条件 | 位置 |
| --- | --- |
| route template 中的同名字段 | path |
| `GET`、`DELETE` 的其它字段 | query；禁止 body |
| `POST`、`PUT`、`PATCH` 的其它字段 | 单一 JSON object body；禁止业务 query/header 例外 |

operation metadata 只保留 method、path、`auth required|none`、permission、request type 和 response type。
`Authorization: Bearer`、tenant、`X-Request-ID` 与 `traceparent` 由全局 middleware/interceptor 注入和提取，不进入
业务 DTO。tenant 默认来自认证主体；跨租户选择必须是显式业务输入。

## 成功响应与分页

有 JSON body 的请求和成功响应使用 `application/json`，成功直接返回业务 DTO，不使用 `{code,data}` envelope。

- 查询：`200` 与结果；
- 创建：collection route 使用 `201`、创建后的资源与该资源的 `Location`；
- 更新和 action：`200` 与结果；`POST` action 必须使用 `/actions/<name>` route；
- 删除：`204`，不返回 body 或 `Content-Type`。

分页 query 固定为 `page`（从 1 开始，默认 1）和 `pageSize`（默认 20，范围 1..100）。分页响应精确为
`{items,total}`；`items` 必填且始终为 array，空集合使用 `[]`；`total` 必填、非负并位于 JavaScript safe integer
范围。PageSpec 只允许覆盖页面大小和声明真实业务语义绑定，不再声明 `itemsPath`、`totalPath`、`pagePath` 或
`pageSizePath`。标准资源主键为 `id`，关联 ID 保留 `tenantId`、`accountId` 等业务语义。

## 错误

所有错误使用 RFC 9457 `application/problem+json`。固定成员为 `type`、`title`、`status`、`code`；其中 `type` 表示全局
错误 category，`code` 保留 consumer 的稳定业务码；可选成员仅为
`detail`、`instance`、`requestId`、`traceId`、`fieldErrors`。禁止 `error`、`message` 等兼容别名。
`type` 为 `https://nexa.dev/problems/v1/<category 的 kebab-case>`，`status` 必须与 HTTP status 相同。业务 `code` 只需
满足 lower_snake_case，不需要注册到全局表。

| code | status |
| --- | --- |
| `invalid_input` | 400 |
| `unauthenticated`、`invalid_credentials`、`session_expired`、`session_replayed` | 401 |
| `permission_denied` | 403 |
| `not_found` | 404 |
| `conflict`、`concurrent_write` | 409 |
| `failed_precondition` | 422 |
| `rate_limited` | 429 |
| `internal_error` | 500 |
| `unavailable`、`not_ready` | 503 |

业务 `code` 保留在 problem document 中；operation 不再声明 error projection。5xx `detail` 只能使用安全通用文案，
不得泄露内部错误、SQL、路径、credential 或 stack。

`fieldErrors` 固定为 `[{pointer,code,detail?}]` 数组：`pointer` 是 RFC 6901 指向 canonical request DTO 的非空 JSON
Pointer，`code` 是 lower_snake_case，`detail` 非空时为面向用户的安全文本。

## 标量与空值

- 资源 ID 使用非空 opaque string；`int64` 使用可选负号的规范十进制 string，`uint64` 使用无符号规范十进制 string；
  可能超过 JavaScript safe integer 的计数和金额使用不带指数、无前导零、无尾随零的十进制 string。
- 普通 `int32`、`uint32`、`page` 和 `total` 在 JavaScript safe integer 范围内使用 JSON number。
- timestamp 使用 RFC3339 UTC 且以 `Z` 结尾；date 使用 `YYYY-MM-DD`。
- optional 只表示 key 缺省；V1 禁止 `null`。集合存在时必须是 array，空集合使用 `[]`。
- generated frontend v1 拒绝 `bytes`、dynamic JSON 及其它未定义 wire 类型。

Path/query 只接受 scalar。数组、对象、过滤表达式和排序在 v1 不定义编码；重复 key、未知参数、空值、非法 lexical value
和非 RFC 3986 percent-encoding 直接返回 `invalid_input` 400。query bool 只接受 `true|false`，数字使用 canonical
decimal，timestamp/date 使用本 Convention 的字符串形式。

body 必须是单一 JSON object。框架 strict decoder 对错误 Content-Type、未知字段、重复 key、类型错误、trailing token、
非空 body 缺失和 `null` 统一返回 `invalid_input` 400；`204` 响应不解码。`PUT` 是 full replacement，`PATCH` 是 omitted
fields unchanged 的 partial update；因 v1 禁止 `null`，清空可选值使用显式 action，不使用 merge-patch。

401 必须返回 `WWW-Authenticate: Bearer`；429 必须返回 `Retry-After` 秒数。v1 不使用 ETag/If-Match，条件更新使用业务
`expectedVersion` 等 canonical DTO 字段并以 `concurrent_write` 409 表达冲突。

## 生成与验证边界

Nexa 的 validator、canonical naming、generator 和 conformance fixture 必须共同消费 `generation/httpconvention`。
CI 对非 canonical authoring 直接失败。FrontendIR 只携 `httpConvention`、页面引用的 canonical operation/type closure、
页面、permission 与 locale；不携完整 HTTP snapshot、HTTP digest 或 wire mapping。Vben renderer 直用该 closure，不推断或
转换字段。后端 domain/read-model 到 canonical API DTO 的显式投影仍是合法边界，真实业务语义绑定（例如
`version -> expectedVersion`）也继续由 consumer PageSpec 持有。
