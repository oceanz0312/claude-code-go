package tests

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	claudecodego "claude-code-go"
)

type authResponse struct {
	AuthMode    string `json:"auth_mode"`
	Status      string `json:"status"`
	ShortAnswer string `json:"short_answer"`
}

type memoryResponse struct {
	Remembered string `json:"remembered"`
}

type imageResponse struct {
	DominantColor string   `json:"dominant_color"`
	Shape         string   `json:"shape"`
	Confidence    string   `json:"confidence"`
	ShapeCount    int      `json:"shape_count"`
	Shapes        []string `json:"shapes"`
	Snippet       string   `json:"snippet"`
}

var (
	imageDir        = filepath.Join("/Users/bytedance/Documents/ttls_repo/claude-code-go/testdata/images")
	redSquarePath   = filepath.Join(imageDir, "red-square.png")
	shapesDemoPath  = filepath.Join(imageDir, "shapes-demo.png")
	receiptDemoPath = filepath.Join(imageDir, "receipt-demo.png")
)

func requireAuthModes(t *testing.T) []AuthMode {
	t.Helper()
	modes, err := ListAvailableAuthModes()
	if err != nil {
		t.Skipf("skipping without E2E env vars: %v", err)
	}
	if len(modes) == 0 {
		t.Fatalf("No real E2E auth path is configured. Set E2E_API_KEY or E2E_AUTH_TOKEN + E2E_BASE_URL env vars before running go test ./...")
	}
	return modes
}

func TestE2ESetupRequiresE2EEnvVars(t *testing.T) {
	_, err := loadSecretsFromEnv()
	if err == nil {
		t.Skip("E2E env vars are configured; setup-failure case is not applicable")
	}
	if !strings.Contains(err.Error(), "No E2E_AUTH_TOKEN or E2E_API_KEY env var found") {
		t.Fatalf("unexpected setup error: %v", err)
	}
}

func TestE2EConfigLoadsLocalSecretsAndDefaultSessionSettings(t *testing.T) {
	config, err := LoadE2EConfig()
	if err != nil {
		t.Skipf("skipping without E2E env vars: %v", err)
	}
	if config.Model == "" {
		t.Fatal("expected model")
	}
	if !config.DefaultSessionOptions.Bare {
		t.Fatal("expected bare=true")
	}
	if config.DefaultSessionOptions.SettingSources != "" {
		t.Fatalf("unexpected setting sources: %q", config.DefaultSessionOptions.SettingSources)
	}
	if config.DefaultSessionOptions.IncludePartialMessages == nil || !*config.DefaultSessionOptions.IncludePartialMessages {
		t.Fatal("expected includePartialMessages=true")
	}
}

func TestE2EAuthPathsRunsTheAPIKeyPathThroughClaudeCodeOptionsWhenConfigured(t *testing.T) {
	modes := requireAuthModes(t)
	if !containsAuthMode(modes, AuthModeAPIKey) {
		t.Skip("api-key mode not configured")
	}
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName:      "auth-api-key",
		AuthMode:      AuthModeAPIKey,
		PoisonHostEnv: true,
		Input:         []claudecodego.InputItem{{Type: claudecodego.InputTypeText, Text: `Reply with strict JSON only: {"auth_mode":"api-key","status":"ok","short_answer":"<one short sentence>"}. Do not use markdown.`}},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	parsed, err := ParseJSONResponse[authResponse](result.Turn.FinalResponse)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if parsed.AuthMode != "api-key" || parsed.Status != "ok" || parsed.ShortAnswer == "" {
		t.Fatalf("unexpected auth response: %#v", parsed)
	}
	if result.Turn.SessionID == "" || result.Turn.Usage == nil {
		t.Fatalf("unexpected turn metadata: %#v", result.Turn)
	}
	spawn := GetSpawnEvent(result.RawEvents)
	if !strings.Contains(spawn.Command, "@anthropic-ai/claude-code/cli.js") {
		t.Fatalf("unexpected spawn command: %q", spawn.Command)
	}
}

