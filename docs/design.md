# claude-code-go 技术设计方案

> 基于 `claude-code-node` TypeScript SDK 完整移植，Go 语言版本。  
> 目标：在团队通用 SDK 场景下，实现与 Node 版功能严格对等的 Go 库；同时将 `claude-code-parser` 作为独立子技术方案和独立目录实现。  
> parser 细节见 [docs/parser/design.md](./parser/design.md)，E2E 细节见 [docs/e2e/design.md](./e2e/design.md)。

## 1. 项目定位

### 核心思路

本 SDK **不调用任何 Node.js API**，而是通过 `os/exec.Cmd` spawn Claude Code CLI 子进程，以 `--output-format stream-json --verbose` 模式运行，逐行读取 NDJSON 输出，翻译为结构化 Go 事件流。

```text
业务代码
  ↓
ClaudeCode (入口 + 全局配置)
  ↓
Session (会话状态机 + 事件翻译)
  ↓
ClaudeCodeExec (进程管理 + 参数构建)
  ↓
os/exec.CommandContext("claude", args...)
  ↓
stdout NDJSON → parser.Translator → RelayEvent
```

### 功能目标（完整对等移植）

| 能力 | 说明 |
|------|------|
| 会话管理 | 自动 `--resume`，支持 start / resume / continue 三种模式 |
| 缓冲模式 | `Run()` 返回完整 `Turn` |
| 流式模式 | `RunStreamed()` 返回事件 channel / iterator 风格接口 |
| 35+ CLI 参数 | `SessionOptions` 完整映射 Node 版 `buildArgs()` |
| 流去重 | parser 级 + session 级双层去重 |
| 图片输入 | magic bytes MIME 检测 + base64 + `stream-json` stdin |
| 结构化输出 | `--json-schema` → `Turn.StructuredOutput` |
| FailFast | stderr `API Error:` + stdout `system.api_retry` 实时终止 |
| 原始事件日志 | NDJSON 文件落盘 |
| 环境隔离 | 不继承全局环境，仅传显式 env + PATH |
| 取消机制 | `context.Context` 驱动全链路取消 |

---

## 2. 仓库结构

```text
claude-code-go/
├── go.mod
├── go.sum
├── README.md
├── LICENSE
├── .gitignore
├── .github/
│   └── workflows/
│       └── ci.yml
├── client.go                # ClaudeCode 入口类
├── session.go               # Session 状态机
├── exec.go                  # ClaudeCodeExec 进程管理
├── options.go               # 公开类型定义
├── raw_event_log.go         # NDJSON 原始日志
├── parser/                  # claude-code-parser 独立子技术方案
│   ├── protocol.go
│   ├── events.go
│   ├── parse.go
│   ├── translator.go
│   └── writer.go
├── internal/
│   ├── eventstream/
│   │   └── channel.go       # channel 封装 + 结束/错误传播
│   ├── image/
│   │   └── mime.go          # 图片 magic bytes 探测
│   └── jsonutil/
│       └── first_value.go   # JSON 抽取/兼容辅助（若需要）
├── testdata/
│   ├── fakeclaude/
│   │   └── main.go          # Go 原生 fake CLI 源码，对齐 Node 版 tests/unit/fixtures/fake-claude.mjs
│   └── images/
│       ├── red-square.png
│       ├── shapes-demo.png
│       └── receipt-demo.png
├── tests/
│   ├── exec_test.go
│   ├── session_test.go
│   ├── raw_event_log_test.go
│   ├── parser_parse_test.go
│   ├── parser_translator_test.go
│   ├── parser_writer_test.go
│   ├── config.go
│   ├── harness.go
│   ├── reporters.go
│   └── e2e_real_cli_test.go
└── docs/
    ├── design.md
    ├── parser/
    │   └── design.md
    └── e2e/
        └── design.md
```

说明：
- `parser/` 必须独立存在，不与 `session.go` / `exec.go` 混写。
- `docs/parser/design.md` 单独承载 `claude-code-parser` 子方案，避免主设计被解析细节淹没。

---

## 3. 核心模块设计

### 3.1 `client.go` — ClaudeCode 入口

职责对标 Node 版 `src/claude-code.ts`：

```go
type ClaudeCode struct {
	cliPath string
	env     map[string]string
}

func NewClaudeCode(opts ClaudeCodeOptions) *ClaudeCode
func (c *ClaudeCode) StartSession(opts SessionOptions) *Session
func (c *ClaudeCode) ResumeSession(sessionID string, opts SessionOptions) *Session
func (c *ClaudeCode) ContinueSession(opts SessionOptions) *Session
```

#### 环境合并策略

Node 版 `mergeClaudeEnv()` 的关键行为：
- `apiKey` → `ANTHROPIC_API_KEY`
- `authToken` → `ANTHROPIC_AUTH_TOKEN`
- `baseUrl` → `ANTHROPIC_BASE_URL`
- 不继承整个 `process.env`
- 只显式带入 PATH 等必要字段

Go 版实现：

```go
func mergeClaudeEnv(opts ClaudeCodeOptions) map[string]string {
	env := map[string]string{}
	for k, v := range opts.Env {
		env[k] = v
	}
	if opts.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = opts.APIKey
	}
	if opts.AuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = opts.AuthToken
	}
	if opts.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = opts.BaseURL
	}
	copyPathIfPresent(env)
	return env
}
```

