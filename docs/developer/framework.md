# 框架开发者路径

本页用于判断一次框架变更应该落在哪里、会影响谁。精确字段和命令仍由 public type、schema、CLI inspection
和测试拥有。

## 系统地图

```text
Consumer authoring facts
  -> Nexa public typed contracts and strict loaders
  -> versioned IR and deterministic generation
  -> Consumer-owned ordinary source and manifests

Source Provider -> materialized source -> Consumer authoring/generation path
Runtime packages ----------------------> Consumer runtime composition
Nexa Skills -> owner and task routing --> source/schema/CLI/test facts
```

依赖沿箭头方向流动。Projection 可以追踪 authoring fact，但不能反向成为新的人工入口。

## 三条边界

### Framework / Consumer

Framework 拥有可复用的 public contract、loader、parser、IR、generator、validator 和 CLI 协议。Consumer
拥有业务事实实例、组合选择、生成产物、manual logic、迁移、部署和运行结果。框架测试证明公共行为，不
证明某个 consumer 已采用或上线。

### Public / Internal

Canonical module 下不含 `internal` path、具备清晰职责并有 external-consumer 行为测试的 exported API，才
可能成为公共面。`internal/**`、参考 composition 的偶然链接关系和目录存在都不是兼容承诺。Consumer 不能
import Nexa internal package。

### Authoring / Projection

Ent typed annotation、Proto、`.api` 和 Service Catalog 是各自领域的 authoring surface。IR、manifest、
inspection、生成源码和 read model 是 projection。新增字段前先确定 owner；若它能从现有事实确定性重建，
就不应再创建人工配置入口。

## Change-impact matrix

| 变更 | 首要 owner | 必查影响 |
| --- | --- | --- |
| public Go type 或 constructor | 对应 public package | external consumer、错误/可选值、版本说明 |
| machine document | type/schema owner package | strict parse、canonical projection、`apiVersion` |
| CLI command 或 flag | `nexactl/host` 或 owning plugin | inspection、envelope、exit code、stdout/stderr |
| authoring fact 或 relation | 最近的 consumer fact owner | 重复入口、引用闭合、下游 IR |
| generator | 对应 `generation/*` owner | 确定性、staging、ownership、drift、consumer build/test |
| source release | Provider/source contract | immutable tree、selection、materialize/upgrade/detach |
| runtime package | 对应 `runtime/*` package | import/constructor 选择、未选择时零要求 |
| Nexa Skill | 同版本 Skill asset | owner 路由、实际 contract 链接、不得复制机器清单 |

完整组件关系见[当前架构](../architecture/framework.md)，公共承诺判断见[稳定边界](stable-surface.md)，
文档 owner 见[文档治理](../architecture/documentation-governance.md)。
