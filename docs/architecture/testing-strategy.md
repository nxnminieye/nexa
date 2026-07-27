# 测试策略

Nexa 使用不同层次的测试证明不同主张。任何单一测试都不能替代完整 consumer 验证。

## 证据层次

| 测试类型 | 证明什么 | 不证明什么 | 主要入口 |
| --- | --- | --- | --- |
| Unit | constructor、value object、错误和局部算法 | 外部 module 可用或生成物可编译 | owner package 的 `*_test.go` |
| Strict parser/schema | version、字段闭合、canonical round-trip 和无效输入拒绝 | 业务语义选择正确 | contract package tests |
| External consumer | public import 不依赖 `internal`，最小调用可编译运行 | 特定业务仓目录和配置正确 | `fixtures/consumers/*`、`integration/*external*` |
| Generated output | 生成结果可 parse、typecheck、compile，ownership 可验证 | 生成前的业务决定正确 | `generation/*` 与 generation integration tests |
| Optional composition | 缺席 package/plugin/provider 时基础组合仍成立 | 被选择能力的线上依赖可用 | unlinked/minimum/optional tests |
| Integration | facts、CLI、source、generation 和 runtime 的跨包行为 | 部署、数据库和远端系统健康 | `integration` |

测试通过行为、schema 和结果证明能力，不通过搜索源码字符串、统计文件数量或检查某个目录存在来证明。

## Make targets

开发者通常只需要记住三个入口：

| 入口 | 什么时候运行 | 包含内容 | 失败意味着什么 |
| --- | --- | --- | --- |
| `make check` | 编码过程中、提交前 | 全仓测试代码编译、vet、CLI build、独立 consumer fixture；不运行重型测试 | 当前代码的基础编译或局部组合已损坏 |
| `make integration` | PR 提交前；PR CI 自动运行 | `make check`、非 integration package tests，以及除完整 source-bundle/runtime 闭环外的 integration tests | 公共能力之间或外部 consumer 用法不兼容 |
| `make release-check` | 合并到 `main` 或发布版本前；对应 CI 自动运行 | `make integration` 加完整 source materialize、generate、compile、runtime、detach 和重复生成验证 | 该版本不能作为可发布的 consumer 闭环 |

`make help` 会在终端显示这三个入口。耗时较长的
`TestSourceBundleCore*` 测试族只属于 `make release-check`，日常检查和 PR 不会重复执行它。这个测试族
保留发布级证据，但不再阻塞每次本地修改。

CI 将 `make check`、非 integration package tests 和 PR integration tests 拆成独立 job 并行执行，失败时
直接显示对应层次。`main` 和版本 tag 另外运行 source-bundle/runtime 发布门禁。本地三个入口仍按从快到全
的顺序串行执行，行为与 CI 的验证集合一致。

`make contracts`、`make generated-check`、`make source-contracts`、`make service-contracts`、
`make source-bundle-runtime`、`make runtime` 等是定位特定问题的专项入口。兼容入口
`make service-bundles` 仍会执行 service contract 和完整 source-bundle/runtime 闭环。

这些 target 是当前仓库的执行入口，不是永久命令清单。变更 Makefile 时必须同步本页；文档不记录当前
测试数量或某次输出。

## 生成验证

Generator 应在独立 staging 中验证完整候选：

1. owner facts 和 versioned IR 可 strict load；
2. rendered Proto/`.api`/Go 可由真实 parser 或 toolchain 接受；
3. generated Go 在隔离 module context 中 typecheck/compile；
4. plan 中的 sources、target 和 ownership 在发布前仍未漂移；
5. manual file 默认不被重复生成覆盖，显式 overwrite 绑定旧内容。

测试不把一次成功写入解释为部署成功，也不建立同一 worktree 并发生成的支持承诺。

## Starter 与 source 验证

Source Provider 测试应验证 manifest/tree digest、profile closure、路径安全和不可变读取。Integration test
再把选定 profile 物化到临时 consumer，执行必要生成、build/test 和进程行为，并验证 detach 后普通源码不
依赖 provider 或 source tool。

## Consumer 验证

Framework 测试只提供中立证据。业务采用还必须在 consumer 隔离 worktree 中验证 module graph、实际
`nexactl inspect --json`、facts、generated diff、build/test、运行行为和回滚。具体检查见
[验证矩阵](../adoption/verification-matrix.md)。
