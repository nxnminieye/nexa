# Tenant Mixin 与统一 CRUD 生成设计

## 状态

本文定义已经人工确认的目标架构。实现尚未完成，当前二进制是否提供对应 package、命令、flag
与行为，仍以代码、测试和 `nexactl inspect --json` 为准。本文不能作为实现完成或可发布证明。

## 业务审核摘要

- Tenant mixin 只表示“这条记录永久属于一个大于零的租户”，不支持空值、零值或变更租户。
- 服务配置启用多租户且 Entity 显式使用 mixin 时，标准 CRUD 才自动执行租户隔离；不做名称推断。
- `tenant_id` 业务值统一为 `int64`，只在内部 RPC 传递；外部 CRUD 请求和返回不暴露该字段。
- 标准 CRUD 按 Entity 明确选择的 list/get/create/update/delete 子集生成；delete 只表示物理删除。
- 一次 CRUD 生成同时产出 Proto 和完整默认 logic；通用 RPC Go 仍由现有 RPC 生成步骤负责。
- logic 首次直接生成，已有文件默认不覆盖；用户显式选择 overwrite mode 时直接覆盖。
- 覆盖模式必须先进入同模式 plan 和 exact write set，但平台不读取 Git、不生成 diff、不备份。
- 手写 logic 通过生成的 `RequireTenantID(int64)` helper 显式接入，不存在全局 Ent 拦截器。
- 缺失租户上下文返回 `Unauthenticated`；访问其他租户记录与不存在一样返回 `NotFound`。
- 标准 Ent 数值宽度和合法 JSON model 不写入第二套 IR；生成后的 Ent model 是最终 Go 类型事实，
  candidate logic 必须在写入前对它完成真实编译校验。
- 非 JSON 的自定义 `GoType` 不进入标准 CRUD；这类字段只有实际参与已选择的 CRUD operation 时才阻断生成。
- Framework 不保存任何具体业务 Schema 清单，也不会为了证明 CRUD 而创建虚构业务实体。

后续章节冻结实现必须遵守的公开接口、错误语义、生成路径和验证要求。

## 目标

Nexa 为使用 Ent 的业务服务提供两项直接服务于代码生成的公共能力：

1. 一个严格、可识别的 Tenant mixin，用于声明一条记录永久属于一个正租户；
2. 一个由 `nexaent.CRUD(...)` 驱动的统一 CRUD 生成入口，同时生成协议和可运行的默认 logic。

业务仓拥有 Schema、是否启用 CRUD、是否启用多租户、生成后的业务 logic 和最终发布。Nexa
只拥有公共 typed contract、生成器、校验和普通文件写入行为。

## 不做的范围

本设计不引入以下能力：

- 不根据字段名、旧 annotation、scope 文本或历史代码推断多租户；
- 不提供旧协议 adapter、双 annotation、双写、fallback 或兼容别名；
- 不提供全局 Ent interceptor、privacy layer 或透明查询接管；
- 不提供可空、允许零值、可修改租户、自定义字段名等 Tenant mixin 模式；
- 不把 soft delete、disable、revoke 或领域命令伪装成标准 delete；
- 不生成跨表副作用、外部系统同步或业务特例；
- 不接入 Git，不检查工作树，不生成或解释 `git diff`，不自动合并、备份、提交或回滚业务修改；
- 不增加同一 worktree 的并发 generation、事务锁、自动 Recover 或旧事务重放。

## 事实所有权

| 事实 | Authoring owner | Nexa projection |
| --- | --- | --- |
| 实体 metadata | consumer Ent Schema 的 `nexaent.Schema(...)` | Entity IR、协议与代码 |
| 字段 metadata | consumer Ent field 的 `nexaent.Field(...)` | Entity IR、协议与代码 |
| CRUD 操作集合 | consumer Ent Schema 的 `nexaent.CRUD(...)` | CRUD 协议和默认 logic |
| 严格租户归属 | consumer 显式使用公共 Tenant mixin | 内部 tenant field、租户隔离 logic |
| 服务是否启用多租户 | consumer project provider 的 typed generation config | 生成选择与一致性校验 |
| 认证后的租户值 | consumer request context | CRUD invocation 的租户注入与过滤 |
| 生成后的 logic 修改 | consumer repository | 无反向投影 |