func TestE2EAuthPathsRunsTheAuthTokenAndBaseURLPathThroughClaudeCodeOptionsWhenConfigured(t *testing.T) {
	modes := requireAuthModes(t)
	if !containsAuthMode(modes, AuthModeAuthToken) {
		t.Skip("auth-token mode not configured")
	}
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName:      "auth-token-base-url",
		AuthMode:      AuthModeAuthToken,
		PoisonHostEnv: true,
		Input:         []claudecodego.InputItem{{Type: claudecodego.InputTypeText, Text: `Reply with strict JSON only: {"auth_mode":"auth-token","status":"ok","short_answer":"<one short sentence>"}. Do not use markdown.`}},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	parsed, err := ParseJSONResponse[authResponse](result.Turn.FinalResponse)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if parsed.AuthMode != "auth-token" || parsed.Status != "ok" || parsed.ShortAnswer == "" {
		t.Fatalf("unexpected auth response: %#v", parsed)
	}
	if result.Turn.SessionID == "" {
		t.Fatalf("unexpected turn metadata: %#v", result.Turn)
	}
}

func TestE2ESessionLifecyclePreservesContextAcrossMultipleRunCallsOnTheSameSession(t *testing.T) {
	modes := requireAuthModes(t)
	authMode := modes[0]
	config, err := LoadE2EConfig()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	clientOptions, err := GetClientOptions(config.Secrets, authMode)
	if err != nil {
		t.Fatalf("client options failed: %v", err)
	}
	clientOptions.CLIPath = config.RealCLIPath
	client := claudecodego.NewClaudeCode(clientOptions)
	session := client.StartSession(claudecodego.SessionOptions{
		Bare:                       config.DefaultSessionOptions.Bare,
		SettingSources:             config.DefaultSessionOptions.SettingSources,
		Verbose:                    config.DefaultSessionOptions.Verbose,
		IncludePartialMessages:     config.DefaultSessionOptions.IncludePartialMessages,
		DangerouslySkipPermissions: true,
		Model:                      config.Model,
		RawEventLog:                false,
	})
	first, err := session.Run(context.Background(), `Remember this token exactly: E2E_SESSION_TOKEN_314159. Reply with JSON only: {"remembered":"E2E_SESSION_TOKEN_314159"}`)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	firstParsed, err := ParseJSONResponse[memoryResponse](first.FinalResponse)
	if err != nil || firstParsed.Remembered != "E2E_SESSION_TOKEN_314159" {
		t.Fatalf("unexpected first response: %#v err=%v", firstParsed, err)
	}
	second, err := session.Run(context.Background(), `What token did I ask you to remember in the previous turn? Reply with JSON only: {"remembered":"<token>"}`)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	secondParsed, err := ParseJSONResponse[memoryResponse](second.FinalResponse)
	if err != nil || secondParsed.Remembered != "E2E_SESSION_TOKEN_314159" {
		t.Fatalf("unexpected second response: %#v err=%v", secondParsed, err)
	}
	if second.SessionID == "" {
		t.Fatal("expected session id on second turn")
	}
	resumed := client.ResumeSession(second.SessionID, claudecodego.SessionOptions{
		Bare:                       config.DefaultSessionOptions.Bare,
		SettingSources:             config.DefaultSessionOptions.SettingSources,
		Verbose:                    config.DefaultSessionOptions.Verbose,
		IncludePartialMessages:     config.DefaultSessionOptions.IncludePartialMessages,
		DangerouslySkipPermissions: true,
		Model:                      config.Model,
		RawEventLog:                false,
	})
	resumedTurn, err := resumed.Run(context.Background(), `Repeat the remembered token as JSON only: {"remembered":"<token>"}`)
	if err != nil {
		t.Fatalf("resumed run failed: %v", err)
	}
	resumedParsed, err := ParseJSONResponse[memoryResponse](resumedTurn.FinalResponse)
	if err != nil || resumedParsed.Remembered != "E2E_SESSION_TOKEN_314159" {
		t.Fatalf("unexpected resumed response: %#v err=%v", resumedParsed, err)
	}
}

func TestE2EStreamingEmitsTextDeltasWhenIncludePartialMessagesIsTrue(t *testing.T) {
	modes := requireAuthModes(t)
	result, err := ExecuteStreamedCase(ExecuteStreamedCaseOptions{
		CaseName:       "streaming-partials-" + string(modes[0]),
		AuthMode:       modes[0],
		Input:          "Count from 1 to 8 in one sentence, but stream naturally.",
		SessionOptions: claudecodego.SessionOptions{IncludePartialMessages: boolPtr(true)},
	})
	if err != nil {
		t.Fatalf("execute streamed case failed: %v", err)
	}
	if !relayEventMapTypePresent(result.RelayEvents, "text_delta") || !relayEventMapTypePresent(result.RelayEvents, "turn_complete") {
		t.Fatalf("unexpected relay event types: %#v", result.RelayEvents)
	}
	if result.FinalResponse == "" {
		t.Fatal("expected final response")
	}
}

