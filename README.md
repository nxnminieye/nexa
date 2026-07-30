# Nexa

Nexa 是面向业务后端的 AI-first Go 框架。它提供强类型业务事实、可审查的代码生成、可选的标准服务
starter，以及按 Go package 独立组合的运行时契约；业务事实、生成后的源码、部署和产品规则始终由
consumer repository 拥有。

首个公开检查点是 `v0.1.0-alpha.1`。当前 `main` 可以领先于该 tag；使用已发布版本时，必须以对应 tag 的
README、Skill、源码和 CLI inspection 为准。

## 安装

```bash
go get github.com/nxnminieye/nexa@<version>
```

## AI 采用入口

AI 开发或采用 Nexa 时，先读取 consumer 的 `AGENTS.md` 并确认精确 module pin，再用同版本 `nexactl`
同步 Nexa Skills：

```bash
GOWORK=off go run github.com/nxnminieye/nexa/cmd/nexactl@<version> version --json
GOWORK=off go run github.com/nxnminieye/nexa/cmd/nexactl@<version> \
  skills sync --repo-root "$(pwd)" --json
```

对真实包含 `skills sync` 的后续版本，`version --json` 应报告 `go run ...@<version>` 解析到的 Nexa module
版本；先核对它与 consumer module pin 一致。`v0.1.0-alpha.1` 尚无同步命令，不能按本段流程采用。

同步完成后，先读取 consumer 中的 `.codex/skills/nexa-framework-router/SKILL.md`，再按任务进入专项 Skill。
`skills sync` 完整替换 Nexa 管理的 `nexa-*` Skill 目录，但保留 consumer 的其他 Skill；不要从当前分支手工
复制 Skill 去解释旧 module。同步失败时不得继续使用目标中的旧或部分更新 Skill；修复错误后重新运行同步。
Nexa Skills 自包含，不依赖 Superpowers 或其他外部工作流。它们约束 AI 如何定位事实、选择能力和验证结果，
但不成为 Go build 或业务服务运行依赖，也不替代当前二进制的真实能力发现。

完整边界和顺序见 [Nexa Skill 路由](docs/adoption/skills.md)。

参考 `nexactl` 的实际命令、flag、schema、capability 和副作用只能从编译后的二进制读取：

```bash
GOWORK=off go run github.com/nxnminieye/nexa/cmd/nexactl@<version> inspect --json
```

`cmd/nexactl` 是参考组合。业务仓应组合自己需要的 build-time plugin，并对自己的二进制重新执行
`inspect --json`；没有出现在 inspection 中的能力不可调用。

## 当前边界

- `nexa.dev/source-comment/v1` 是 Ent、Proto、`.api` 与 frontend source 的统一补充事实入口；原生结构仍由各语言语法拥有。
- 已验证的 generation package 把 consumer-owned facts 投影为 versioned IR、普通源码和可重建 manifest。
- Service Source Provider 可以交付标准服务源码；源码物化后由 consumer 修改、构建和运行。
- `runtime/*` package 按 import 和 constructor 显式选择，不构成运行时插件系统。
- S3 公共入口为 `runtime/s3`，AWS SDK v2 adapter 为可选的 `runtime/s3/aws`；完整边界见 [Runtime packages](docs/contracts/runtime-packages.md)。
- 可发布的 Python SDK/wheel、runtime `nexa` CLI、动态插件、远程控制和产品私有规则不属于本 alpha 支持面；
  未发布的 Python SDK runtime 已随旧 `sdk/api` descriptor contract 删除。

当前参考 CLI 提供 CRUD Proto 生成，但统一 CRUD logic 不属于本 alpha 支持面。源码目录或 exported API
存在不能替代通过的 public behavior test，也不能据此推断 consumer 命令。

## 从这里开始

- [五个核心概念](docs/concepts/README.md)
- [业务开发者视角](docs/developer/nexa.md)
- [框架开发者视角](docs/developer/framework.md)
- [采用快速开始](docs/adoption/quick-start.md)
- [稳定边界](docs/developer/stable-surface.md)
- [文档任务索引](docs/README.md)

安全问题请参阅 [SECURITY.md](SECURITY.md)。