Service Catalog、生成 manifest、旧 metadata 和字段名称都不拥有上述事实。

## 公共 Tenant mixin

公共 authoring API 固定为：

```go
import nexamixin "github.com/nxnminieye/nexa/nexaent/mixin"

func (Account) Mixin() []ent.Mixin {
    return []ent.Mixin{nexamixin.Tenant{}}
}
```

`nexaent/mixin.Tenant` 没有配置字段。它生成的 Ent 字段等价于：

```go
field.Int("tenant_id").
    Positive().
    Immutable()
```

该 package 同时公开只读 machine contract：

```go
const TenantAnnotationName = "nexa.dev/ent-tenant-field/v1"
func TenantAnnotationSchema() []byte
func DecodeTenantAnnotation([]byte) error
```

Schema accessor 每次返回防御副本。Decode 只校验 marker，不返回可配置业务值。

Mixin 同时附加固定的 `nexaent.FieldMeta`：

| FieldMeta | 固定值 |
| --- | --- |
| label | key `nexa.tenant_id.label`，中文“租户 ID”，英文“Tenant ID” |
| description | key `nexa.tenant_id.description`，中文“记录所属租户的内部标识。”，英文“Internal identifier of the tenant that owns the record.” |
| UIHint | `UIHintReadonly` |
| visibility | `VisibilityInternal` |
| CRUD | `nil`，仅在 Entity 选择 CRUD 后由显式 marker 派生固定 policy |

固定语义如下：

- `tenant_id` 必填且必须大于零；
- 创建后不可修改，记录不能被迁移到另一个租户；
- mixin 不自动声明到名为 `Tenant` 的 Ent edge；
- mixin 不假设 consumer 拥有本地 Tenant Schema；
- mixin 为字段附加名称为 `nexa.dev/ent-tenant-field/v1` 的 framework-owned typed marker；
- marker 使用 v1 strict empty-payload schema，重复、未知字段或非空 payload 都非法；
- 生成器只认经过 strict decode 的 marker，不认字段名称；
- consumer 只使用 mixin，不直接 author marker。

该字段在标准 CRUD 中是服务端内部业务字段：

- 不进入外部 HTTP Create/Update DTO；
- 不进入任何外部 HTTP CRUD response；
- 进入内部 RPC request，作为认证上下文到 RPC logic 的 transport field；
- 保留在 Ent model 和数据库中，业务代码可以显式读取。

对于没有选择 CRUD 的实体，Tenant field 的 CRUD policy 保持 absent。对于同时选择 CRUD 的实体，
生成投影根据显式 Tenant marker 派生固定的 read-exclude、mutation-none policy。该行为是 Tenant mixin
本身的公共语义，不是字段名推断或 consumer 特例。

## 多租户启用条件

Consumer project provider 为每个服务提供 typed generation config：

```go
type MultiTenantConfig struct {
    Enabled bool
}

type ServiceProject struct {
    // 既有 service、Schema、Proto、API 与 toolchain 字段保持不变。
    MultiTenant MultiTenantConfig
    LogicRoot   string
}
```

`LogicRoot` 是 repository-relative 的 go-zero `internal/logic` 目录，例如 `backend/account/internal/logic`。
服务存在任意 CRUD Entity，或启用多租户且存在任意 Tenant mixin 时，该字段必填；其他情况忽略。它属于
consumer provider 的 typed project relation，不进入 Service Catalog、Schema annotation 或 sidecar。

标准 CRUD 租户隔离只在以下两个条件同时成立时生效：

1. 服务配置 `MultiTenant.Enabled = true`；
2. 当前 Entity 显式使用公共 Tenant mixin。

服务配置、Entity mixin 和可选 CRUD annotation 的状态矩阵固定为：

| 服务配置 | Entity mixin | 生成结果 |
| --- | --- | --- |
| disabled | absent | Entity 保持普通语义；有 CRUD annotation 时生成非租户 CRUD |
| enabled | absent | Entity 仍是全局 Entity；有 CRUD annotation 时生成非租户 CRUD |
| enabled | present | 生成 tenant helper；有 CRUD annotation 时生成租户隔离 CRUD |
| disabled | present | 生成失败，报告配置与 Schema 冲突 |

