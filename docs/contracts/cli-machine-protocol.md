# CLI 机器协议

## 1. 适用范围

本协议定义 Nexa CLI Host 的稳定机器边界。`nexactl` 使用该协议输出执行结果和编译组成；业务 composition root 与私有 Build Plugin 必须保持相同 wire behavior。

机器调用只有在显式选择一个可执行 command 时，才读取一个 stdout JSON value，并结合 process exit code 判断结果。以下调用是明确的人类输出例外：显式使用 `--help` 或 `-h`；以及没有选择任何可执行 command 的 root invocation，包括无参数调用和仅提供 `--json` 的调用。它们都向 stdout 写 Cobra help text、成功返回 0、保持 stderr 为空，不使用 JSON envelope。`--json` 只选择 JSON 格式，不选择 command；机器调用方必须显式调用 `inspect`、`version` 或其他可执行 command。

## 2. Envelope v1

协议版本是 `nexa.dev/cli-envelope/v1`。

成功 envelope：

```json
{
  "apiVersion": "nexa.dev/cli-envelope/v1",
  "ok": true,
  "operationId": "op_0123456789abcdef0123456789abcdef",
  "result": {
    "name": "nexactl",
    "version": "v0.0.0-dev"
  }
}
```

失败 envelope：

```json
{
  "apiVersion": "nexa.dev/cli-envelope/v1",
  "ok": false,
  "operationId": "op_0123456789abcdef0123456789abcdef",
  "error": {
    "code": "command_not_found",
    "domain": "nexactl.host",
    "category": "usage",
    "message": "command was not found",
    "retryable": false
  }
}
```

顶层字段：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `apiVersion` | string | 固定为 `nexa.dev/cli-envelope/v1` |
| `ok` | boolean | 成功为 `true`，失败为 `false` |
| `operationId` | string | 本次调用的稳定关联 id |
| `result` | any | 仅成功时出现；具体结构由 command output schema 定义 |
| `error` | object | 仅失败时出现 |

`error` 字段：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `code` | string | 稳定、可分支处理的错误码 |
| `domain` | string | 产生错误的稳定领域 |
| `category` | string | 决定 exit code 的错误类别 |
| `message` | string | 面向使用者的安全说明；不是稳定解析键 |
| `recommendedAction` | string | 可选的恢复建议 |
| `retryable` | boolean | 是否适合在输入不变时重试 |
| `details` | object | 可选结构化详情；不得包含 secret 或未经投影的底层错误 |

未知 Go error 统一投影为 `code=internal_error`、`domain=internal`、`category=internal` 和安全 message，不泄露原始 error text。

## 3. Category 与 exit code

| 结果/category | Exit | 语义 |
| --- | ---: | --- |
| success | 0 | 命令成功 |
| `usage` | 2 | 命令、flag 或调用方式错误 |
| `input` | 3 | 业务仓输入或事实不合法 |
| `review` | 5 | 需要人工判断或批准 |
| `unavailable` | 6 | 命令存在，但所需能力不可用 |
| `external` | 7 | 外部工具或系统失败 |
| `drift` | 12 | 事实与派生产物不一致 |
| `conflict` | 13 | ownership 或并发冲突，不能安全继续 |
| `internal` | 70 | Host、插件或输出投影内部失败 |
| `canceled` | 130 | context canceled 或 deadline exceeded |

命令不存在、未知 flag、flag 类型错误和 required flag 缺失属于 `usage`。取消统一为 `code=operation_canceled`、`domain=nexactl.host`、`category=canceled`，不把 context error 原文写入 envelope。

## 4. Operation ID

正常 operation id 必须匹配：

```text
^op_[0-9a-f]{32}$
```

Host 在解析和执行命令前创建 operation id，因此成功、usage error、handler failure 和 diagnostic 可以使用同一 id 关联。

随机 id 无法生成、生成器 panic 或返回非法 id 时，Host 使用 sentinel：

```text
op_00000000000000000000000000000000
```

对应失败为 `operation_id_generation_failed`、`nexactl.host`、`internal`，exit 70。composition root 初始化失败也使用同一 sentinel，并投影为 `host_initialization_failed`、`nexactl.bootstrap`、`internal`。

