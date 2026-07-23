# 核心概念

理解 Nexa 不需要先读完整架构和协议。先掌握下面五个概念，再按任务进入开发者路径或精确契约：

1. [人做判断，AI 做工程](human-and-ai.md)：人拥有目标、业务边界和接受判断，AI 负责工程实施与验证。
2. [业务事实保持唯一](business-facts.md)：事实只在最接近业务含义的位置编写，其他内容由它投影。
3. [AI 读取真实契约](executable-contracts.md)：AI 必须从同版本 Nexa Skill 进入，并以源码、schema、CLI 自省和测试确认实际能力。
4. [变更过程受控](controlled-change.md)：先确认输入、写集和 ownership，再在隔离位置生成、验证和发布。
5. [结果可验证、可回退](verification-and-rollback.md)：验证绑定当前输入和产物，回退按依赖、生成物和普通源码分层处理。

业务开发者接着阅读 [Nexa 是什么](../developer/nexa.md)；框架开发者接着阅读
[框架开发者路径](../developer/framework.md)。精确行为由[业务事实契约](../contracts/business-facts.md)、
[受控生成契约](../contracts/controlled-generation.md)、其他主题 owner 文档和当前实现拥有。