没有 CRUD annotation 的 Entity 始终不生成 CRUD。Mixin presence 不会反向创建 CRUD operation；但
Tenant mixin 与 disabled 配置的矛盾始终报错，不以 Entity 是否选择 CRUD 为前提。

旧 `ScopeTenant`、普通 `tenant_id` 字段和业务命名都不触发该矩阵。允许空租户、允许零值或同时承载
系统与租户数据的 Entity 使用普通字段和业务 logic，不扩展公共 mixin。

## Request context 类型

`generation/protocol.ContextValue` 继续是字符串枚举标识，其中 Tenant 的标识仍为 `"tenant-id"`。
枚举字符串不是租户业务值。

Context binding 属于 RPC method/request，不属于 HTTP route。目标 Proto contract 增加独立 method option：

```proto
message RPCContext {
  repeated ContextBinding context_fields = 1;
}

extend google.protobuf.MethodOptions {
  RPCContext rpc_context = 51002;
}
```

目标最新版同时从 `HTTPProxy` 移除 `context_fields`，保留原 field number/name 为 reserved。现有 Proto
authoring surface 在同一变更中直接迁移到 `rpc_context`；decoder 不读取旧位置，不合并两个来源，也不
提供 fallback。Protocol IR 在 method 层暴露 context bindings，即使该 method 没有 HTTP proxy 也可读取。
HTTP composition 只在 method 另有完整 HTTP proxy 时消费同一 method-level context facts，不反向创建
route、auth、permission 或 operation id。

生成的 request context 和 RPC target 使用以下类型：

- `TenantID`: `int64`；
- `SubjectID`: `string`；
- `RequestID`: `string`；
- `TraceID`: `string`。

`TENANT_ID` context binding 只能绑定到 singular `int64` RPC field。生成器为启用租户隔离的每个 CRUD
RPC request 增加 internal `tenant_id` transport field，并通过 `rpc_context` 将它标记为 `TENANT_ID`
binding target。
该字段不进入外部 HTTP request mapping；API mapper 总是使用认证后的 `RequestContext.TenantID` 写入它。
其他现有 context binding 继续使用 singular string。Core reference starter 中的 tenant RPC field 同步使用
`int64`，不保留 string tenant 兼容或双类型。

直接调用内部 RPC 的 consumer 必须在自己的认证边界提供同一可信 TenantID。CRUD logic 只读取 internal
RPC transport field，不接受另一个业务 tenant 参数，也不从 gRPC metadata、全局变量或字段名称推断租户。

在 Ent 持久化边界，生成 logic 对 `int64` 做一次受检查的 `int` 转换。缺失、零、负数或无法安全转换的
TenantID 都按无效认证上下文处理。

因此，“客户端不能提交 tenant_id”指外部业务 DTO 不暴露该字段；内部 RPC request 仍显式承载已经认证
并由 mapper 覆盖后的 `int64` TenantID。两者不是同一个 authoring surface。

## 标准 CRUD 行为

`nexaent.CRUD(...)` 继续接受非空的闭合操作子集：`list`、`get`、`create`、`update`、`delete`。
Annotation 缺失即不生成 CRUD。生成器不要求五个操作全部存在。

默认 logic 只实现无领域副作用的标准 Ent 行为：

- `list`: 分页查询并返回总数；
- `get`: 按 Entity identity 查询；
- `create`: 按 create field policy 创建；
- `update`: 按 identity、field mask 和 update field policy 更新；
- `delete`: 按 identity 做物理删除。

需要禁用、撤销、soft delete、跨表事务、授权同步或外部系统同步的操作属于业务 logic。Consumer 可以
不选择对应 CRUD operation，或在首次生成后修改业务拥有的 logic。生成器不提供 hook registry、
operation mode 或领域 adapter。

