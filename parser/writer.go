package parser

import "encoding/json"

func CreateUserMessage(content string) string {
	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}

func CreateApproveMessage(toolUseID string) string {
	payload := map[string]any{
		"type":        "approve",
		"tool_use_id": toolUseID,
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}

func CreateDenyMessage(toolUseID string) string {
	payload := map[string]any{
		"type":        "deny",
		"tool_use_id": toolUseID,
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}

func CreateToolResultMessage(toolUseID, content string) string {
	payload := map[string]any{
		"type":        "tool_result",
		"tool_use_id": toolUseID,
		"content":     content,
	}
	bytes, _ := json.Marshal(payload)
	return string(bytes) + "\n"
}
