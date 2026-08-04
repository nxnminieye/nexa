# Nexa 统一事实注释协议 v1

状态：冻结

契约标识：`nexa.dev/source-comment/v1`

所有者：Nexa

## 目标与边界

Nexa 从 Ent、Proto、原生 `.api` 或 comment-capable frontend source contract 构建同一份
typed FactGraph。越早出现的事实必须在越早的事实源声明；下游可以增加本阶段才出现的 node/fact，
但不得覆盖、删除或重复声明上游事实。

FactGraph 是按 source stage 排序的有向森林：每个 semantic node/fact 从最早实际拥有它的
authoring stage 开始，独立 root 可以同时存在；projection edge 才表达可证明的下游继承关系。
不存在 Ent 时，Proto、`.api` 或 page 可以直接作为 root。生成器和 runtime 只能消费校验后的
typed facts，不能直接解释注释字符串、任意 map、JSON metadata 或运行时 mapping。

## Carrier 与语法

v1 支持 Go/Ent、Proto、`.api` 与固定的 YAML frontend source contract。TypeScript/Vue 仅是确定性生成输出，
v1 不把它们作为 authoring carrier。每个包含
directive 的文件必须且只能声明一次：

```text
// @nexa $contract: "nexa.dev/source-comment/v1"
```

YAML 使用 `#` 注释前缀。每条 directive 独占一行：

```text
<comment-prefix> @nexa <key>: <json-value>
```

key 必须满足 `[a-z][A-Za-z0-9]*(\.[A-Za-z][A-Za-z0-9-]*)*`；系统 key 以 `$` 开头。
v1 只保留 `$contract` 与 `$source`。value 必须是单行严格 JSON，string 必须加双引号，禁止
`null`、模板、表达式、环境变量或代码。未知 key、错误类型、非法枚举和错误挂载位置一律失败。

directive 必须紧邻目标 semantic node，绑定由对应语言 AST、descriptor 或 source-location 完成，
不能用正则猜测最近标识符。JSON 不支持本协议；JSON 可以是确定性生成物，但不能成为 authoring
carrier，也不能通过 metadata object 建立第二 DSL。

## 原生事实与补充事实

语言已经表达的结构继续由原生语法拥有：

- Ent：字段类型、optional/nillable/default/unique/validator/index/edge；
- Proto：message、field type/number、service 与 RPC；
- `.api`：type、field、operation、method 与 route；
- frontend YAML：page identity。

`@nexa` 仅声明原生结构无法表达但生成确实需要的补充事实，例如多语言 label/description、CRUD
选择、scope/visibility、auth/permission 和 UI presentation。注释不得重复原生类型、字段号、
method 或 path。

## Registry

每个 registry entry 必须定义 exact key 或封闭 pattern、value type/enum、允许的 node、最早 stage、
传播规则、消费者和安全属性。标准 namespace 是：

- `label.*`、`description.*`；
- `crud.*`；
- `scope`、`visibility`；
- `auth`、`permission`；
- `ui.*`；
- `route.*`、`menu.*`；
- `$*`。

v1 不提供 `x-*` 或 consumer 自定义 namespace。新增事实必须先进入所属 canonical contract registry。

v1 首版 registry 是以下闭合集合。`schema`、`field`、`rpc`、`operation` 和 `page` 是 semantic node
类型，不是任意字符串 target。

| Key | Value | Node | Earliest stage | Propagates to | Consumer | Security |
| --- | --- | --- | --- | --- | --- | --- |
| `label.zh-CN`、`label.en-US` | non-empty string | schema, field, message, rpc, operation, page | node first stage | all downstream | frontend | no |
| `description.zh-CN`、`description.en-US` | non-empty string | schema, field, message, rpc, operation, page | node first stage | all downstream | frontend | no |
| `crud.operations` | unique array of `list|get|create|update|delete` | schema, message, API type | Ent, Proto when no Ent exists, or native `.api` when no earlier source exists | API/frontend | CRUD compiler | yes |
| `crud.read` | `include|exclude` | field | Ent, or Proto when field has no Ent source | API/frontend | CRUD compiler | yes |
| `crud.mutation` | `none|create|update|create-update` | field | Ent, or Proto when field has no Ent source | API/frontend | CRUD compiler | yes |
| `scope` | `global|tenant` | schema, message, API type | Ent, Proto when no Ent exists, or native `.api` when no earlier source exists | all downstream | backend compiler | yes |
| `visibility` | `public|internal|sensitive` | field | field first stage | all downstream | backend compiler | yes |
| `auth` | `required|none` | rpc, operation | RPC/API operation first stage | API/frontend | HTTP compiler | yes |
| `permission` | lower-dot identifier string | rpc, operation | RPC/API operation first stage | API/frontend | HTTP compiler | yes |
| `http.method` | `GET|POST|PUT|DELETE` | rpc | Proto | generated `.api` native operation | HTTP compiler | no |
| `http.path` | canonical relative HTTP path | rpc | Proto | generated `.api` native operation | HTTP compiler | no |
| `ui.control` | closed control enum | field | field first stage | frontend | frontend compiler | no |
| `ui.reference` | object `{target,display}` resolved to typed `NodeRef + FieldRef` | field | Ent, or field first stage without Ent | frontend | entity/frontend compiler | yes |
| `ui.entity` | semantic schema/message reference resolved to typed `NodeRef` | page | frontend | frontend | frontend compiler | no |
| `ui.pageSize` | integer 1..100 | page | frontend | frontend | frontend compiler | no |
| `ui.extensionComponent` | canonical repository-relative component id | page | frontend | frontend | frontend compiler | no |
| `route.path` | absolute lower-kebab route path | page | frontend | frontend | frontend compiler | no |
| `route.name` | canonical route identifier | page | frontend | frontend | frontend compiler | no |
| `route.icon` | non-empty icon identifier | page | frontend | frontend | frontend compiler | no |
| `menu.order` | integer | page | frontend | frontend | frontend compiler | no |

