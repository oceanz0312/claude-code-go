# claude-code-parser Go 移植技术方案

> 对标源码：`claude-code-parser@0.1.1`（npm 包，MIT 许可）  
> 目标：在 `claude-code-go` 仓库中作为独立子包 `parser` 实现完整功能对等移植

---

## 1. 源码定位与模块总览

原始 npm 包共含 5 个逻辑模块：

| 原始模块 | 职责 | Go 对应文件 |
|---------|------|-------------|
| `types/protocol.ts` | CLI 线路格式类型（ClaudeEvent, ClaudeMessage, ClaudeContent, ModelUsageEntry） | `parser/protocol.go` |
| `types/events.ts` | 翻译后 RelayEvent 联合类型（7 种事件） | `parser/events.go` |
| `parser.ts` | `parseLine()` — JSON 行解析 | `parser/parse.go` |
| `translator.ts` | `Translator` 类 + `extractContent()` + 内部辅助函数 | `parser/translator.go` |
| `writer.ts` | `createMessage` — 构造 stdin NDJSON 消息 | `parser/writer.go` |

依赖关系：

```text
parser/
  protocol.go      // 线路格式 struct
  events.go        // RelayEvent 密封接口 + 7 种实现
  parse.go         // ParseLine()
  translator.go    // Translator + extractContent + blockFingerprint + parseDoubleEncodedResult
  writer.go        // CreateUserMessage / CreateApproveMessage / ...
```

---

## 2. 目录结构

```text
claude-code-go/
  parser/
    protocol.go
    events.go
    parse.go
    translator.go
    writer.go
  parser_test/
    parse_test.go
    translator_test.go
    writer_test.go
    extract_content_test.go
```

如果仓库采用更常见的测试布局，也可以使用：

```text
claude-code-go/
  parser/
    protocol.go
    events.go
    parse.go
    translator.go
    writer.go
    parse_test.go
    translator_test.go
    writer_test.go
    extract_content_test.go
```

建议直接采用第二种，便于 `go test ./...`。

---

## 3. 类型定义

### 3.1 protocol.go — 线路格式类型

严格对应 `claude-code-parser/dist/types/protocol.d.ts`。Go 侧使用 struct + JSON tag 显式映射 wire format；所有可能缺失字段都用指针或零值可区分结构表示。

```go
package parser

type ModelUsageEntry struct {
	InputTokens              int `json:"inputTokens"`
	OutputTokens             int `json:"outputTokens"`
	CacheReadInputTokens     int `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int `json:"cacheCreationInputTokens"`
	ContextWindow            int `json:"contextWindow"`
}

type ClaudeContent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	Content   any    `json:"content,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   *bool  `json:"is_error,omitempty"`
}

type ClaudeMessage struct {
	Content    []ClaudeContent `json:"content"`
	Role       string          `json:"role,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	ID         string          `json:"id,omitempty"`
}

type ClaudeEvent struct {
	Type          string                     `json:"type"`
	Subtype       string                     `json:"subtype,omitempty"`
	Message       *ClaudeMessage             `json:"message,omitempty"`
	Result        any                        `json:"result,omitempty"`
	SessionID     string                     `json:"session_id,omitempty"`
	Model         string                     `json:"model,omitempty"`
	Tools         []string                   `json:"tools,omitempty"`
	DurationMS    *int                       `json:"duration_ms,omitempty"`
	DurationAPIMS *int                       `json:"duration_api_ms,omitempty"`
	CostUSD       *float64                   `json:"cost_usd,omitempty"`
	TotalCostUSD  *float64                   `json:"total_cost_usd,omitempty"`
	IsError       *bool                      `json:"is_error,omitempty"`
	NumTurns      *int                       `json:"num_turns,omitempty"`
	ModelUsage    map[string]ModelUsageEntry `json:"modelUsage,omitempty"`
	Usage         any                        `json:"usage,omitempty"`
	Event         any                        `json:"event,omitempty"`
	Attempt       *int                       `json:"attempt,omitempty"`
	MaxRetries    *int                       `json:"max_retries,omitempty"`
	RetryDelayMS  *int                       `json:"retry_delay_ms,omitempty"`
	ErrorStatus   *int                       `json:"error_status,omitempty"`
	Error         string                     `json:"error,omitempty"`
	Raw           map[string]any             `json:"-"`
}
```

说明：

- `ClaudeContent.Type` 保持 `string`，不能收窄为 enum，因为上游 CLI 可新增 block 类型。
- `Input` / `Content` / `Result` / `Usage` 必须保持 `any`，以匹配 JS 的松散对象。
- `ModelUsage` JSON key 必须是 `modelUsage`，不能改为 snake_case。
- `ClaudeMessage.ID` 虽未在 `claude-code-parser` `d.ts` 中显式声明，但 Go 版必须保留，
  因为上游 `agent-sdk/src/session.ts` 会通过松散对象读取 `message.id` 做二层去重。
- `ClaudeEvent.Event` / `Attempt` / `MaxRetries` / `RetryDelayMS` / `ErrorStatus` / `Error`
  也必须显式建模，分别承载 `stream_event` envelope 与 `system.api_retry` FailFast 所需字段。
- `ClaudeEvent.Raw` 保存原始 JSON，防止 Go 严格结构体把未来 CLI 扩展字段吞掉。

### 3.2 events.go — 翻译后 RelayEvent 联合类型

原始 TypeScript 是 discriminated union。Go 中用“密封接口 + 私有 marker 方法”实现。

```go
package parser

