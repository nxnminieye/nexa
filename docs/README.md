# Nexa 文档

本目录维护 Nexa 最终态架构、稳定契约和公开使用方式。

## 渐进式阅读

1. 从仓库根 `AGENTS.md` 了解 AI-native、所有权和测试边界。
2. 阅读[框架架构](architecture/framework.md)，确认对象分类、依赖方向和业务事实所有权。
3. 涉及自动化时读取[CLI 机器协议](contracts/cli-machine-protocol.md)，再通过 `nexactl inspect --json` 查询当前二进制的真实能力。
4. 涉及业务事实、受控生成、生成清单或标准源码物化时读取对应公共契约，再按 owner package accessor 获取 machine schema。
5. 涉及代码组合时进入对应插件文档；具体实现与可用性以 public Go API、测试和 release 证据为准。

## 架构

- [架构文档索引](architecture/README.md)：框架边界与设计来源入口。
- [框架架构](architecture/framework.md)：module 边界、最近事实源优先级、领域 owner、Service Catalog v1 封闭 binding、typed relation 演进、Business API Composition、可选能力和 Minimum Runtime。
- [设计影响](architecture/design-influences.md)：参考设计到 Nexa owner、authoring surface、公开契约和行为 gate 的固定版本映射。
- [Tenant Mixin 与统一 CRUD 生成目标设计](architecture/tenant-mixin-and-crud-generation.md)：实现待验证的严格 Tenant mixin、多租户生成条件、统一 CRUD 协议与默认 logic、覆盖行为及非目标。

## 采用

- [Consumer 闭包](adoption/consumers.md)：neutral、backend-only 与真实业务采用边界。
- [Skill 分发与路由](adoption/skills.md)：skill asset、inspect-first 能力发现和已审核任务计划的执行边界。
- [升级与回滚](adoption/upgrade-and-rollback.md)：本地 module/workspace 切换保护、四层回滚和各层验证责任。

## 公共契约

- [CLI 机器协议](contracts/cli-machine-protocol.md)：envelope、错误分类、exit code、operation id、输出通道和能力自省。
- [业务事实契约](contracts/business-facts.md)：事实所有权、Service Catalog、最近事实源、typed relation、Ent 责任边界和 machine schema accessor。
- [受控生成契约](contracts/controlled-generation.md)：typed owner facts、Entity/Protocol/API/Composition IR、12 命令矩阵、ProviderTool、serial staged publish ownership、可选组合与设计影响边界。
- [生成清单契约](contracts/generated-manifests.md)：Artifact/API Manifest、provenance、确定性、artifact ownership 和 stale policy。
- [Source Bundle 契约](contracts/source-bundles.md)：owner、identity、Provider、resolver/cache、安全树、七命令、provenance、三方升级、serial staged publish 与错误投影。
- [Quality Read Model 契约](contracts/quality-read-model.md)：只读 requirement coverage wire、strict schema、canonical snapshot、digest、empty 与可选消费边界。
- [Runtime 公共契约](contracts/runtime-packages.md)：CRUD、Kafka/franz、slog、gRPC access、OTel adapter、实例所有权与 Minimum Runtime 可选链接边界。

## 插件

- [Service Source Plugin](plugins/service-source-plugin.md)：标准服务 Provider、物化后的业务所有权和普通服务边界。
- [标准服务 Source Bundles](plugins/standard-service-source-bundles.md)：Framework Minimum、Core reference 组合、官方可选源码 package 和 materialize/generate/run 独立性。
- [Nexactl Build Plugin](plugins/nexactl-build-plugin.md)：工程命令的编译期显式组合、能力依赖和业务私有扩展。

## 维护规则

- 文档描述持续有效的最终契约，不记录仓库差异、一次性实施步骤或临时状态。
- 新增或移动文档必须同步更新本索引或对应分域索引。
- 文档不能替代代码、测试、schema、CLI 输出或 release 证据。
