package parser

import (
	"reflect"
	"testing"
)

func TestTranslatorSystemInitEmitsSessionMetaAndCapturesState(t *testing.T) {
	translator := NewTranslator()
	events := translator.Translate(ClaudeEvent{Type: "system", Subtype: "init", SessionID: "sid-1", Model: "sonnet"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	meta, ok := events[0].(SessionMetaEvent)
	if !ok {
		t.Fatalf("expected session meta event, got %#v", events[0])
	}
	if meta.Model != "sonnet" {
		t.Fatalf("unexpected model: %q", meta.Model)
	}
	if translator.SessionID() != "sid-1" {
		t.Fatalf("unexpected session id: %q", translator.SessionID())
	}
	if translator.Model() != "sonnet" {
		t.Fatalf("unexpected translator model: %q", translator.Model())
	}
}

func TestTranslatorResultSuccessAggregatesUsage(t *testing.T) {
	translator := NewTranslator()
	cost := 0.25
	events := translator.Translate(ClaudeEvent{
		Type:         "result",
		Subtype:      "success",
		SessionID:    "sid-2",
		TotalCostUSD: &cost,
		ModelUsage: map[string]ModelUsageEntry{
			"claude-sonnet": {
				InputTokens:              100,
				OutputTokens:             20,
				CacheReadInputTokens:     5,
				CacheCreationInputTokens: 3,
				ContextWindow:            200000,
			},
		},
	})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	complete, ok := events[0].(TurnCompleteEvent)
	if !ok {
		t.Fatalf("expected turn complete event, got %#v", events[0])
	}
	if complete.SessionID != "sid-2" {
		t.Fatalf("unexpected session id: %q", complete.SessionID)
	}
	if complete.CostUSD == nil || *complete.CostUSD != cost {
		t.Fatalf("unexpected cost: %#v", complete.CostUSD)
	}
	if complete.InputTokens == nil || *complete.InputTokens != 108 {
		t.Fatalf("unexpected input tokens: %#v", complete.InputTokens)
	}
	if complete.OutputTokens == nil || *complete.OutputTokens != 20 {
		t.Fatalf("unexpected output tokens: %#v", complete.OutputTokens)
	}
	if complete.ContextWindow == nil || *complete.ContextWindow != 200000 {
		t.Fatalf("unexpected context window: %#v", complete.ContextWindow)
	}
}

func TestTranslatorResultErrorAndSystemResultErrorEmitErrorEvent(t *testing.T) {
	translator := NewTranslator()
	trueValue := true

	for _, raw := range []ClaudeEvent{
		{Type: "result", Subtype: "error", Result: `"boom"`},
		{Type: "system", Subtype: "result", Result: `"kaboom"`, IsError: &trueValue},
	} {
		events := translator.Translate(raw)
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if _, ok := events[0].(ErrorEvent); !ok {
			t.Fatalf("expected error event, got %#v", events[0])
		}
	}
}

func TestTranslatorAssistantDedupsIncrementalSnapshotsAndResetsOnContextSwitch(t *testing.T) {
	translator := NewTranslator()
	first := translator.Translate(ClaudeEvent{Type: "assistant", Message: &ClaudeMessage{Content: []ClaudeContent{{Type: "text", Text: "Hello"}}}})
	if len(first) != 1 {
		t.Fatalf("expected first assistant snapshot to emit 1 event, got %d", len(first))
	}

	second := translator.Translate(ClaudeEvent{Type: "assistant", Message: &ClaudeMessage{Content: []ClaudeContent{{Type: "text", Text: "Hello"}, {Type: "text", Text: " world"}}}})
	if len(second) != 1 {
		t.Fatalf("expected only delta block, got %d", len(second))
	}
	if delta, ok := second[0].(TextDeltaEvent); !ok || delta.Content != " world" {
		t.Fatalf("unexpected delta event: %#v", second[0])
	}

	switched := translator.Translate(ClaudeEvent{Type: "assistant", Message: &ClaudeMessage{Content: []ClaudeContent{{Type: "thinking", Thinking: "New stream"}}}})
	if len(switched) != 1 {
		t.Fatalf("expected reset on context switch, got %d", len(switched))
	}
	if delta, ok := switched[0].(ThinkingDeltaEvent); !ok || delta.Content != "New stream" {
		t.Fatalf("unexpected switched event: %#v", switched[0])
	}

	shrunk := translator.Translate(ClaudeEvent{Type: "assistant", Message: &ClaudeMessage{Content: []ClaudeContent{{Type: "text", Text: "Hi again"}}}})
	if len(shrunk) != 1 {
		t.Fatalf("expected shrink to reset state, got %d", len(shrunk))
	}
}

func TestTranslatorUserToolResultAndToolUseBlock(t *testing.T) {
	trueValue := true
	translator := NewTranslator()

	assistant := translator.Translate(ClaudeEvent{Type: "assistant", Message: &ClaudeMessage{Content: []ClaudeContent{{Type: "tool_use", ID: "tool-1", Name: "Read", Input: map[string]any{"file": "/tmp/test.txt"}}}}})
	if len(assistant) != 1 {
		t.Fatalf("expected tool use event, got %d", len(assistant))
	}
	toolUse, ok := assistant[0].(ToolUseEvent)
	if !ok {
		t.Fatalf("expected tool use event, got %#v", assistant[0])
	}
	if toolUse.ToolUseID != "tool-1" || toolUse.ToolName != "Read" || toolUse.Input != `{"file":"/tmp/test.txt"}` {
		t.Fatalf("unexpected tool use event: %#v", toolUse)
	}

	user := translator.Translate(ClaudeEvent{Type: "user", Message: &ClaudeMessage{Content: []ClaudeContent{{Type: "tool_result", ToolUseID: "tool-1", Content: []any{map[string]any{"text": "line 1"}, map[string]any{"text": "line 2"}}, IsError: &trueValue}}}})
	if len(user) != 1 {
		t.Fatalf("expected tool result event, got %d", len(user))
	}
	toolResult, ok := user[0].(ToolResultEvent)
	if !ok {
		t.Fatalf("expected tool result event, got %#v", user[0])
	}
	if !toolResult.IsError || toolResult.Output != "line 1\nline 2" {
		t.Fatalf("unexpected tool result event: %#v", toolResult)
	}
}

func TestExtractContentHandlesAllSupportedShapes(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "string", in: "plain text", want: "plain text"},
		{name: "list", in: []any{map[string]any{"text": "a"}, map[string]any{"text": "b"}}, want: "a\nb"},
		{name: "other", in: 42, want: "42"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ExtractContent(testCase.in); got != testCase.want {
				t.Fatalf("unexpected content: got %q want %q", got, testCase.want)
			}
		})
	}
}