type RelayEvent interface {
	relayEvent()
	EventType() string
}

type TextDeltaEvent struct {
	Type    string
	Content string
}
func (TextDeltaEvent) relayEvent() {}
func (e TextDeltaEvent) EventType() string { return e.Type }

type ThinkingDeltaEvent struct {
	Type    string
	Content string
}
func (ThinkingDeltaEvent) relayEvent() {}
func (e ThinkingDeltaEvent) EventType() string { return e.Type }

type ToolUseEvent struct {
	Type      string
	ToolUseID string
	ToolName  string
	Input     string
}
func (ToolUseEvent) relayEvent() {}
func (e ToolUseEvent) EventType() string { return e.Type }

type ToolResultEvent struct {
	Type      string
	ToolUseID string
	Output    string
	IsError   bool
}
func (ToolResultEvent) relayEvent() {}
func (e ToolResultEvent) EventType() string { return e.Type }

type SessionMetaEvent struct {
	Type  string
	Model string
}
func (SessionMetaEvent) relayEvent() {}
func (e SessionMetaEvent) EventType() string { return e.Type }

type TurnCompleteEvent struct {
	Type          string
	SessionID     string
	CostUSD       *float64
	InputTokens   *int
	OutputTokens  *int
	ContextWindow *int
}
func (TurnCompleteEvent) relayEvent() {}
func (e TurnCompleteEvent) EventType() string { return e.Type }

type ErrorEvent struct {
	Type      string
	Message   string
	SessionID string
}
func (ErrorEvent) relayEvent() {}
func (e ErrorEvent) EventType() string { return e.Type }
```

辅助构造函数：

```go
func NewTextDelta(content string) TextDeltaEvent {
	return TextDeltaEvent{Type: "text_delta", Content: content}
}

func NewThinkingDelta(content string) ThinkingDeltaEvent {
	return ThinkingDeltaEvent{Type: "thinking_delta", Content: content}
}

func NewToolUse(toolUseID, toolName, input string) ToolUseEvent {
	return ToolUseEvent{Type: "tool_use", ToolUseID: toolUseID, ToolName: toolName, Input: input}
}

func NewToolResult(toolUseID, output string, isError bool) ToolResultEvent {
	return ToolResultEvent{Type: "tool_result", ToolUseID: toolUseID, Output: output, IsError: isError}
}

func NewSessionMeta(model string) SessionMetaEvent {
	if model == "" {
		model = "unknown"
	}
	return SessionMetaEvent{Type: "session_meta", Model: model}
}

func NewTurnComplete(sessionID string) TurnCompleteEvent {
	return TurnCompleteEvent{Type: "turn_complete", SessionID: sessionID}
}

func NewError(message, sessionID string) ErrorEvent {
	return ErrorEvent{Type: "error", Message: message, SessionID: sessionID}
}
```

---

## 4. ParseLine — 行解析器

### 4.1 原始逻辑（对标 parser.js）

```javascript
export function parseLine(line) {
    const trimmed = line.trim();
    if (trimmed.length === 0)
        return null;
    try {
        return JSON.parse(trimmed);
    }
    catch {
        return null;
    }
}
```

### 4.2 Go 实现

```go
package parser

import (
	"bytes"
	"encoding/json"
)