### 3.2 `options.go` — 公开类型定义

#### ClaudeCodeOptions

```go
type ClaudeCodeOptions struct {
	CLIPath   string
	Env       map[string]string
	APIKey    string
	AuthToken string
	BaseURL   string
}
```

#### SessionOptions

需完整覆盖 Node 版 `src/options.ts` 的字段，并一一映射到 CLI flag：

```go
type PermissionMode string
const (
	PermissionModeDefault            PermissionMode = "default"
	PermissionModeAcceptEdits        PermissionMode = "acceptEdits"
	PermissionModePlan               PermissionMode = "plan"
	PermissionModeAuto               PermissionMode = "auto"
	PermissionModeDontAsk            PermissionMode = "dontAsk"
	PermissionModeBypassPermissions  PermissionMode = "bypassPermissions"
)

type Effort string
const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

type AgentDefinition struct {
	Description    string
	Prompt         string
	Tools          []string
	AllowedTools   []string
	DisallowedTools []string
	Model          string
	Effort         Effort
	MaxTurns       *int
	PermissionMode PermissionMode
	Isolation      string
	InitialPrompt  string
	MCPServers     map[string]any
}

type SessionOptions struct {
	Model                              string
	CWD                                string
	AdditionalDirectories              []string
	MaxTurns                           *int
	MaxBudgetUSD                       *float64
	SystemPrompt                       string
	SystemPromptFile                   string
	AppendSystemPrompt                 string
	AppendSystemPromptFile             string
	PermissionMode                     PermissionMode
	DangerouslySkipPermissions         bool
	AllowedTools                       []string
	DisallowedTools                    []string
	Tools                              string
	PermissionPromptTool               string
	MCPConfig                          []string
	StrictMCPConfig                    bool
	Effort                             Effort
	FallbackModel                      string
	Bare                               bool
	NoSessionPersistence               bool
	Chrome                             *bool
	Agents                             any
	Agent                              string
	Name                               string
	Settings                           string
	SettingSources                     string
	Verbose                            *bool
	IncludePartialMessages             *bool
	IncludeHookEvents                  bool
	Betas                              string
	Worktree                           string
	DisableSlashCommands               bool
	PluginDir                          []string
	ExcludeDynamicSystemPromptSections bool
	Debug                              any
	DebugFile                          string
	RawEventLog                        string
	JSONSchema                         string
}
```

#### Turn / Run 相关类型

```go
type Turn struct {
	FinalResponse     string
	StructuredOutput  any
	Events            []parser.RelayEvent
	SessionID         string
	Usage             *Usage
}

type Usage struct {
	InputTokens   int
	OutputTokens  int
	ContextWindow int
	CostUSD       float64
}

type TurnOptions struct {
	OnRawEvent func(RawClaudeEvent)
	Context    context.Context
}
```

---

## 4. `exec.go` — ClaudeCodeExec 进程管理

### 4.1 职责

对标 Node 版 `src/exec.ts`：
- 构造 CLI args
- 构造 stdin payload
- 启动进程
- 逐行读取 stdout / stderr
- 生成原始事件 `RawClaudeEvent`
- 支持 `resumeSessionId` / `continueSession`
- 处理图片输入与 `stream-json`

### 4.2 结构设计

```go
type ClaudeCodeExec struct {
	defaultCLIPath string
	env            map[string]string
}

func NewClaudeCodeExec(cliPath string, env map[string]string) *ClaudeCodeExec

func (e *ClaudeCodeExec) Run(ctx context.Context, req ExecRequest) error
```

请求结构：

```go
type ExecRequest struct {
	Input           Input
	CLIPath         string
	SessionOptions  SessionOptions
	ResumeSessionID string
	ContinueSession bool
	OnLine          func(string)
	OnRawEvent      func(RawClaudeEvent)
	Env             map[string]string
}
```

### 4.3 buildArgs() 映射策略

必须完整对标 Node 版 `buildArgs()` 的所有 flag。核心规则：

- 默认总是传：`--output-format stream-json`
- `verbose` 默认开启；显式 `false` 时不传 `--verbose`
- `includePartialMessages` 默认开启；显式 `false` 时不传 `--include-partial-messages`
- `continueSession=true` 优先于 `resumeSessionId`
- `dangerouslySkipPermissions=true` 时传 `--dangerously-skip-permissions`，并压过 `--permission-mode`
- `systemPrompt` 优先于 `systemPromptFile`
- list 类型参数展开为重复 flag

示例实现：

```go
func buildArgs(input Input, opts SessionOptions, resumeSessionID string, continueSession bool) []string {
	args := []string{"--output-format", "stream-json"}

	if inputIsSimplePromptWithoutImages(input) {
		args = append(args, "-p", extractPrompt(input))
	}

	verbose := true
	if opts.Verbose != nil {
		verbose = *opts.Verbose
	}
	if verbose {
		args = append(args, "--verbose")
	}

	partials := true
	if opts.IncludePartialMessages != nil {
		partials = *opts.IncludePartialMessages
	}
	if partials {
		args = append(args, "--include-partial-messages")
	}

	if continueSession {
		args = append(args, "--continue")
	} else if resumeSessionID != "" {
		args = append(args, "--resume", resumeSessionID)
	}

	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	for _, dir := range opts.AdditionalDirectories {
		args = append(args, "--add-dir", dir)
	}
	for _, tool := range opts.AllowedTools {
		args = append(args, "--allowedTools", tool)
	}
	for _, tool := range opts.DisallowedTools {
		args = append(args, "--disallowedTools", tool)
	}
	for _, path := range opts.MCPConfig {
		args = append(args, "--mcp-config", path)
	}
	for _, path := range opts.PluginDir {
		args = append(args, "--plugin-dir", path)
	}

	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	} else if opts.SystemPromptFile != "" {
		args = append(args, "--system-prompt-file", opts.SystemPromptFile)
	}

	if opts.DangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	} else if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", string(opts.PermissionMode))
	}

	// 其余字段同理全部一一映射
	return args
}
```