func TestE2EStreamingStillCompletesWhenIncludePartialMessagesIsFalse(t *testing.T) {
	modes := requireAuthModes(t)
	result, err := ExecuteStreamedCase(ExecuteStreamedCaseOptions{
		CaseName:       "streaming-no-partials-" + string(modes[0]),
		AuthMode:       modes[0],
		Input:          "Count from 1 to 8 in one sentence.",
		SessionOptions: claudecodego.SessionOptions{IncludePartialMessages: boolPtr(false)},
	})
	if err != nil {
		t.Fatalf("execute streamed case failed: %v", err)
	}
	if !relayEventMapTypePresent(result.RelayEvents, "turn_complete") {
		t.Fatalf("unexpected relay event types: %#v", result.RelayEvents)
	}
	if result.FinalResponse == "" {
		t.Fatal("expected final response")
	}
}

func TestE2EImageInputUnderstandsASimpleRedSquareImage(t *testing.T) {
	modes := requireAuthModes(t)
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName: "image-red-square-" + string(modes[0]),
		AuthMode: modes[0],
		Input: []claudecodego.InputItem{
			{Type: claudecodego.InputTypeText, Text: `Look at the image and reply with JSON only: {"dominant_color":"<color>","shape":"<shape>","confidence":"<high|medium|low>"}`},
			{Type: claudecodego.InputTypeLocalImage, Path: redSquarePath},
		},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	parsed, err := ParseJSONResponse[imageResponse](result.Turn.FinalResponse)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	spawn := GetSpawnEvent(result.RawEvents)
	if !containsString(GetFlagValues(spawn.Args, "--input-format"), "stream-json") || HasFlag(spawn.Args, "--image") {
		t.Fatalf("unexpected spawn args: %#v", spawn.Args)
	}
	if !strings.Contains(strings.ToLower(parsed.DominantColor), "red") || !strings.Contains(strings.ToLower(parsed.Shape), "square") {
		t.Fatalf("unexpected image response: %#v", parsed)
	}
}

func TestE2EImageInputCountsObviousShapesFromASyntheticImage(t *testing.T) {
	modes := requireAuthModes(t)
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName: "image-shapes-" + string(modes[0]),
		AuthMode: modes[0],
		Input: []claudecodego.InputItem{
			{Type: claudecodego.InputTypeText, Text: `Count the obvious geometric shapes in the image. Reply with JSON only: {"shape_count":<number>,"shapes":["..."]}`},
			{Type: claudecodego.InputTypeLocalImage, Path: shapesDemoPath},
		},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	parsed, err := ParseJSONResponse[imageResponse](result.Turn.FinalResponse)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if parsed.ShapeCount < 3 || len(parsed.Shapes) == 0 {
		t.Fatalf("unexpected image response: %#v", parsed)
	}
}

func TestE2EImageInputExtractsAVisibleSnippetFromASyntheticReceiptImage(t *testing.T) {
	modes := requireAuthModes(t)
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName: "image-receipt-" + string(modes[0]),
		AuthMode: modes[0],
		Input: []claudecodego.InputItem{
			{Type: claudecodego.InputTypeText, Text: `Extract one clearly visible short text snippet from the image. Reply with JSON only: {"snippet":"<text>"}`},
			{Type: claudecodego.InputTypeLocalImage, Path: receiptDemoPath},
		},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	parsed, err := ParseJSONResponse[imageResponse](result.Turn.FinalResponse)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if parsed.Snippet == "" {
		t.Fatalf("unexpected image response: %#v", parsed)
	}
}

