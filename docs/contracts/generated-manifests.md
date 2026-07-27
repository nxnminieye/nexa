# 生成清单

生成清单 package 是为已有 consumer 保留的可重建状态投影数据结构。它不保存业务配置，也不是人工
authoring surface；当前 official RPC/API direct generation command 不创建、读取或依赖这些 manifest，
下述 managed/write lifecycle 也不是当前 official generation 契约。

## Owner

| Manifest | Public owner | 表达内容 |
| --- | --- | --- |
| Artifact Manifest | `generation/artifact` | 一个 generator 管理的 artifact closure |
| API Manifest | `generation/api` | HTTP schema、operation、binding 和 provenance projection |
| Service Manifest | `generation/service` | 一个 consumer service 的 contract source closure |

每个 owner 同时提供 immutable value、constructor/strict parser、canonical JSON 和 versioned schema。

## Identity 与 canonical form

Artifact identity 至少绑定 artifact id、repository-relative path、owner、content digest、source refs、input
digest 和 stale policy。Generator identity 独立包含 id 与 version。Canonical parse 拒绝 unknown field、
错误 `apiVersion`、非法 path、重复 identity、错误 digest 和 non-canonical document。

Digest 使用标准 SHA-256 value，由 `provenance` public type 表达。普通 Git SHA、module checksum 和 release
checksum 保留其各自用途，不被 manifest digest 替代。

## 冻结的 legacy managed artifact

Generator 更新或删除文件前必须用 ownership probe 验证当前内容仍属于相同 generator/artifact/input。
未知文件、业务方已修改且 ownership 不再成立的文件以及 manual logic 都不会被当作普通 generated artifact
接管。

Stale policy 由 artifact owner 明确选择：

- `retain`：输出不再需要时仍保留；
- `delete-if-unmodified`：只有 ownership probe 证明仍为对应 generated output 时才删除。

当前 direct replace-tree 规则见[受控生成](controlled-generation.md)。

## 冻结的 legacy 写入顺序

Plan 从 previous manifest 和当前 facts 计算 next manifest。Write 先验证并发布受控 artifact，最后才写 next
manifest；如果中途失败，不能发布一份声称未完成 artifact 已存在的 manifest。再次执行必须从当前 repository
建立 fresh plan。

## API 与 Service projection

API Manifest 描述 merged native/generated HTTP contract，并保留每个 node 的 provenance。它供 generated
API source 和 consumer 工具读取，不拥有原始 Proto、`.api` 或 Service Catalog。

Service Manifest 只包含当前 service contract sources 及其 closure digest。Source set 或 digest 改变时，
check 返回 drift；它不表示 service 已部署、健康或获得业务批准。

精确字段始终以 owner package schema accessor 为准。