### 4.4 图片输入与 stdin payload

当输入包含图片时，不能使用 `-p <prompt>` 直接传 prompt，而必须走 `--input-format stream-json` + stdin NDJSON。流程对标 Node 版：

1. 读取本地图片文件
2. 用 magic bytes 推断 MIME：`image/png`, `image/jpeg`, `image/webp`, `image/gif`
3. base64 编码
4. 构造 Claude Code 所需的 stream-json 输入

```go
type InputItem interface{ inputItem() }

type TextInput struct { Text string }
func (TextInput) inputItem() {}

type LocalImageInput struct { Path string }
func (LocalImageInput) inputItem() {}

type Input []InputItem
```

stdin payload 示例：

```json
{"type":"user","message":{"role":"user","content":[
  {"type":"text","text":"Look at the image and reply with JSON only."},
  {"type":"image","source":{"type":"base64","media_type":"image/png","data":"..."}}
]}}
```

Go 侧实现建议：

```go
func buildStdinPayload(input Input) (string, error)
func detectImageMediaType(data []byte) (string, error)
```

---

## 5. `session.go` — Session 状态机

### 5.1 职责

对标 Node 版 `src/session.ts`，是最复杂模块：

- 维护 `_id`、`_hasRun`、`_continueMode`
- 自动 `--resume`
- 调用 parser.Translator 翻译 raw events
- 做第二层去重（屏蔽 assistant snapshot 与 stream_event 重复输出）
- 实现 `Run()` / `RunStreamed()`
- FailFast
- structured output 提取
- 汇总最终 Turn

### 5.2 状态结构

```go
type Session struct {
	client       *ClaudeCode
	id           string
	hasRun       bool
	continueMode bool
	options      SessionOptions
	exec         *ClaudeCodeExec
}
```

### 5.3 公开方法

```go
func (s *Session) Run(ctx context.Context, input Input, opts ...TurnOptions) (*Turn, error)
func (s *Session) RunPrompt(ctx context.Context, prompt string, opts ...TurnOptions) (*Turn, error)
func (s *Session) RunStreamed(ctx context.Context, input Input, opts ...TurnOptions) (*StreamedTurn, error)
```

`Run()` 语义：
- 内部启动流式处理
- 收集所有 relay event
- 聚合出 `FinalResponse`, `StructuredOutput`, `Usage`, `SessionID`
- 返回完整 `Turn`

### 5.4 第二层去重（Session 级）

Node 版除了 parser 的内容块去重外，还有一层流事件去重，用于避免：
- `stream_event.content_block_delta` 已经产生 `text_delta`
- 后续 `assistant` snapshot 再次重复输出同一段文本/思考

Go 版也必须保留两层去重：

```go
type streamState struct {
	textStreamed     map[string]bool
	thinkingStreamed map[string]bool
}
```

键建议：`messageID + ":" + contentIndex`

关键逻辑：
- 先处理 `stream_event`，标记这个 message block 已通过 delta 发送
- 后续处理 `assistant` snapshot 时，如果对应 block 已流式发出，则 suppress

因为 parser 层只负责“content block 增量”，不知道 stream_event 与 assistant snapshot 的对应关系，这层必须保留在 session 层。

### 5.5 stream_event 翻译

Node 版 `translateStreamEvent()` 会处理：
- `message_start`
- `content_block_start`
- `content_block_delta`（核心：text_delta / thinking_delta）
- `message_stop`

Go 版建议保留同结构：

```go
func translateStreamEvent(raw map[string]any, st *streamState) []parser.RelayEvent
```

### 5.6 FailFast

必须保留两个触发源：

#### 5.6.1 stderr API 错误

对标 Node 正则：`
\bAPI Error:`

```go
var apiErrorPattern = regexp.MustCompile(`\bAPI Error:`)

func extractFatalCLIAPIError(line string) string {
	if !apiErrorPattern.MatchString(line) {
		return ""
	}
	return strings.TrimSpace(line)
}
```

一旦命中：
- 立即 cancel context
- 返回 error
- 不等待正常 `result` 尾包

#### 5.6.2 stdout system.api_retry

原始 fake CLI 已验证该场景：

```json
{
  "type":"system",
  "subtype":"api_retry",
  "attempt":1,
  "max_retries":10,
  "retry_delay_ms":600,
  "error_status":401,
  "error":"authentication_failed",
  "session_id":"..."
}
```

Go 版检测逻辑：

