# 业务事实保持唯一

同一项业务事实只保留一个人工入口，并放在最接近其业务含义的位置。Nexa 读取这些事实并生成投影，但不接管
事实所有权。

例如，一个字段的含义和 CRUD 选择属于 consumer 的 Ent typed annotation；RPC method 属于 Proto；native
HTTP route 属于 `.api`；服务拓扑属于 Service Catalog。IR、manifest、生成源码和文档都不能反向成为这些
事实的配置入口。

这样做直接减少两类风险：AI 不需要在多份配置之间猜哪一份有效，生成器也不会用过期投影覆盖业务定义。
只有关系真正跨越多个 owner、且原生格式无法完整表达时，才引入封闭的 typed relation；它引用原事实，不
复制原字段。

物化到业务仓的 starter source 和生成后的普通源码仍归 consumer。框架拥有 loader、IR、generator 和
validator，不因为读取或生成而获得业务所有权。

精确 ownership 见[业务事实契约](../contracts/business-facts.md)。
