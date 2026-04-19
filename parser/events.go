package parser

type RelayEvent interface {
	relayEvent()
	EventType() string
}

type TextDeltaEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

func (TextDeltaEvent) relayEvent()            {}
func (event TextDeltaEvent) EventType() string { return event.Type }

type ThinkingDeltaEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

func (ThinkingDeltaEvent) relayEvent()            {}
func (event ThinkingDeltaEvent) EventType() string { return event.Type }

type ToolUseEvent struct {
	Type      string `json:"type"`
	ToolUseID string `json:"toolUseId"`
	ToolName  string `json:"toolName"`
	Input     string `json:"input"`
}

func (ToolUseEvent) relayEvent()            {}
func (event ToolUseEvent) EventType() string { return event.Type }

type ToolResultEvent struct {
	Type      string `json:"type"`
	ToolUseID string `json:"toolUseId"`
	Output    string `json:"output"`
	IsError   bool   `json:"isError"`
}

func (ToolResultEvent) relayEvent()            {}
func (event ToolResultEvent) EventType() string { return event.Type }

type SessionMetaEvent struct {
	Type  string `json:"type"`
	Model string `json:"model"`
}

func (SessionMetaEvent) relayEvent()            {}
func (event SessionMetaEvent) EventType() string { return event.Type }

type TurnCompleteEvent struct {
	Type          string   `json:"type"`
	SessionID     string   `json:"sessionId,omitempty"`
	CostUSD       *float64 `json:"costUsd,omitempty"`
	InputTokens   *int     `json:"inputTokens,omitempty"`
	OutputTokens  *int     `json:"outputTokens,omitempty"`
	ContextWindow *int     `json:"contextWindow,omitempty"`
}

func (TurnCompleteEvent) relayEvent()            {}
func (event TurnCompleteEvent) EventType() string { return event.Type }

type ErrorEvent struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	SessionID string `json:"sessionId,omitempty"`
}

func (ErrorEvent) relayEvent()            {}
func (event ErrorEvent) EventType() string { return event.Type }

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
