package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	claudecodego "github.com/oceanz0312/claude-code-go"
)

const initialPrompt = "使用 ai-friendly-evaluate 技能评估当前仓库的 ai-friendly 分数"

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fatalf("failed to get current working directory: %v", err)
	}

	clientOptions := claudecodego.ClaudeCodeOptions{
		APIKey:    firstNonEmpty(os.Getenv("E2E_API_KEY"), os.Getenv("ANTHROPIC_API_KEY")),
		AuthToken: firstNonEmpty(os.Getenv("E2E_AUTH_TOKEN"), os.Getenv("ANTHROPIC_AUTH_TOKEN")),
		BaseURL:   firstNonEmpty(os.Getenv("E2E_BASE_URL"), os.Getenv("ANTHROPIC_BASE_URL")),
	}
	if clientOptions.APIKey == "" && clientOptions.AuthToken == "" {
		fatalf("missing credentials: set E2E_API_KEY or E2E_AUTH_TOKEN (+ E2E_BASE_URL), or the ANTHROPIC_* equivalents")
	}

	pluginDir, err := resolvePluginDir(cwd)
	if err != nil {
		fatalf("failed to locate demo plugin directory: %v", err)
	}

	client := claudecodego.NewClaudeCode(clientOptions)
	session := client.StartSession(claudecodego.SessionOptions{
		CWD:                        cwd,
		DangerouslySkipPermissions: true,
		PluginDir:                  pluginDir,
		Model:                      firstNonEmpty(os.Getenv("E2E_MODEL"), "sonnet"),
		IncludePartialMessages:     boolPtr(true),
		Verbose:                    boolPtr(true),
	})

	fmt.Println("多轮 skill demo（输入 exit 退出）")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	firstTurn := true
	for {
		userInput := initialPrompt
		if !firstTurn {
			fmt.Print("\n你: ")
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				fatalf("failed to read user input: %v", readErr)
			}
			userInput = strings.TrimSpace(line)
		}

		if userInput == "exit" {
			break
		}

		if firstTurn {
			fmt.Printf("你: %s\n", userInput)
			firstTurn = false
		}

		if err := runTurn(session, userInput); err != nil {
			fatalf("turn failed: %v", err)
		}
	}

	fmt.Println()
	fmt.Println("--- 完成 ---")
	fmt.Printf("Session ID: %s\n", session.ID())
}

func runTurn(session *claudecodego.Session, userInput string) error {
	fmt.Println()
	fmt.Println("Claude:")

	stream, err := session.RunStreamed(context.Background(), userInput)
	if err != nil {
		return err
	}

	streamingMode := ""
	flushStreamingLine := func() {
		if streamingMode != "" {
			fmt.Println()
			streamingMode = ""
		}
	}

	for {
		event, ok, nextErr := stream.Next(context.Background())
		if nextErr != nil {
			return nextErr
		}
		if !ok {
			break
		}

		switch typed := event.(type) {
		case claudecodego.TextDeltaEvent:
			if streamingMode != "text_delta" {
				flushStreamingLine()
				fmt.Print("[text_delta] ")
				streamingMode = "text_delta"
			}
			fmt.Print(typed.Content)
		case claudecodego.ThinkingDeltaEvent:
			if streamingMode != "thinking_delta" {
				flushStreamingLine()
				fmt.Print("[thinking_delta] ")
				streamingMode = "thinking_delta"
			}
			fmt.Print(typed.Content)
		case claudecodego.ToolUseEvent:
			flushStreamingLine()
			printJSONLine("tool_use", map[string]any{
				"toolUseId": typed.ToolUseID,
				"toolName":  typed.ToolName,
				"input":     typed.Input,
			})
		case claudecodego.ToolResultEvent:
			flushStreamingLine()
			printJSONLine("tool_result", map[string]any{
				"toolUseId": typed.ToolUseID,
				"output":    typed.Output,
				"isError":   typed.IsError,
			})
		case claudecodego.SessionMetaEvent:
			flushStreamingLine()
			printJSONLine("session_meta", map[string]any{"model": typed.Model})
		case claudecodego.TurnCompleteEvent:
			flushStreamingLine()
			printJSONLine("turn_complete", map[string]any{
				"sessionId":     typed.SessionID,
				"costUsd":       typed.CostUSD,
				"inputTokens":   typed.InputTokens,
				"outputTokens":  typed.OutputTokens,
				"contextWindow": typed.ContextWindow,
			})
		case claudecodego.ErrorEvent:
			flushStreamingLine()
			printJSONLine("error", map[string]any{
				"message":   typed.Message,
				"sessionId": typed.SessionID,
			})
		}
	}

	flushStreamingLine()
	if err := stream.Wait(); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func printJSONLine(label string, value any) {
	bytes, err := json.Marshal(value)
	if err != nil {
		fatalf("failed to marshal %s payload: %v", label, err)
	}
	fmt.Printf("[%s] %s\n", label, string(bytes))
}

func resolvePluginDir(cwd string) (string, error) {
	candidates := []string{
		filepath.Join(cwd, "demo", "claude-code-plugin"),
		filepath.Join(cwd, "claude-code-plugin"),
		filepath.Join("/Users/bytedance/Documents/ttls_repo/agent-sdk", "demo", "claude-code-plugin"),
	}
	for _, candidate := range candidates {
		if isDir(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no demo plugin directory found in %v", candidates)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func boolPtr(value bool) *bool {
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
