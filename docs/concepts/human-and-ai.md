# 人做判断，AI 做工程

Nexa 的 AI-first 不是让 AI 替代业务 owner，而是把人的判断与可自动化的工程工作分开。

```text
人：目标、业务语义、风险取舍、结果接受
                 |
                 v
AI：发现契约、分析影响、实施变更、运行验证
                 ^
                 |
业务仓：事实与源码       Nexa：Skill、公共契约与工程能力
```

人不必逐条拼装生成命令，但必须给出足够明确的目标和边界，并对语义冲突、兼容性取舍与最终结果作出判断。
AI 应主动读取仓库规则和 Nexa Skill，发现当前能力，限制 affected files，并用最新验证说明结果。它不能因为
能够修改文件，就自行决定产品语义或扩大副作用。

能稳定检查的规则应落在类型、schema、CLI、测试或 CI 中。Skill 和文档负责导航与解释，不应成为唯一约束。

继续阅读：[Skill 路由](../adoption/skills.md)和 [CLI 机器协议](../contracts/cli-machine-protocol.md)。
