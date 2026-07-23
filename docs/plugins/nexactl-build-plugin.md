# Nexactl Build Plugin

Nexactl Build Plugin 是显式编译进工程 CLI 的命令模块。它扩展 build-time/repository workflow，不提供
运行时插件发现、服务 lifecycle 或远程控制。

## Host 与 plugin contract

`nexactl/host` 负责组合、command dispatch 和机器 envelope；`nexactl/plugin` 定义 public plugin contract。
Composition root 显式构造 plugin 并传给：

```go
host.New(host.Options{Version: version}, plugins...)
```

没有 `init()` registry、blank import discovery、Go `plugin`、`.so` 或目录扫描。独立 Host 不共享可变全局
state，可以用不同 plugin set 构造。

Plugin spec 包含 descriptor、provided/required capability、command spec、typed flag、input/output schema、
side effect 和 handler。`plugin.NewStatic` 在构造时校验并复制 spec，后续调用方修改输入 slice 或 schema
不会改变已构造 plugin。

## 组合约束

Host 在执行前拒绝：

- duplicate plugin id、command path 或 capability provider；
- 与内建 `inspect` / `version` 相同或形成 prefix ambiguity 的 command；
- 缺失、版本不兼容或多个 provider 的 required capability；
- capability dependency cycle；
- 非法 plugin/capability/flag identity 和不闭合 schema。

这些检查证明 composition 自洽，不证明 repository input 或业务决定正确。

## Inspection

`nexactl inspect --json` 从当前编译后的 specs 投影：

- binary identity 和 global flags；
- plugin id/version/contract version；
- capability 与 provider；
- command owner、flag、schema、delegated tool 和 side effect。

最小 Host 只有 `inspect` 和 `version`。未编入的 plugin 不注册空 command，也不创建占位 capability。完整
wire 见[CLI 机器协议](../contracts/cli-machine-protocol.md)。

## Generation plugin

Generation plugin 通过 consumer 注入的 ProjectProvider 定位 Ent、Proto、`.api` 和 service topology，并绑定
明确的 delegated tool。Provider 只拥有路径和 toolchain binding，不复制节点 facts。生成生命周期和
manual/generated ownership 见[受控生成](../contracts/controlled-generation.md)。

Reference alpha CLI 没有 consumer ProjectProvider，因此 inspection 能展示 generation command contract，
但具体业务 generation 需要 consumer composition 提供真实 facts/toolchain。

## Source adapter

Official source adapter 接收零个或多个显式 Provider，但只提供一个 source capability 和一组统一 command。
Provider 不各自注册 CLI；未把 adapter 传给 `host.New` 时，binary 中没有 source capability。

Source release、lock 和 repository behavior 见[Source Bundle](../contracts/source-bundles.md)，Provider 的产品
边界见[Service Source Plugin](service-source-plugin.md)。

## Consumer-private plugin

业务方可以在自己的 repository 实现相同 public plugin contract，并与官方 plugin 一起静态组合。Private
plugin 可以 import Nexa public package，但不能 import Nexa `internal`，也不需要提交到 Nexa module。

分类为 official/private 不证明能力存在；仍以该 consumer binary 的 fresh inspection 为准。

## Side effect 与错误

Command 明确声明 `none`、`repository-read` 或 `repository-write`。Handler 只能使用 constructor/closure
显式注入的依赖和 invocation context，不从 package-global state 获取 repository、executor 或凭据。

Handler 返回 typed protocol error；Host 统一 success/failure envelope、operation id、exit code、stdout 和
stderr diagnostic。Plugin 不自行打印第二份结果。