func TestE2EOptionBehaviorAppliesSystemPromptAndAppendSystemPromptBehaviorToTheFinalOutput(t *testing.T) {
	modes := requireAuthModes(t)
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName: "system-prompt-" + string(modes[0]),
		AuthMode: modes[0],
		Input:    "Reply with JSON only.",
		SessionOptions: claudecodego.SessionOptions{
			SystemPrompt:       "Always respond with JSON containing system_tag=SYS_TAG_ALPHA.",
			AppendSystemPrompt: "Also include append_tag=APPEND_TAG_BETA in the JSON.",
		},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	parsed, err := ParseJSONResponse[map[string]string](result.Turn.FinalResponse)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.Contains(strings.ToLower(parsed["system_tag"]), "sys_tag_alpha") || !strings.Contains(strings.ToLower(parsed["append_tag"]), "append_tag_beta") {
		t.Fatalf("unexpected prompt response: %#v", parsed)
	}
}

func TestE2EOptionBehaviorReadsSystemPromptsFromFilesAndCanAccessCwdAndAdditionalDirectories(t *testing.T) {
	modes := requireAuthModes(t)
	workspace, err := CreateTempWorkspace("agent-sdk-e2e")
	if err != nil {
		t.Fatalf("create workspace failed: %v", err)
	}
	extraDir, err := CreateTempWorkspace("agent-sdk-e2e-extra")
	if err != nil {
		t.Fatalf("create extra dir failed: %v", err)
	}
	defer func() { _ = CleanupPath(workspace) }()
	defer func() { _ = CleanupPath(extraDir) }()
	cwdFile, err := WriteProbeFile(workspace, "cwd-probe.txt", "CWD_PROBE_TOKEN\n")
	if err != nil {
		t.Fatalf("write cwd probe failed: %v", err)
	}
	addDirFile, err := WriteProbeFile(extraDir, "additional-probe.txt", "ADDITIONAL_PROBE_TOKEN\n")
	if err != nil {
		t.Fatalf("write add dir probe failed: %v", err)
	}
	systemPromptFile, err := WritePromptFile(workspace, "system-prompt.txt", "Always include FILE_TAG_GAMMA in your final answer.")
	if err != nil {
		t.Fatalf("write system prompt failed: %v", err)
	}
	appendSystemPromptFile, err := WritePromptFile(workspace, "append-prompt.txt", "Also include APPEND_FILE_TAG_DELTA in your final answer.")
	if err != nil {
		t.Fatalf("write append prompt failed: %v", err)
	}
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName: "file-prompts-and-directories-" + string(modes[0]),
		AuthMode: modes[0],
		Input:    fmt.Sprintf("Read the file at %s and the file at %s. Reply with one JSON object containing cwd_token, additional_token, system_tag, and append_tag.", cwdFile, addDirFile),
		SessionOptions: claudecodego.SessionOptions{
			CWD:                    workspace,
			AdditionalDirectories:  []string{extraDir},
			SystemPromptFile:       systemPromptFile,
			AppendSystemPromptFile: appendSystemPromptFile,
		},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	normalized := strings.ToLower(result.Turn.FinalResponse)
	if !containsAllStrings(normalized, []string{"cwd_probe_token", "additional_probe_token", "file_tag_gamma", "append_file_tag_delta"}) {
		t.Fatalf("unexpected file prompt response: %s", result.Turn.FinalResponse)
	}
}

