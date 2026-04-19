package parser

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Translator struct {
	lastContentIndex  int
	lastFirstBlockKey string
	sessionID         string
	model             string
}

func NewTranslator() *Translator {
	return &Translator{}
}

func (translator *Translator) SessionID() string {
	return translator.sessionID
}

func (translator *Translator) Model() string {
	return translator.model
}

func (translator *Translator) Reset() {
	translator.lastContentIndex = 0
	translator.lastFirstBlockKey = ""
}

func (translator *Translator) Translate(raw ClaudeEvent) []RelayEvent {
	switch raw.Type {
	case "system":
		return translator.translateSystem(raw)
	case "result":
		return translator.translateResult(raw)
	case "assistant":
		return translator.translateAssistant(raw)
	case "user":
		return translator.translateUser(raw)
	default:
		return nil
	}
}

func ExtractContent(raw any) string {
	if raw == nil {
		return ""
	}

	if text, ok := raw.(string); ok {
		return text
	}

	if items, ok := raw.([]any); ok {
		parts := make([]string, 0, len(items))
		for _, item := range items {
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

func (translator *Translator) translateSystem(raw ClaudeEvent) []RelayEvent {
	switch raw.Subtype {
	case "init":
		if raw.SessionID != "" {
			translator.sessionID = raw.SessionID
		}
		if raw.Model != "" {
			translator.model = raw.Model
		}
		return []RelayEvent{NewSessionMeta(raw.Model)}
	case "result":
		resultText := parseDoubleEncodedResult(raw.Result)
		translator.Reset()
		if raw.IsError != nil && *raw.IsError {
			return []RelayEvent{NewError(resultText, raw.SessionID)}
		}
		return []RelayEvent{NewTurnComplete(raw.SessionID)}
	default:
		return nil
	}
}

func (translator *Translator) translateResult(raw ClaudeEvent) []RelayEvent {
	resultText := parseDoubleEncodedResult(raw.Result)
	if raw.Subtype == "error" || (raw.IsError != nil && *raw.IsError) {
		translator.Reset()
		return []RelayEvent{NewError(resultText, raw.SessionID)}
	}

	event := NewTurnComplete(raw.SessionID)
	event.CostUSD = raw.TotalCostUSD
	if raw.ModelUsage != nil {
		for _, usage := range raw.ModelUsage {
			inputTokens := usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
			outputTokens := usage.OutputTokens
			contextWindow := usage.ContextWindow
			event.InputTokens = &inputTokens
			event.OutputTokens = &outputTokens
			event.ContextWindow = &contextWindow
			break
		}
	}

	translator.Reset()
	return []RelayEvent{event}
}

func (translator *Translator) translateAssistant(raw ClaudeEvent) []RelayEvent {
	if raw.Message == nil || len(raw.Message.Content) == 0 {
		return nil
	}

	firstKey := blockFingerprint(raw.Message.Content[0])
	if firstKey != translator.lastFirstBlockKey {
		translator.lastContentIndex = 0
		translator.lastFirstBlockKey = firstKey
	}

	if len(raw.Message.Content) < translator.lastContentIndex {
		translator.lastContentIndex = 0
	}

	events := make([]RelayEvent, 0, len(raw.Message.Content)-translator.lastContentIndex)
	for index := translator.lastContentIndex; index < len(raw.Message.Content); index++ {
		event := translator.translateContentBlock(raw.Message.Content[index])
		if event != nil {
			events = append(events, event)
		}
	}

	translator.lastContentIndex = len(raw.Message.Content)
	return events
}

func (translator *Translator) translateUser(raw ClaudeEvent) []RelayEvent {
	if raw.Message == nil {
		return nil
	}

	var events []RelayEvent
	for _, block := range raw.Message.Content {
		if block.Type != "tool_result" {
			continue
		}

		isError := false
		if block.IsError != nil {
			isError = *block.IsError
		}

		events = append(events, NewToolResult(block.ToolUseID, ExtractContent(block.Content), isError))
	}

	return events
}

func (translator *Translator) translateContentBlock(block ClaudeContent) RelayEvent {
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
		payload := ""
		if block.Input != nil {
			bytes, _ := json.Marshal(block.Input)
			payload = string(bytes)
		}
		return NewToolUse(block.ID, block.Name, payload)
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

func parseDoubleEncodedResult(result any) string {
	if result == nil {
		return ""
	}

	text, ok := result.(string)
	if !ok {
		return fmt.Sprint(result)
	}

	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		if unwrapped, ok := parsed.(string); ok {
			return unwrapped
		}
	}

	return text
}