`ui.control` 的封闭值为 `text|textarea|number|switch|select|multi-select|datetime|readonly|sensitive|member|reference|attachment|tags|component|i18n|iconify|permission|route|scope|http-method|http-path|module|locale|timezone`。
`ui.reference` 只在 compiler 中解析；`target` 必须唯一指向当前 FactGraph 的 schema/message node，
`display` 必须唯一指向该 node 的 field。解析后只向 typed IR 传递 `NodeRef + FieldRef`，不得把原始对象、
字符串路径或 source/target mapping 交给 renderer/runtime。

localized output key 由 semantic node identity 与 locale 确定性生成，不另设人工 `label.key` 或
`description.key`。标准 CRUD 的 operation、client method、分页字段、Formily validation 和 Grid column
由结构及上述 facts 推导；registry 不提供 per-operation binding、itemsPath/totalPath 或 wire rename。
Proto RPC 仅在需要投影为 HTTP operation 时声明一组 `http.method`/`http.path`；request/response type
由 RPC 原生签名拥有，operation id 由 fully-qualified RPC identity 确定性生成。生成后的 `.api` 用其
原生 operation/method/path 表达这些 inherited native facts，不复制 `http.*` comment。若下游 proxy
需要 `google.api.http` option，只能由这组已校验 facts 确定性渲染，不能成为第二 authoring surface。

### YAML page carrier

v1 的 frontend carrier 固定为 `nexa.dev/frontend-source/v1` YAML。其原生结构只允许文件级
`apiVersion`、`kind: Page` 和单个 canonical `id`，用于建立 page semantic node；其他 UI-only 事实必须
以紧邻该 node 的 `@nexa` comment 声明。标准 CRUD page 必须用 `ui.entity` 绑定一个 typed upstream
node，operations、request/response fields、pagination、wire path 和 runtime descriptor 均由 FactGraph 与
Convention 推导，不得出现在 YAML 字段中。一个文件只能有一个 page node，不支持 JSON reader。

## 第一事实源

FactID 是 semantic node identity 与 fact key 的组合，一个 FactID 只有一个 first source：

- 对每个 FactID，实际最早拥有该事实的 authoring source 是 first source；Ent 能表达时优先由 Ent 拥有；
- 没有更早 owner 时，Proto、原生 `.api` 或 frontend source contract 都可以成为第一来源；
- 最终 Vue、TypeScript client、Formily schema 和 locale JSON 不是 authoring source。

projected node 必须携带 compiler 生成的 `$source`：

```text
<stage>://<repository-relative-path>#<semantic-symbol>
```

stage 仅允许 `ent`、`proto`、`api`、`page`。path 必须位于已声明 repository/source roots 内，
禁止绝对路径、`.`、`..`、symlink escape、query 和大小写漂移。CLI 不信任文本中的 `$source`，
必须从当前 source graph 重新计算并校验。

## 下游扩展与生成

生成后的 Proto 和 `.api` 不是整文件不可编辑。generator 必须：

1. 解析当前 downstream source 并构建 local semantic graph；
2. 从上游重新计算 inherited projection；
3. 拒绝 inherited node/fact 的修改、删除和重复声明；
4. 保留当前阶段合法新增的 local node/fact；
5. 组合并确定性渲染。