标准 CRUD 支持 Ent 当前正式生成的 builtin 数值宽度、bool、string、float、bytes、time、默认 enum、
`github.com/google/uuid.UUID`，以及所有合法 `TypeJSON` model，包括 struct、pointer、map、slice、array、
`json.RawMessage` 和 Ent 的 `field.Strings/Ints/Floats/Any`。EntityIR 继续只保存已冻结的逻辑 scalar，
不增加 exact Go type 字段，也不增加另一套 CRUD IR。

字段的 exact Go type 以 consumer 当前生成的 Ent model 为事实。Generator 在 entity loader 阶段读取 Ent graph
已经提供的类型信息，仅做以下资格判断，不解析 Ent 源码 AST：

- 标准 builtin、默认 enum、`google/uuid.UUID` 和合法 JSON model 可以参与 CRUD；
- 非 JSON 的自定义 `GoType(...)`、custom enum、named scalar、named time/bytes 以及非 Google UUID model，
  在字段实际被所选 operation 读取或修改时，以 `entity_ir_invalid / field_type_unsupported` 提前拒绝；
- 未选择 CRUD 的 Entity，以及没有被所选 operation 使用的 excluded/immutable 字段，不因该限制失败；
- identity 使用既有 `/entities/N/identity` pointer，普通字段使用 `/entities/N/fields/M/type` pointer。

该资格判断只限制标准 CRUD renderer 能可靠生成的类型，不限制 consumer 在普通 Ent Schema 或手写业务 logic
中使用自定义 Go 类型。

### Logic 输出契约

生成器从 `EntSchemaDir` 所在 module 与 Proto `go_package` 解析 Ent、RPC 和 service import path，并将
logic 写入 `ServiceProject.LogicRoot`。目录必须已属于同一 service module，且该 module 必须使用
Nexa go-zero 约定：

- Ent client package 位于 `<service-module>/ent`；
- service context 位于 `<service-module>/internal/svc`；
- context 类型是 `svc.ServiceContext`；
- Ent client 字段是 `ServiceContext.DB`，类型为 `*ent.Client`；
- logic package 名称是 `logic`。

每个 operation 生成一个 consumer-owned 文件：

```text
<LogicRoot>/<operation><entity>logic.go
```

文件中的公开形状与 go-zero 保持一致：

```go
type ListAccountLogic struct {
    ctx    context.Context
    svcCtx *svc.ServiceContext
    logx.Logger
}

func NewListAccountLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListAccountLogic
func (l *ListAccountLogic) ListAccount(in *pb.ListAccountRequest) (*pb.ListAccountResponse, error)
```

文件是完整可运行的默认实现，不是空 hook。统一 CRUD 命令生成 Proto fragment 与 logic source，但不重复
拥有 Proto 到 Go 的通用生成职责；随后仍由既有 `generation rpc` 入口从更新后的 Proto 生成 RPC Go。
在 plan 发布任何文件前，CRUD staging 使用 `ServiceProject.RPCGoTool` 对 candidate Proto 生成临时 RPC Go，
再通过 overlay 将 candidate logic、临时 RPC Go 与一个 validation-only wiring probe 同当前仓库中的
`ServiceContext`、Ent Go 一起做真实 Go typecheck。Probe 从已验证 CRUD snapshot 派生，在 service module 的
内部校验 package 中逐一构造 `New<Method>Logic(...)` 并调用对应 method；它只验证 server 所需的 constructor、
method、request 和 response 接线形状，不发布，也不进入 transaction input 或 manifest。

每次 validation 使用调用独占、初始为空且与 repository 不重叠的 staging root。RPC tool 执行后，只接纳
candidate Proto `go_package` 对应的预期 RPC Go 输出；staging 中任何试图提供 `internal/svc`、Ent model、
logic 或其他 repository package 的 `.go` 文件都必须拒绝，不能覆盖当前仓库事实。Candidate logic/helper
直接来自 sealed plan bytes，当前 Ent 和 `ServiceContext` 始终从 repository 读取。Repository root、staging
root 为空、重叠或无法 canonicalize 时在执行 tool 前失败。

