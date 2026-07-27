# 变更过程受控

AI 从业务事实走到文件写入，中间必须经过能力发现、输入验证、影响分析和写后验证。一次受控生成的基本方向是：

```text
owner facts -> strict load -> canonical typed document
            -> generated/extensions scope validation
            -> replace generated tree -> direct tool -> Git diff and tests
```

IR 用来隔离 parser 与 generator，是可重建投影，不是第二份业务配置。整个声明 generated 目录是唯一替换
单元；extensions 和其他人工源码必须位于其外部。

生成采用普通文件写入语义。若中途失败，返回非零并保留部分变化；使用方通过 Git diff 审阅和恢复。

精确 direct generation 和 replace-tree 行为见[受控生成契约](../contracts/controlled-generation.md)。