```go
func extractFatalCLIAPIErrorFromStdoutEvent(ev *parser.ClaudeEvent) string {
	if ev == nil || ev.Type != "system" || ev.Subtype != "api_retry" {
		return ""
	}
	if ev.ErrorStatus == nil && ev.Error == "" {
		return ""
	}
	status := "unknown"
	if ev.ErrorStatus != nil {
		status = strconv.Itoa(*ev.ErrorStatus)
	}
	return fmt.Sprintf("Claude CLI API retry failed: status=%s error=%s", status, ev.Error)
}
```

---

## 6. `raw_event_log.go` — 原始事件日志

职责对标 Node 版 `src/raw-event-log.ts`：
- 将原始事件按 NDJSON 落盘
- 支持写入 backpressure / flush / close
- Error 对象要可序列化

Go 版结构：

```go
type RawEventLogger struct {
	mu   sync.Mutex
	file *os.File
	w    *bufio.Writer
}

func NewRawEventLogger(path string) (*RawEventLogger, error)
func (l *RawEventLogger) Write(event RawClaudeEvent) error
func (l *RawEventLogger) Close() error
```

日志记录结构：

```go
type TimestampedRawEvent struct {
	Timestamp string        `json:"timestamp"`
	Event     RawClaudeEvent `json:"event"`
}
```

---

## 7. `RawClaudeEvent` 设计

上层 SDK 需要保留进程级原始事件，与 Node 版一致，包括：

- `spawn`
- `stdin_closed`
- `stdout_chunk`
- `stdout_line`
- `stderr_chunk`
- `stderr_line`
- `exit`
- `error`

Go 建议定义：

```go
type RawClaudeEventType string

const (
	RawEventSpawn      RawClaudeEventType = "spawn"
	RawEventStdinClosed RawClaudeEventType = "stdin_closed"
	RawEventStdoutChunk RawClaudeEventType = "stdout_chunk"
	RawEventStdoutLine  RawClaudeEventType = "stdout_line"
	RawEventStderrChunk RawClaudeEventType = "stderr_chunk"
	RawEventStderrLine  RawClaudeEventType = "stderr_line"
	RawEventExit        RawClaudeEventType = "exit"
	RawEventError       RawClaudeEventType = "error"
)

type RawClaudeEvent struct {
	Type    RawClaudeEventType `json:"type"`
	Command string             `json:"command,omitempty"`
	Args    []string           `json:"args,omitempty"`
	Chunk   string             `json:"chunk,omitempty"`
	Line    string             `json:"line,omitempty"`
	Code    *int               `json:"code,omitempty"`
	Signal  string             `json:"signal,omitempty"`
	Message string             `json:"message,omitempty"`
}
```

---

## 8. 并发模型

### 8.1 为什么用 `context.Context`

Go 版没有 `AbortController`，最佳对等机制是：
- `context.WithCancelCause`（Go 1.20+）或 `context.WithCancel`
- 所有 goroutine 监听 `ctx.Done()`
- FailFast / 用户取消 / 进程退出都通过 context 收敛

### 8.2 流式接口设计

建议提供两种形式：

#### 方案 A：channel 风格（推荐）

```go
type StreamedTurn struct {
	Events     <-chan parser.RelayEvent
	Result     <-chan *Turn
	Err        <-chan error
}
```

优点：原生、简单。

#### 方案 B：iterator 风格包装

```go
type EventIterator interface {
	Next(ctx context.Context) (parser.RelayEvent, bool, error)
}
```

最终建议：底层用 channel，实现简单；外层可追加 iterator wrapper。

### 8.3 goroutine 拆分

建议 4 条 goroutine：

1. `stdinWriter`：写入 prompt / stream-json payload
2. `stdoutReader`：扫描 stdout，按行解析 NDJSON
3. `stderrReader`：扫描 stderr，执行 FailFast 检测
4. `waiter`：等待进程退出，发送 exit raw event

所有路径收敛到一个 `errgroup.Group` 或手写 `sync.WaitGroup + cancel`。

---

## 9. 测试设计

### 9.1 fake CLI

对标 Node 版 `tests/unit/fixtures/fake-claude.mjs`，Go 仓库需要 **Go 原生 fake CLI**：

- 源码放在 `testdata/fakeclaude/main.go`
- 在 `TestMain` 或测试 setup 中预编译为临时 binary，再把该 binary 作为 `cliPath` 传入
- **禁止**在单测中用 `go run` 代替 `cliPath`，否则 spawn command、启动延迟、参数形态都会偏离
  `agent-sdk` 的测试语义
- binary 行为必须与 Node 版一致：
  - 读取 argv
  - 读取 stdin
  - 根据 prompt 触发特殊场景：
  - `__inspect_exec_options__`
  - `__inspect_session_flags__`
  - `__inspect_raw_events__`
  - `__stderr_api_error__`
  - `__stdout_api_retry_auth__`
  - `force-error`
  - `slow-run`
- 输出与 Node 版相同的 NDJSON 事件序列

### 9.2 测试先行总原则

本次实施采用 **Unit Test + E2E Test 先行**，并区分两类测试迁移标准：

- **literal-port**：CLI args 向量、artifact 文件名、raw event 字段、env 隔离、`cwd`
  语义、session 模式切换等，要求与 `agent-sdk` 逐项一致
- **semantic-port**：Go 运行时特有的异常值、权限报错文本、`context` 取消细节等，要求行为等价，
  不强求逐字符一致

额外约束：

