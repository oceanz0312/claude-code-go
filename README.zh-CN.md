# claude-code-go

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen.svg)](go.mod)
[![E2E Tests](https://img.shields.io/badge/E2E_tests-all_passed-brightgreen.svg)](#测试)

> **[claude-code-node](https://github.com/oceanz0312/claude-code-node) 的完整 Go 复刻版本** — 封装 Claude Code CLI 的 Go SDK，让你在 Go 中以编程方式驱动 Claude Code，干净、惯用的 API，零外部依赖。

[English Documentation](./README.md)

---

## 你能得到什么

```go
package main

import (
	"context"
	"fmt"

	claudecodego "github.com/oceanz0312/claude-code-go"
)

func main() {
	claude := claudecodego.NewClaudeCode(claudecodego.ClaudeCodeOptions{
		APIKey: "sk-ant-...",
	})

	session := claude.StartSession(claudecodego.SessionOptions{
		Model:                      "sonnet",
		DangerouslySkipPermissions: true,
	})

	turn, err := session.Run(context.Background(), "Fix the failing tests in src/")
	if err != nil {
		panic(err)
	}
	fmt.Println(turn.FinalResponse)

	// 多轮对话：直接继续 Run() — 会话恢复是自动的
	turn2, _ := session.Run(context.Background(), "Now add test coverage for edge cases")
	fmt.Println(turn2.FinalResponse)
}
```

**没有 HTTP 服务器，没有协议转换，没有过度抽象。** 只是对 Claude Code CLI 的类型化 Go 封装，替你处理繁琐的部分：

| 能力 | 做了什么 |
|------|----------|
| **会话管理** | 跨轮次自动 `--resume` — 你永远不需要手动管理 session ID |
| **流式输出** | `StreamedTurn` + `Next()` 迭代器，7 种类型化事件 |
| **35+ CLI 选项** | 每个常用 flag 都有类型化字段 — `Model`、`SystemPrompt`、`AllowedTools`、`JSONSchema`、`MaxBudgetUSD`、`Agents`、`MCPConfig`… |
| **结构化输出** | 传入 JSON Schema，从 `turn.StructuredOutput` 拿到解析后的对象 |
| **图片输入** | 通过 `InputItem` 将本地图片与文本一起发送 |
| **取消控制** | 用 `context.Context` 取消任意 turn |
| **FailFast** | 秒级检测 API 错误，而非等待数分钟（CI/CD 场景关键能力） |
| **零依赖** | 纯标准库，无任何外部模块 |

---

## 与 claude-code-node 的关系

本项目是 [claude-code-node](https://github.com/oceanz0312/claude-code-node)（TypeScript SDK）的**完整 Go 复刻**，保持了一致的架构设计和功能覆盖：

| 特性 | claude-code-node (TS) | claude-code-go (Go) |
|------|----------------------|---------------------|
| 会话管理 & 自动恢复 | ✅ | ✅ |
| 流式输出（7 种事件） | ✅ AsyncIterable | ✅ StreamedTurn.Next() |
| 35+ CLI 参数覆盖 | ✅ | ✅ |
| 双层事件体系 | ✅ | ✅ |
| 流式去重 | ✅ | ✅ |
| Fake CLI 模拟器 | ✅ fake-claude.mjs | ✅ fakeclaude (Go) |
| E2E 测试全通过 | ✅ | ✅ |
| 零外部依赖 | ❌ (Node.js) | ✅ 纯标准库 |

---

## 测试

**E2E 测试全部通过。** 本项目拥有完整的测试体系，确保与 Claude Code CLI 的兼容性：

| 指标 | 详情 |
|------|------|
| **单元测试** | 基于 Go 版 Fake CLI 模拟器（`testdata/fakeclaude/`），无需真实 CLI 或 API Key |
| **E2E 测试** | 11 个真实模型测试，覆盖流式/非流式、图片输入、系统提示词、Agent 角色、会话管理、CLI 参数转发 |
| **全部通过** | 所有 E2E 测试用例均通过，验证了与 claude-code-node 的功能一致性 |
| **测试产物** | 每次运行保存 NDJSON 日志、中继事件和最终响应到 `tests/e2e/artifacts/`，便于事后分析 |

---

## 前置要求

- Go >= 1.26
- 已安装 Claude Code CLI，且 `claude` 可在 `PATH` 中找到（或通过 `CLIPath` 显式指定路径）

## 安装

```bash
go get github.com/oceanz0312/claude-code-go
```

---

## 快速上手

### 缓冲模式（非流式）

```go
claude := claudecodego.NewClaudeCode(claudecodego.ClaudeCodeOptions{
    APIKey: "sk-ant-...",
})

session := claude.StartSession(claudecodego.SessionOptions{
    Model:                      "sonnet",
    DangerouslySkipPermissions: true,
})

turn, err := session.Run(context.Background(), "解释这个项目的架构")
if err != nil {
    log.Fatal(err)
}

fmt.Println(turn.FinalResponse)
fmt.Printf("Cost: $%.4f\n", turn.Usage.CostUSD)
fmt.Printf("Session: %s\n", turn.SessionID)
```

### 流式输出

```go
stream, err := session.RunStreamed(context.Background(), "重构 auth 模块")
if err != nil {
    log.Fatal(err)
}

for {
    event, ok, err := stream.Next(context.Background())
    if err != nil {
        log.Fatal(err)
    }
    if !ok {
        break
    }

    switch e := event.(type) {
    case claudecodego.TextDeltaEvent:
        fmt.Print(e.Content)
    case claudecodego.ThinkingDeltaEvent:
        fmt.Printf("[思考中] %s", e.Content)
    case claudecodego.ToolUseEvent:
        fmt.Printf("\n> 工具: %s(%s)\n", e.ToolName, e.Input)
    case claudecodego.ToolResultEvent:
        fmt.Printf("> 结果: %s\n", e.Output)
    case claudecodego.SessionMetaEvent:
        fmt.Printf("[模型: %s]\n", e.Model)
    case claudecodego.TurnCompleteEvent:
        fmt.Printf("\n--- 轮次完成 (session: %s) ---\n", e.SessionID)
    case claudecodego.ErrorEvent:
        fmt.Printf("错误: %s\n", e.Message)
    }
}

if err := stream.Wait(); err != nil {
    log.Fatal(err)
}
```

### 图片输入

```go
turn, err := session.Run(ctx, []claudecodego.InputItem{
    {Type: claudecodego.InputTypeText, Text: "这张截图里有什么？"},
    {Type: claudecodego.InputTypeLocalImage, Path: "/path/to/screenshot.png"},
})
```

### 多轮对话

```go
session := claude.StartSession(sessionOptions)

// 第 1 轮
turn1, _ := session.Run(ctx, "哪些文件处理认证逻辑？")
fmt.Println(turn1.FinalResponse)

// 第 2 轮 — 会话恢复是自动的
turn2, _ := session.Run(ctx, "把那些文件重构为 JWT 认证")
fmt.Println(turn2.FinalResponse)

// 第 3 轮
turn3, _ := session.Run(ctx, "为新的 JWT 认证编写测试")
fmt.Println(turn3.FinalResponse)
```

### 恢复 / 继续会话

```go
// 通过 ID 恢复特定会话
session := claude.ResumeSession("之前的-session-id", sessionOptions)
turn, _ := session.Run(ctx, "继续之前的工作")

// 继续最近的会话
session := claude.ContinueSession(sessionOptions)
turn, _ := session.Run(ctx, "我们之前在做什么？")
```

### 取消控制

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

turn, err := session.Run(ctx, "执行一个可能耗时的操作")
if err != nil {
    // context.DeadlineExceeded 或 context.Canceled
    log.Printf("已取消: %v", err)
}
```

### 原始事件日志

```go
// 自动在 ./agent_logs/ 创建日志
session := claude.StartSession(claudecodego.SessionOptions{
    Model:       "sonnet",
    RawEventLog: true,
})

// 或指定目录
session := claude.StartSession(claudecodego.SessionOptions{
    Model:       "sonnet",
    RawEventLog: "/path/to/logs/",
})
```

### API 错误快速失败

```go
turn, err := session.Run(ctx, "prompt", claudecodego.TurnOptions{
    FailFastOnCLIAPIError: true, // 秒级检测认证/配额错误
})
```

---

## API 参考

### ClaudeCode（客户端）

```go
func NewClaudeCode(options ClaudeCodeOptions) *ClaudeCode
func (c *ClaudeCode) StartSession(options SessionOptions) *Session
func (c *ClaudeCode) ResumeSession(sessionID string, options SessionOptions) *Session
func (c *ClaudeCode) ContinueSession(options SessionOptions) *Session
```

### ClaudeCodeOptions

| 字段 | 类型 | 说明 |
|------|------|------|
| `CLIPath` | `string` | `claude` 二进制路径（默认：`"claude"`） |
| `Env` | `map[string]string` | 额外环境变量 |
| `APIKey` | `string` | 设置 `ANTHROPIC_API_KEY` |
| `AuthToken` | `string` | 设置 `ANTHROPIC_AUTH_TOKEN` |
| `BaseURL` | `string` | 设置 `ANTHROPIC_BASE_URL` |

### Session

```go
func (s *Session) ID() string
func (s *Session) Run(ctx context.Context, input Input, options ...TurnOptions) (*Turn, error)
func (s *Session) RunStreamed(ctx context.Context, input Input, options ...TurnOptions) (*StreamedTurn, error)
```

### SessionOptions

| 字段 | 类型 | 说明 |
|------|------|------|
| `Model` | `string` | 模型名称（如 `"sonnet"`、`"opus"`） |
| `CWD` | `string` | CLI 工作目录 |
| `AdditionalDirectories` | `[]string` | 额外包含的目录 |
| `MaxTurns` | `*int` | 最大 Agent 轮次 |
| `MaxBudgetUSD` | `*float64` | 花费上限（美元） |
| `SystemPrompt` | `string` | 自定义系统提示词 |
| `SystemPromptFile` | `string` | 从文件读取系统提示词 |
| `AppendSystemPrompt` | `string` | 追加到系统提示词 |
| `PermissionMode` | `PermissionMode` | `default` / `acceptEdits` / `plan` / `auto` / `bypassPermissions` |
| `DangerouslySkipPermissions` | `bool` | 跳过所有权限检查 |
| `AllowedTools` | `[]string` | 工具白名单 |
| `DisallowedTools` | `[]string` | 工具黑名单 |
| `Effort` | `Effort` | `low` / `medium` / `high` / `xhigh` / `max` |
| `MCPConfig` | `any` | MCP 服务器配置 |
| `Agents` | `any` | 多 Agent 定义 |
| `JSONSchema` | `any` | 结构化输出 Schema |
| `RawEventLog` | `any` | `true` 自动创建目录，或指定 `string` 路径 |
| `Bare` | `bool` | 精简模式（不加载 CLAUDE.md） |
| `NoSessionPersistence` | `bool` | 不持久化会话 |
| `Verbose` | `*bool` | CLI verbose 标志（默认：`true`） |
| `IncludePartialMessages` | `*bool` | 包含部分消息（默认：`true`） |

### Input（输入）

`Input` 为 `any` 类型，接受：
- `string` — 纯文本提示
- `[]InputItem` — 多模态输入（文本 + 图片）

```go
type InputItem struct {
    Type InputItemType // InputTypeText 或 InputTypeLocalImage
    Text string        // 文本项
    Path string        // 图片项（本地路径）
}
```

### Turn（缓冲结果）

```go
type Turn struct {
    Events           []RelayEvent // 本轮所有事件
    FinalResponse    string       // 最终文本响应
    Usage            *TurnUsage   // 费用与 Token 信息
    SessionID        string       // 用于恢复的 Session ID
    StructuredOutput any          // JSON Schema 解析后的结构化输出
}

type TurnUsage struct {
    CostUSD       float64
    InputTokens   int
    OutputTokens  int
    ContextWindow int
}
```

### StreamedTurn（流式输出）

```go
func (t *StreamedTurn) Next(ctx context.Context) (RelayEvent, bool, error)
func (t *StreamedTurn) Wait() error
```

### RelayEvent 事件类型

| 事件类型 | 字段 | 说明 |
|---------|------|------|
| `TextDeltaEvent` | `Content` | 增量文本输出 |
| `ThinkingDeltaEvent` | `Content` | 增量思考/推理内容 |
| `ToolUseEvent` | `ToolUseID`, `ToolName`, `Input` | 工具调用 |
| `ToolResultEvent` | `ToolUseID`, `Output`, `IsError` | 工具返回结果 |
| `SessionMetaEvent` | `Model` | 会话元信息 |
| `TurnCompleteEvent` | `SessionID`, `CostUSD`, `InputTokens`, `OutputTokens` | 轮次结束 |
| `ErrorEvent` | `Message`, `SessionID` | 发生错误 |

### TurnOptions

| 字段 | 类型 | 说明 |
|------|------|------|
| `OnRawEvent` | `func(RawClaudeEvent)` | 原始进程事件回调 |
| `FailFastOnCLIAPIError` | `bool` | API 错误时快速中止 |
| `Signal` | `context.Context` | 额外取消上下文 |

---

## 架构

```
┌─────────────────────────────────────────────────┐
│                   你的 Go 代码                    │
│                                                  │
│  ClaudeCode ──> Session ──> Run / RunStreamed     │
└──────────────────────┬──────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────┐
│              ClaudeCodeExec                        │
│  启动 `claude` CLI，使用 --output-format            │
│  stream-json，连接 stdin/stdout/stderr             │
└──────────────────────┬───────────────────────────┘
                       │ stdout 行 (NDJSON)
                       ▼
┌──────────────────────────────────────────────────┐
│              parser 包                             │
│                                                    │
│  ParseLine ──> ClaudeEvent ──> Translator          │
│                                  │                 │
│                                  ▼                 │
│                            []RelayEvent            │
│  (TextDelta, ThinkingDelta, ToolUse, ToolResult,   │
│   SessionMeta, TurnComplete, Error)                │
└──────────────────────────────────────────────────┘
```

---

## 它不是什么

- 不是 HTTP API 服务
- 不是多模型网关（它封装的是 Claude Code，仅此而已）
- 不是 CLI 的替代品（它驱动 CLI）

---

## 开发

```bash
# 运行测试（单元测试使用 Fake CLI，无需 API Key）
go test ./...

# 运行 E2E 测试（需要真实凭据）
E2E_AUTH_TOKEN=your-token E2E_BASE_URL=https://... go test ./tests/ -run TestE2E -v
```

---

## 许可证

[MIT](LICENSE)
