# Nexactl Build Plugin

## 1. 定义

Nexactl Build Plugin 是实现公开 plugin contract、在编译期贡献工程命令的 Go package。业务仓通过显式 import 和 constructor 选择官方模块与私有模块，并构建自己的 `nexactl` composition root。

构建插件不在运行时下载、扫描或加载。最终二进制包含哪些命令，完全由 composition root 的 Go 依赖和 constructor 调用决定。

在使用任何 CLI 示例、生成命令或自动化参数前，先对 consumer 实际编译的二进制执行：

```bash
nexactl inspect --json
```

后续调用只能使用该结果中实际存在的 command path、flag、schema、delegated tool 和 side effect；本文不
为某个 consumer 二进制复制这些字段。

## 2. 公开 Go API

插件通过 `plugin.NewStatic` 构造不可变 spec，通过 `host.New` 显式组合，并由 `(*host.Host).Execute` 执行：

```go
package example

import (
    "context"
    "encoding/json"
    "io"

    "github.com/nxnminieye/nexa/nexactl/host"
    "github.com/nxnminieye/nexa/nexactl/plugin"
)

func execute(
    ctx context.Context,
    args []string,
    stdout io.Writer,
    stderr io.Writer,
) (int, error) {
    privatePlugin, err := plugin.NewStatic(plugin.Spec{
        Descriptor: plugin.Descriptor{
            ID:              "private-example",
            Version:         "v0.1.0",
            ContractVersion: plugin.ContractVersion,
            Provides: []plugin.Capability{
                {ID: "private.ping", Version: "v1.0.0"},
            },
        },
        Commands: []plugin.CommandSpec{
            {
                Path:         []string{"private", "ping"},
                Summary:      "return the private readiness state",
                InputSchema:  json.RawMessage(`{"type":"object"}`),
                OutputSchema: json.RawMessage(`{"type":"object"}`),
                SideEffect:   plugin.SideEffectNone,
                Run: func(context.Context, plugin.Invocation) (any, error) {
                    return map[string]bool{"pong": true}, nil
                },
            },
        },
    })
    if err != nil {
        return 0, err
    }

    composed, err := host.New(
        host.Options{Version: "v0.1.0"},
        privatePlugin,
    )
    if err != nil {
        return 0, err
    }
    return composed.Execute(ctx, args, stdout, stderr), nil
}
```

`host.Options.Version` 必须是合法 semantic version；`Name` 为空时使用 `nexactl`；`OperationIDs` 为空时使用随机 operation id。composition root 负责把构造失败投影为稳定 bootstrap envelope。官方 `cmd/nexactl` 是该投影的参考实现。

Host 只负责组合、命令解析和机器协议，不隐式提供 repo root、文件系统、子进程执行器或 package-global context。命令 handler 只接收调用方传入的 `context.Context` 和 `plugin.Invocation`；其他依赖由插件 constructor 或 handler closure 显式注入。

## 3. PluginSpec

公开 contract version 是 `nexa.dev/nexactl-plugin/v1`。一个 spec 包含：

- `Descriptor`：插件 id、插件 semantic version、contract version、provided/required capabilities。
- `CommandSpec`：命令 path、summary、flags、JSON input/output schema、side effect 和 handler。
- `FlagSpec`：name、type、summary、required 和可选 JSON default。
- `Invocation`：位置参数 `Args` 与完成类型解析的 `Flags`。

插件 id 和 capability id 使用 lower-kebab dotted segments；capability 的 id 与 semantic version 分开声明。flag type 只允许 `string`、`bool`、`int`、`string-slice`。`json` 与 `help` 是 host 保留 flag，插件不得重定义。

`plugin.NewStatic` 在构造时校验 spec 并深拷贝输入；`Spec()` 返回独立副本。插件不能依赖调用方之后修改原始 slice、schema 或 default 值。

## 4. 组合与 capability

Host 在执行前完成组合校验：

- 插件 id、provided capability 和可执行命令 path 全局唯一。
- 命令 path 不得与 `inspect`、`version` 或其他 path 形成相同/前缀冲突。
- 每个 required capability 必须有且只有一个 provider。
- provider 与 requirement 的 capability id 和 major version 相同，且 provider version 不低于 required version。
- capability dependency graph 无环。

禁止 `init()`、blank import、可变全局 registry、Go `plugin`、动态 `.so` 和运行时目录扫描。Host 可重复构造，独立 Host 可以并发执行而不共享 Cobra 或 package-global 状态。

## 5. 能力发现

`nexactl inspect --json` 是唯一能力发现入口。其结果由 Host 从编译后的 plugin specs 投影，包含：