func TestE2EOptionBehaviorRecordsToolRestrictionsDebugFilesSettingsAndPluginDirectoryInTheRealSpawnArgs(t *testing.T) {
	modes := requireAuthModes(t)
	workspace, err := CreateTempWorkspace("agent-sdk-e2e-debug")
	if err != nil {
		t.Fatalf("create workspace failed: %v", err)
	}
	pluginDir, err := CreateEmptyPluginDir("agent-sdk-plugin")
	if err != nil {
		t.Fatalf("create plugin dir failed: %v", err)
	}
	defer func() { _ = CleanupPath(workspace) }()
	defer func() { _ = CleanupPath(pluginDir) }()
	debugFile := filepath.Join(workspace, "claude-debug.log")
	settings := `{"env":{"E2E_SETTINGS_TAG":"SETTINGS_OK"}}`
	maxTurns := 2
	maxBudget := 1.0
	result, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName: "spawn-args-" + string(modes[0]),
		AuthMode: modes[0],
		Input:    `Reply with JSON only: {"status":"ok"}`,
		SessionOptions: claudecodego.SessionOptions{
			AllowedTools:                       []string{"Read"},
			DisallowedTools:                    []string{"Bash"},
			Tools:                              "Read,Edit",
			Settings:                           settings,
			PluginDir:                          pluginDir,
			Debug:                              true,
			DebugFile:                          debugFile,
			MaxTurns:                           &maxTurns,
			MaxBudgetUSD:                       &maxBudget,
			Effort:                             claudecodego.EffortLow,
			FallbackModel:                      "opus",
			PermissionMode:                     claudecodego.PermissionModeDontAsk,
			NoSessionPersistence:               true,
			ExcludeDynamicSystemPromptSections: true,
			DisableSlashCommands:               true,
			IncludeHookEvents:                  true,
			Betas:                              "beta-test",
			Name:                               "e2e-spawn-args",
		},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	spawn := GetSpawnEvent(result.RawEvents)
	assertContainsString(t, GetFlagValues(spawn.Args, "--allowedTools"), "Read")
	assertContainsString(t, GetFlagValues(spawn.Args, "--disallowedTools"), "Bash")
	assertContainsString(t, GetFlagValues(spawn.Args, "--tools"), "Read,Edit")
	assertContainsString(t, GetFlagValues(spawn.Args, "--settings"), settings)
	assertContainsString(t, GetFlagValues(spawn.Args, "--plugin-dir"), pluginDir)
	if !HasFlag(spawn.Args, "--debug") {
		t.Fatal("expected --debug")
	}
	assertContainsString(t, GetFlagValues(spawn.Args, "--debug-file"), debugFile)
	if !HasFlag(spawn.Args, "--no-session-persistence") || !HasFlag(spawn.Args, "--exclude-dynamic-system-prompt-sections") || !HasFlag(spawn.Args, "--disable-slash-commands") || !HasFlag(spawn.Args, "--include-hook-events") {
		t.Fatalf("unexpected spawn args: %#v", spawn.Args)
	}
	assertContainsString(t, GetFlagValues(spawn.Args, "--betas"), "beta-test")
	assertContainsString(t, GetFlagValues(spawn.Args, "--name"), "e2e-spawn-args")
	debugText, err := ReadDebugFile(debugFile)
	if err != nil || debugText == "" {
		t.Fatalf("unexpected debug file: %q err=%v", debugText, err)
	}
}

func TestE2ESessionModesAndAgentsUsesConfiguredAgentIdentityAndNoSessionPersistenceBlocksImplicitReuse(t *testing.T) {
	modes := requireAuthModes(t)
	first, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName: "agent-role-" + string(modes[0]),
		AuthMode: modes[0],
		Input:    `Reply with JSON only: {"role":"<role>","status":"ok"}`,
		SessionOptions: claudecodego.SessionOptions{
			Agents: map[string]claudecodego.AgentDefinition{
				"reviewer": {
					Description: "Always identify yourself as reviewer-agent.",
					Prompt:      "When answering, include reviewer-agent in a JSON field named role.",
				},
			},
			Agent:                "reviewer",
			NoSessionPersistence: true,
		},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(first.Turn.FinalResponse), "reviewer-agent") {
		t.Fatalf("unexpected agent response: %s", first.Turn.FinalResponse)
	}
	second, err := ExecuteBufferedCase(ExecuteBufferedCaseOptions{
		CaseName:       "no-session-persistence-" + string(modes[0]),
		AuthMode:       modes[0],
		Input:          `What token did I ask you to remember previously? Reply with JSON only: {"remembered":"<token or none>"}`,
		SessionOptions: claudecodego.SessionOptions{NoSessionPersistence: true},
	})
	if err != nil {
		t.Fatalf("execute buffered case failed: %v", err)
	}
	if strings.Contains(strings.ToLower(second.Turn.FinalResponse), "e2e_session_token_314159") {
		t.Fatalf("unexpected memory reuse: %s", second.Turn.FinalResponse)
	}
}

func containsAuthMode(modes []AuthMode, want AuthMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}

func relayEventMapTypePresent(events []map[string]any, want string) bool {
	for _, event := range events {
		if event["type"] == want {
			return true
		}
	}
	return false
}

func assertContainsString(t *testing.T, values []string, want string) {
	t.Helper()
	if !containsString(values, want) {
		t.Fatalf("expected %q in %#v", want, values)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsAllStrings(text string, wants []string) bool {
	for _, want := range wants {
		if !strings.Contains(text, want) {
			return false
		}
	}
	return true
}
