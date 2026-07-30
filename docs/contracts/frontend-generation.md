# 前端生成契约

本契约定义 `nexa.dev/frontend-source/v1`、FrontendIR 与 Vben renderer 的最小公共边界。HTTP method、
relative path、auth、permission、request/response type 和外部字段名来自 canonical `.api` 与
`nexa.dev/http-convention/v1`；frontend source 只声明不能从前序 FactGraph 推导的 UI-only facts。

Nexa 拥有 Source Comment registry、frontend YAML adapter、FrontendIR 和 closure 构建；Vben 拥有通用
CRUD/Formily/VXE 基座与 renderer；consumer 拥有事实实例、生成物和业务 extension。

## Frontend source

`nexa.dev/frontend-source/v1` YAML 只允许：

```yaml
# @nexa $contract: "nexa.dev/source-comment/v1"
# @nexa ui.entity: "Record"
# @nexa route.path: "/records"
# @nexa route.name: "records"
# @nexa route.icon: "lucide:database"
# @nexa ui.pageSize: 20
apiVersion: nexa.dev/frontend-source/v1
kind: Page
id: records
```

`apiVersion`、`kind`、`id` 只建立 page native node；其余事实由紧邻 node 的 `@nexa` comment 承载。
一个文件一个 page。JSON、PageSpec、`listOperationId`、field surface、request/response binding、
`itemsPath`、`totalPath`、pagination path、context binding、semantic binding 和 runtime descriptor 均不再
是 authoring surface。

`ui.entity` 在编译期解析为 typed upstream NodeRef。标准 CRUD closure、field presentation、validation、
locale key、client method 和 pagination 由 Entity/Proto/API FactGraph 与 Convention 推导。真实 consumer
extension 使用 `ui.extensionComponent`，并位于 generated scope 外；复杂 action/modal/hook 用正常 typed
源码实现，不扩充 binding DSL。

`.api` 保留 Go-zero 原生 `@server group` 与 `@handler` 作为 operation identity 的作者。Nexa 编译器将其
确定性投影为内部 `operation.id`，并在 FrontendIR 的每个 operation 上一次性输出 `clientName`：默认使用
`lowerFirst(handler)`；仅当多个 group 导出同名 handler 时，才按 PDCL 根索引规则追加 canonical group 前缀
消歧。转换后的 TypeScript symbol collision 直接失败。renderer 只使用已验证的 `clientName`，不重新解析
handler、operation ID 或 HTTP 规则。

## 标准管理页

标准列表调用 `GET <relative collection path>`，固定使用 `limit/offset`，从统一 envelope 的
`data.items/data.total` 读取。Vben 必须复用 PDCL 已验证的 `CrudManagedPage + ManagedVxeGrid`，并生成
Formily search、创建/编辑表单、校验、提交反馈、删除确认、typed list/get/create/update/delete 与 pager。
页面内容区域不重复展示 H1，搜索区包含明确的搜索按钮。

标准 create/get/update/delete 只接受可由 Convention 和 CRUD facts 推导的输入。删除、更新或 action 额外
要求 expectedVersion、reason 或授权范围时，compiler 要求 consumer extension 使用 generated typed client
写显式领域动作；不得把这类语义变成通用 source/target mapping。

## FrontendIR 与 renderer

`nexa.dev/frontend-ir/v1` 只携带页面实际引用的 canonical operation/type closure、resolved typed UI
projection 和 locale。它不携带完整 HTTP snapshot/digest、comment string、raw fact map、YAML、wire mapping、
response decoder 或兼容 metadata。

renderer 直接生成 typed request/response、唯一 requestClient API function、typed CRUD definition、Formily
schema、Grid、route/menu/locale 和 Vue entry。shared transport 固定 `responseReturn: 'data'` 并统一处理
PDCL `{code,msg,data}` / `{code,msg,message}` 契约。它不提供 second client、operation response selector、
runtime adapter 或 page descriptor interpreter。

## 验证边界

Nexa 验证 source comment、YAML AST binding、typed reference resolution、HTTP Convention、CRUD closure、
identifier collision 和 renderer request/path。Vben 验证 renderer output typecheck/unit/build，并证明没有
comment/YAML parser。Example 验证重复生成零漂移、登录、一个资源 CRUD、PostgreSQL、前端 build 与浏览器
E2E。最小纵向闭环通过前，不新增通用 action/options/form workflow 抽象。