- `go test` 默认串行执行这些套件；当前阶段禁止 `t.Parallel()`，否则会破坏环境变量污染类用例
- 测试先行的“红灯”必须是断言失败，而不是包无法编译。因此先建立最小可编译骨架，再迁移测试
- `SessionOptions.Cwd` 表示 subprocess working directory，不是 `--cd` flag；这一点要与
  Node 版 `exec.test.ts` 的断言保持一致

### 9.3 Unit Test 1:1 迁移

Go 单测要逐条迁移 Node 版两个主文件：

- `exec_test.go`：1:1 对齐
  [agent-sdk/tests/unit/exec.test.ts](../../agent-sdk/tests/unit/exec.test.ts) 全部 13 条用例
- `session_test.go`：1:1 对齐
  [agent-sdk/tests/unit/session.test.ts](../../agent-sdk/tests/unit/session.test.ts) 全部 27 条用例
- `parser_*_test.go`：按 `docs/parser/design.md` 派生 parser 行为测试
- `raw_event_log_test.go`：补齐相对路径报错、默认 `agent_logs/`、backpressure、预占文件名导致
  `close()` 失败等场景

单测迁移时必须保留：

- `buildArgs()` 的 flag 顺序、重复 flag 展开、`continue` / `resume` / prompt 来源优先级
- env 隔离与显式 env 覆盖语义
- raw event logger 的测试缝：需要可注入时间源 / 随机后缀生成器，以等价迁移上游的固定文件名测试

### 9.4 E2E 设计拆分

E2E 的目录布局、helper 契约、artifact 契约以及 14 条 `real-cli.test.ts`
逐条映射，已经拆到 [docs/e2e/design.md](./e2e/design.md)。

主文档只保留四条摘要：

- E2E 基础设施与 Node 版 1:1 对齐
- 无凭证时保留 `requires E2E env vars` setup-failure 用例
- 默认优先解析仓库内安装的 Claude CLI
- E2E artifacts 作为跨 SDK 对拍契约

### 9.5 Unit Test 逐条映射附录

本附录只覆盖 **direct 1:1 unit ports**：也就是直接从 `agent-sdk/tests/unit/*.ts`
迁移过来的测试。`parser` 的派生测试统一见 `docs/parser/design.md`；E2E 的详细设计与
14 条 real-cli 映射统一见 `docs/e2e/design.md`。

#### 9.5.1 `exec.test.ts` → `exec_test.go`

1. **Source:** `tests/unit/exec.test.ts:135` `yields NDJSON lines from the fake CLI`。**Target:** `exec_test.go::TestExec_YieldsNDJSONLinesFromFakeCLI`。`literal-port`。要求 `ClaudeCodeExec.Run()` 至少收到一行 fake CLI 输出，且每行都能反序列化为 JSON，并包含 `type` 字段。
2. **Source:** `tests/unit/exec.test.ts:156` `enables default streaming flags unless explicitly disabled`。**Target:** `exec_test.go::TestExec_EnablesDefaultStreamingFlagsUnlessExplicitlyDisabled`。`literal-port`。要求默认情况下传 `-p <prompt>`、`--output-format stream-json`、`--verbose`、`--include-partial-messages`，且不自动传 `--input-format`。
3. **Source:** `tests/unit/exec.test.ts:168` `omits default-on flags when verbose and partial messages are disabled`。**Target:** `exec_test.go::TestExec_OmitsDefaultOnFlagsWhenVerboseAndPartialMessagesAreDisabled`。`literal-port`。要求当 `Verbose=false` 且 `IncludePartialMessages=false` 时，这两个默认开启的 flag 都不出现。
4. **Source:** `tests/unit/exec.test.ts:180` `applies precedence for continue, permission mode, and system prompt source`。**Target:** `exec_test.go::TestExec_AppliesPrecedenceForContinuePermissionModeAndSystemPromptSource`。`literal-port`。要求 `ContinueSession=true` 压过 `ResumeSessionID`，inline `SystemPrompt` 压过 `SystemPromptFile`，`DangerouslySkipPermissions=true` 压过 `PermissionMode`。
5. **Source:** `tests/unit/exec.test.ts:204` `expands repeated flags for list-style options and uses stream-json stdin for images`。**Target:** `exec_test.go::TestExec_ExpandsRepeatedFlagsForListStyleOptionsAndUsesStreamJSONStdinForImages`。`literal-port`。要求数组类选项展开为重复 flag；输入中带图片时切到 `--input-format stream-json`，并通过 stdin 发送 text + image blocks。
6. **Source:** `tests/unit/exec.test.ts:249` `passes scalar flags through and serializes object agents`。**Target:** `exec_test.go::TestExec_PassesScalarFlagsThroughAndSerializesObjectAgents`。`literal-port`。要求所有标量选项落到正确 flag；`Cwd` 仅作为 subprocess working directory，不生成 `--cd`；`Agents` 的 object 形式需要 JSON 序列化给 `--agents`。
7. **Source:** `tests/unit/exec.test.ts:353` `supports chrome, debug, and agents string forms`。**Target:** `exec_test.go::TestExec_SupportsChromeDebugAndAgentsStringForms`。`literal-port`。要求 `Chrome=true` 走 `--chrome`，`Debug=true` 走无参数 `--debug`，`Agents` 为原始字符串时不做二次编码。
8. **Source:** `tests/unit/exec.test.ts:369` `passes --resume when resumeSessionId is set`。**Target:** `exec_test.go::TestExec_PassesResumeWhenResumeSessionIDIsSet`。`literal-port`。要求 `ResumeSessionID` 被传入 fake CLI 后，返回的 `session_id` 等于该值。
9. **Source:** `tests/unit/exec.test.ts:389` `emits raw process events including stdout and stderr chunks/lines`。**Target:** `exec_test.go::TestExec_EmitsRawProcessEventsIncludingStdoutAndStderrChunksAndLines`。`literal-port`。要求 raw event 流里能观察到 `spawn`、`stdin_closed`、`stdout_line`、`stderr_chunk`、`stderr_line`、`exit`，并验证关键字段。
10. **Source:** `tests/unit/exec.test.ts:447` `uses explicit env override without inheriting process.env`。**Target:** `exec_test.go::TestExec_UsesExplicitEnvOverrideWithoutInheritingOSEnviron`。`literal-port`。要求显式构造函数 env 只携带指定值，不继承宿主环境中的 `ANTHROPIC_*` 或其他污染值。
11. **Source:** `tests/unit/exec.test.ts:472` `allows per-run env to override constructor env`。**Target:** `exec_test.go::TestExec_AllowsPerRunEnvToOverrideConstructorEnv`。`literal-port`。要求单次运行传入的 env 覆盖构造函数 env 中同名键。
12. **Source:** `tests/unit/exec.test.ts:493` `does not inherit global env when no explicit env is provided`。**Target:** `exec_test.go::TestExec_DoesNotInheritGlobalEnvWhenNoExplicitEnvIsProvided`。`literal-port`。要求未显式传 env 时，CLI 进程中也看不到宿主环境里的 `ANTHROPIC_*` 与测试污染变量。
13. **Source:** `tests/unit/exec.test.ts:509` `merges constructor env with per-run env without credential mutual exclusion`。**Target:** `exec_test.go::TestExec_MergesConstructorEnvWithPerRunEnvWithoutCredentialMutualExclusion`。`literal-port`。要求 per-run env 只覆盖同名键，未覆盖的构造函数 env 保留；`APIKey` / `AuthToken` / `BaseURL` 不做互斥清空。

