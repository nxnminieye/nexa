# 快速开始

本路径先创建 consumer 并固定 Nexa 版本，再建立同版本 Skill 入口、验证公共 typed facts，最后说明如何
接入 consumer 自己的 build-time composition。需要 Go 1.25 或兼容该 module `go` directive 的 toolchain。

## 1. 创建 consumer 并固定版本

```bash
mkdir nexa-quick-start
cd nexa-quick-start
go mod init example.com/nexa-quick-start
go get github.com/nxnminieye/nexa@<version>
```

`<version>` 必须替换为要采用的精确版本。先写入并读取 consumer 的 `AGENTS.md`，确认本仓的集成、生成和
外部写入规则。

## 2. 同步同版本 Nexa Skill

在 consumer 根目录运行已固定版本的 CLI：

```bash
GOWORK=off go run github.com/nxnminieye/nexa/cmd/nexactl@<version> version --json
GOWORK=off go run github.com/nxnminieye/nexa/cmd/nexactl@<version> \
  skills sync --repo-root "$(pwd)" --json
```

先确认 `version --json` 报告的 Nexa module 版本与 consumer pin 一致。该身份解析适用于真实包含同步命令的
后续版本；`v0.1.0-alpha.1` 尚无该命令，不能用当前文档反推旧 tag 能力。

任何 Nexa 专项操作开始前必须读取同步后的 `nexa-framework-router`，再由 router 按任务选择专项 Skill。
不要使用另一版本的本地 Skill 或当前分支文档解释旧 module；请选择 inspection 中真实包含 `skills sync`
的后续精确版本。同步失败时不得继续使用目标中的旧或部分更新 Skill；修复错误后重新运行同步。

Skill 只负责路由。它不授权写入，也不替代当前源码、schema、CLI inspection、consumer 规则或测试。完整
顺序见 [Skill 路由](skills.md)。

## 3. 声明一个可生成完整前端的 Ent Schema

在 consumer 的 `ent/schema/account.go` 中直接声明 native Ent 结构，并只用 `@nexa` 补充原生语法不能表达的
事实：

```go
// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)


// @nexa label.zh-CN: "账号"
// @nexa label.en-US: "Account"
// @nexa description.zh-CN: "登录账号"
// @nexa description.en-US: "Login account"
// @nexa scope: "global"
// @nexa crud.operations: ["list","get","create","update","delete"]
type Account struct{ ent.Schema }

func (Account) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "名称"
		// @nexa label.en-US: "Name"
		// @nexa description.zh-CN: "账号显示名称"
		// @nexa description.en-US: "Account display name"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		// @nexa crud.read: "include"
		// @nexa crud.mutation: "create-update"
		field.String("name").NotEmpty(),
	}
}
```

验证：

```bash
GOWORK=off go mod tidy
GOWORK=off go test ./...
```

这只证明 module pin、Ent native schema 和 Source Comment carrier 可编译。完整页面还要求同一 FactGraph 中
存在 canonical `.api` CRUD closure 与 frontend YAML page source；生成器据此直接生成 typed client、Formily、
Grid、route/menu/locale 和 Vue entry，不读取 `nexaent`、JSON PageSpec 或字段 mapping。

## 4. 检查参考 CLI

```bash
GOWORK=off go run github.com/nxnminieye/nexa/cmd/nexactl@<version> inspect --json
```

保存并 strict decode envelope，确认 `ok=true` 和 inspection `apiVersion`。不要从本文推断完整命令或 flag。
Reference CLI 的 generation plugin 没有 consumer ProjectProvider，因此它不能替代业务仓自己的事实定位和
toolchain binding。

## 5. 组合 consumer CLI

Consumer 在自己的工程工具入口中显式构造所需 plugin，并交给 `host.New`。常见选择是：

- generation plugin：注入只定位本仓 authoring facts 和受控 toolchain 的 ProjectProvider；
- source adapter：注入明确选择的 Source Provider、cache、executor 和 toolchain；
- 业务私有 plugin：保留在业务仓，通过 public `nexactl/plugin` contract 实现。

构建完成后，必须对这个 consumer binary 重新执行 `inspect --json`。只有 inspection 中真实存在的 command
才能进入后续计划。

## 6. 受控生成

对选定 generation command 使用固定顺序：

```text
inspect -> validate typed inputs and declared scopes -> direct generate
        -> review git diff -> parse/typecheck/compile -> consumer build/test
```

`repo-root`、provider、service 等参数从当前 inspection 的 flag/schema 读取。当前 official generation plugin
公开 Ent CRUD Proto check/generate、RPC、API 与 frontend direct generate；Reference CLI 没有 consumer ProjectProvider，真实生成必须由
consumer composition 提供 typed facts、delegated tool 和 generated/extensions scopes。Frontend 还必须提供
frontend YAML source、canonical FrontendIR 与 exact frontend source lock digest。生成后用 Git diff 审阅。

需要标准服务源码时，在 generation 前按[标准服务 starter](../starters/standard-services.md)执行 source plan 和
materialize。完整检查见[验证矩阵](verification-matrix.md)。
