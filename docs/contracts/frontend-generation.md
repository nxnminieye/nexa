# 前端生成契约

Nexa 拥有 `FrontendPageSpec`、`FrontendIR` 与 renderer request；Vben 拥有通用 renderer 和运行时；consumer
拥有 PageSpec、HTTP API 业务事实、handler/context provider、扩展源码和 frontend source lock。PageSpec 只选择
API contract 的 UI 投影，不复制权限或业务事实。permission code 按完整字符串精确匹配，不推断层级、通配或父子关系。

## PageSpec v1

`nexa.dev/frontend-page-spec/v1` 是 closed JSON/YAML document。未知字段、重复 key、大小写别名和动态 route
参数均拒绝。顶层必填 `mode`（`collection|singleton`）与 `accessOperation`。`accessOperation` 只能指向 collection
的 primary `list` 或 singleton 的 `get`；IR 显式投影其 permission。permission 为空表示不做 UI permission gate，
也不得调用 `hasPermission`；非空 permission 的 `hasPermission` 仍只控制 UI 呈现，不能替代服务端授权。访问 operation
permission 被拒绝时，route/page fail closed，且不得调用 access operation。action 只在其 operation permission 允许时显示
和调用；action 任一 field 引用的 options operation permission 被拒绝时，整个 action 隐藏且不得请求 options。服务端始终是
授权事实源，UI gate 不是安全边界。

operation role 封闭为 `list|get|action|options`，每项必填 `contextBindings[]`。collection 恰好一个 `list`、禁止
`get`，可有多个 `action/options`；singleton 恰好一个 `get` 且无其它 operation/action。每个 `action` operation
必须且只被一个 `actions[]` 引用，每个 `options` operation 至少被一个 field options 引用。

`contextBindings[] { context, path }` 的 context 使用稳定 ID grammar；path 是 request DTO 的 direct leaf，目标必须是
required string 或准确 integer scalar。同一 context ID 在整个 IR 中保持相同 scalar name。auth、tenant 等由 context
provider 注入，不进入 form field。

list/options 的 result 必填 `itemsPath,totalPath`，且必须使用 offset pagination：

```text
pagination { mode: "offset", limitPath, offsetPath, totalPath, pageSize }
```

list 还必填相对 `itemsPath` 的 `rowKeyPath`，终点只能是 required、非 optional 的 non-empty string 或 integer scalar；
不能越过其它 array/map。非 list 禁止 rowKeyPath。singleton get 的 `itemPath` 可省略（response root），也可指向
object/ref。

## Field、binding 与 action

Field surface 封闭为 `search|list|detail`，`surfaces` 可为空。声明 surface 时必须有对应 binding：search 对 primary
list request 且有 control，list 对 primary list response，detail 对 singleton get response。list response binding
可以不声明 list surface，表示仅投影到 row 供 action 使用。完全没有 surface、action.fields、options 或 hidden row
source 消费的 field 被拒绝。

所有 request binding 只能是 direct scalar 或 scalar-array leaf。response path 可嵌套，但 list binding 必须严格位于
itemsPath 下，singleton detail binding 必须位于 itemPath 下（省略 itemPath 时相对 response root），且只能穿越 list
itemsPath 那一个 array，不能穿越 map。响应 binding 不允许指向 action/options。

页面级 `(operation,direction,path)` 唯一，field 级 `(field,operation,direction)` 至多一个。每个 operation request DTO
的 required direct leaf 必须且只能由 field request binding、context binding、pagination limit/offset 之一覆盖；可选
未绑定 leaf 省略。object/ref/map/nested request leaf 在 v1 拒绝。

同一 field 的全部 binding 保持准确 underlying type，包括 `scalar|ref|object|array` identity、scalar/ref name、array
cardinality 与 element；ref/object 不得伪装成 scalar，optional/map 不进入 renderer `valueType`。IR 为每条 binding 投影
`valueType` 和整路径 `required`：任一父字段 `required=false`、optional wrapper 或允许穿越的 optional array element 都使
结果为 false，而不是只读取 terminal field。required response 可进入 optional request；optional response 不得进入 required hidden row
request。controlled form 提交 required target 前必须校验；optional `undefined` 省略，绝不补 null 或零值。control/type
固定为 toggle/bool、select/string、multi-select/string array、number/numeric、text/password/textarea/string。