#### 9.5.2 `session.test.ts` → `session_test.go`

1. **Source:** `tests/unit/session.test.ts:105` `returns a complete Turn with finalResponse and usage`。**Target:** `session_test.go::TestSessionRun_ReturnsCompleteTurnWithFinalResponseAndUsage`。`literal-port`。要求 `Session.Run()` 返回完整 `Turn`，包含 `FinalResponse`、`Events`、`SessionID` 与 `Usage` 聚合字段。
2. **Source:** `tests/unit/session.test.ts:122` `captures session ID from session_meta`。**Target:** `session_test.go::TestSessionRun_CapturesSessionIDFromSessionMeta`。`literal-port`。要求 `session.ID()` 在收到 `session_meta` 后被更新。
3. **Source:** `tests/unit/session.test.ts:133` `throws on error response`。**Target:** `session_test.go::TestSessionRun_ThrowsOnErrorResponse`。`semantic-port`。要求 result/error 最终转成返回 error，并包含 fake CLI 返回的错误文本。
4. **Source:** `tests/unit/session.test.ts:142` `can fail fast on fatal CLI API errors written to stderr`。**Target:** `session_test.go::TestSessionRun_CanFailFastOnFatalCLIAPIErrorsWrittenToStderr`。`literal-port`。要求启用 fail-fast 后，stderr 命中 `API Error:` 时快速失败，而不是等待进程自然退出。
5. **Source:** `tests/unit/session.test.ts:159` `can fail fast on fatal CLI api_retry events written to stdout`。**Target:** `session_test.go::TestSessionRun_CanFailFastOnFatalCLIAPIRetryEventsWrittenToStdout`。`literal-port`。要求 stdout 中的 `system.api_retry` 事件同样触发快速失败，并携带 `authentication_failed` 等信息。
6. **Source:** `tests/unit/session.test.ts:176` `supports multi-turn via automatic --resume`。**Target:** `session_test.go::TestSessionRun_SupportsMultiTurnViaAutomaticResume`。`literal-port`。要求同一 `Session` 首轮不带 `--resume`，第二轮自动带上前一轮拿到的 `session_id`。
7. **Source:** `tests/unit/session.test.ts:200` `yields RelayEvents as AsyncGenerator`。**Target:** `session_test.go::TestSessionRunStreamed_YieldsRelayEventsAsStream`。`literal-port`。要求 `RunStreamed()` 返回可消费的事件流，并至少包含 `session_meta` 与 `turn_complete`。
8. **Source:** `tests/unit/session.test.ts:221` `streams text_delta events incrementally`。**Target:** `session_test.go::TestSessionRunStreamed_StreamsTextDeltaEventsIncrementally`。`literal-port`。要求 `text_delta` 被拆成 `"Here is "` 与 `"my response."` 两段，并能重组为完整文本。
9. **Source:** `tests/unit/session.test.ts:240` `streams tool_use and tool_result events`。**Target:** `session_test.go::TestSessionRunStreamed_StreamsToolUseAndToolResultEvents`。`literal-port`。要求 streaming 模式里同时看到 `tool_use` 与 `tool_result` 两种 relay event。
10. **Source:** `tests/unit/session.test.ts:259` `can surface fatal CLI API stderr as a RelayEvent error`。**Target:** `session_test.go::TestSessionRunStreamed_CanSurfaceFatalCLIAPIStderrAsRelayEventError`。`literal-port`。要求 streaming 模式下 stderr fail-fast 最终以 `error` relay event 暴露，而不是直接吞掉。
11. **Source:** `tests/unit/session.test.ts:287` `can surface fatal CLI api_retry stdout events as a RelayEvent error`。**Target:** `session_test.go::TestSessionRunStreamed_CanSurfaceFatalCLIAPIRetryStdoutEventsAsRelayEventError`。`literal-port`。要求 stdout `api_retry` fail-fast 同样生成 `error` relay event，消息中保留 `status 401` 与 `authentication_failed`。
12. **Source:** `tests/unit/session.test.ts:317` `resumes with given session ID`。**Target:** `session_test.go::TestResumeSession_ResumesWithGivenSessionID`。`literal-port`。要求 `ResumeSession("id")` 的首轮直接使用该 `session_id`。
13. **Source:** `tests/unit/session.test.ts:331` `uses --continue flag`。**Target:** `session_test.go::TestContinueSession_UsesContinueFlag`。`literal-port`。要求 `ContinueSession()` 走 `--continue` 路径并正常完成。
14. **Source:** `tests/unit/session.test.ts:345` `accepts UserInput array with text`。**Target:** `session_test.go::TestStructuredInput_AcceptsUserInputArrayWithText`。`literal-port`。要求多段 text input 被归一化后正常发送并成功完成。
15. **Source:** `tests/unit/session.test.ts:359` `sends local_image items through stream-json stdin instead of --image`。**Target:** `session_test.go::TestStructuredInput_SendsLocalImageItemsThroughStreamJSONStdinInsteadOfImageFlag`。`literal-port`。要求 `local_image` 输入走 stdin `stream-json`，`args` 中出现 `--input-format` 而不出现 `--image`。
16. **Source:** `tests/unit/session.test.ts:404` `aborts a running session`。**Target:** `session_test.go::TestAbortSignal_AbortsARunningSession`。`semantic-port`。要求外部取消信号能中断慢请求，并把 error 向上传播。
17. **Source:** `tests/unit/session.test.ts:423` `passes only explicit env from ClaudeCodeOptions into the CLI process`。**Target:** `session_test.go::TestSessionGlobalOptions_PassesOnlyExplicitEnvFromClaudeCodeOptionsIntoTheCLIProcess`。`literal-port`。要求 `ClaudeCodeOptions` 里的 `APIKey` / `AuthToken` / `BaseURL` 会显式进入 CLI env，且不掺入宿主其他变量。
18. **Source:** `tests/unit/session.test.ts:461` `forwards TurnOptions.onRawEvent through runStreamed`。**Target:** `session_test.go::TestRawClaudeEvents_ForwardsTurnOptionsOnRawEventThroughRunStreamed`。`literal-port`。要求 `RunStreamed(..., OnRawEvent=...)` 能把 raw event 一路转发给调用方。
19. **Source:** `tests/unit/session.test.ts:482` `writes raw event logs as NDJSON when enabled`。**Target:** `session_test.go::TestRawClaudeEvents_WritesRawEventLogsAsNDJSONWhenEnabled`。`literal-port`。要求启用 `RawEventLog` 后，目录里生成一份 NDJSON 文件，记录 `spawn/stdout_line/stderr_chunk/stderr_line/exit` 等事件。
20. **Source:** `tests/unit/session.test.ts:529` `rejects a pending streamed iterator when processing fails`。**Target:** `session_test.go::TestSessionInternalBranches_RejectsAPendingStreamedIteratorWhenProcessingFails`。`semantic-port`。要求底层执行器延迟报错时，已经暴露出去的事件流在下一次读取时收到 error，而不是无穷阻塞。
21. **Source:** `tests/unit/session.test.ts:542` `merges abort signals without a reason and cleans up listeners`。**Target:** `session_test.go::TestSessionInternalBranches_MergesAbortSignalsWithoutAReasonAndCleansUpListeners`。`semantic-port`。要求外部取消信号与内部 fail-fast 信号合并后仍能正确清理监听器。
22. **Source:** `tests/unit/session.test.ts:564` `preserves abort reasons when merging abort signals`。**Target:** `session_test.go::TestSessionInternalBranches_PreservesAbortReasonsWhenMergingAbortSignals`。`semantic-port`。要求合并后的取消信号保留原始 reason。
23. **Source:** `tests/unit/session.test.ts:586` `rejects relative rawEventLog paths`。**Target:** `session_test.go::TestRawEventLogger_RejectsRelativeRawEventLogPaths`。`literal-port`。要求 `RawEventLog="relative/path"` 直接报错，且错误文本说明必须使用绝对路径。
24. **Source:** `tests/unit/session.test.ts:592` `uses the default agent_logs directory and serializes process errors`。**Target:** `session_test.go::TestRawEventLogger_UsesTheDefaultAgentLogsDirectoryAndSerializesProcessErrors`。`literal-port`。要求 `RawEventLog=true` 默认写入 `<cwd>/agent_logs`，并把 `process_error` 序列化成 `{name,message,stack}`。
25. **Source:** `tests/unit/session.test.ts:635` `waits for drain before closing after a backpressured write`。**Target:** `session_test.go::TestRawEventLogger_WaitsForDrainBeforeClosingAfterABackpressuredWrite`。`literal-port`。要求 logger 在大块写入触发 backpressure 时先 `drain` 再 `Close()`，最终文件内容完整落盘。
26. **Source:** `tests/unit/session.test.ts:657` `rethrows fatal stream errors captured before close completes`。**Target:** `session_test.go::TestRawEventLogger_RethrowsFatalStreamErrorsCapturedBeforeCloseCompletes`。`literal-port`。要求通过固定时间源与随机后缀复现目标文件名冲突，验证 `Close()` 会重新抛出底层 fatal stream error。
27. **Source:** `tests/unit/session.test.ts:699` `throws close errors when the underlying stream cannot open`。**Target:** `session_test.go::TestRawEventLogger_ThrowsCloseErrorsWhenTheUnderlyingStreamCannotOpen`。`semantic-port`。要求底层流因为权限或目录状态无法打开时，`Close()` 不能静默成功，必须返回 error。