## 5. JSON 格式与参数终止符

- 不提供 `--json` 时，成功和失败 envelope 使用两空格缩进并以换行结尾。
- `--json` 或 `--json=true` 请求单行 compact JSON，并以一个换行结尾。
- `--json=false` 请求缩进 JSON；多个合法 `--json` 值出现时，最后一个值生效。
- `--` 终止 flag 解析；其后的 `--json` 是命令位置参数，不改变输出格式。
- bootstrap failure 使用同一 compact/indented 判断，因此 Host 尚未构造成功时机器调用仍得到一致格式。

## 6. stdout 与 stderr

- 普通机器成功或失败只向 stdout 写一个完整 envelope。
- command handler 不直接打印 envelope，也不让 Cobra 重复打印 error。
- stderr 只承载稳定 diagnostic，不承载第二份结果 JSON。
- handler panic 和 JSON encoding failure 不把原始 panic value、encoding error 或业务 result 放入 failure envelope 或 diagnostic。
- stdout writer error 或 short write 不把原始 writer error 或业务 result 复制到 stderr；stdout 可能已经包含带有业务 result 的不完整 payload 前缀。

Host diagnostic 格式为：

```text
nexactl.host operation=<operation-id> failure=<failure-class>
```

Bootstrap diagnostic 使用 `nexactl.bootstrap`。如果 stdout writer 拒绝或发生 short write，进程返回 70，并在可用 stderr 上报告 `stdout_write_failed`。此时 stdout 可能为空，也可能只包含不完整的 bootstrap failure envelope payload 前缀；调用方必须丢弃全部 stdout，不能解析、采信或把它视为完整 envelope。

如果 command result 无法 JSON encode，Host 报告 `output_encoding_failed` diagnostic，并尝试向 stdout 写同 code 的 internal failure envelope。handler panic 投影为安全 `internal_error` failure envelope，stderr failure class 为 `handler_panic`。

## 7. Inspection v1

`inspect` 的 result 使用 `nexa.dev/cli-inspection/v1`，是当前二进制能力发现的唯一事实。结构如下：

| 路径 | 字段 |
| --- | --- |
| `apiVersion` | 固定为 `nexa.dev/cli-inspection/v1` |
| `binary` | `name`, `version` |
| `globalFlags[]` | `name`, `type`, `summary`, optional `required`, optional JSON `default` |
| `plugins[]` | `id`, `version`, `contractVersion`, optional `provides[]`, optional `requires[]` |
| `plugins[].provides[]`, `plugins[].requires[]` | `id`, `version` |
| `capabilities[]` | `id`, `version`, `providerPluginId` |
| `commands[]` | `path[]`, `summary`, optional `flags[]`, `inputSchema`, `outputSchema`, `sideEffect`, `ownerPluginId` |

plugin 按 id、capability 按 id、command 按 path 稳定排序。最小 Host 的 `plugins` 和 `capabilities` 可以为空；内建 `inspect` 与 `version` 仍出现在 `commands`，owner 是 `nexactl.host`。global flags 是 `help` 和 `json`。

`inputSchema` 和 `outputSchema` 是 JSON object schema。command flag type 只允许 `string`、`bool`、`int` 和 `string-slice`。

## 8. Side effect

| 值 | 允许的副作用 |
| --- | --- |
| `none` | 不读取或写入 repository |
| `repository-read` | 只读 repository |
| `repository-write` | 在命令声明的 repository 范围内写入 |

Nexactl Build Plugin 不声明数据库、网络或 remote-runtime side effect。数据库 apply/status、远端系统修改和运行时状态转换由 consumer-selected runtime administration or private ops CLI 负责。

## 9. 可执行示例

以下命令直接执行本 module 的 reference composition root：

```bash
GOWORK=off go run ./cmd/nexactl inspect --json
GOWORK=off go run ./cmd/nexactl version --json
```

两条命令都必须返回 exit 0、空 stderr 和一个可解析的 compact success envelope。`inspect` 结果决定该二进制实际编入哪些插件与 capability；文档中的插件分类或命名空间不能替代该结果。
