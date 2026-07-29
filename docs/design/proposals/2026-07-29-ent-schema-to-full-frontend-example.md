# 一个 Ent Schema 自动生成完整前端：开发者直觉示例

状态：目标标准。

这篇示例帮助开发者建立一个简单直觉：维护一个 Ent Schema，Nexa 就能沿着 Proto、`.api` 和前端生成链，交付一个可以搜索、新建、编辑、删除和切换语言的管理页面。

真实业务不一定是标准 CRUD，也可以从 Proto 或 `.api` 开始；这里使用 `Record`，只是为了完整展示这条链。

## 1. 先看前端最终得到了什么

用户访问 `/records` 时，会直接看到搜索区、数据表格和操作按钮。新建、编辑以弹窗表单完成；分页、加载状态、权限、确认和反馈沿用已经验证的统一行为。

consumer 中最终会出现这些前端文件：

```text
frontend/apps/nexa-admin/src/views/_nexa/
├── api.ts
├── routes.ts
├── locales/zh-CN.json
├── locales/en-US.json
├── pages/records/definition.ts
├── pages/records/index.vue
└── manifest.json
```

| 文件 | 它决定的用户体验 | 它依赖的输入 |
| --- | --- | --- |
| `api.ts` | 页面调用真实的 List/Get/Create/Update/Delete API | canonical `.api` 中的 operation 和类型 |
| `definition.ts` | 表格列、搜索项、新建/编辑表单、按钮权限 | API 请求/响应、Ent 字段约束和语义注释 |
| `index.vue` | 将生成定义接入标准管理页面 | `definition.ts` 和公共前端能力 |
| `routes.ts` | `/records` 路由、菜单图标和顺序 | 页面自己的 route/menu 声明 |
| `locales/*.json` | 菜单、列标题、表单标签和页面文案 | 实体/字段的中英文注释 |
| `manifest.json` | 本次输出文件集合 | renderer 的生成结果 |

例如，用户看到表格列“名称”，是因为 `definition.ts` 使用了名称字段的 locale key，而 `zh-CN.json` 中的值来自 Ent 的 `label.zh-CN`。用户点击“编辑”，是因为 `definition.ts` 中生成了 Update 表单定义，公共页面再根据它加载详情并打开编辑弹窗。

对应源码：Example `frontend/tools/frontend-renderer/lib/generate.mjs`、`frontend/apps/nexa-admin/src/runtime/frontend/generated-collection-page.vue`、`frontend/apps/nexa-admin/src/locales/index.ts`；Nexa `generation/frontend/build.go`。

## 2. 最早输入是一个普通 Ent Schema

文件：`backend/records/ent/schema/record.go`

```go
// @nexa $contract: nexa.dev/source-comment/v1
// @nexa label.zh-CN: "记录"
// @nexa label.en-US: "Record"
// @nexa description.zh-CN: "可管理的业务记录"
// @nexa description.en-US: "A manageable business record"
// @nexa crud.operations: "list,get,create,update,delete"
type Record struct {
    ent.Schema
}

func (Record) Fields() []ent.Field {
    return []ent.Field{
        // @nexa label.zh-CN: "名称"
        // @nexa label.en-US: "Name"
        // @nexa description.zh-CN: "记录的显示名称"
        // @nexa description.en-US: "Display name of the record"
        // @nexa visibility: "public"
        // @nexa crud.read: "include"
        // @nexa crud.mutation: "create-update"
        // @nexa ui.control: "text"
        field.String("name").NotEmpty().MaxLen(100),

        // @nexa label.zh-CN: "启用"
        // @nexa label.en-US: "Enabled"
        // @nexa description.zh-CN: "记录是否启用"
        // @nexa description.en-US: "Whether the record is enabled"
        // @nexa visibility: "public"
        // @nexa crud.read: "include"
        // @nexa crud.mutation: "create-update"
        // @nexa ui.control: "switch"
        field.Bool("enabled").Default(true),
    }
}
```

Ent 原生语法表达类型和约束：`name` 是必填字符串，最长 100；`enabled` 是布尔值，默认启用。`@nexa` 注释只补充跨层语义：用户怎样称呼字段、字段是否公开、是否进入 CRUD、使用什么控件。

因此同一份输入可以同时影响后端和前端：

| Ent 中的信息 | 生成结果 |
| --- | --- |
| `String`、`Bool` | Proto/`.api`/TypeScript 的字段类型 |
| `NotEmpty()`、`MaxLen(100)` | API 校验和前端表单校验 |
| `Default(true)` | 新建表单默认值和后端默认行为 |
| `crud.read: include` | List/Get 响应和表格列 |
| `crud.mutation: create-update` | Create/Update 请求和新建/编辑字段 |
| `ui.control: switch` | 前端使用开关控件，而不是文本框 |
| `label.zh-CN: 名称` | 中文表头和表单标签 |
| `label.en-US: Name` | 英文表头和表单标签 |

HTTP method、JSON 大小写、分页和错误格式由全局 HTTP Convention 决定，不需要在 Ent 中逐字段配置。

对应规范与源码：Nexa `docs/contracts/source-comment.md`、`generation/sourcecomment/registry.go`、`generation/sourcecomment/entity_facts.go`。

## 3. 信息如何从 Ent 传到前端

完整链路如下：

```text
backend/records/ent/schema/record.go
        |
        | 字段、约束、CRUD 和中英文语义
        v
backend/records/desc/record.proto
        |
        | RPC operation 和从 Ent 传递的语义注释
        v
backend/core/api/desc/records.api
        |
        | Core proxy 承载的 canonical HTTP contract
        v
frontend page 声明
        |
        | 只补充 /records、图标和菜单顺序
        v
api.ts + definition.ts + index.vue + routes.ts + locales/*.json
        |
        v
标准 CRUD 页面运行时
```