例如 Proto 可以给 Ent-derived read model 增加 Ent 中不存在的 response 字段，并成为这些字段的
第一事实源；但不能修改 Ent-derived 字段类型，也不能重复声明 Ent 已有 label。组合必须基于目标语言
AST/descriptor，不提供通用文本 merge、diff3、fallback、alias 或 override marker。没有可靠 semantic
composer 的 family 不得宣称支持生成后就地扩展。

### Projection lock

stateless 比较无法区分“用户删除了 inherited node”和“上游刚新增、尚未写入 downstream 的 node”。
因此 source composer 使用唯一的生成证据 `nexa.dev/source-projection-lock/v1`。该 lock 只记录上一轮
成功组合后的 inherited semantic node/FactID、first source 与 canonical digest：

- 它是可重建、提交审阅的 source identity 证据，不是 authoring source；
- 不记录 local node、consumer ownership、整文件 digest、兼容 alias 或 runtime mapping；
- 旧 lock 有、当前 downstream 缺失且 upstream 仍存在，判定为非法 inherited deletion；
- 当前 upstream 新增且旧 lock 不存在，判定为新 projection 并正常加入；
- 旧 lock 有、当前 upstream 已删除，按新的 upstream projection 确定性删除 downstream inherited node；
- 只有所有 source file 语义校验和组合成功后才写新 lock；失败返回非零并保留可由 Git 审阅的变化。

一个最终 FactGraph 只生成并校验一份 projection lock；lock 可以同时记录多个 root 之间的
projection edge 及其 first source fact，不按 Ent、Proto、API 或 Page 拆分。独立 source fragment
可以先通过 `MergeGraphs` 合并，但进入 composer/generator 前必须重新执行一次完整 `BuildGraph`。
consumer 不得人工声明 fact。它不恢复旧 reader，也不形成双写期。

## Lint 与诊断

所有生成命令必须在首次写入前完成：carrier/contract、语法/value、registry、target、FactID、最早
stage、`$source`、inherited native/supplemental facts，以及 exact/case-fold/转换后标识符 collision
检查。

稳定诊断为：

| Code | Category |
| --- | --- |
| `NEXA-SC001` | `invalid_syntax` |
| `NEXA-SC002` | `unknown_key` |
| `NEXA-SC003` | `invalid_value` |
| `NEXA-SC004` | `invalid_target` |
| `NEXA-SC005` | `duplicate_fact` |
| `NEXA-SC006` | `misplaced_fact` |
| `NEXA-SC007` | `inherited_fact_changed` |
| `NEXA-SC008` | `inherited_node_changed` |
| `NEXA-SC009` | `source_mismatch` |
| `NEXA-SC010` | `semantic_collision` |

每条诊断至少包含 code/category、当前文件与行号、semantic node、适用时的 FactID、first source、
expected、actual 和可直接执行的修复建议。不得用整文件 digest 或源码字符串搜索替代语义比较。

## 安全与确定性

parser 不执行代码、模板、shell、环境变量或网络引用；必须限制 directive 长度、单 node fact 数和
JSON 深度。auth/permission/scope/visibility 使用封闭类型。相同 source graph 必须产生 byte-stable
artifact。renderer 不得自行补 alias、猜测缺失事实或接受非法值。

## 一次性迁移

迁移必须在同一实施链中完成，不保留双轨：

1. 将 `SchemaMeta`、`FieldMeta`、CRUD 中的 supplemental facts 迁为 `@nexa`；
2. 将 Nexa-specific Proto option 和 `.api` metadata tag 迁为 `@nexa`；
3. 保留各语言原生结构，不复制到注释；
4. 删除 `nexaent` 公共包、内部 model、序列化契约、旧 reader 和测试假设；
5. Ent AST/descriptor/entc 只作为 compiler-internal source adapter；
6. 人工 `.page.json` 迁为 comment-capable carrier或把事实上移；
7. 删除兼容 reader、alias、fallback、双写期和 runtime normalizer。

## 验收

conformance 必须证明：Ent-only、Proto-only 和 `.api`-only 起点；Ent 投影之外两个 Proto local 字段
重复生成后仍保留；修改 inherited field/fact、删除或伪造 `$source` 会失败并定位 first source；未知
key、非法 enum/JSON/target、重复 FactID、misplaced fact 和 semantic collision 都失败；仓库不再存在
`nexaent`、Nexa Proto option、`.api` metadata tag 或 JSON PageSpec 双写；重复生成零漂移；renderer 与
browser runtime 不解析 comment string 或动态 fact map。

## 明确不做

v1 不猜测自然语言，不开放任意扩展 key，不允许下游 override，不提供 alias/fallback/normalizer，
不做通用文本 merge，不从生成的 Vue/TypeScript/i18n 反推 contract，也不把领域命令或非标准 UI
逻辑压入注释 DSL。