func ParseLine(line string) (*ClaudeEvent, error) {
	trimmed := bytes.TrimSpace([]byte(line))
	if len(trimmed) == 0 {
		return nil, nil
	}

	var event ClaudeEvent
	if err := json.Unmarshal(trimmed, &event); err != nil {
		return nil, nil
	}
	return &event, nil
}
```

说明：

- 为了严格对标 JS 行为，非 JSON 行与空行都返回 `(nil, nil)`，不向上抛错误。
- Go 函数保留 `error` 返回值只是为了 API 扩展余地；当前实现中解析失败仍返回 `nil, nil`。
- 若想更严格模仿 JS，也可签名写成 `func ParseLine(line string) *ClaudeEvent`。

推荐最终签名：

```go
func ParseLine(line string) *ClaudeEvent
```

因为这与 npm 包更一致，也能简化上层调用。

---

## 5. Translator — 核心翻译器

### 5.1 状态字段

严格对应 `translator.js`：

```go
type Translator struct {
	lastContentIndex int
	lastFirstBlockKey string
	sessionID string
	model string
}
```

构造器：

```go
func NewTranslator() *Translator {
	return &Translator{}
}
```

公开方法：

```go
func (t *Translator) SessionID() string {
	return t.sessionID
}

func (t *Translator) Model() string {
	return t.model
}

func (t *Translator) Reset() {
	t.lastContentIndex = 0
	t.lastFirstBlockKey = ""
}
```

### 5.2 Translate 主分发

严格对应 `translate(raw)`：

```go
func (t *Translator) Translate(raw ClaudeEvent) []RelayEvent {
	switch raw.Type {
	case "system":
		return t.translateSystem(raw)
	case "result":
		return t.translateResult(raw)
	case "assistant":
		return t.translateAssistant(raw)
	case "user":
		return t.translateUser(raw)
	default:
		return nil
	}
}
```

### 5.3 translateSystem

严格对应原始 JS：

```go
func (t *Translator) translateSystem(raw ClaudeEvent) []RelayEvent {
	switch raw.Subtype {
	case "init":
		if raw.SessionID != "" {
			t.sessionID = raw.SessionID
		}
		if raw.Model != "" {
			t.model = raw.Model
		}
		return []RelayEvent{NewSessionMeta(raw.Model)}

	case "result":
		resultText := parseDoubleEncodedResult(raw.Result)
		t.Reset()
		if raw.IsError != nil && *raw.IsError {
			return []RelayEvent{NewError(resultText, raw.SessionID)}
		}
		return []RelayEvent{NewTurnComplete(raw.SessionID)}

	default:
		return nil
	}
}
```

### 5.4 translateResult

最关键的细节是 token 聚合：

```javascript
ev.inputTokens =
  usage.inputTokens + usage.cacheReadInputTokens + usage.cacheCreationInputTokens;
