# claude-code-go

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen.svg)](go.mod)
[![E2E Tests](https://img.shields.io/badge/E2E_tests-all_passed-brightgreen.svg)](#tested--reliable)
[![TDD](https://img.shields.io/badge/built_with-TDD-orange.svg)](#pure-tdd-driven)

> A **complete Go port of [claude-code-node](https://github.com/oceanz0312/claude-code-node)** — Go SDK for Claude Code CLI, **built entirely with TDD**, perfectly replicating every capability of the Claude Code CLI. Zero external dependencies, idiomatic Go API.

[中文文档](./README.md)

---

## What You Get

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

	// Multi-turn: just keep calling Run() — session resume is automatic
	turn2, _ := session.Run(context.Background(), "Now add test coverage for edge cases")
	fmt.Println(turn2.FinalResponse)
}
```

**No HTTP server, no protocol translation, no abstractions over abstractions.** Just a typed, idiomatic Go wrapper around the Claude Code CLI that handles the messy parts for you:

| Capability | What it does |
|------------|-------------|
| **Session management** | Auto `--resume` across turns — you never touch session IDs |
| **Streaming** | `StreamedTurn` with `Next()` iterator — 7 typed event kinds |
| **35+ CLI options** | Every useful flag mapped to a typed field — `Model`, `SystemPrompt`, `AllowedTools`, `JSONSchema`, `MaxBudgetUSD`, `Agents`, `MCPConfig`... |
| **Structured output** | Pass a JSON Schema, get parsed objects back in `turn.StructuredOutput` |
| **Image input** | Send local images alongside text prompts via `InputItem` |
| **Cancellation** | Cancel any turn with `context.Context` |
| **FailFast** | Detect API errors in seconds, not minutes (critical for CI/CD) |
| **Zero dependencies** | Pure standard library — no external modules |

---

## Relationship to claude-code-node

This project is a **complete Go port** of [claude-code-node](https://github.com/oceanz0312/claude-code-node) (TypeScript SDK), maintaining the same architecture and feature coverage:

| Feature | claude-code-node (TS) | claude-code-go (Go) |
|---------|----------------------|---------------------|
| Session management & auto-resume | ✅ | ✅ |
| Streaming (7 event types) | ✅ AsyncIterable | ✅ StreamedTurn.Next() |
| 35+ CLI parameter coverage | ✅ | ✅ |
| Dual-layer event system | ✅ | ✅ |
| Stream deduplication | ✅ | ✅ |
| Fake CLI simulator | ✅ fake-claude.mjs | ✅ fakeclaude (Go) |
| E2E tests all passed | ✅ | ✅ |
| Zero external dependencies | ❌ (Node.js) | ✅ Pure stdlib |

---

## Pure TDD-Driven

This entire SDK was **built from scratch using strict Test-Driven Development**. Every feature — from session lifecycle to streaming event translation — was implemented by writing tests first, then writing the minimal code to pass them. The result: a Go SDK that **perfectly replicates every capability of the Claude Code CLI**.

### How it was built

```
 ┌─────────────────────────────────────────────────────────────┐
 │                    TDD Development Cycle                     │
 │                                                              │
 │  1. Study claude-code-node behavior & CLI protocol           │
 │  2. Write Go test capturing the expected behavior            │
 │  3. Build Fake CLI simulator to reproduce CLI output          │
 │  4. Implement until test passes                              │
 │  5. Validate against real Claude Code CLI (E2E)              │
 │  6. Repeat for next capability                               │
 └─────────────────────────────────────────────────────────────┘
```

### Two-layer test architecture

| Layer | What it tests | How |
|-------|--------------|-----|
| **Unit tests** (Fake CLI) | Every parsing path, event translation, session state machine, error handling, streaming dedup, CLI argument building | Go-based Fake CLI simulator (`testdata/fakeclaude/`) emulates the full `stream-json` protocol — **no real CLI or API key needed** |
| **E2E tests** (Real CLI) | Actual Claude Code CLI compatibility — auth, streaming, images, system prompts, agent roles, multi-turn, 15+ CLI flags | Hits the real `claude` binary with real credentials, saves full artifacts for post-mortem |

### Test results

**All tests passed — unit and E2E.** This is not "it compiles and we hope it works." Every behavior was verified against the real CLI:

| Metric | Detail |
|--------|--------|
| **Unit tests** | Comprehensive coverage via Fake CLI — tests run in < 1s with zero network calls |
| **E2E tests** | 11 real-model test cases, **all passed** |
| **Verified parity** | Every E2E scenario was cross-validated against claude-code-node to ensure identical behavior |
| **Test artifacts** | Every E2E run saves NDJSON logs, relay events, terminal transcript, and final responses to `tests/e2e/artifacts/` |

### What "perfectly replicates" means

- **35+ CLI flags**: every flag that claude-code-node supports is mapped 1:1 in Go
- **Dual-layer event system**: raw process events (spawn, stdout, stderr, exit) + semantic relay events (text delta, thinking, tool use, tool result, session meta, turn complete, error) — same architecture as the TS version
- **Stream deduplication**: eliminates duplicate text fragments from CLI verbose mode, producing clean incremental deltas
- **Session state machine**: auto-resume, continue, fork — identical lifecycle semantics
- **Error detection**: FailFast API error detection parses stderr/stdout in real-time, same patterns as the TS implementation

---

## Prerequisites

- Go >= 1.26
- Claude Code CLI installed and `claude` available on your `PATH` (or specify `CLIPath` explicitly)

## Installation

```bash
go get github.com/oceanz0312/claude-code-go
```

---

## Quick Start

### Buffered (Non-Streaming)

```go
claude := claudecodego.NewClaudeCode(claudecodego.ClaudeCodeOptions{
    APIKey: "sk-ant-...",
})

session := claude.StartSession(claudecodego.SessionOptions{
    Model:                      "sonnet",
    DangerouslySkipPermissions: true,
})

turn, err := session.Run(context.Background(), "Explain the architecture of this project")
if err != nil {
    log.Fatal(err)
}

fmt.Println(turn.FinalResponse)
fmt.Printf("Cost: $%.4f\n", turn.Usage.CostUSD)
fmt.Printf("Session: %s\n", turn.SessionID)
```

### Streaming

```go
stream, err := session.RunStreamed(context.Background(), "Refactor the auth module")
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
        fmt.Printf("[thinking] %s", e.Content)
    case claudecodego.ToolUseEvent:
        fmt.Printf("\n> Tool: %s(%s)\n", e.ToolName, e.Input)
    case claudecodego.ToolResultEvent:
        fmt.Printf("> Result: %s\n", e.Output)
    case claudecodego.SessionMetaEvent:
        fmt.Printf("[model: %s]\n", e.Model)
    case claudecodego.TurnCompleteEvent:
        fmt.Printf("\n--- Turn complete (session: %s) ---\n", e.SessionID)
    case claudecodego.ErrorEvent:
        fmt.Printf("ERROR: %s\n", e.Message)
    }
}

if err := stream.Wait(); err != nil {
    log.Fatal(err)
}
```

### Image Input

```go
turn, err := session.Run(ctx, []claudecodego.InputItem{
    {Type: claudecodego.InputTypeText, Text: "What's in this screenshot?"},
    {Type: claudecodego.InputTypeLocalImage, Path: "/path/to/screenshot.png"},
})
```

### Multi-Turn Conversation

```go
session := claude.StartSession(sessionOptions)

// Turn 1
turn1, _ := session.Run(ctx, "What files handle authentication?")
fmt.Println(turn1.FinalResponse)

// Turn 2 — session resume is automatic
turn2, _ := session.Run(ctx, "Refactor those files to use JWT")
fmt.Println(turn2.FinalResponse)

// Turn 3
turn3, _ := session.Run(ctx, "Now write tests for the new JWT auth")
fmt.Println(turn3.FinalResponse)
```

### Resume / Continue a Session

```go
// Resume a specific session by ID
session := claude.ResumeSession("session-id-from-before", sessionOptions)
turn, _ := session.Run(ctx, "Continue where we left off")

// Continue the most recent session
session := claude.ContinueSession(sessionOptions)
turn, _ := session.Run(ctx, "What were we working on?")
```

### Cancellation

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

turn, err := session.Run(ctx, "Do something that might take a while")
if err != nil {
    // context.DeadlineExceeded or context.Canceled
    log.Printf("Cancelled: %v", err)
}
```

### Raw Event Logging

```go
session := claude.StartSession(claudecodego.SessionOptions{
    Model:       "sonnet",
    RawEventLog: true, // auto-creates logs in ./agent_logs/
})

// Or specify a directory
session := claude.StartSession(claudecodego.SessionOptions{
    Model:       "sonnet",
    RawEventLog: "/path/to/logs/",
})
```

### FailFast on API Errors

```go
turn, err := session.Run(ctx, "prompt", claudecodego.TurnOptions{
    FailFastOnCLIAPIError: true, // detect auth/quota errors in seconds
})
```

---

## API Reference

### ClaudeCode (Client)

```go
func NewClaudeCode(options ClaudeCodeOptions) *ClaudeCode
func (c *ClaudeCode) StartSession(options SessionOptions) *Session
func (c *ClaudeCode) ResumeSession(sessionID string, options SessionOptions) *Session
func (c *ClaudeCode) ContinueSession(options SessionOptions) *Session
```

### ClaudeCodeOptions

| Field | Type | Description |
|-------|------|-------------|
| `CLIPath` | `string` | Path to `claude` binary (default: `"claude"`) |
| `Env` | `map[string]string` | Additional environment variables |
| `APIKey` | `string` | Sets `ANTHROPIC_API_KEY` |
| `AuthToken` | `string` | Sets `ANTHROPIC_AUTH_TOKEN` |
| `BaseURL` | `string` | Sets `ANTHROPIC_BASE_URL` |

### Session

```go
func (s *Session) ID() string
func (s *Session) Run(ctx context.Context, input Input, options ...TurnOptions) (*Turn, error)
func (s *Session) RunStreamed(ctx context.Context, input Input, options ...TurnOptions) (*StreamedTurn, error)
```

### SessionOptions

| Field | Type | Description |
|-------|------|-------------|
| `Model` | `string` | Model name (e.g. `"sonnet"`, `"opus"`) |
| `CWD` | `string` | Working directory for the CLI |
| `AdditionalDirectories` | `[]string` | Extra directories to include |
| `MaxTurns` | `*int` | Maximum agentic turns |
| `MaxBudgetUSD` | `*float64` | Spending cap in USD |
| `SystemPrompt` | `string` | Custom system prompt |
| `SystemPromptFile` | `string` | System prompt from file |
| `AppendSystemPrompt` | `string` | Appended to system prompt |
| `PermissionMode` | `PermissionMode` | `default` / `acceptEdits` / `plan` / `auto` / `bypassPermissions` |
| `DangerouslySkipPermissions` | `bool` | Skip all permission checks |
| `AllowedTools` | `[]string` | Tool allowlist |
| `DisallowedTools` | `[]string` | Tool denylist |
| `Effort` | `Effort` | `low` / `medium` / `high` / `xhigh` / `max` |
| `MCPConfig` | `any` | MCP server configuration |
| `Agents` | `any` | Multi-agent definitions |
| `JSONSchema` | `any` | Structured output schema |
| `RawEventLog` | `any` | `true` for auto-dir, or `string` path |
| `Bare` | `bool` | Minimal mode (no CLAUDE.md) |
| `NoSessionPersistence` | `bool` | Don't persist session |
| `Verbose` | `*bool` | CLI verbose flag (default: `true`) |
| `IncludePartialMessages` | `*bool` | Include partial messages (default: `true`) |

### Input

`Input` is `any` and accepts:
- `string` — plain text prompt
- `[]InputItem` — multi-modal input (text + images)

```go
type InputItem struct {
    Type InputItemType // InputTypeText or InputTypeLocalImage
    Text string        // for text items
    Path string        // for image items
}
```

### Turn (Buffered Result)

```go
type Turn struct {
    Events           []RelayEvent // all events from this turn
    FinalResponse    string       // final text response
    Usage            *TurnUsage   // cost and token info
    SessionID        string       // session ID for resumption
    StructuredOutput any          // parsed JSON schema output
}

type TurnUsage struct {
    CostUSD       float64
    InputTokens   int
    OutputTokens  int
    ContextWindow int
}
```

### StreamedTurn

```go
func (t *StreamedTurn) Next(ctx context.Context) (RelayEvent, bool, error)
func (t *StreamedTurn) Wait() error
```

### RelayEvent Types

| Event Type | Fields | Description |
|-----------|--------|-------------|
| `TextDeltaEvent` | `Content` | Incremental text output |
| `ThinkingDeltaEvent` | `Content` | Incremental thinking/reasoning |
| `ToolUseEvent` | `ToolUseID`, `ToolName`, `Input` | Tool invocation |
| `ToolResultEvent` | `ToolUseID`, `Output`, `IsError` | Tool result |
| `SessionMetaEvent` | `Model` | Session metadata |
| `TurnCompleteEvent` | `SessionID`, `CostUSD`, `InputTokens`, `OutputTokens` | Turn finished |
| `ErrorEvent` | `Message`, `SessionID` | Error occurred |

### TurnOptions

| Field | Type | Description |
|-------|------|-------------|
| `OnRawEvent` | `func(RawClaudeEvent)` | Callback for raw process events |
| `FailFastOnCLIAPIError` | `bool` | Abort early on API errors |
| `Signal` | `context.Context` | Additional cancellation context |

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   Your Go Code                   │
│                                                  │
│  ClaudeCode ──> Session ──> Run / RunStreamed     │
└──────────────────────┬──────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────┐
│              ClaudeCodeExec                        │
│  Spawns `claude` CLI with --output-format          │
│  stream-json, wires stdin/stdout/stderr            │
└──────────────────────┬───────────────────────────┘
                       │ stdout lines (NDJSON)
                       ▼
┌──────────────────────────────────────────────────┐
│              parser package                        │
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

## What It's NOT

- Not an HTTP API server
- Not a multi-model gateway (it wraps Claude Code, period)
- Not a replacement for the CLI (it drives it)

---

## Development

```bash
# Run tests (unit tests with fake CLI — no API key needed)
go test ./...

# Run E2E tests (requires real credentials)
E2E_AUTH_TOKEN=your-token E2E_BASE_URL=https://... go test ./tests/ -run TestE2E -v
```

---

## License

[MIT](LICENSE)
