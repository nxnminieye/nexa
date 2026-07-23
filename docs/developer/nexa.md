# Nexa 是什么

Nexa 是面向业务后端的 AI-first Go framework。它让 AI 能从业务仓中的强类型事实出发，使用可发现的工程
契约生成、检查和验证普通源码，同时让业务团队继续拥有事实、源码、部署和产品决策。

## 它带来的价值

- **减少重复事实**：Ent、Proto、`.api` 和 service topology 各自在最近的 owner 中维护一次。
- **让自动化可检查**：公共 type、versioned schema、CLI inspection 和稳定错误语义共同约束 AI 行为。
- **让生成结果可接管**：starter 与 generated output 都落为 consumer 可阅读、构建和测试的普通源码。
- **按需采用**：事实契约、受控生成、标准源码 starter 和 runtime package 可以独立选择。

## 能力组

Nexa 的公共能力分为四组：

1. typed facts 与 strict loader，把 consumer authoring surface 投影为稳定模型；
2. versioned IR、plan/check/write、staging validation 与 generated/manual ownership；
3. Source Provider 与 source lifecycle，用于交付可复现的标准服务源码；
4. 可独立 import 和 constructor 组合的 runtime contract 与 adapter。

`nexactl` 是工程期工具 Host。Plugin 通过显式 Go import 和 constructor 编入二进制，不是运行时动态插件；
某个 consumer 实际拥有的命令只能由它使用的二进制 `inspect --json` 证明。

## Consumer ownership

Consumer repository 始终拥有业务 Ent schema、Proto、`.api`、服务拓扑、产品配置、已物化源码、业务修改、
生成选择、数据库迁移、部署配置、secret、运行实例和发布节奏。Nexa 拥有公共 contract、loader、IR、
generator 与 validator，但不会因为读取这些内容而获得业务事实或最终文件的决定权。

删除工程期 source/generation 工具后，已经移交的普通源码仍按其 Go 依赖构建和运行。Manifest 和 lock 只
记录可重建的来源与状态，不把源码所有权移回框架。

## 当前 alpha 可用性

当前公开检查点是 `v0.1.0-alpha.1`，用于验证公共契约和 consumer 集成，还不是完整 V0.1 或 RC。它包含
typed Ent metadata、已验证的 generation 能力、参考 `nexactl` composition、标准源码能力与若干可选 runtime
packages。参考 CLI 不等于 consumer CLI；package 存在也不等于已经进入最终二进制或支持面。

可发布的 Python SDK/wheel、runtime `nexa` CLI、动态插件、远程控制、自动部署和产品私有规则不属于当前
alpha 支持面；reference CLI 中存在 `sdk-python-assets` 工程命令不等于 Python SDK 已发布。统一 CRUD logic
同样不属于本 alpha 支持面；目录或 exported API 存在不能替代通过的 public behavior test。

采用前先从[核心概念](../concepts/README.md)建立共同语言，再按[快速开始](../adoption/quick-start.md)执行。
需要修改框架本身时进入[框架开发者路径](framework.md)。