```

Go 实现：

```go
func (t *Translator) translateResult(raw ClaudeEvent) []RelayEvent {
	resultText := parseDoubleEncodedResult(raw.Result)

	if raw.Subtype == "error" || (raw.IsError != nil && *raw.IsError) {
		t.Reset()
		return []RelayEvent{NewError(resultText, raw.SessionID)}
	}

	ev := NewTurnComplete(raw.SessionID)
	ev.CostUSD = raw.TotalCostUSD

	if raw.ModelUsage != nil {
		for _, usage := range raw.ModelUsage {
			inputTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
			outputTokens := usage.OutputTokens
			contextWindow := usage.ContextWindow
			ev.InputTokens = &inputTokens
			ev.OutputTokens = &outputTokens
			ev.ContextWindow = &contextWindow
			break
		}
	}

	t.Reset()
	return []RelayEvent{ev}
}
```

### 5.5 translateAssistant — 去重核心

原始逻辑：

```javascript
translateAssistant(raw) {
    const msg = raw.message;
    if (!msg?.content || msg.content.length === 0) return [];

    const firstKey = blockFingerprint(msg.content[0]);
    if (firstKey !== this.lastFirstBlockKey) {
        this.lastContentIndex = 0;
        this.lastFirstBlockKey = firstKey;
    }

    if (msg.content.length < this.lastContentIndex) {
        this.lastContentIndex = 0;
    }

    const events = [];
    for (let i = this.lastContentIndex; i < msg.content.length; i++) {
        const block = msg.content[i];
        const ev = this.translateContentBlock(block);
        if (ev) events.push(ev);
    }

    this.lastContentIndex = msg.content.length;
    return events;
}
```

Go 实现必须逐行保持同语义：

```go
func (t *Translator) translateAssistant(raw ClaudeEvent) []RelayEvent {
	msg := raw.Message
	if msg == nil || len(msg.Content) == 0 {
		return nil
	}

	firstKey := blockFingerprint(msg.Content[0])
	if firstKey != t.lastFirstBlockKey {
		t.lastContentIndex = 0
		t.lastFirstBlockKey = firstKey
	}

	if len(msg.Content) < t.lastContentIndex {
		t.lastContentIndex = 0
	}

	events := make([]RelayEvent, 0, len(msg.Content)-t.lastContentIndex)
	for i := t.lastContentIndex; i < len(msg.Content); i++ {
		block := msg.Content[i]
		ev := t.translateContentBlock(block)
		if ev != nil {
			events = append(events, ev)
		}
	}

	t.lastContentIndex = len(msg.Content)
	return events
}
```

### 5.6 translateUser

```go
func (t *Translator) translateUser(raw ClaudeEvent) []RelayEvent {
	msg := raw.Message
	if msg == nil {
		return nil
	}

	var events []RelayEvent
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			isError := false
			if block.IsError != nil {
				isError = *block.IsError
			}
			events = append(events, NewToolResult(
				block.ToolUseID,
				ExtractContent(block.Content),
				isError,
			))
		}
	}
	return events
}
```

### 5.7 translateContentBlock

```go
func (t *Translator) translateContentBlock(block ClaudeContent) RelayEvent {
	switch block.Type {
	case "text":
		return NewTextDelta(block.Text)

	case "thinking":
		text := block.Thinking
		if text == "" {
			text = block.Text
		}
		if text == "" {
			return nil
		}
		return NewThinkingDelta(text)

	case "tool_use":
		input := ""
		if block.Input != nil {
			bytes, err := json.Marshal(block.Input)
			if err == nil {
				input = string(bytes)
			}
		}
		return NewToolUse(block.ID, block.Name, input)

	case "tool_result":
		isError := false
		if block.IsError != nil {
			isError = *block.IsError
		}
		return NewToolResult(block.ToolUseID, ExtractContent(block.Content), isError)

	default:
		return nil
	}
}
```

---

## 6. extractContent / blockFingerprint / parseDoubleEncodedResult

### 6.1 ExtractContent

严格对应导出函数 `extractContent(raw)`：

```go
func ExtractContent(raw any) string {
	if raw == nil {
		return ""
	}

	if s, ok := raw.(string); ok {
		return s
	}

	if arr, ok := raw.([]any); ok {
		parts := make([]string, 0, len(arr))
		for _, item := range arr {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, ok := block["text"].(string)
			if !ok || text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	}

	return fmt.Sprint(raw)
}
```

### 6.2 blockFingerprint

严格对应内部私有函数：

```go
func blockFingerprint(block ClaudeContent) string {
	if block.ID != "" {
		return block.Type + ":" + block.ID
	}

	text := block.Thinking
	if text == "" {
		text = block.Text
	}
	if text != "" {
		runes := []rune(text)
		if len(runes) > 64 {
			text = string(runes[:64])
		}
		return block.Type + ":" + text
	}

	fallback := block.ToolUseID
	if fallback == "" {
		fallback = "unknown"
	}
	return block.Type + ":" + fallback
}
```

说明：
- JS `slice(0, 64)` 按 UTF-16 code unit 切；Go 若直接按 byte 会破坏 UTF-8，建议按 rune 截断，更安全。
- 如果要做到“字节级”绝对一致，可按 byte 截断；但这会对中文表情产生非法 UTF-8 风险。推荐 rune 截断。
- 因为此指纹只用于内部上下文切换检测，不对外暴露，rune 截断是可接受的工程适配。

### 6.3 parseDoubleEncodedResult

```go
func parseDoubleEncodedResult(result any) string {
	if result == nil {
		return ""
	}

	s, ok := result.(string)
	if !ok {
		return fmt.Sprint(result)
	}

	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err == nil {
		if unwrapped, ok := parsed.(string); ok {
			return unwrapped
		}
	}
	return s
}
```

---

## 7. Writer — stdin 消息构造器

原始 JS：

```javascript
export const createMessage = {
    user(content) { ... };
    approve(toolUseId) { ... };
    deny(toolUseId) { ... };
    toolResult(toolUseId, content) { ... };
}
```

Go 用 4 个函数更自然：

```go
func CreateUserMessage(content string) string {
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": content,
		},
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}

