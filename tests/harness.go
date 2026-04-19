package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	claudecodego "claude-code-go"
)

type BufferedCaseResult struct {
	ArtifactDir      string
	AuthMode         AuthMode
	CaseName         string
	Turn             *claudecodego.Turn
	RelayEvents      []map[string]any
	RawEvents        []TimestampedRawEvent
	RawEventLogFiles []string
}

type StreamedCaseResult struct {
	ArtifactDir      string
	AuthMode         AuthMode
	CaseName         string
	RelayEvents      []map[string]any
	RawEvents        []TimestampedRawEvent
	FinalResponse    string
	RawEventLogFiles []string
}

type ExecuteBufferedCaseOptions struct {
	CaseName       string
	AuthMode       AuthMode
	Input          claudecodego.Input
	SessionOptions claudecodego.SessionOptions
	PoisonHostEnv  bool
}

func ExecuteBufferedCase(options ExecuteBufferedCaseOptions) (*BufferedCaseResult, error) {
	var result *BufferedCaseResult
	err := WithOptionalPoisonedEnv(options.PoisonHostEnv, func() error {
		config, err := LoadE2EConfig()
		if err != nil {
			return err
		}
		artifactDir, err := CreateArtifactDir(config.ArtifactRoot, options.CaseName)
		if err != nil {
			return err
		}
		sessionOptions := buildSessionOptions(config.DefaultSessionOptions, config.Model, artifactDir, options.SessionOptions)
		clientOptions, err := GetClientOptions(config.Secrets, options.AuthMode)
		if err != nil {
			return err
		}
		clientOptions.CLIPath = config.RealCLIPath
		client := claudecodego.NewClaudeCode(clientOptions)
		session := client.StartSession(sessionOptions)
		rawEvents := make([]TimestampedRawEvent, 0)
		turn, err := session.Run(context.Background(), options.Input, claudecodego.TurnOptions{OnRawEvent: CreateRawEventCollector(&rawEvents)})
		if err != nil {
			return err
		}
		rawEventLogFiles, err := collectRawEventLogFiles(artifactDir)
		if err != nil {
			return err
		}
		relayEvents := relayEventsToMaps(turn.Events)
		if err := WriteCaseArtifacts(CaseArtifactPayload{
			CaseName:              options.CaseName,
			AuthMode:              string(options.AuthMode),
			ArtifactDir:           artifactDir,
			InputSummary:          SummarizeInput(options.Input),
			SessionOptionsSummary: SummarizeSessionOptions(sessionOptions),
			RawEvents:             rawEvents,
			RelayEvents:           relayEvents,
			FinalResponse:         turn.FinalResponse,
			Metadata: map[string]any{
				"sessionId":        turn.SessionID,
				"usage":            turn.Usage,
				"rawEventLogFiles": rawEventLogFiles,
			},
		}); err != nil {
			return err
		}
		PrintCaseSummary(options.CaseName, string(options.AuthMode), artifactDir, rawEvents, relayEvents, turn.FinalResponse, SummarizeInput(options.Input), SummarizeSessionOptions(sessionOptions))
		result = &BufferedCaseResult{
			ArtifactDir:      artifactDir,
			AuthMode:         options.AuthMode,
			CaseName:         options.CaseName,
			Turn:             turn,
			RelayEvents:      relayEvents,
			RawEvents:        rawEvents,
			RawEventLogFiles: rawEventLogFiles,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type ExecuteStreamedCaseOptions struct {
	CaseName       string
	AuthMode       AuthMode
	Input          claudecodego.Input
	SessionOptions claudecodego.SessionOptions
}

func ExecuteStreamedCase(options ExecuteStreamedCaseOptions) (*StreamedCaseResult, error) {
	config, err := LoadE2EConfig()
	if err != nil {
		return nil, err
	}
	artifactDir, err := CreateArtifactDir(config.ArtifactRoot, options.CaseName)
	if err != nil {
		return nil, err
	}
	sessionOptions := buildSessionOptions(config.DefaultSessionOptions, config.Model, artifactDir, options.SessionOptions)
	clientOptions, err := GetClientOptions(config.Secrets, options.AuthMode)
	if err != nil {
		return nil, err
	}
	clientOptions.CLIPath = config.RealCLIPath
	client := claudecodego.NewClaudeCode(clientOptions)
	session := client.StartSession(sessionOptions)
	rawEvents := make([]TimestampedRawEvent, 0)
	relayEvents := make([]map[string]any, 0)
	finalResponse := ""
	stream, err := session.RunStreamed(context.Background(), options.Input, claudecodego.TurnOptions{OnRawEvent: CreateRawEventCollector(&rawEvents)})
	if err != nil {
		return nil, err
	}
	for {
		event, ok, err := stream.Next(context.Background())
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		relayEvents = append(relayEvents, relayEventToMap(event))
		if text, ok := event.(claudecodego.TextDeltaEvent); ok {
			finalResponse += text.Content
		}
	}
	if err := stream.Wait(); err != nil {
		return nil, err
	}
	rawEventLogFiles, err := collectRawEventLogFiles(artifactDir)
	if err != nil {
		return nil, err
	}
	if err := WriteCaseArtifacts(CaseArtifactPayload{
		CaseName:              options.CaseName,
		AuthMode:              string(options.AuthMode),
		ArtifactDir:           artifactDir,
		InputSummary:          SummarizeInput(options.Input),
		SessionOptionsSummary: SummarizeSessionOptions(sessionOptions),
		RawEvents:             rawEvents,
		RelayEvents:           relayEvents,
		FinalResponse:         finalResponse,
		Metadata:              map[string]any{"rawEventLogFiles": rawEventLogFiles},
	}); err != nil {
		return nil, err
	}
	PrintCaseSummary(options.CaseName, string(options.AuthMode), artifactDir, rawEvents, relayEvents, finalResponse, SummarizeInput(options.Input), SummarizeSessionOptions(sessionOptions))
	return &StreamedCaseResult{
		ArtifactDir:      artifactDir,
		AuthMode:         options.AuthMode,
		CaseName:         options.CaseName,
		RelayEvents:      relayEvents,
		RawEvents:        rawEvents,
		FinalResponse:    finalResponse,
		RawEventLogFiles: rawEventLogFiles,
	}, nil
}

func CreateTempWorkspace(prefix string) (string, error) {
	return os.MkdirTemp("", prefix+"-")
}

func WriteProbeFile(dir, fileName, content string) (string, error) {
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func WritePromptFile(dir, fileName, content string) (string, error) {
	return WriteProbeFile(dir, fileName, content)
}

func CreateEmptyPluginDir(prefix string) (string, error) {
	return CreateTempWorkspace(prefix)
}

func CleanupPath(targetPath string) error {
	return os.RemoveAll(targetPath)
}

func GetSpawnEvent(rawEvents []TimestampedRawEvent) claudecodego.RawClaudeEvent {
	for _, entry := range rawEvents {
		if entry.Event.Type == claudecodego.RawEventSpawn {
			return entry.Event
		}
	}
	panic("missing spawn event in raw event stream")
}

func GetFlagValues(args []string, flag string) []string {
	values := make([]string, 0)
	for index := 0; index < len(args); index++ {
		if args[index] == flag && index+1 < len(args) {
			values = append(values, args[index+1])
		}
	}
	return values
}

func HasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func ReadDebugFile(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func ParseJSONResponse[T any](text string) (T, error) {
	var zero T
	normalized := stripCodeFence(text)
	var value T
	if err := json.Unmarshal([]byte(normalized), &value); err == nil {
		return value, nil
	}
	extracted := extractFirstJSONValue(normalized)
	if extracted == "" {
		return zero, fmt.Errorf("JSON parse error: unable to parse JSON string")
	}
	if err := json.Unmarshal([]byte(extracted), &value); err != nil {
		return zero, err
	}
	return value, nil
}

func WithOptionalPoisonedEnv(enabled bool, fn func() error) error {
	if !enabled {
		return fn()
	}
	originalAPIKey, hasAPIKey := os.LookupEnv("ANTHROPIC_API_KEY")
	originalAuthToken, hasAuthToken := os.LookupEnv("ANTHROPIC_AUTH_TOKEN")
	originalBaseURL, hasBaseURL := os.LookupEnv("ANTHROPIC_BASE_URL")
	_ = os.Setenv("ANTHROPIC_API_KEY", "host-api-key-should-not-leak")
	_ = os.Setenv("ANTHROPIC_AUTH_TOKEN", "host-auth-token-should-not-leak")
	_ = os.Setenv("ANTHROPIC_BASE_URL", "https://host-base-url-should-not-leak.invalid")
	defer restoreEnv("ANTHROPIC_API_KEY", originalAPIKey, hasAPIKey)
	defer restoreEnv("ANTHROPIC_AUTH_TOKEN", originalAuthToken, hasAuthToken)
	defer restoreEnv("ANTHROPIC_BASE_URL", originalBaseURL, hasBaseURL)
	return fn()
}

func CreateRawEventCollector(rawEvents *[]TimestampedRawEvent) func(claudecodego.RawClaudeEvent) {
	return func(event claudecodego.RawClaudeEvent) {
		*rawEvents = append(*rawEvents, TimestampedRawEvent{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Event:     event,
		})
	}
}

func SummarizeInput(input claudecodego.Input) map[string]any {
	switch typed := input.(type) {
	case string:
		return map[string]any{"prompt": typed}
	case []claudecodego.InputItem:
		items := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			entry := map[string]any{"type": string(item.Type)}
			if item.Type == claudecodego.InputTypeText {
				entry["text"] = item.Text
			} else {
				entry["path"] = item.Path
			}
			items = append(items, entry)
		}
		return map[string]any{"items": items}
	default:
		return map[string]any{}
	}
}

func SummarizeSessionOptions(options claudecodego.SessionOptions) map[string]any {
	return map[string]any{
		"model":                              options.Model,
		"cwd":                                options.CWD,
		"additionalDirectories":              options.AdditionalDirectories,
		"maxTurns":                           options.MaxTurns,
		"maxBudgetUsd":                       options.MaxBudgetUSD,
		"permissionMode":                     options.PermissionMode,
		"dangerouslySkipPermissions":         options.DangerouslySkipPermissions,
		"allowedTools":                       options.AllowedTools,
		"disallowedTools":                    options.DisallowedTools,
		"tools":                              options.Tools,
		"mcpConfig":                          options.MCPConfig,
		"strictMcpConfig":                    options.StrictMCPConfig,
		"effort":                             options.Effort,
		"fallbackModel":                      options.FallbackModel,
		"bare":                               options.Bare,
		"noSessionPersistence":               options.NoSessionPersistence,
		"chrome":                             options.Chrome,
		"agent":                              options.Agent,
		"name":                               options.Name,
		"settings":                           options.Settings,
		"settingSources":                     options.SettingSources,
		"verbose":                            options.Verbose,
		"includePartialMessages":             options.IncludePartialMessages,
		"includeHookEvents":                  options.IncludeHookEvents,
		"betas":                              options.Betas,
		"worktree":                           options.Worktree,
		"disableSlashCommands":               options.DisableSlashCommands,
		"pluginDir":                          options.PluginDir,
		"excludeDynamicSystemPromptSections": options.ExcludeDynamicSystemPromptSections,
		"debug":                              options.Debug,
		"debugFile":                          options.DebugFile,
	}
}

func buildSessionOptions(defaults claudecodego.SessionOptions, model, artifactDir string, overrides claudecodego.SessionOptions) claudecodego.SessionOptions {
	result := defaults
	result.Model = model
	result.RawEventLog = artifactDir
	mergeSessionOverrides(&result, overrides)
	return result
}

func mergeSessionOverrides(dst *claudecodego.SessionOptions, src claudecodego.SessionOptions) {
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.CWD != "" {
		dst.CWD = src.CWD
	}
	if src.AdditionalDirectories != nil {
		dst.AdditionalDirectories = src.AdditionalDirectories
	}
	if src.MaxTurns != nil {
		dst.MaxTurns = src.MaxTurns
	}
	if src.MaxBudgetUSD != nil {
		dst.MaxBudgetUSD = src.MaxBudgetUSD
	}
	if src.SystemPrompt != "" {
		dst.SystemPrompt = src.SystemPrompt
	}
	if src.SystemPromptFile != "" {
		dst.SystemPromptFile = src.SystemPromptFile
	}
	if src.AppendSystemPrompt != "" {
		dst.AppendSystemPrompt = src.AppendSystemPrompt
	}
	if src.AppendSystemPromptFile != "" {
		dst.AppendSystemPromptFile = src.AppendSystemPromptFile
	}
	if src.PermissionMode != "" {
		dst.PermissionMode = src.PermissionMode
	}
	if src.DangerouslySkipPermissions {
		dst.DangerouslySkipPermissions = true
	}
	if src.AllowedTools != nil {
		dst.AllowedTools = src.AllowedTools
	}
	if src.DisallowedTools != nil {
		dst.DisallowedTools = src.DisallowedTools
	}
	if src.Tools != "" {
		dst.Tools = src.Tools
	}
	if src.PermissionPromptTool != "" {
		dst.PermissionPromptTool = src.PermissionPromptTool
	}
	if src.MCPConfig != nil {
		dst.MCPConfig = src.MCPConfig
	}
	if src.StrictMCPConfig {
		dst.StrictMCPConfig = true
	}
	if src.Effort != "" {
		dst.Effort = src.Effort
	}
	if src.FallbackModel != "" {
		dst.FallbackModel = src.FallbackModel
	}
	if src.Bare {
		dst.Bare = true
	}
	if src.NoSessionPersistence {
		dst.NoSessionPersistence = true
	}
	if src.Chrome != nil {
		dst.Chrome = src.Chrome
	}
	if src.Agents != nil {
		dst.Agents = src.Agents
	}
	if src.Agent != "" {
		dst.Agent = src.Agent
	}
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.Settings != "" {
		dst.Settings = src.Settings
	}
	if src.SettingSources != "" || dst.SettingSources == "" {
		dst.SettingSources = src.SettingSources
	}
	if src.Verbose != nil {
		dst.Verbose = src.Verbose
	}
	if src.IncludePartialMessages != nil {
		dst.IncludePartialMessages = src.IncludePartialMessages
	}
	if src.IncludeHookEvents {
		dst.IncludeHookEvents = true
	}
	if src.Betas != "" {
		dst.Betas = src.Betas
	}
	if src.Worktree != "" {
		dst.Worktree = src.Worktree
	}
	if src.DisableSlashCommands {
		dst.DisableSlashCommands = true
	}
	if src.PluginDir != nil {
		dst.PluginDir = src.PluginDir
	}
	if src.ExcludeDynamicSystemPromptSections {
		dst.ExcludeDynamicSystemPromptSections = true
	}
	if src.Debug != nil {
		dst.Debug = src.Debug
	}
	if src.DebugFile != "" {
		dst.DebugFile = src.DebugFile
	}
	if src.JSONSchema != nil {
		dst.JSONSchema = src.JSONSchema
	}
	if src.SessionID != "" {
		dst.SessionID = src.SessionID
	}
	if src.ForkSession {
		dst.ForkSession = true
	}
	if src.RawEventLog != nil {
		dst.RawEventLog = src.RawEventLog
	}
}

func relayEventsToMaps(events []claudecodego.RelayEvent) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for _, event := range events {
		result = append(result, relayEventToMap(event))
	}
	return result
}

func relayEventToMap(event claudecodego.RelayEvent) map[string]any {
	bytes, _ := json.Marshal(event)
	var decoded map[string]any
	_ = json.Unmarshal(bytes, &decoded)
	return decoded
}

func restoreEnv(key, value string, existed bool) {
	if !existed {
		_ = os.Unsetenv(key)
		return
	}
	_ = os.Setenv(key, value)
}

func stripCodeFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```")
	if newline := strings.Index(trimmed, "\n"); newline >= 0 {
		trimmed = trimmed[newline+1:]
	}
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func extractFirstJSONValue(text string) string {
	trimmed := strings.TrimSpace(text)
	start := strings.IndexAny(trimmed, "[{")
	if start < 0 {
		return ""
	}
	opening := trimmed[start]
	closing := byte('}')
	if opening == '[' {
		closing = ']'
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(trimmed); index++ {
		char := trimmed[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		if char == '"' {
			inString = true
			continue
		}
		if char == opening {
			depth++
			continue
		}
		if char == closing {
			depth--
			if depth == 0 {
				return trimmed[start : index+1]
			}
		}
	}
	return ""
}
