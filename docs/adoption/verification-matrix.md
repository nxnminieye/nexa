# 采用验证矩阵

每一层都需要独立证据。上一层通过不能替代下一层，也不能把 framework fixture 结果当作业务仓结果。

| 层 | 必查内容 | 权威入口 | 通过标准 |
| --- | --- | --- | --- |
| Module | module path、version、`go.mod/go.sum` | `go list -m`、`go mod verify` | 解析到预期版本且 checksum 有效 |
| CLI | binary identity、command、flag、schema、side effect | `nexactl version --json`、`inspect --json` | 当前二进制明确暴露待用能力 |
| Starter/source | selection、profile closure、manifest/tree digest、target | source plan/result 与 Source Lock | 选择可复现，无未审核冲突 |
| Facts | Ent/Proto/`.api`/catalog owner 和 strict parse | owner package/schema、consumer tests | 无 legacy fallback 或重复 source |
| Generated | plan digest、affected files、ownership、staging validation | generation plan/check/result、manifest | 候选可 parse/typecheck/compile，输入未漂移 |
| Manual logic | 默认 create-once 或显式 overwrite | generation plan 与 target prior digest | 默认保留业务修改；overwrite 明确且无写前漂移 |
| Build/test | consumer module 和目标服务 | `go test`、`go vet`、consumer build command | 使用真实 consumer composition 通过 |
| Optional absence | 未选择 plugin/package/provider | minimal binary、unlinked build、启动 smoke | 不需要该能力的配置、目录或运行依赖 |
| Upgrade | old/local/new source 与新 module/generated projection | source diff/upgrade、fresh generation plan | 冲突显式，clean path 重新验证 |
| Rollback | module、generated、source checkpoint 分离 | 文件 checkpoint、manifest/lock、fresh inspection | 只恢复目标层且其他层不被暗改 |

## 推荐顺序

1. 在隔离 worktree 记录 module/workspace 和目标业务文件状态。
2. 验证 module，再读取 consumer binary 的 fresh inspection。
3. 如需 starter，先 plan/materialize 并验证 source，再从 native facts 生成。
4. 对 generation 执行 plan/check，审核 write set 后才 write。
5. 运行 generated parse/typecheck/compile 和 consumer build/test。
6. 证明未选择能力确实缺席，不创建占位配置或空 plugin。
7. 在同一 worktree 演练对应层的 rollback。

数据库、集群、部署和远端服务属于 consumer ops 验证，不由本矩阵的 framework checks 代替。