func CreateApproveMessage(toolUseID string) string {
	payload := map[string]any{
		"type": "approve",
		"tool_use_id": toolUseID,
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}

func CreateDenyMessage(toolUseID string) string {
	payload := map[string]any{
		"type": "deny",
		"tool_use_id": toolUseID,
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}

func CreateToolResultMessage(toolUseID, content string) string {
	payload := map[string]any{
		"type": "tool_result",
		"tool_use_id": toolUseID,
		"content": content,
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}
```

---

## 8. 测试策略

### 8.1 测试矩阵

| 测试场景 | 目标 | 文件 |
|---------|------|------|
| 空行返回 nil | 对标 parseLine | `parse_test.go` |
| 非 JSON 返回 nil | 对标 parseLine | `parse_test.go` |
| `stream_event` 保留 `Event` payload | session 二层去重依赖 envelope | `parse_test.go` |
| `system.api_retry` 保留 retry 扩展字段 | stdout FailFast 依赖 `Attempt/MaxRetries/...` | `parse_test.go` |
| `assistant.message.id` 保留 `ID` | stream_event / assistant snapshot 去重依赖 message ID | `parse_test.go` |
| system.init → session_meta | model / session_id 捕获 | `translator_test.go` |
| result.success → turn_complete | total_cost_usd + usage 聚合 | `translator_test.go` |
| result.error → error | subtype / is_error 双分支 | `translator_test.go` |
| assistant 连续累积快照 | 去重只输出新增 block | `translator_test.go` |
| assistant 上下文切换 | fingerprint 变化时 reset | `translator_test.go` |
| assistant 内容缩短 | 安全回退 reset | `translator_test.go` |
| user.tool_result | 提取 output / is_error | `translator_test.go` |
| extractContent 四类输入 | nil/string/list/other | `extract_content_test.go` |
| parseDoubleEncodedResult | 双重编码解包 | `translator_test.go` |
| writer 4 个函数 | JSON + 换行格式 | `writer_test.go` |

### 8.2 测试示例

```go
func TestTranslatorDedupIncremental(t *testing.T) {
	tr := NewTranslator()

	ev1 := ClaudeEvent{
		Type: "assistant",
		Message: &ClaudeMessage{Content: []ClaudeContent{
			{Type: "text", Text: "Hello"},
		}},
	}
	out1 := tr.Translate(ev1)
	if len(out1) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out1))
	}

	ev2 := ClaudeEvent{
		Type: "assistant",
		Message: &ClaudeMessage{Content: []ClaudeContent{
			{Type: "text", Text: "Hello"},
			{Type: "text", Text: " World"},
		}},
	}
	out2 := tr.Translate(ev2)
	if len(out2) != 1 {
		t.Fatalf("expected only new block, got %d", len(out2))
	}

	text, ok := out2[0].(TextDeltaEvent)
	if !ok || text.Content != " World" {
		t.Fatalf("unexpected event: %#v", out2[0])
	}
}
```

---

## 9. 与上层 SDK 的集成点

parser 包应作为纯翻译层存在，不依赖 exec/session/client：

```text
claude-code-go/
  client.go
  session.go   ← import parser.NewTranslator / parser.ParseLine
  exec.go      ← import parser.CreateUserMessage 等
  parser/
```

上层使用方式：

```go
translator := parser.NewTranslator()
for scanner.Scan() {
	line := scanner.Text()
	raw := parser.ParseLine(line)
	if raw == nil {
		continue
	}
	for _, ev := range translator.Translate(*raw) {
		// 转发到 session event stream
	}
}
```

注意：`ParseLine` 虽然本身只负责 JSON 解析，但绝不能丢掉 `Message.ID`、
`ClaudeEvent.Event` 与 `system.api_retry` 扩展字段；这些字段会被上层 `session.go`
直接消费，用于 `stream_event` 二层去重和 stdout FailFast。

---

## 10. 对标合规检查清单

- [ ] `ParseLine` 对空行返回 nil
- [ ] `ParseLine` 对非法 JSON 返回 nil，不抛错
- [ ] `Translate` 对未知 type 返回空切片或 nil
- [ ] `translateContentBlock` 对未知 block 类型返回 nil
- [ ] `translateAssistant` 使用 `lastContentIndex + lastFirstBlockKey` 双机制去重
- [ ] `blockFingerprint` 优先级：ID → thinking/text[:64] → tool_use_id → unknown
- [ ] `parseDoubleEncodedResult` 仅在解包结果为 string 时返回解包值
- [ ] `ExtractContent` 正确处理 nil / string / []any / 其他
- [ ] `translateResult` 中 inputTokens = input + cacheRead + cacheCreation
- [ ] `translateResult` 只取 `ModelUsage` 的第一项
- [ ] Writer 4 个函数输出 JSON + `\n`
- [ ] `ClaudeMessage.ID` 必须保留，供 session 层 message ID 识别
- [ ] `ClaudeEvent.Event` 必须保留，供 `stream_event` envelope 透传
- [ ] `system.api_retry` 扩展字段必须保留：`Attempt/MaxRetries/RetryDelayMS/ErrorStatus/Error`
- [ ] `ClaudeEvent.Raw` 必须保留原始 JSON，避免未来协议扩展字段被吞掉
- [ ] 全实现仅依赖 Go stdlib
