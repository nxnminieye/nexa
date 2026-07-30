# HTTP Convention v1

`nexa.dev/http-convention/v1` 是 Nexa JSON HTTP API 的公共契约。它以 PDCL 已运行的 Go-zero/Vben 调用链为基线：原生 HTTP
只由 `.api` 原生结构拥有 route、DTO 与字段位置；需要代理为 HTTP 的 RPC 只在 Proto method 上用
`source-comment/v1` 声明 method、path、auth 和 permission，operation id 由 RPC identity 推导，composition 按固定规则生成 canonical `.api`。HTTPAPIIR 不是公共 contract，不能序列化、跨进程传递或
嵌入 FrontendIR。

V1 不提供 legacy reader、alias、fallback、normalizer、per-operation response decoder 或逐字段运行时映射。后端 domain/read-model
到公开 DTO 的显式投影仍是正常的业务边界。

## 路径和字段

- `.api` 的 operation path 是相对于 consumer 配置的 API base URL 的绝对相对路径，例如 `/auth/login`、`/tenants`。
  部署或开发 proxy 通过唯一 `apiURL` 配置提供 `/api` 前缀；该前缀不重复写进每个 `.api` route。
- literal route segment 使用 lower-kebab-case；path placeholder 与 request 字段的外部名称完全相同。
- `.api` field 使用 PDCL 已有的 lowerCamelCase 或 lower_snake_case 外部名称。`json`、`form`、`path` tag 可以声明该唯一外部
  名称和位置；tag 值只能重复 source 字段名，或使用由同一 source identifier 词法唯一推导的 lowerCamelCase/lower_snake_case
  形式，不能改名、创建 alias 或额外 logical name。
- 未写 transport tag 的字段按 source identifier 的确定性 lowerCamelCase 形式暴露。生成器、FrontendIR 与 TypeScript 只携带这个
  已确定的外部名称，不再携带 source/wire 双字段。
- `Authorization`、tenant、request ID 和 trace context 属全局 middleware/interceptor；它们不进入业务 DTO。跨租户选择是明确的
  业务字段，不是 context binding。

## 请求位置

字段位置由 method、route 和与之相符的 `.api` tag 固定：

| 条件 | 位置 |
| --- | --- |
| route template 同名字段 | `path` |
| `GET`、`DELETE` 的其余字段 | query，使用 `form` |
| `POST`、`PUT`、`PATCH` 的其余字段 | JSON body，使用 `json` |

`header` 业务字段不属于 v1。显式 tag 与上述固定位置冲突时，Nexa 编译失败。`PATCH` 按 PDCL 既有实践被允许；其字段语义由具体
request DTO 和后端业务实现决定，Nexa 不另建 patch/mapping DSL。

## 响应和错误

所有成功操作使用 HTTP 200、`application/json` 和统一 envelope：

```json
{"code": 0, "msg": "ok", "data": {}}
```

`data` 是 operation response DTO；无结果操作可以省略 `data`。客户端在唯一的 shared transport 上固定使用
`responseReturn: 'data'` 并由统一 response interceptor 校验 envelope；不允许按 operation 配置 projection，或由调用方自选
`body/data` 模式。

所有错误也使用 `application/json`，HTTP status 仍表达 HTTP 失败类别，body 固定为：

```json
{"code": 409, "msg": "conflict", "message": "conflict"}
```

`code` 必须等于 HTTP status，`msg` 和 `message` 都是必填且相等的消息字段；它们是 PDCL 的正式 response shape，不是兼容
fallback。Go RPC status 映射沿用 PDCL：`InvalidArgument`、`FailedPrecondition`、`OutOfRange` 为 400，`Unauthenticated` 为
401，`PermissionDenied` 为 403，`NotFound` 为 404，`AlreadyExists`、`Aborted` 为 409，`ResourceExhausted` 为 429，
`Canceled` 为 408，`Unimplemented` 为 501，`Unavailable` 为 503，其余内部失败为 500。operation 不声明 error projection。

## 分页和标量

标准分页 request 使用 `limit` 和零基 `offset`；默认页面大小为 20。标准管理列表的 `data` 为 `{items,total}`，其中 `total`
是非负整数。frontend source 只可声明 UI 页面大小，不保存 `itemsPath`、`totalPath`、分页 path 或 transport binding；renderer 直接把
VXE 的 page/pageSize 换算为 `offset=(page-1)*pageSize` 和 `limit=pageSize`。

PDCL 已有 DTO 的 `int64`、`uint64`、资源 ID 和时间字段按其 `.api` 声明以 JSON number 或 string 传输；v1 不擅自重写成
decimal string、branded TypeScript number 或另一套 timestamp format。JavaScript 精度风险属于引入超过安全整数的新业务字段时的
显式评审事项，不能通过单方面改 wire contract 解决。optional、`null`、严格 JSON decoder、数组 query、map/dynamic JSON 的
细节继续由 `.api` 类型和 consumer 的 Go-zero parser 事实约束，Nexa 不在 v1 另造不兼容的全局语义。

## 生成边界

Nexa 的 validator、`.api` loader、composition、FrontendIR builder 和 conformance fixture 必须使用本包的同一规则。FrontendIR
只包含页面引用的 operation/type closure、resolved typed UI projection 和 locale；不包含完整 HTTP snapshot、HTTP digest、wire mapping 或
response decoder 规则。Vben renderer 直接消费该 closure，生成单一 typed transport 调用，不重实现 Go/Node 两套 HTTP 协议。

`nexa.dev/api-manifest/v1`、序列化 HTTPAPIIR、public `BindingSpec`、Proto 的 request/response/context/error projection
均不属于 v1。source lock、schema version 和 Git diff 升级仍是身份和审阅契约，不是 transport compatibility layer。