Typecheck Go tool 不新增 provider field 或 tool role；统一 CRUD command 复用当前 service 已由 provider 选择、
按 `ent-crud` role 校验并完成 version probe 的 `ServiceProject.EntCRUDTool`，内部原样映射为 validation
`GoTool`。该 tool 必须是实际 Go executable。Closed `PATH` 只包含其 canonical executable 所在目录；
它只约束已启动 Go 子进程看到的 PATH，不负责选择 `packages.Load` 的 binary。调用 `packages.Load` 前，
validator 使用 ambient `exec.LookPath("go")` 解析 `go/packages` 将实际选择的 executable，canonicalize 后必须
与已 probe 的 `EntCRUDTool.Executable` 精确相同；不一致直接失败。这里的 `LookPath` 只验证实际选择，
不得用它替换 provider tool。

`packages.Config.Env` 从空环境构造，不继承 ambient Go 配置；固定
`GOPACKAGESDRIVER=off`、`GOWORK=off`、`GOENV=off`、`GOTOOLCHAIN=local`、
`CGO_ENABLED=0`，并将 `HOME`、`TMPDIR`、`GOPATH`、`GOCACHE`、`GOMODCACHE` 指向本 invocation staging
内的独立目录。其他允许的非敏感 Go 环境由同一 `EntCRUDTool` contract 显式提供。`go/packages` 同时固定
`-mod=readonly`。

`-mod=readonly` 只保证正常 Go module resolution 不补写 `go.mod/go.sum`；`GOWORK=off` 排除
`go.work/go.work.sum` 参与。RPCGoTool、Go tool 与 Runner 是调用方已选择并完成 probe 的 trusted tool
boundary，`toolchain.WriteScopes` 只是声明和 preflight，不是 filesystem sandbox。实现测试必须证明这些选定
正常工具执行前后 repository fixture 全树无持久变化，但该证据不声称可以隔离恶意 executable。若未来要求
约束任意外部进程写入，必须另行审核真正的 filesystem sandbox，不能从本设计推导。

临时 RPC Go 只用于验证，不发布，也不进入 CRUD manifest；既有 `generation rpc` 仍是 RPC Go 的唯一发布
owner。Candidate message 必须是 protoc 生成的 direct struct，candidate enum 必须是 direct `int32` defined
type；message/enum alias 或借用旧 PB package 都拒绝。RPC tool 与 Go tool 的稳定 identity/version/probe、
candidate Proto、candidate logic、wiring probe、closed typecheck environment 的非敏感语义以及本次实际读取的
repository-relative Go path/content digest 进入 validation digest。绝对 staging/cache path 和 executable path
不进入 canonical bytes，也不对 executable bytes 做 attestation。

Ent selector/setter 名称与 protobuf Go 字段名称使用两套明确规则：Ent 名称遵循 Ent v0.14.5 的完整 initialism
集合，protobuf 字段遵循 protoc-gen-go 的 Go naming（例如 `id -> Id`、`tenant_id -> TenantId`、
`required_uuid -> RequiredUuid`）。两者不得共用同一个 identifier formatter。

因此 `LogicRoot`、Proto `go_package`、constructor/method naming 或 `ServiceContext.DB` 不匹配时，plan 在仓库
写入前失败；默认创建和显式覆盖使用同一验证强度。框架不增加字符串形式的 client selector、任意
constructor template 或 runtime adapter registry。

## 租户隔离行为

启用租户隔离的标准 CRUD 固定执行以下行为：

- `create` 从认证后的 request context 注入 `tenant_id`，客户端不能提交或覆盖该值；
- `list` 自动增加当前 `tenant_id` predicate；
- `get`、`update` 和 `delete` 使用 Entity identity 与当前 `tenant_id` 共同定位；
- 认证上下文缺少 TenantID、TenantID 为零或负数时，返回 `Unauthenticated`，固定消息为
  `tenant context is required`；
- 目标不存在或属于其他租户时统一返回 `NotFound`；
- 生成器不先做全局查询，不向调用方暴露其他租户是否存在目标记录。

`PermissionDenied` 保留给业务已经明确识别出可信跨租户目标的领域操作；标准 CRUD 不创建该探测路径。
`InvalidArgument` 保留给调用方显式提交的普通请求参数错误。

### 普通 CRUD 错误语义

默认 logic 使用固定 gRPC status，不把原始数据库错误文本返回给调用方：

