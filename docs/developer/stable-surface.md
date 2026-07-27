# 稳定边界

本页说明如何判断某个 Nexa surface 是否可以被 consumer 依赖。它不列出所有 exported symbol、命令或
schema 字段；精确事实由对应 owner 提供。

## 同版本边界

已发布版本的采用必须把三个对象视为同一个 Git tag 集合：

- `github.com/nxnminieye/nexa` 的精确 module 版本；
- 该版本仓库中的 public docs 与 versioned schema；
- 该版本 module 内嵌并由同版本 `nexactl skills sync` 导出的 Nexa Skills，入口为 `nexa-framework-router`。

未发布源码工作则让 module source、docs、CLI 和 Skills 来自同一个 commit。AI 不得用当前分支的较新 Skill 或
文档操作较旧 tag，也不得用旧文档解释较新二进制。三者无法确认来自同一 ref 时，先停止框架操作并对齐。
对齐后仍需读取 consumer 实际二进制的 fresh inspection，因为相同 module 也可组成不同的 `nexactl`。

## Public Go API

位于 canonical module `github.com/nxnminieye/nexa` 下、可被外部 module import 且不含 `internal` path 的
exported API，才可能是公共 Go surface。是否适合采用还应同时确认：

- package documentation 明确其职责；
- constructor/type 对错误和可选值有稳定定义；
- external-consumer 或 integration test 覆盖目标用法；
- 当前版本说明没有把它排除在支持面外。

目录存在、测试内部可见或被参考二进制间接链接，都不能单独证明公共承诺。Consumer 不得 import Nexa
的 `internal` package。

## CLI 机器协议

CLI 稳定边界是 envelope、版本化 inspection/result schema、错误 category、exit code 和 stdout/stderr
分工。某个命令是否存在、有哪些 flag 和副作用，由**当前实际二进制**的以下输出决定：

```bash
nexactl inspect --json
nexactl version --json
```

README、Skill、Make target 和旧 inspection 不能替代 fresh inspection。Reference `cmd/nexactl` 的组合也
不代表业务二进制必须包含相同 plugin。

## Versioned schema 与 canonical document

Machine document 的 owner package 同时拥有 Go type、strict parse、schema accessor 和 canonical projection。
Consumer 应保存并校验 `apiVersion`，拒绝 unknown field、错误类型和不完整 document；不能根据 Markdown
重建 schema。Schema accessor 返回的 bytes 只描述协议，不授权 repository write 或业务决策。

## Generated 与 extensions source

当前 official generation 的稳定边界是 typed input、声明的 generated/extensions scopes、direct tool 协议
和确定性输出。Generator 校验路径边界后直接清空并重建 generated 目录，不维护 ownership manifest、plan、
staging、manual create-once 或 overwrite 契约。

Materialized starter source、generated output 和 extensions 都归 consumer。Extensions 必须位于 generated
scopes 外且不受 generator 影响；框架不会解释 Git diff，也不会自动决定业务方是否接受新版本差异。

## 可选与实验性组合

Public package 可以是可选能力。只有 import、link、constructor composition 和 inspection 共同证明它进入
了某个 consumer。未选择能力时，不应要求其 broker、provider、配置、目录或运行实例存在。

Alpha 可以暴露供集成验证的公共 API，但不等同于 RC 稳定性。升级前应重新运行
[验证矩阵](../adoption/verification-matrix.md)，并按版本说明处理变化。
