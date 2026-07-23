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

## 3. 验证公共 typed facts

创建 `main.go`：

```go
package main

import (
	"fmt"

	"github.com/nxnminieye/nexa/nexaent"
)

func main() {
	annotation := nexaent.Schema(nexaent.SchemaMeta{
		Label: nexaent.LocalizedText{
			Key: "account.label", ZhCN: "账号", EnUS: "Account",
		},
		Description: nexaent.LocalizedText{
			Key: "account.description", ZhCN: "登录账号", EnUS: "Login account",
		},
		Identity: nexaent.IdentityEntID,
		Scope:    nexaent.ScopeGlobal,
	})
	data, err := annotation.CanonicalJSON()
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
}
```

验证：

```bash
GOWORK=off go mod tidy
GOWORK=off go test ./...
GOWORK=off go run .
```

这只证明 module pin、public import 和 typed annotation 可用，不代表 generation 或 source plugin 已进入
consumer。

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
inspect -> plan -> review affected files and current inputs -> check -> bounded write
        -> parse/typecheck/compile -> consumer build/test
```

`repo-root`、provider、service 等参数从当前 inspection 的 flag/schema 读取。Plan 和 check 是只读操作；
write 前重新确认 facts、target、affected files 和 manual/generated ownership。当前 reference CLI 暴露
CRUD Proto、RPC、API 和 Service Manifest 生成；统一 CRUD logic 不属于本 alpha 支持面。

需要标准服务源码时，在 generation 前按[标准服务 starter](../starters/standard-services.md)执行 source plan 和
materialize。完整检查见[验证矩阵](verification-matrix.md)。
