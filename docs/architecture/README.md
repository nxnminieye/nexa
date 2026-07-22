# 架构文档

本目录描述 Nexa 持续有效的框架边界与设计来源。架构文档解释 owner、依赖方向和公开契约，但不替代代码、schema、CLI 自省或行为测试。

- [框架架构](framework.md)：module 边界、事实所有权、构建期组合、Source Bundle、受控生成和 Minimum Runtime。
- [设计影响](design-influences.md)：外部参考设计到 Nexa owner、authoring surface、公开契约和行为 gate 的可追溯映射。
- [Tenant Mixin 与统一 CRUD 生成目标设计](tenant-mixin-and-crud-generation.md)：实现待验证的严格租户归属、typed config、租户隔离、默认 logic、显式覆盖和明确不做的范围。