- binary name/version；
- global flags；
- plugin id/version/contract version 与 provides/requires；
- capability id/version/provider；
- command path/owner/flags/input schema/output schema/side effect。
- command 声明的 delegated tool id/version/inputs/writes；没有 provider tool 时保持显式缺席。

最小 Host 自带 `inspect` 和 `version`，可以不编入任何业务插件。未编入的 capability 不出现在自省结果中，也不注册空命令。详细 wire contract 见[CLI 机器协议](../contracts/cli-machine-protocol.md)。

### 5.1 Source adapter

官方 source Build Plugin 由 `plugins/nexactl/source.New` 构造。一个 adapter 可以接收零个、一个或多个
`sourceplugin.Provider`，但只提供一个 `source.bundle` capability 和七个命令：`plan`、`materialize`、
`status`、`diff`、`upgrade`、`detach`、`check`。Provider 不各自注册 CLI，也不通过运行时 registry 发现。

未把 adapter 传给 `host.New` 时，真实 consumer binary 仍可执行 Host 的 `inspect` 与 `version`，但自省中
没有 source plugin、capability 或 command。Source Bundle 的 identity、公开 API、resolver/cache、安全树、
provenance、三方升级、serial staged publish 与错误投影见[Source Bundle 契约](../contracts/source-bundles.md)。

### 5.2 Generation ProjectProvider

官方 generation Build Plugin 通过 consumer 显式注入的 `ProjectProvider` 解析项目关系。Provider descriptor
用 `ProviderTool` 把 inspectable delegated tool 绑定到闭合 role：`ent-generate`、`ent-crud`、`rpc-go`
或 `api-go`。Provider resolve 结果再为每个 service 选择对应 `toolchain.Tool`；只有 role、tool id 和
version 与 descriptor 一致时才能执行，工具不能跨命令族复用。

Project provider 只拥有入口定位和 toolchain binding，不复制 Ent、Proto、API 或 Service Catalog 节点
事实。Service Manifest 由结构化 contract source set 生成，不声明外部 tool role。完整 owner 链、12
命令矩阵、serial staged publish 与 optional composition 见
[受控生成契约](../contracts/controlled-generation.md)。

## 6. 官方与业务私有插件

官方插件和业务私有插件实现同一公开 contract，但所有权保持独立：

- 官方插件不得 import 产品或业务 package。
- 私有插件可以保留在业务仓 `internal`，但不得 import Nexa `internal`。
- 私有插件不需要进入 Nexa 仓库或官方发行版。
- 官方与业务 composition root 使用同一 CLI 机器协议。

官方插件是否包含在某个二进制中，只能由该二进制的 `inspect` 结果证明；插件分类文档不构成可用性声明。

## 7. 可选功能边界

Build Plugin 可以独立承载：

- generation；
- migration；
- frontend；
- source；
- requirements；
- gate；
- evidence；
- governance；
- deployment。

requirements/work/UserOperation、human gate、TestSpec/evidence、deployment instance 和 frontend 可以全部缺席，Core 与业务后端仍必须可构建和运行。

Migration plugin 只处理 repository 内的 plan、diff、render、lint 和 check，不执行数据库 `apply` 或
`status`。运行时数据库操作由 consumer-selected runtime administration or private ops CLI 负责。

Source plugin 同样可以缺席；它不拥有 runtime config、deployment instance、health state、远端系统或进程
lifecycle。Materialized source 是 consumer ordinary source，不以 Build Plugin 身份参与运行。

## 8. 副作用与错误

命令 side effect 只允许：

- `none`：不读取或写入 repository；
- `repository-read`：只读 repository；
- `repository-write`：可以在声明的 repository 范围内写入。

side effect 是可自省 contract，插件实现必须与声明一致。插件返回 typed protocol error，不自行向 stdout 打印结果或重复打印 error。Host 统一 envelope、operation id、exit code、取消和诊断分流。

## 9. 测试契约

- 使用真实 Host 构造官方插件和业务私有测试插件。
- 验证命令参数、JSON envelope、exit code、错误投影和 side effect。
- 验证重复 id/命令、缺失/冲突 capability、循环依赖和版本不兼容。
- 验证多次构造、输入深拷贝和并发执行不共享可变状态。
- 不通过搜索 import 字符串、函数位置或源码文本证明插件已注册；通过 `Inspect`、`Execute` 和 subprocess 行为证明组合。

Nexa 对公共平台 CLI、版本、facade、reference consumer 与 source asset 实践的采用边界，以及不进入
Nexa 的 runtime plugin platform 能力，见[受控生成契约](../contracts/controlled-generation.md)。