E2E 的 14 条 `real-cli.test.ts` direct port 映射已迁到 [docs/e2e/design.md](./e2e/design.md)。

---

## 10. `claude-code-parser` 独立子方案落地要求

两个语言仓库都必须单独建立 parser 子目录和独立设计文档；Go 版要求：

```text
claude-code-go/
  parser/
    protocol.go
    events.go
    parse.go
    translator.go
    writer.go
  docs/
    parser/
      design.md
```

其实现细节、源码逐函数对标关系、测试矩阵，统一放在：

- `docs/parser/design.md`

主设计文档只引用，不重复铺开全部 translator 细节。

---

## 11. 实施顺序

### Phase 1 — fake CLI + 最小可编译骨架

1. `testdata/fakeclaude/main.go`
2. 最小 `options.go` / `client.go` / `session.go` / `exec.go` / `raw_event_log.go`
3. 确保 `go test` 可以加载测试文件，而不是先报编译错误

### Phase 2 — Unit Test 1:1 迁移

1. 迁移 `exec.test.ts` → `exec_test.go`
2. 迁移 `session.test.ts` → `session_test.go`
3. 补 raw event logger 单测

### Phase 3 — E2E 基础设施与用例迁移

1. `config.go`
2. `harness.go`
3. `reporters.go`
4. `e2e_real_cli_test.go`
5. `testdata/images/*`
6. `docs/e2e/design.md`