`actions[]` 必填 `effect`（`create|update|delete`）和 `fields`（可空 field ID 数组）。create 只能 toolbar；update/delete
只能 row。effect 只选择固定 lifecycle，不从 HTTP method 或 operationId 推断。action.fields 是用户输入全集：每个成员
必须有 control 和指向该 action operation 的 request binding，反向也成立。未列入 fields 的 action request binding 是
row source，必须由同一 field 的 primary list response 唯一提供；toolbar create 禁止 row source。

options 只通过 `field.options { operation,valuePath,labelPath }` 使用；对应 field control 必须是 `select|multi-select`，必须被
至少一个 `action.fields` 引用，且不得出现在 search/list/detail；options 本身不构成 field 已消费。operation 自身拥有
result/pagination。value/label 相对 option item，终点必须是 string scalar，不能穿越额外 array/map。options required
request closure 与普通 operation 相同。静态 `choices` 与动态 `options` 在 v1 互斥；两者都要求 `select|multi-select`，
choice value 必须唯一。

`columns[] { id,labelKey,path }` 仅用于 access operation response 中恰好一个 binding 所指向的 `array<ref|object>`。
这种 field 有 display surface 时 columns 必须非空，且 control 必须为空；其它类型声明 columns 均拒绝。column path 相对数组
item，可穿越 object/ref/optional object，但不能穿越 map 或中间 array；终点只能是 scalar 或 `array<scalar>`，后者 element
不得 optional。IR 为每列补充准确 `valueType`，并以 binding、所有中间字段及终点的 required/optional 联合计算整条路径的
`required`。

## Canonical IR 与 schema

`nexa.dev/frontend-ir/v1` 内嵌 canonical HTTP APIIR、来源 union/digest、locale 和排序后的 page。每个引用 operation 显式
携带 request/response type identity、permission、context exact type；binding 显式携带 exact value type/presence，Node
renderer 不得猜测 DTO。PageSpec、Locale、IR 与 renderer request schema 分别为：

- `frontend-page-spec-v1.schema.json`
- `frontend-locale-v1.schema.json`
- `frontend-ir-v1.schema.json`
- `frontend-render-request-v1.schema.json`

schema 均 fail closed；IR schema 通过 `$ref` 绑定正式 HTTP APIIR schema，Go canonical 路径实际注册并验证两者。
canonical bytes 统一使用 JCS。跨语言 corpus 位于 `generation/frontend/testdata/renderer-contract/v1/`，manifest 固定每个
fixture 的 SHA-256、accept/reject 与稳定 code；包含 empty/nonempty IR/request，以及 unknown field、wrong version、
noncanonical bytes、API/source digest、APIIR semantic、排序、access/context/result/pagination/row key、action/options、
ref identity、optional-parent presence、binding closure 和 scope/path 负例。测试只读校验 corpus，不写工作树。

renderer 输入参考 validator 按固定顺序执行：1 MiB 上限、strict JSON、closed schema、JCS、repository/scope/digest，随后以
稳定 `DomainSource` 调用 `httpapi.ParseSnapshot` 验证 embedded APIIR 的 schema、canonical/source digest 与完整 semantics；
再校验 `apiDigest=SHA256(api)`、排序且唯一的 IR source union、复算 `sourceDigest`、API source 子集及 page/locale source ref。
IR source union 必须精确等于 embedded API sources、全部 page `specSourceRef` 与 locale `sourceRef` 的去重并集，重算
`sourceDigest` 也不能使额外 source 合法化。
最后校验 Build 规定的全部排序和 page/operation/field/action/options/columns cross-reference 与 request closure。任何阶段均
fail closed，不因 renderer 语言不同放宽 Go Build contract。

Locale message key 在 IR 中保持 flat，Vben renderer 必须按 `.` 物化为 vue-i18n 嵌套对象；Build 拒绝 `a` 与 `a.b`
同时成为 required key的 prefix collision。`-` 不是层级分隔符。

## Renderer 与运行时

