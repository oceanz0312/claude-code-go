package parser

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestWriterCreatesNDJSONMessages(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want map[string]any
	}{
		{name: "user", got: CreateUserMessage("hello"), want: map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "hello"}}},
		{name: "approve", got: CreateApproveMessage("tool-1"), want: map[string]any{"type": "approve", "tool_use_id": "tool-1"}},
		{name: "deny", got: CreateDenyMessage("tool-2"), want: map[string]any{"type": "deny", "tool_use_id": "tool-2"}},
		{name: "tool result", got: CreateToolResultMessage("tool-3", "done"), want: map[string]any{"type": "tool_result", "tool_use_id": "tool-3", "content": "done"}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if !strings.HasSuffix(testCase.got, "\n") {
				t.Fatalf("expected newline suffix, got %q", testCase.got)
			}

			var decoded map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(testCase.got)), &decoded); err != nil {
				t.Fatalf("expected valid json, got error: %v", err)
			}

			if !reflect.DeepEqual(decoded, testCase.want) {
				t.Fatalf("unexpected payload: got %#v want %#v", decoded, testCase.want)
			}
		})
	}
}