### Phase 4 — parser 子包实现

1. `parser/protocol.go`
2. `parser/events.go`
3. `parser/parse.go`
4. `parser/translator.go`
5. `parser/writer.go`
6. 让 parser 单测变绿

### Phase 5 — options + raw event log

1. 定义 `options.go`
2. 实现 `raw_event_log.go`
3. 让类型/logger 测试变绿

### Phase 6 — exec

1. `buildArgs` 完整映射
2. stdin payload
3. 图片 MIME 探测
4. raw event 发送
5. 让 exec 单测变绿

### Phase 7 — session + client

1. `Run` / `RunStreamed`
2. 双层去重
3. structured output
4. FailFast
5. auto-resume
6. env merge
7. 让 session 单测与 E2E 变绿

### Phase 8 — polish

1. README
2. godoc 注释
3. CI：`go test ./...`, `go vet`

---

## 12. 关键风险与处理

### 风险 1：stream_event 与 assistant snapshot 重复

处理：保留 Node 版 session 层第二次去重，不能只移植 parser。

### 风险 2：stdin / stdout 并发导致 goroutine 泄露

处理：统一使用 context 取消；stderr FailFast 时立即 cancel；所有 goroutine 监听 `ctx.Done()`。

### 风险 3：环境继承泄露宿主凭据

处理：默认不继承 `os.Environ()`；仅复制 PATH 与显式 env。

### 风险 4：图片 MIME 误判

处理：按 magic bytes 检测，不依赖扩展名；未知格式直接返回错误。

### 风险 5：double-encoded result 兼容性

处理：保留 parser 子包中的 `parseDoubleEncodedResult()` 行为，不在 session/exec 重复实现。

### 风险 6：测试并行导致 env 污染与假失败

处理：测试默认串行，禁止 `t.Parallel()`；若未来要并行，必须先把环境变量污染类用例改成子进程隔离。

---

## 13. 验收标准

- [ ] `parser/` 独立目录存在，且实现对标 `claude-code-parser`
- [ ] `docs/parser/design.md` 独立存在
- [ ] `SessionOptions` 覆盖 Node 版全部字段
- [ ] `buildArgs()` 对应 Node 版 35+ flag 全量映射
- [ ] `Run()` / `RunStreamed()` 行为与 Node 版一致
- [ ] 支持 start / resume / continue 三种会话模式
- [ ] structured output, images, systemPrompt, agents, debug, pluginDir 等场景均可用
- [ ] FailFast 能在 stderr API 错误和 stdout api_retry 时提前终止
- [ ] 单测覆盖 parser / exec / session / raw_event_log
- [ ] 实施顺序为测试先行：fixture → unit/e2e 迁移 → 实现
- [ ] fake CLI 为 Go 原生预编译 binary，不依赖 Python / `go run`
- [ ] E2E 覆盖真实 CLI 路径
