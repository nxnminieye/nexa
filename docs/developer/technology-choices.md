# 技术选择

Nexa 的技术选择服务于一个目标：让业务事实和生成结果可读、可审查、可复现，并在框架工具退出后仍由
业务仓独立维护。

## Go 与普通源码

框架、工具和公共 runtime 使用 Go。标准服务沿用 go-zero 的 handler/server、logic、svc/model 分层，
Ent 管理数据模型，Proto 与 `.api` 管理传输契约。Generator 输出普通 Go、Proto 和 `.api` 文件，不输出
只能由 Nexa runtime 解释的私有执行格式。

这使 consumer 可以继续使用 Go compiler、Ent、Proto/goctl 和现有 review 工具。框架不接管业务进程
lifecycle，也不要求线上加载动态 plugin。

## Typed facts 而不是注释猜测

机器需要消费的业务语义使用 typed annotation、versioned document 或领域 owner 的结构化 source。普通
comments 只服务人类阅读。Typed facts 可以由 strict parser 验证并投影为 canonical source/digest，避免
generator 从名称、目录或自由文本猜测业务含义。

事实保持在最接近业务 owner 的位置：Ent 语义在 Ent schema，RPC 在 Proto，HTTP contract 在 `.api`，
跨服务关系才进入 Service Catalog。Derived IR 和 manifest 不能反向成为 authoring surface。

## 静态组合而不是运行时插件

Nexactl plugin 通过显式 Go import、constructor 和 `host.New` 组合。编译后的 binary 可用 inspection 暴露
命令、capability、schema 和副作用。没有全局 registry、blank-import discovery、`.so` 或运行时目录扫描。

Service Source Provider 也是工程期对象：它发布 immutable source tree，consumer 选择物化后就获得普通
源码 ownership。服务本身不是 plugin。

## Strict schema 与受控生成

Machine contracts 使用 versioned、closed schema 和 canonical JSON。生成采用 plan/check/write：先从当前
facts 建立完整候选，在 invocation-local staging 中 parse/typecheck/compile，再检查输入和目标未漂移，
最后按文件发布。失败报告真实错误并清理本次 staging；下次从当前工作树重新计划。

同一 worktree 的生成由调用方串行调度。Nexa 不为该低频本地流程提供事务锁、旧事务重放或自动恢复。

## Behavior tests

契约通过 strict parser/schema、external consumer compile、generated output validation、optional absence 和
integration behavior 验证。源码字符串和目录数量不能证明能力存在；测试应调用 public constructor、CLI
协议或真实生成结果。

文档治理方法参考了
[Wails3 Plugin Platform 的公开文档实践](https://github.com/nxnminieye/wails3-plugin-platform/tree/main/docs)，
但 Nexa 的对象边界和主张只由本仓库当前源码与测试决定。
