package parser

import "encoding/json"

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

func (event *ClaudeEvent) UnmarshalJSON(data []byte) error {
	type alias ClaudeEvent
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*event = ClaudeEvent(decoded)
	event.Raw = raw
	return nil
}