| 条件 | gRPC code | 固定 message |
| --- | --- | --- |
| identity 不满足下述类型规则 | `InvalidArgument` | `invalid identity` |
| offset/limit 无法安全转换为 Ent 使用的 `int` | `InvalidArgument` | `invalid pagination` |
| Update 的 field mask 缺失或为空 | `InvalidArgument` | `update_mask is required` |
| field mask 包含未知、identity、tenant、internal 或非 update 字段 | `InvalidArgument` | `update_mask contains unsupported field` |
| TenantID 缺失、非正数或无法安全转换 | `Unauthenticated` | `tenant context is required` |
| get/update/delete 未定位到记录，包括跨租户目标 | `NotFound` | `entity not found` |
| Ent field validation error | `InvalidArgument` | `invalid field value` |
| Ent constraint error | `FailedPrecondition` | `constraint violation` |
| 其他 Ent 或数据库错误 | `Internal` | `crud operation failed` |

List 的 `offset` 和 `limit` 保持协议中的 `uint64`。`limit = 0` 是有效的零条结果请求，仍返回 total；生成器
不引入隐藏默认页大小或最大页大小。Consumer 需要不同分页策略时修改业务拥有的 logic。

Identity 校验覆盖当前 closed identity 类型：

- `int64`: 必须大于零，并且能安全转换到对应 Ent identity Go 类型；
- `uint64`: 必须大于零，并且能安全转换到对应 Ent identity Go 类型；
- `string`: 空字符串非法，非空字符串按原值查询，不 trim、不改写；
- `uuid`: 必须是可解析且非零的 UUID；
- 其他 identity 类型在 generation 阶段拒绝，不生成 logic。

Ent 的 `Int8/16/32/Int/Int64` 与 `Uint8/16/32/Uint/Uint64` 都使用对应 exact model type。Proto 值写入
Ent 前必须做不截断、不饱和、不 wrapping 的受检查转换；转换后回转不等即返回
`InvalidArgument / invalid field value`，identity 则返回 `InvalidArgument / invalid identity`。Ent 数值读回
Proto 时只做无损扩大转换。TenantID 保持独立的 `int64 -> int` 正值检查，失败仍是
`Unauthenticated / tenant context is required`。

Required Timestamp/JSON request field 为 nil 时返回 `InvalidArgument / invalid field value`。Timestamp 输入
必须通过 protobuf validity 检查，持久化值无法编码为有效 Timestamp 时返回 `Internal / crud operation failed`。
JSON 写入固定为 `structpb.Value -> JSON bytes -> exact Ent model`，decode 失败返回
`InvalidArgument / invalid field value`；JSON 读出固定为 `exact Ent model -> JSON bytes -> structpb.Value`，
encode/decode 失败返回 `Internal / crud operation failed`。只有合法 JSON `null` 才映射为 protobuf Null，
不得把转换错误静默替换成 Null。

`google.protobuf.Value` 的 number 使用 double；超过其精确整数范围的 JSON number 不在本版本保证内。
支持任意精度 JSON number 需要修改公共协议并另行审核，不能在 renderer 中增加兼容分支。

Create 的 required 和 Ent validator 失败按 field validation error 投影；生成器不解析数据库 vendor 文本
来猜测更细错误。Update mask 使用 Proto field name，重复 path、嵌套 path 和空 path 都属于 unsupported
field。Delete 固定为物理删除，不从 status 字段推断 soft delete。

## 手写业务 logic 的切入点

当 service 设置 `MultiTenant.Enabled = true` 且至少有一个 Tenant mixin Entity 时，生成器持续管理以下
helper，不要求该 Entity 同时选择 CRUD：

```text
<LogicRoot>/crudtenant/tenant.generated.go
```

公开给该 service 内部使用的签名固定为：

```go
func RequireTenantID(value int64) (int, error)
```

该 helper 只负责：

1. 接收 internal RPC request 中的 TenantID；
2. 校验正值；
3. 安全转换为 Ent 使用的 `int`；
4. 返回与标准 CRUD 相同的认证错误。