后端对应文件形状是：

```text
backend/records/
├── ent/schema/record.go
├── ent/record/
└── desc/record.proto

backend/core/api/desc/
└── records.api                    # 统一 API proxy 承载的 HTTP contract
```

Ent 生成 Proto 时，字段语义注释也会出现在 Proto 中：

```proto
// @nexa label.zh-CN: "名称"
// @nexa label.en-US: "Name"
string name = 1;
```

传递注释有两个直接作用：

- 下游不需要根据 `name` 猜测“名称”、`Name` 或表单控件；
- 没有 Ent 的服务可以直接从 Proto 开始，继续使用相同的 `.api` 和前端生成链。

Proto 中由 Ent 传来的注释仍由 Ent 拥有。它是为了携带语义，不是让 Proto 成为第二个可独立修改的来源。如果某项事实最早就在 Proto 中出现，Proto 才拥有它；`.api` 和页面声明遵循同样规则。

`records.api` 虽然物理上位于 `backend/core/api/desc/`，但这只表示 Core 是统一 HTTP proxy 的运行与汇集位置，不表示 Core 拥有所有 Records 语义。三类情况必须区分：

| HTTP operation 类型 | 最早 owner | Core `.api` 的角色 |
| --- | --- | --- |
| Records 的标准 CRUD | Records Ent/Proto | 保留来源关系的 proxy HTTP contract |
| 登录、会话、菜单等 Core 自身能力 | Core | Core `.api` 是事实源 |
| Dashboard、跨服务聚合等 BFF 操作 | Core | Core `.api` 是事实源 |

这样前端始终只消费统一的 Core HTTP contract，服务又不会因为 `.api` 放在 Core 而失去自身领域接口的事实 owner。

页面只补充 Ent、Proto 和 `.api` 都不应该拥有的 UI 事实：

```yaml
# @nexa $contract: nexa.dev/source-comment/v1
apiVersion: nexa.dev/frontend-source/v1
kind: Page
# @nexa ui.entity: "Record"
# @nexa route.path: "/records"
# @nexa route.icon: "lucide:files"
# @nexa menu.order: 30
id: records
```

它不重复字段类型、API 路径、列标题、分页路径、表单定义或 request binding。

对应处理源码：Nexa `generation/sourcecomment/frontend_source.go`、`generation/frontend/build.go`；Example `frontend/tools/frontend-renderer/lib/generate.mjs`。

## 4. Nexa 如何处理前端依赖并防止漂移

从前端看，Nexa 需要五类输入：

1. Core proxy 中的 `.api` 提供前端消费的统一 operation、请求和响应类型；标准服务 operation 可追溯到对应服务的 Ent/Proto；
2. Ent/Proto/`.api` 上的语义注释提供标签、说明、公开性和控件提示；
3. 页面声明提供路由、菜单、图标和排序；
4. 前端公共 release 提供标准 CRUD、表格、表单和 locale 能力；
5. consumer 提供 API base、品牌和环境配置。

Nexa 会先检查这些输入是否完整和一致，再只选择当前页面实际使用的 operation 和 type，最后一次生成 typed client、页面定义、Vue 入口、路由和 locale。同样输入重复生成应当没有无关 diff。

事实归属也在这个过程中检查。例如：

```text
Ent record.go:       name.label.zh-CN = "名称"
Proto record.proto:  被手工改成 "记录名称"
```

lint 应报告：

```text
事实冲突：Record.name 的中文标签
最早来源：backend/records/ent/schema/record.go
当前修改：backend/records/desc/record.proto
建议：回到 Ent 修改，然后重新生成 Proto 和前端
```

用户不需要记住哪个文件“禁止编辑”。如果修改发生在错误的层，Nexa 指出应该回到哪里；如果事实本来就从 Proto 或 `.api` 开始，则直接在那一层修改。正确流程始终是：修改最早来源、重新生成、审阅 Git diff、运行 typecheck/build/API 测试和浏览器 E2E。

例如手工修改 `backend/core/api/desc/records.api` 中一个由 Records 投影的字段标签，lint 同样会要求回到 Records Ent/Proto 修改；但一个只在 Core 做跨服务组合的 Dashboard operation 则应当直接在 Core `.api` 维护。

对应校验源码：Nexa `generation/sourcecomment/diagnostic.go`、`generation/sourcecomment/projection_lock_test.go`、`generation/frontend/frontend_test.go`；前端 locale 装载见 Example `frontend/apps/nexa-admin/src/locales/index.ts`。

## 5. 非标准业务仍然可以接入

标准 CRUD 只负责可推导的公共行为。删除必须带 `expectedVersion`、重置密码、开通租户、审批或复杂分步表单等需求，应在 Proto 或 `.api` 中声明真实的 typed operation，再由 consumer extension 实现。若该 operation 只是某个服务的领域能力，事实 owner 仍在该服务；只有跨服务组合语义才由 Core `.api` 首次拥有。

这不会破坏主链：标准部分仍生成 typed client、locale 和公共页面能力，只有不可推导的业务差异由业务代码维护。

所以“一个 Ent Schema 自动生成完整前端”的含义不是把所有业务都压成 CRUD，而是：有 Ent 时尽可能从 Ent 开始，语义沿 Proto 和 `.api` 传递，Nexa 自动补齐依赖并生成完整标准前端；没有 Ent 时，从 Proto 或 `.api` 接入同一条链，并保持同样的事实归属和漂移检查。
