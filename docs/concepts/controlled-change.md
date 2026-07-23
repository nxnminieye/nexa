# 变更过程受控

AI 从业务事实走到文件写入，中间必须经过能力发现、输入验证、影响分析和写后验证。一次受控生成的基本方向是：

```text
owner facts -> strict load -> versioned IR -> complete plan
            -> isolated staging and validation -> input/target recheck
            -> bounded file publish -> manifest written last
```

IR 用来隔离 parser 与 generator，是可重建投影，不是第二份业务配置。Plan 应让调用方看清 create、update、
delete 和 conflict；写入前必须重新确认输入、目标与 generated/manual ownership。生成器只能更新能够证明由
自己管理的文件，未知文件和 consumer 已接管的 manual logic 不能被顺手覆盖。

发布采用普通文件写入语义。若中途失败，保留真实错误并从当前工作树重新计算；不把部分完成解释为成功，
也不依靠隐藏的流程状态继续推进。

精确 plan/check/write 和 staging 行为见[受控生成契约](../contracts/controlled-generation.md)。