默认 CRUD logic 和手写业务 logic 都调用 `crudtenant.RequireTenantID`，随后使用 Ent 自己生成的
`<entity>.TenantIDEQ(tenantID)` predicate。Nexa 不包装 Ent client，不自动修改任意 query，也不拦截
业务直接访问 Ent 的行为。没有 Tenant mixin Entity 时不生成空 helper package。

## 统一 CRUD 生成入口

目标 CLI 使用一个 CRUD 命令族，同时消费同一份 Entity facts 并生成 Proto 与默认 logic：

```text
nexactl generation crud plan
nexactl generation crud plan --overwrite-logic
nexactl generation crud check
nexactl generation crud check --overwrite-logic
nexactl generation crud write
nexactl generation crud write --overwrite-logic
```

该目标入口替代当前只生成协议的 `generation crud-proto` 命令族，不保留旧命令别名。实现完成前，
实际命令仍以 `nexactl inspect --json` 为准。

同一次 plan/check/write 中：

- Proto、compatibility lock 和 manifest 是持续受控的 generated artifacts；
- logic 是 create-once 的 consumer-owned source；
- logic 不存在时生成完整可运行的默认实现；
- logic 已存在时默认跳过，不比较内容，也不报告为 drift；
- `--overwrite-logic` 必须在 plan/check/write 使用同一模式；
- overwrite plan 明确列出每个将被直接覆盖的 consumer-owned logic 路径，并把 mode 与 candidate digest
  纳入 canonical plan input、plan digest 和 structured result；
- overwrite write 只能执行同一 source snapshot、同一 mode 和同一 exact write set 的 accepted plan；
- write 缺 flag、额外增加 flag，或 mode 与 accepted plan 不一致时直接拒绝，不能扩大写集；
- accepted overwrite plan 执行时，直接以当前 framework 版本的默认实现覆盖目标 logic；
- 覆盖不读取 Git 状态，不要求 clean worktree，也不生成 diff 或备份；
- flag 只改变 logic 的覆盖选择，不改变 `nexaent.CRUD(...)` 事实或 Proto 生成规则。

默认 create-once 行为复用现有 manual artifact 语义。显式覆盖是命令级、可审计的普通文件写入模式，
不会把 logic 重新登记为持续 managed artifact，也不会让后续无 flag 的生成开始覆盖它。

## 写入与失败边界

统一 CRUD 生成沿用普通 serial staged publish：

- 同一 worktree 的并发 generation 明确 unsupported，由调用方串行调度；
- 每次 invocation 使用唯一 staging；
- staging 内完成 Proto parse、Go parse/format、临时 RPC Go 生成和 candidate logic 的真实 Go typecheck；
- 发布前重新检查输入和当前目标文件没有在本次 invocation 期间漂移；
- 每个目标使用普通原子单文件替换；
- 失败报告真实错误并 best-effort 清理本次 staging；
- 不自动回滚已经发布的文件；
- 下次从当前工作树重新生成，不恢复或重放旧 invocation。

这些是代码生成的文件安全边界，不是跨进程事务或崩溃恢复协议。

## 当前契约与目标契约的切换

本文是尚未实现的目标契约。实现合入前，以下当前文档继续描述现有二进制：

- `docs/contracts/controlled-generation.md` 中的 `generation crud-proto` 命令与 manual artifact 规则；
- `docs/architecture/framework.md` 中“CRUD 只生成 Proto”的说明。

实现任务必须在同一候选中更新上述两份权威文档、CLI inspect capability、命令 schema 和公开使用文档，
然后用代码与行为测试证明切换完成。本文对应的设计 commit 不单独合入发布分支，避免目标设计与当前实现
同时成为公开真相。切换后统一入口直接替代 `generation crud-proto`，不保留 alias 或兼容分支。

## Consumer 所有权边界

Consumer 必须逐 Entity 显式决定是否使用 Tenant mixin 和 `nexaent.CRUD(...)`。公共框架不保存具体
业务 Schema 清单，也不因某个 consumer 当前没有合适的标准 CRUD 对象而创建示例业务表。

旧 annotation、parser、adapter、业务 metadata、具体 Schema 选择和迁移批次属于对应 consumer adoption
plan，不进入 framework 公共架构文档。Framework 只保证最新 public contract，不为 consumer 保留旧协议
兼容路径。