func TestTranslatorIgnoresUnknownTypes(t *testing.T) {
	translator := NewTranslator()
	if got := translator.Translate(ClaudeEvent{Type: "progress"}); got != nil {
		t.Fatalf("expected nil for unknown type, got %#v", got)
	}
}

func TestRelayEventConstructors(t *testing.T) {
	text := NewTextDelta("hello")
	think := NewThinkingDelta("hmm")
	toolUse := NewToolUse("tool", "Read", `{}`)
	toolResult := NewToolResult("tool", "ok", false)
	meta := NewSessionMeta("")
	complete := NewTurnComplete("sid")
	errEvent := NewError("boom", "sid")

	if text.EventType() != "text_delta" || think.EventType() != "thinking_delta" {
		t.Fatal("unexpected delta constructor type")
	}
	if toolUse.EventType() != "tool_use" || toolResult.EventType() != "tool_result" {
		t.Fatal("unexpected tool constructor type")
	}
	if meta.Model != "unknown" {
		t.Fatalf("expected unknown fallback model, got %q", meta.Model)
	}
	if complete.SessionID != "sid" || errEvent.SessionID != "sid" {
		t.Fatal("unexpected session id in constructors")
	}

	got := []string{text.EventType(), think.EventType(), toolUse.EventType(), toolResult.EventType(), meta.EventType(), complete.EventType(), errEvent.EventType()}
	want := []string{"text_delta", "thinking_delta", "tool_use", "tool_result", "session_meta", "turn_complete", "error"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected constructor event types: got %#v want %#v", got, want)
	}
}
