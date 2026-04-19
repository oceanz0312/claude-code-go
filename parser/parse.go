package parser

import (
	"encoding/json"
	"strings"
)

func ParseLine(line string) *ClaudeEvent {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}

	var event ClaudeEvent
	if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
		return nil
	}

	return &event
}