## 验证要求

实现至少需要以下行为证据：

1. 外部 consumer 可以 import 并使用公共 Tenant mixin；
2. Tenant marker strict decode 拒绝重复、未知字段和非空 payload；
3. Tenant field 是 required、positive、immutable，且没有隐式 Ent edge；
4. generator 只根据 typed marker 和服务 config 选择租户隔离；
5. 四种 config/mixin 状态矩阵均有测试；
6. 外部 HTTP CRUD DTO 和 response 均不含 tenant field；
7. 独立 `rpc_context` 可在没有 HTTP proxy 时解析，旧 HTTPProxy context 位置被拒绝且无 fallback；
8. 内部每个租户 CRUD RPC request 含 `int64 tenant_id` binding，mapper 使用可信 context 覆盖；
9. 其他 context 类型不漂移，string tenant target 被拒绝；
10. CRUD operation 子集和 int64/uint64/string/UUID identity 均按固定规则工作；
11. operation x identity 矩阵至少覆盖 List+UUID、Get+string、Create+窄 numeric、Update+JSON/Timestamp、
    Delete+uint；全部五项 operation 仍在组合 fixture 中覆盖；
12. 标准 signed/unsigned widths 以 exact Ent model type 编译，越界写入稳定失败且不发生截断；
13. typed JSON、required JSON/Timestamp presence、JSON/Timestamp 编解码失败均按固定错误语义处理；
14. 非 JSON custom `GoType` 只在实际参与所选 CRUD 时由 entity loader 提前拒绝；
15. go-zero logic 的 path、package、constructor、method 与 `ServiceContext.DB` contract 可编译；
16. validation-only wiring probe 对每个已选 operation 调用 constructor 和 method；
17. staging 不能遮蔽 repository Ent、`ServiceContext` 或 logic，message/enum alias 不能绕过 candidate gate；
18. repository/staging root 为空或重叠时在 tool 执行前失败；closed environment 关闭 workspace、ambient
    GOENV 与 external package driver，`-mod=readonly` 阻止正常 module 补写；
19. validation GoTool 精确复用 provider 的 `EntCRUDTool`；ambient `LookPath("go")` 的 canonical 结果与
    该 tool 匹配时才允许进入 `packages.Load`，不匹配则在执行前失败；closed PATH 只约束已启动子进程；
20. 选定 trusted RPCGoTool/Go tool/Runner 的正反 fixture 都比较 repository 全树前后状态并保持无持久变化；
    该测试不表述为对恶意 executable 的隔离证明；
21. create 注入租户，list/get/update/delete 均带租户 predicate；
22. 普通参数、field mask、Ent validation、constraint、not-found 与 internal 错误按固定表投影；
23. 无效 context 返回 `Unauthenticated`，跨租户目标返回 `NotFound`；
24. `crudtenant.RequireTenantID` 与标准 CRUD 使用相同校验和转换；
25. 默认生成不会覆盖已有 logic；
26. overwrite mode 进入 plan digest 和 exact write set，mode 不匹配的 write 被拒绝；
27. default/overwrite candidate 在写入前均通过临时 RPC Go + 当前 Ent/ServiceContext 的真实 typecheck；
28. validation digest 对 plan、RPC/实际 EntCRUD Go tool identity、closed env 语义、candidate、probe 和读取文件双向绑定：
    任一已声明输入变化使旧 digest 失效，同一输入重建得到同一 digest；
29. helper execution owner 固定为 `nexa.dev/generator/crud-logic/v1`，既有 manifest artifact owner 仍为
    `crud-proto`，两层 ownership 不混用；
30. accepted `--overwrite-logic` plan 直接覆盖 logic，之后无 flag 的生成再次跳过；
31. Proto、logic 和 manifest 在真实外部 consumer 中可以生成、编译并运行；
32. 同一输入重复生成的受控产物保持确定性；
33. 生成失败不创建锁、恢复状态或 Git 副作用。

Consumer adoption 的定向 tests、race、vet、generated diff、回滚和重新应用要求由对应 adoption plan 根据
实际写集确定，不属于所有 framework consumer 的无条件公共义务。
