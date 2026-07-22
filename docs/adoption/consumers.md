# Consumer 闭包

Nexa 的采用单位是一个通过本地 module replace 或临时 `go.work` 使用框架、只依赖公开契约并能独立验证的 consumer。文档中的闭包描述长期有效的依赖和行为边界，不把目录存在、文档说明或 capability presence 当成执行计划。

## V0.1 闭包

| Consumer | 必需输入 | 允许的组成 | 必须证明 | 必须缺席 |
| --- | --- | --- | --- | --- |
| `neutral` | 本地 Nexa module 与对应 `nexactl` | 仅 `nexactl/host` 与内建 `inspect`、`version` | 本地 replace 或临时 `go.work` 指向预期框架目录；进程完成 start、health、stop；清理后无工作目录残留 | nonbuiltin plugin、repository write、网络发现、数据库、集群、凭据 provider |
| `backend-only` | 本地 Nexa module；consumer-owned Service Catalog、Proto、Core API 与健康契约；已审核任务计划 | 公开 runtime/package、显式构造的 Build Plugin、受控 source 与 generation kernel | fresh inspection 与计划命令、输入、affected files、副作用一致；生成物 parse/typecheck/compile；loopback health 与清理通过 | frontend、requirements、human-gate producer、TestSpec/evidence producer、quality producer、deployment instance、运行时插件发现 |

两种闭包都从干净 consumer module 开始，只使用公开 package、machine schema 和 CLI 协议。生成结果、Source Lock、Artifact Manifest 和 inspection 是可重建或可核验的 projection，不是新的人工事实源。

## 实际业务边界

- Consumer repository 拥有服务拓扑、Ent schema、Proto、Core API、运行配置、菜单、locale、项目 adapter 和部署事实。
- Nexa module 拥有中性 public contract、strict parser、versioned IR、generator、serial staged publish kernel、CLI host 和可选源码发布契约。
- 业务代码不得 import Nexa `internal` package。Consumer 自己的 adapter 可以组合公开 constructor，但不会因此成为 framework capability 或运行时插件。
- Service Source Plugin 只负责精确 release 选择、校验、物化、三方升级和 repository-scoped serial staged publish。物化后的源码是 consumer-owned 普通源码；detach 只移除来源管理关系，不移除或降级这些源码。
- Nexactl Build Plugin 只在编译期通过显式 import 和 constructor 组成。未被编入实际二进制的 command、capability 或 provider 不存在，也不以空实现补齐。
- 实际采用必须另外证明业务事实、生成物、运行行为和回滚检查点；neutral/backend-only 参考闭包不能替代 consumer 自己的验收。

## 发现与执行

Consumer 自动化先读取自己的 `AGENTS.md`，再用以下 built-in 从实际二进制读取当前实现信息：

```bash
nexactl inspect --json
nexactl version --json
```

任何 nonbuiltin 命令都必须由当前已审核任务计划明确列出。Inspection 用于核对实际 command path、owner、flags、schema 和副作用；它不批准命令。计划与 inspection 不一致、输入不完整或副作用越界时停止执行。Repository write 前重新确认 affected files，并在执行后运行计划中的正常 test/build/vet/generated-diff 和回滚验证。

任何不直接服务 starter 或受控代码生成的新增框架能力，必须先交用户审核。

首个 RC 明确不选择 runtime `nexa` CLI、Python SDK、Python artifacts 和 HTTP parity；这些项目是非声明，不是待调用能力。V0.1 的 consumer、文档和验证不会写入或探测其命令路径，也不会创建占位 artifact。
