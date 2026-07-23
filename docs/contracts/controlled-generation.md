# 受控生成

Nexa 把 consumer-owned typed facts 投影为可编译、可追踪的普通源码。Consumer 拥有输入、生成选择、
manual logic、最终文件和发布；framework 拥有 public IR、generator、validation 和 plan/check/write contract。

## 主链

```text
owner facts
  -> strict load and validation
  -> versioned IR
  -> deterministic complete plan
  -> invocation-local staging
  -> parse/typecheck/compile validation
  -> source and target drift recheck
  -> per-file publish
  -> generated manifest written last
```

IR、plan、result 和 manifest 都是 derived projection，不是人工配置入口。

## 能力发现

任何自动化先对 consumer 实际二进制执行：

```bash
nexactl inspect --json
```

Inspection 是 command、flag、input/output schema、delegated tool 和 side effect 的唯一清单。Reference alpha
CLI 当前包含 Ent delegation、CRUD Proto、RPC、API 和 Service Manifest generation；没有统一 CRUD logic CLI。
Consumer 自己编译的 composition 可以不同。

## Plan、check 与 write

- **plan** 从当前 facts、previous manifest 和 target state 建立完整候选，返回 plan digest、changes 和 conflicts；
- **check** 重新读取 target，报告 clean、changes 或 conflicts，不写 repository；
- **write** 只接受同一 plan digest，在写入前重验 sources、target 和 ownership。

每次 invocation 使用唯一 staging。Delegated tool 只在声明的 scratch/staging 内工作，候选完成后执行真实
parser、typecheck 或 compile。发布使用普通原子单文件替换；generated manifest 在受控文件成功后最后写入。

同一 worktree 的两个 generator invocation 不受支持，由调用方/CLI 串行调度。Nexa 不维护跨进程事务锁、
lease、WAL、旧事务重放或自动 Recover。失败保留真实错误并 best-effort 清理本次 staging；下次从当前工作树
重新生成完整 plan。

## Generated 与 manual ownership

Generated file 通过 generator id、artifact id、input digest、content digest 和 ownership probe 证明归属。
Generator 不接管未知文件，也不删除无法证明仍由自己拥有的旧路径。

Manual logic 有两种明确模式：

- 默认 `create-manual`：仅当目标缺失时创建；文件存在后不再覆盖，也不进入 generated artifact manifest；
- 显式 `overwrite-manual`：直接写入新候选，但 plan 必须绑定 prior digest，check/write 发现目标变化就拒绝。

两种模式互斥。Framework 不读取 Git diff、不自动 merge manual logic，也不替 consumer 决定是否保留业务
修改。

## Ent 与 CRUD

`nexactl gen ent` 只委托 consumer 明确绑定的 Ent toolchain，不重实现 Ent schema 语义。CRUD 选择只读取
`nexaent.CRUD(...)`：

```text
Schema/Field/CRUD typed annotations
  -> EntityIR
  -> CRUDProtocolIR
  -> Proto artifact + wire compatibility lock
  -> optional generation/crudlogic plan
```

Compatibility lock 只记录已发布 CRUD Proto field/enum number 历史，阻止 wire-breaking reuse；它不是生成
事务锁，也不授权写入。没有 CRUD schema 时不创建或修改该 lock。

`generation/crudlogic` 从 verified CRUD Proto/Entity projection 生成 runnable go-zero logic。当前 alpha 将它
作为 public kernel，而不是 reference CLI command。默认 logic 按选择的五类 operation 生成，业务方可以在
create-once 后修改；显式 overwrite 使用上一节的直接覆盖语义。

## Multi-tenant projection

Multi-tenant 由调用方明确启用，并且只对同时满足以下事实的 entity 生效：

- schema `ScopeTenant`；
- 使用 framework `nexaent/mixin.Tenant` 的 typed marker；
- schema 选择了对应 CRUD operation。

CRUD request 使用内部 `int64 tenant_id` context binding。该值不出现在公开 item 或外部 create/update field；
generated logic 通过 tenant helper 校验为正且能安全转换为 Ent 使用的 `int`。Global schema、普通同名字段或
只使用 mixin 而未声明 scope 都不会自动启用 tenant isolation。

## RPC、API 与 Service Manifest

RPC generation 从 consumer Proto/ProtocolIR 生成普通 Go。Business API generation 把 Proto proxy metadata、
Core native `.api` 和 Service Catalog binding 投影为 Composition/API IR，再生成 client、mapper、error adapter、
logic 和 registration source。空 catalog 或没有 proxy binding 是合法输入，不生成占位 route。

Service Manifest 从该服务当前 contract source set 计算 digest，不调用外部生成工具，也不拥有 source facts。

## Error 与重试

Invalid facts、tool failure、staging validation、plan mismatch、source drift、target drift 和 ownership conflict
保持不同 typed reason，并由 CLI 统一投影为 machine envelope。自动化只可在重新读取当前状态并生成 fresh
plan 后重试，不能忽略 conflict 或沿用旧 plan digest。

Manifest identity 与 stale policy 见[生成清单](generated-manifests.md)。
