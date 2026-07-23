# 结果可验证、可回退

“命令成功”不是充分证据。验证必须对应当前 module、输入、生成产物和 consumer composition，并根据变更类型
执行解析、类型检查、编译、测试或真实行为检查。输入、commit、binary 或产物变化后，旧结果不能证明新候选。

Manifest 可以记录来源、generator identity、input digest 和 content digest，用于判断 ownership 与 drift；
它是生成状态的投影，不是业务配置或批准记录。物化后的 starter source、manual logic 和最终发布决定仍由
consumer 拥有。

回退也要区分层级：

- module 问题回到已知依赖版本，并重新构建和读取 inspection；
- generated artifact 从对应事实和 generator 重建，再运行 consumer 验证；
- consumer-owned 普通源码用业务仓自己的版本控制和测试恢复；
- detach 只解除来源追踪，保留已经物化并由 consumer 接管的源码。

具体步骤见[升级与回滚](../adoption/upgrade-and-rollback.md)和[验证矩阵](../adoption/verification-matrix.md)。
