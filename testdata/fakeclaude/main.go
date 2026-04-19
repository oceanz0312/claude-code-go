package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type inspectionPayload struct {
	Args  []string       `json:"args"`
	CWD   string         `json:"cwd"`
	Flags inspectionFlag `json:"flags"`
	Input inspectionIn   `json:"input"`
	Env   inspectionEnv  `json:"env"`
}

type inspectionFlag struct {
	ResumeSessionID *string `json:"resumeSessionId"`
	ContinueSession bool    `json:"continueSession"`
}

type inspectionIn struct {
	Prompt      string  `json:"prompt"`
	ImageCount  int     `json:"imageCount"`
	InputFormat *string `json:"inputFormat"`
}

type inspectionEnv struct {
	AnthropicAPIKey    *string `json:"ANTHROPIC_API_KEY"`
	AnthropicAuthToken *string `json:"ANTHROPIC_AUTH_TOKEN"`
	AnthropicBaseURL   *string `json:"ANTHROPIC_BASE_URL"`
	InspectCustomEnv   *string `json:"INSPECT_CUSTOM_ENV"`
	InspectInherited   *string `json:"INSPECT_INHERITED_ENV"`
}

func main() {
	args := os.Args[1:]
	inputFormat := getArgPointer(args, "--input-format")
	prompt, imageCount := readInput(args, valueOrEmpty(inputFormat))
	sessionID := firstNonEmpty(valueOrEmpty(getArgPointer(args, "--resume")), valueOrEmpty(getArgPointer(args, "--session-id")), "test-session-001")
	cwd, _ := os.Getwd()

	inspect := inspectionPayload{
		Args: args,
		CWD:  cwd,
		Flags: inspectionFlag{
			ResumeSessionID: getArgPointer(args, "--resume"),
			ContinueSession: hasFlag(args, "--continue"),
		},
		Input: inspectionIn{
			Prompt:      prompt,
			ImageCount:  imageCount,
			InputFormat: inputFormat,
		},
		Env: inspectionEnv{
			AnthropicAPIKey:    envPtr("ANTHROPIC_API_KEY"),
			AnthropicAuthToken: envPtr("ANTHROPIC_AUTH_TOKEN"),
			AnthropicBaseURL:   envPtr("ANTHROPIC_BASE_URL"),
			InspectCustomEnv:   envPtr("INSPECT_CUSTOM_ENV"),
			InspectInherited:   envPtr("INSPECT_INHERITED_ENV"),
		},
	}

	switch {
	case strings.Contains(prompt, "__inspect_exec_options__"):
		emit(map[string]any{
			"type":            "result",
			"subtype":         "success",
			"is_error":        false,
			"result":          "inspect-exec-options",
			"session_id":      sessionID,
			"inspection":      inspect,
			"total_cost_usd":  0,
			"duration_ms":     1,
			"duration_api_ms": 1,
			"num_turns":       1,
			"modelUsage":      map[string]any{},
		})
		return

	case strings.Contains(prompt, "__inspect_session_flags__"):
		reportBytes, _ := json.Marshal(inspect.Flags)
		report := string(reportBytes)
		emitInit(sessionID)
		emitMessageStart("msg_inspect", sessionID)
		emitTextDelta("msg_inspect", report, sessionID)
		emitAssistantText(report, sessionID, "msg_inspect")
		emitResult(report, sessionID)
		return

	case strings.Contains(prompt, "__inspect_raw_events__"):
		_, _ = os.Stderr.WriteString("raw stderr line\n")
		emit(map[string]any{
			"type":            "result",
			"subtype":         "success",
			"is_error":        false,
			"result":          "inspect-raw-events",
			"session_id":      sessionID,
			"total_cost_usd":  0,
			"duration_ms":     1,
			"duration_api_ms": 1,
			"num_turns":       1,
			"modelUsage":      map[string]any{},
		})
		return

	case strings.Contains(prompt, "__stderr_api_error__"):
		emitInit(sessionID)
		_, _ = os.Stderr.WriteString(`API Error: 502 {"error":{"message":"proxy failed","type":"proxy_error"}}`)
		time.Sleep(1500 * time.Millisecond)
		os.Exit(1)

	case strings.Contains(prompt, "__stdout_api_retry_auth__"):
		emitInit(sessionID)
		emit(map[string]any{
			"type":           "system",
			"subtype":        "api_retry",
			"attempt":        1,
			"max_retries":    10,
			"retry_delay_ms": 600,
			"error_status":   401,
			"error":          "authentication_failed",
			"session_id":     sessionID,
		})
		time.Sleep(1500 * time.Millisecond)
		os.Exit(1)

	case strings.Contains(prompt, "force-error"):
		emit(map[string]any{
			"type":       "system",
			"subtype":    "init",
			"session_id": sessionID,
			"model":      "claude-sonnet-4-20250514",
			"tools":      []string{"Read", "Write", "Bash"},
		})
		emit(map[string]any{
			"type":       "result",
			"subtype":    "error",
			"is_error":   true,
			"result":     "Something went wrong",
			"session_id": sessionID,
		})
		return

	case strings.Contains(prompt, "slow-run"):
		emitInit(sessionID)
		emitAssistantThinking("Preparing slow run...", sessionID)
		emitMessageStart("msg_slow", sessionID)
		emitTextDelta("msg_slow", "Still working", sessionID)
		time.Sleep(5 * time.Second)
		emitResult("slow run done", sessionID)
		return
	}

	emit(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": sessionID,
		"model":      "claude-sonnet-4-20250514",
		"tools":      []string{"Read", "Write", "Bash", "Edit", "Glob", "Grep"},
	})
	emitAssistantThinking("Let me analyze this...", sessionID)
	emitMessageStart("msg_main", sessionID)
	emitTextDelta("msg_main", "Here is ", sessionID)
	emitTextDelta("msg_main", "my response.", sessionID)
	emitToolUse("tool_1", "Read", map[string]any{"file_path": "/tmp/test.txt"}, sessionID)
	emitToolResult("tool_1", false, "file contents here", sessionID)
	emitAssistantText("Here is my response.", sessionID, "msg_main")
	emit(map[string]any{
		"type":            "result",
		"subtype":         "success",
		"is_error":        false,
		"result":          "Here is my response.",
		"session_id":      sessionID,
		"total_cost_usd":  0.003,
		"duration_ms":     1200,
		"duration_api_ms": 800,
		"num_turns":       1,
		"modelUsage": map[string]any{
			"claude-sonnet-4-20250514": map[string]any{
				"inputTokens":              500,
				"outputTokens":             100,
				"cacheReadInputTokens":     50,
				"cacheCreationInputTokens": 10,
				"contextWindow":            200000,
			},
		},
	})
}