`nexa.dev/frontend-renderer/v1` request 包含 canonical `frontendIR`、absolute normalized `repositoryRoot`、唯一
`generatedScope`、排序后的外部 `extensionScopes` 与 source lock digest。路径在首次写入前拒绝 escape、`.git` 的
case-fold alias、collision 和 overlap。空 pages 仍清空输出 scope 并调用 renderer；Vben 稳定 router/locale 入口必须
使用 empty-safe `import.meta.glob` bridge，不能静态 import 一个可能不存在的 generated index。

renderer 从 embedded APIIR 生成 referenced operation 的 canonical PascalCase mapped DTO。Example handler 必须用
`satisfies` 穷尽所有 page/options 引用的 operationId，handler 收发 canonical DTO，再由 consumer 映射 HTTP wire。
context provider 同样生成 exact mapped type 并用 `satisfies` 穷尽。缺少配置 fail closed：

renderer 只生成页面实际引用 operation 的 request/response 可达类型闭包；同一个 registry request/response type 只生成
一个 DTO。ref 保留其 canonical name；inline object 不自带 name，由 owning DTO 与完整 field path 确定稳定 PascalCase
interface 名。两者都保留属性 path；array 映射 `Array<T>`；API `optional` 或
`required=false` 映射 optional property / `T | undefined`，绝不映射 `null`。map、`interface{}`、unknown scalar fail closed。
`string -> string`、`bool -> boolean`。`int,int8,int16,int32,int64,uint,uint8,uint16,uint32,uint64,float,float32,float64,number`
均映射 finite JavaScript `number`，但用准确 scalar name brand 保持身份，不得折叠。integer 必须是 safe integer；uint 还必须
非负；int8/16/32 与 uint8/16/32 校验各自范围；int64/uint64 超出 safe integer 即失败。`bytes` 仅通过
`array<uint8> -> Array<branded uint8>` 表达，不生成 Blob。

- `NEXA_FRONTEND_HANDLER_MISSING`
- `NEXA_FRONTEND_CONTEXT_MISSING`
- `NEXA_FRONTEND_RESULT_INVALID`
- `NEXA_FRONTEND_TOTAL_INVALID`
- `NEXA_FRONTEND_ROW_KEY_INVALID`
- `NEXA_FRONTEND_ROW_KEY_DUPLICATE`

collection page 从 1 开始，`offset=(page-1)*pageSize`；改变 pageSize 回到 page 1。total 必须是 finite、non-negative safe
integer。integer row key 必须 `Number.isSafeInteger`，string 必须非空；运行时投影到保留字段 `__nexaRowKey`，string 加
`s:`、integer 加 `i:`。页内非法或重复 key 确定性失败，renderer 不假定 VXE 支持 nested keyField。

action 防重复提交；提交期间禁止二次提交和关闭。失败保留 modal/form，不改变 rows；cancelled error 不展示。create
成功关闭并 reload page 1；update 成功关闭并 reload 当前页；delete 成功 reload 当前页，若为空且 page>1，仅回退一页
再 reload。只有显式 `confirmKey` 才 confirm。runtime 是 action error presentation 唯一 owner，consumer request
interceptor 不得重复 toast。managed-role 不做客户端业务预禁用。

动态 options 只在已授权 action modal 打开且 dependency request/context 已就绪时加载。每次打开及 dependency 变化都从头
生成；每个 field/action 最多一个 in-flight 请求，使用 `AbortController` 取消旧请求，关闭或 unmount 必须取消，已过期结果
必须丢弃。分页从 offset 0 开始，`limit=pageSize`，后续 offset 等于已收集数量，并串行读取直到首次响应给出的 total。
每页 total 必须一致且是非负 safe integer，items 必须为 array、每页不超过 pageSize；`total=0` 只允许空页，在达到 total
前出现空页或累计超过 total 均失败。value/label 必须是非空 string，重复 value 以稳定
`NEXA_FRONTEND_OPTION_VALUE_DUPLICATE` 失败。options 失败以内联 action form 状态呈现、禁用 submit，并允许从 offset 0
重试；transport interceptor 不得另发 toast。

## 验证边界

Nexa 验证 strict parse、cross-reference、closure、canonical identity、schema、request/path gate 与 empty projection。
Vben 验证 renderer、adapter 和运行时；Example 验证真实 handler/context、API、typecheck/build 与浏览器 E2E。
