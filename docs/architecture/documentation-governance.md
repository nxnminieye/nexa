# 文档治理

Nexa 文档帮助人理解当前公开实现；它不替代 Go API、versioned schema、CLI inspection、生成 manifest
或测试。遇到冲突时，以对应 owner 的机器事实和当前行为为准，并修正文档。

## 文档分层

| 层 | 回答的问题 | 权威边界 |
| --- | --- | --- |
| `concepts` | 为什么这样工作、五个核心概念是什么 | 建立认知，不复制完整流程或机器清单 |
| `developer` | Nexa 是什么、什么可依赖、为什么这样设计 | 解释与判断方法，不复制精确清单 |
| `architecture` | 当前组件如何分工和依赖 | 当前结构与 ownership，不保存迁移目标 |
| `contracts` | 稳定协议具有什么语义 | 解释协议；精确字段由 owner type/schema 拥有 |
| `starters` | 首次交付哪些普通源码 | source composition、移交和后续生成衔接 |
| `adoption` | 使用者按什么顺序执行 | 任务步骤与验证入口，不包含特定业务仓材料 |
| `plugins` | build-time 组合点承担什么职责 | public constructor/contract，不声明实际已编入能力 |

## 一个主题一个 owner

一个完整规则只在一份 owner 文档维护。其他文档可以保留理解上下文和 owner 链接，但不复制字段表、
命令矩阵或生命周期。owner 分工如下：

- 核心概念与阅读路径：`concepts/README.md`；
- 业务开发者定位与 consumer ownership：`developer/nexa.md`；
- 框架边界与变更影响：`developer/framework.md`；
- 可依赖 surface 与同版本边界：`developer/stable-surface.md`；
- authoring/typed facts：`contracts/business-facts.md`；
- plan/check/write 与 generated/manual ownership：`contracts/controlled-generation.md`；
- generated manifest：`contracts/generated-manifests.md`；
- source release、lock 与源码升级：`contracts/source-bundles.md`；
- build-time Host/plugin composition：`plugins/nexactl-build-plugin.md`；
- 标准服务源码选择与移交：`starters/standard-services.md`。

## 事实优先级

新增或修改公共主张至少绑定一种当前证据：

1. public Go type、constructor 或 package documentation；
2. owner package 返回的 versioned schema；
3. 当前二进制的 `nexactl inspect --json`；
4. public fixture、external consumer test 或 integration test；
5. Makefile/CI 中真实执行的验证入口。

Markdown 不手抄当前 exported symbol、命令 flag、Provider/profile 或 capability 全量清单。读者需要精确
事实时，直接读取 owner package、schema 或 inspection。

## 当前事实与索引

- 只把已经实现并有行为证据的能力写成可用能力。
- 可选能力必须明确写成可选；package 或源码目录存在不代表它已经进入 consumer binary。
- 概念页只解释“是什么”和“为什么”，并链接到 developer、architecture 或 contract owner。
- 同一主题不要分别为业务开发者和框架开发者复制正文；读者路径在各自 developer 入口汇合。
- 图只在责任、依赖或数据流关系比短文更清晰时使用，不为每页建立固定模板或装饰性图表。
- 新增、移动或删除永久文档时同步更新 `docs/README.md` 及相关 owner 链接。
- 版本限制写在最接近使用入口的位置，不在每份 contract 重复版本流水账。

## 不进入公开文档的材料

实施计划、评审记录、命令输出、业务仓路径、内部阶段名、发布候选过程和本机工作区状态属于过程材料，
不得进入永久公开文档。公开文档也不把 checksum 或 Git history 解释为人工批准。

## 工具边界

本治理不引入 `nexactl docs`、文档链接检查器、stable-surface YAML 或新的 CI gate。发现重复性工具缺口时
先记录并单独审核；不能为整理 Markdown 顺手扩展框架能力。