func readInput(args []string, inputFormat string) (string, int) {
	if inputFormat != "stream-json" {
		return getPromptArg(args), 0
	}

	stdin, _ := readStdinText()
	promptParts := make([]string, 0)
	imageCount := 0

	for _, line := range strings.Split(stdin, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
			continue
		}

		message, _ := payload["message"].(map[string]any)
		content, _ := message["content"].([]any)
		for _, block := range content {
			entry, _ := block.(map[string]any)
			typ, _ := entry["type"].(string)
			switch typ {
			case "text":
				if text, ok := entry["text"].(string); ok {
					promptParts = append(promptParts, text)
				}
			case "image":
				imageCount++
			}
		}
	}

	return strings.Join(promptParts, "\n\n"), imageCount
}

func readStdinText() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	var builder strings.Builder
	for {
		chunk, err := reader.ReadString('\n')
		builder.WriteString(chunk)
		if err != nil {
			if err == io.EOF {
				return builder.String(), nil
			}
			return builder.String(), err
		}
	}
}

func getPromptArg(args []string) string {
	for index := 0; index < len(args); index++ {
		if args[index] != "-p" {
			continue
		}
		if index+1 >= len(args) {
			return ""
		}
		next := args[index+1]
		if strings.HasPrefix(next, "-") {
			return ""
		}
		return next
	}
	return ""
}

func getArgPointer(args []string, flag string) *string {
	for index := 0; index < len(args); index++ {
		if args[index] == flag && index+1 < len(args) {
			value := args[index+1]
			return &value
		}
	}
	return nil
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func emit(payload any) {
	bytes, _ := json.Marshal(payload)
	_, _ = fmt.Fprintln(os.Stdout, string(bytes))
}

func emitInit(sessionID string) {
	emit(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": sessionID,
		"model":      "claude-sonnet-4-20250514",
		"tools":      []string{"Read"},
	})
}

func emitAssistantThinking(text, sessionID string) {
	emit(map[string]any{
		"type":       "assistant",
		"session_id": sessionID,
		"message": map[string]any{
			"content": []map[string]any{{
				"type":     "thinking",
				"thinking": text,
			}},
		},
	})
}

func emitMessageStart(messageID, sessionID string) {
	emit(map[string]any{
		"type":       "stream_event",
		"session_id": sessionID,
		"event": map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":      messageID,
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
			},
		},
	})
	emit(map[string]any{
		"type":       "stream_event",
		"session_id": sessionID,
		"event": map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		},
	})
}

func emitTextDelta(messageID, text, sessionID string) {
	emit(map[string]any{
		"type":       "stream_event",
		"session_id": sessionID,
		"event": map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{
				"type": "text_delta",
				"text": text,
			},
			"message_id": messageID,
		},
	})
}

func emitToolUse(id, name string, input any, sessionID string) {
	emit(map[string]any{
		"type":       "assistant",
		"session_id": sessionID,
		"message": map[string]any{
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    id,
				"name":  name,
				"input": input,
			}},
		},
	})
}

func emitToolResult(toolUseID string, isError bool, content string, sessionID string) {
	emit(map[string]any{
		"type":       "user",
		"session_id": sessionID,
		"message": map[string]any{
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": toolUseID,
				"is_error":    isError,
				"content":     content,
			}},
		},
	})
}

func emitAssistantText(text, sessionID, messageID string) {
	emit(map[string]any{
		"type":       "assistant",
		"session_id": sessionID,
		"message": map[string]any{
			"id": messageID,
			"content": []map[string]any{{
				"type": "text",
				"text": text,
			}},
		},
	})
	emit(map[string]any{
		"type":       "stream_event",
		"session_id": sessionID,
		"event": map[string]any{
			"type":       "message_stop",
			"message_id": messageID,
		},
	})
}

func emitResult(text, sessionID string) {
	emit(map[string]any{
		"type":            "result",
		"subtype":         "success",
		"is_error":        false,
		"result":          text,
		"session_id":      sessionID,
		"total_cost_usd":  0.001,
		"duration_ms":     500,
		"duration_api_ms": 300,
		"num_turns":       1,
		"modelUsage": map[string]any{
			"claude-sonnet-4-20250514": map[string]any{
				"inputTokens":              100,
				"outputTokens":             50,
				"cacheReadInputTokens":     0,
				"cacheCreationInputTokens": 0,
				"contextWindow":            200000,
			},
		},
	})
}

func envPtr(key string) *string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	copy := value
	return &copy
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
