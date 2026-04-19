package claudecodego

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	inspectPrompt   = "__inspect_exec_options__"
	rawEventsPrompt = "__inspect_raw_events__"
	parentEnvKey    = "INSPECT_INHERITED_ENV"
)

type execInspection struct {
	Args  []string `json:"args"`
	CWD   string   `json:"cwd"`
	Flags struct {
		ResumeSessionID *string `json:"resumeSessionId"`
		ContinueSession bool    `json:"continueSession"`
	} `json:"flags"`
	Input struct {
		Prompt      string  `json:"prompt"`
		ImageCount  int     `json:"imageCount"`
		InputFormat *string `json:"inputFormat"`
	} `json:"input"`
	Env struct {
		AnthropicAPIKey    *string `json:"ANTHROPIC_API_KEY"`
		AnthropicAuthToken *string `json:"ANTHROPIC_AUTH_TOKEN"`
		AnthropicBaseURL   *string `json:"ANTHROPIC_BASE_URL"`
		InspectCustomEnv   *string `json:"INSPECT_CUSTOM_ENV"`
		InspectInherited   *string `json:"INSPECT_INHERITED_ENV"`
	} `json:"env"`
}

func inspectExec(t *testing.T, options inspectExecOptions) execInspection {
	t.Helper()

	if options.exec == nil {
		options.exec = NewClaudeCodeExec(fakeClaudePath, nil)
	}

	lines := make([]string, 0)
	err := options.exec.Run(context.Background(), ExecRequest{
		Input:           inspectPrompt,
		CLIPath:         fakeClaudePath,
		SessionOptions:  options.sessionOptions,
		ResumeSessionID: options.resumeSessionID,
		ContinueSession: options.continueSession,
		InputItems:      options.inputItems,
		Images:          options.images,
		Env:             options.env,
		OnLine: func(line string) {
			lines = append(lines, line)
		},
	})
	if err != nil {
		t.Fatalf("exec run failed: %v", err)
	}

	resultLine := findResultLine(t, lines)
	var payload struct {
		Inspection execInspection `json:"inspection"`
	}
	if err := json.Unmarshal([]byte(resultLine), &payload); err != nil {
		t.Fatalf("failed to decode inspection payload: %v", err)
	}
	return payload.Inspection
}

type inspectExecOptions struct {
	exec            *ClaudeCodeExec
	sessionOptions  SessionOptions
	resumeSessionID string
	continueSession bool
	images          []string
	inputItems      []InputItem
	env             map[string]string
}

func TestExecYieldsNDJSONLinesFromFakeCLI(t *testing.T) {
	exec := NewClaudeCodeExec(fakeClaudePath, nil)
	lines := make([]string, 0)
	err := exec.Run(context.Background(), ExecRequest{
		Input:   "hello",
		CLIPath: fakeClaudePath,
		SessionOptions: SessionOptions{
			DangerouslySkipPermissions: true,
		},
		OnLine: func(line string) {
			lines = append(lines, line)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}
	for _, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("expected valid json line, got error: %v", err)
		}
		if _, ok := decoded["type"]; !ok {
			t.Fatalf("expected type field in %#v", decoded)
		}
	}
}

func TestExecEnablesDefaultStreamingFlagsUnlessExplicitlyDisabled(t *testing.T) {
	inspection := inspectExec(t, inspectExecOptions{})
	if !reflect.DeepEqual(getFlagValues(inspection.Args, "-p"), []string{inspectPrompt}) {
		t.Fatalf("unexpected -p values: %#v", getFlagValues(inspection.Args, "-p"))
	}
	if values := getFlagValues(inspection.Args, "--input-format"); len(values) != 0 {
		t.Fatalf("expected no input format flag, got %#v", values)
	}
	if !reflect.DeepEqual(getFlagValues(inspection.Args, "--output-format"), []string{"stream-json"}) {
		t.Fatalf("unexpected output format flags: %#v", getFlagValues(inspection.Args, "--output-format"))
	}
	if !hasFlag(inspection.Args, "--verbose") {
		t.Fatal("expected verbose flag")
	}
	if !hasFlag(inspection.Args, "--include-partial-messages") {
		t.Fatal("expected include partial messages flag")
	}
}

func TestExecOmitsDefaultOnFlagsWhenVerboseAndPartialMessagesAreDisabled(t *testing.T) {
	verbose := false
	partials := false
	inspection := inspectExec(t, inspectExecOptions{sessionOptions: SessionOptions{Verbose: &verbose, IncludePartialMessages: &partials}})
	if hasFlag(inspection.Args, "--verbose") {
		t.Fatal("did not expect verbose flag")
	}
	if hasFlag(inspection.Args, "--include-partial-messages") {
		t.Fatal("did not expect include partial messages flag")
	}
}

func TestExecAppliesPrecedenceForContinuePermissionModeAndSystemPromptSource(t *testing.T) {
	inspection := inspectExec(t, inspectExecOptions{
		continueSession: true,
		resumeSessionID: "resume-me",
		sessionOptions: SessionOptions{
			SystemPrompt:               "inline prompt",
			SystemPromptFile:           "/tmp/system-prompt.txt",
			PermissionMode:             PermissionModePlan,
			DangerouslySkipPermissions: true,
		},
	})
	if !hasFlag(inspection.Args, "--continue") {
		t.Fatal("expected continue flag")
	}
	if values := getFlagValues(inspection.Args, "--resume"); len(values) != 0 {
		t.Fatalf("did not expect resume values, got %#v", values)
	}
	if !reflect.DeepEqual(getFlagValues(inspection.Args, "--system-prompt"), []string{"inline prompt"}) {
		t.Fatalf("unexpected system prompt values: %#v", getFlagValues(inspection.Args, "--system-prompt"))
	}
	if values := getFlagValues(inspection.Args, "--system-prompt-file"); len(values) != 0 {
		t.Fatalf("did not expect system prompt file values, got %#v", values)
	}
	if !hasFlag(inspection.Args, "--dangerously-skip-permissions") {
		t.Fatal("expected dangerously skip permissions flag")
	}
	if values := getFlagValues(inspection.Args, "--permission-mode"); len(values) != 0 {
		t.Fatalf("did not expect permission mode values, got %#v", values)
	}
}

func TestExecExpandsRepeatedFlagsForListStyleOptionsAndUsesStreamJSONStdinForImages(t *testing.T) {
	inspection := inspectExec(t, inspectExecOptions{
		inputItems: []InputItem{
			{Type: InputTypeText, Text: inspectPrompt},
			{Type: InputTypeLocalImage, Path: redSquareImage},
			{Type: InputTypeLocalImage, Path: shapesDemoImage},
		},
		sessionOptions: SessionOptions{
			AdditionalDirectories: []string{"/repo/packages/a", "/repo/packages/b"},
			AllowedTools:          []string{"Read", "Edit"},
			DisallowedTools:       []string{"Bash", "Write"},
			MCPConfig:             []string{"mcp-a.json", "mcp-b.json"},
			PluginDir:             []string{"plugins/a", "plugins/b"},
		},
	})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--add-dir"), []string{"/repo/packages/a", "/repo/packages/b"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--allowedTools"), []string{"Read", "Edit"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--disallowedTools"), []string{"Bash", "Write"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--mcp-config"), []string{"mcp-a.json", "mcp-b.json"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--plugin-dir"), []string{"plugins/a", "plugins/b"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--input-format"), []string{"stream-json"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "-p"), []string{"--input-format"})
	if inspection.Input.Prompt != inspectPrompt {
		t.Fatalf("unexpected prompt: %q", inspection.Input.Prompt)
	}
	if inspection.Input.ImageCount != 2 {
		t.Fatalf("unexpected image count: %d", inspection.Input.ImageCount)
	}
	if inspection.Input.InputFormat == nil || *inspection.Input.InputFormat != "stream-json" {
		t.Fatalf("unexpected input format: %#v", inspection.Input.InputFormat)
	}
}

func TestExecPassesScalarFlagsThroughAndSerializesObjectAgents(t *testing.T) {
	maxTurns := 7
	maxBudget := 1.5
	chrome := false
	inspection := inspectExec(t, inspectExecOptions{
		sessionOptions: SessionOptions{
			Model:                  "sonnet",
			CWD:                    filepath.Dir(fakeClaudePath),
			MaxTurns:               &maxTurns,
			MaxBudgetUSD:           &maxBudget,
			AppendSystemPrompt:     "append this",
			AppendSystemPromptFile: "/tmp/append.txt",
			Tools:                  "Read,Write",
			PermissionPromptTool:   "mcp__permissions__prompt",
			MCPConfig:              "mcp-single.json",
			StrictMCPConfig:        true,
			Effort:                 EffortMax,
			FallbackModel:          "opus",
			Bare:                   true,
			NoSessionPersistence:   true,
			Chrome:                 &chrome,
			Agents: map[string]AgentDefinition{
				"reviewer": {
					Description: "Review code changes",
					Tools:       []string{"Read"},
					MaxTurns:    intPtr(2),
				},
			},
			Agent:                              "reviewer",
			Name:                               "review session",
			Settings:                           `{"source":"test"}`,
			SettingSources:                     "user,project",
			IncludeHookEvents:                  true,
			Betas:                              "beta-one,beta-two",
			Worktree:                           "feature/review",
			DisableSlashCommands:               true,
			ExcludeDynamicSystemPromptSections: true,
			Debug:                              "sdk",
			DebugFile:                          "/tmp/claude-debug.log",
		},
	})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--model"), []string{"sonnet"})
	if values := getFlagValues(inspection.Args, "--cd"); len(values) != 0 {
		t.Fatalf("did not expect cd flag values, got %#v", values)
	}
	if inspection.CWD != filepath.Dir(fakeClaudePath) {
		t.Fatalf("unexpected cwd: %q", inspection.CWD)
	}
	assertStringsEqual(t, getFlagValues(inspection.Args, "--max-turns"), []string{"7"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--max-budget-usd"), []string{"1.5"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--append-system-prompt"), []string{"append this"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--append-system-prompt-file"), []string{"/tmp/append.txt"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--tools"), []string{"Read,Write"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--permission-prompt-tool"), []string{"mcp__permissions__prompt"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--mcp-config"), []string{"mcp-single.json"})
	if !hasFlag(inspection.Args, "--strict-mcp-config") {
		t.Fatal("expected strict mcp config flag")
	}
	assertStringsEqual(t, getFlagValues(inspection.Args, "--effort"), []string{"max"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--fallback-model"), []string{"opus"})
	if !hasFlag(inspection.Args, "--bare") {
		t.Fatal("expected bare flag")
	}
	if !hasFlag(inspection.Args, "--no-session-persistence") {
		t.Fatal("expected no-session-persistence flag")
	}
	if !hasFlag(inspection.Args, "--no-chrome") {
		t.Fatal("expected no-chrome flag")
	}
	assertStringsEqual(t, getFlagValues(inspection.Args, "--agent"), []string{"reviewer"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--name"), []string{"review session"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--settings"), []string{`{"source":"test"}`})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--setting-sources"), []string{"user,project"})
	if !hasFlag(inspection.Args, "--include-hook-events") {
		t.Fatal("expected include-hook-events flag")
	}
	assertStringsEqual(t, getFlagValues(inspection.Args, "--betas"), []string{"beta-one,beta-two"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--worktree"), []string{"feature/review"})
	if !hasFlag(inspection.Args, "--disable-slash-commands") {
		t.Fatal("expected disable-slash-commands flag")
	}
	if !hasFlag(inspection.Args, "--exclude-dynamic-system-prompt-sections") {
		t.Fatal("expected exclude-dynamic-system-prompt-sections flag")
	}
	assertStringsEqual(t, getFlagValues(inspection.Args, "--debug"), []string{"sdk"})
	assertStringsEqual(t, getFlagValues(inspection.Args, "--debug-file"), []string{"/tmp/claude-debug.log"})
	agentsValues := getFlagValues(inspection.Args, "--agents")
	if len(agentsValues) != 1 {
		t.Fatalf("expected one agents value, got %#v", agentsValues)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(agentsValues[0]), &decoded); err != nil {
		t.Fatalf("failed to decode agents json: %v", err)
	}
	if _, ok := decoded["reviewer"]; !ok {
		t.Fatalf("unexpected agents payload: %#v", decoded)
	}
}

func TestExecSupportsChromeDebugAndAgentsStringForms(t *testing.T) {
	chrome := true
	inspection := inspectExec(t, inspectExecOptions{sessionOptions: SessionOptions{Chrome: &chrome, Debug: true, Agents: `{"worker":{"model":"sonnet"}}`}})
	if !hasFlag(inspection.Args, "--chrome") {
		t.Fatal("expected chrome flag")
	}
	if hasFlag(inspection.Args, "--no-chrome") {
		t.Fatal("did not expect no-chrome flag")
	}
	if !hasFlag(inspection.Args, "--debug") {
		t.Fatal("expected debug flag")
	}
	assertStringsEqual(t, getFlagValues(inspection.Args, "--agents"), []string{`{"worker":{"model":"sonnet"}}`})
}

func TestExecPassesResumeWhenResumeSessionIDIsSet(t *testing.T) {
	exec := NewClaudeCodeExec(fakeClaudePath, nil)
	lines := make([]string, 0)
	err := exec.Run(context.Background(), ExecRequest{
		Input:           "resume test",
		CLIPath:         fakeClaudePath,
		ResumeSessionID: "my-session-123",
		SessionOptions: SessionOptions{
			DangerouslySkipPermissions: true,
		},
		OnLine: func(line string) {
			lines = append(lines, line)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resultLine := findResultLine(t, lines)
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(resultLine), &payload); err != nil {
		t.Fatalf("failed to decode result line: %v", err)
	}
	if payload.SessionID != "my-session-123" {
		t.Fatalf("unexpected session id: %q", payload.SessionID)
	}
}

func TestExecEmitsRawProcessEventsIncludingStdoutAndStderrChunksAndLines(t *testing.T) {
	exec := NewClaudeCodeExec(fakeClaudePath, nil)
	rawEvents := make([]RawClaudeEvent, 0)
	lines := make([]string, 0)
	err := exec.Run(context.Background(), ExecRequest{
		Input:          rawEventsPrompt,
		CLIPath:        fakeClaudePath,
		SessionOptions: SessionOptions{},
		OnRawEvent: func(event RawClaudeEvent) {
			rawEvents = append(rawEvents, event)
		},
		OnLine: func(line string) {
			lines = append(lines, line)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	eventTypes := make([]RawClaudeEventType, 0, len(rawEvents))
	for _, event := range rawEvents {
		eventTypes = append(eventTypes, event.Type)
	}
	assertEventTypePresent(t, eventTypes, RawEventSpawn)
	assertEventTypePresent(t, eventTypes, RawEventStdinClosed)
	assertEventTypePresent(t, eventTypes, RawEventStdoutLine)
	assertEventTypePresent(t, eventTypes, RawEventStderrChunk)
	assertEventTypePresent(t, eventTypes, RawEventStderrLine)
	assertEventTypePresent(t, eventTypes, RawEventExit)

	spawnEvent := findRawEvent(t, rawEvents, RawEventSpawn)
	if spawnEvent.Command != fakeClaudePath {
		t.Fatalf("unexpected spawn command: %q", spawnEvent.Command)
	}
	if !hasFlag(spawnEvent.Args, rawEventsPrompt) {
		t.Fatalf("expected spawn args to contain prompt, got %#v", spawnEvent.Args)
	}

	stdoutLine := findRawEvent(t, rawEvents, RawEventStdoutLine)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(stdoutLine.Line), &decoded); err != nil {
		t.Fatalf("failed to decode stdout line: %v", err)
	}
	if decoded["type"] != "result" {
		t.Fatalf("unexpected stdout line payload: %#v", decoded)
	}

	stderrChunk := findRawEvent(t, rawEvents, RawEventStderrChunk)
	if stderrChunk.Chunk != "raw stderr line\n" {
		t.Fatalf("unexpected stderr chunk: %q", stderrChunk.Chunk)
	}
	stderrLine := findRawEvent(t, rawEvents, RawEventStderrLine)
	if stderrLine.Line != "raw stderr line" {
		t.Fatalf("unexpected stderr line: %q", stderrLine.Line)
	}
	exitEvent := findRawEvent(t, rawEvents, RawEventExit)
	if exitEvent.Code == nil || *exitEvent.Code != 0 {
		t.Fatalf("unexpected exit code: %#v", exitEvent.Code)
	}
	if exitEvent.Signal != "" {
		t.Fatalf("unexpected exit signal: %q", exitEvent.Signal)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 stdout line, got %d", len(lines))
	}
}

func TestExecUsesExplicitEnvOverrideWithoutInheritingOSEnviron(t *testing.T) {
	restore := saveEnv(t, parentEnvKey, "from-parent")
	defer restore()
	restoreAPI := saveEnv(t, "ANTHROPIC_API_KEY", "from-parent-api-key")
	defer restoreAPI()
	restoreAuth := saveEnv(t, "ANTHROPIC_AUTH_TOKEN", "from-parent-auth-token")
	defer restoreAuth()
	restoreBase := saveEnv(t, "ANTHROPIC_BASE_URL", "https://from-parent.example.com")
	defer restoreBase()

	exec := NewClaudeCodeExec(fakeClaudePath, map[string]string{
		"INSPECT_CUSTOM_ENV":   "from-override",
		"ANTHROPIC_API_KEY":    "explicit-api-key",
		"ANTHROPIC_AUTH_TOKEN": "explicit-auth-token",
		"ANTHROPIC_BASE_URL":   "https://explicit.example.com",
	})
	inspection := inspectExec(t, inspectExecOptions{exec: exec})
	assertPtrEqual(t, inspection.Env.InspectCustomEnv, "from-override")
	assertNilPtr(t, inspection.Env.InspectInherited)
	assertPtrEqual(t, inspection.Env.AnthropicAPIKey, "explicit-api-key")
	assertPtrEqual(t, inspection.Env.AnthropicAuthToken, "explicit-auth-token")
	assertPtrEqual(t, inspection.Env.AnthropicBaseURL, "https://explicit.example.com")
}

func TestExecAllowsPerRunEnvToOverrideConstructorEnv(t *testing.T) {
	exec := NewClaudeCodeExec(fakeClaudePath, map[string]string{
		"ANTHROPIC_API_KEY":    "constructor-key",
		"ANTHROPIC_AUTH_TOKEN": "constructor-token",
		"ANTHROPIC_BASE_URL":   "https://constructor.example.com",
	})
	inspection := inspectExec(t, inspectExecOptions{
		exec: exec,
		env: map[string]string{
			"ANTHROPIC_API_KEY":    "run-key",
			"ANTHROPIC_AUTH_TOKEN": "run-token",
			"ANTHROPIC_BASE_URL":   "https://run.example.com",
		},
	})
	assertPtrEqual(t, inspection.Env.AnthropicAPIKey, "run-key")
	assertPtrEqual(t, inspection.Env.AnthropicAuthToken, "run-token")
	assertPtrEqual(t, inspection.Env.AnthropicBaseURL, "https://run.example.com")
}

func TestExecDoesNotInheritGlobalEnvWhenNoExplicitEnvIsProvided(t *testing.T) {
	restore := saveEnv(t, parentEnvKey, "from-parent")
	defer restore()
	restoreAPI := saveEnv(t, "ANTHROPIC_API_KEY", "from-parent-api-key")
	defer restoreAPI()
	restoreAuth := saveEnv(t, "ANTHROPIC_AUTH_TOKEN", "from-parent-auth-token")
	defer restoreAuth()
	restoreBase := saveEnv(t, "ANTHROPIC_BASE_URL", "https://from-parent.example.com")
	defer restoreBase()

	inspection := inspectExec(t, inspectExecOptions{exec: NewClaudeCodeExec(fakeClaudePath, nil)})
	assertNilPtr(t, inspection.Env.InspectInherited)
	assertNilPtr(t, inspection.Env.AnthropicAPIKey)
	assertNilPtr(t, inspection.Env.AnthropicAuthToken)
	assertNilPtr(t, inspection.Env.AnthropicBaseURL)
}

func TestExecMergesConstructorEnvWithPerRunEnvWithoutCredentialMutualExclusion(t *testing.T) {
	exec := NewClaudeCodeExec(fakeClaudePath, map[string]string{
		"ANTHROPIC_API_KEY":    "env-key",
		"ANTHROPIC_AUTH_TOKEN": "env-token",
		"ANTHROPIC_BASE_URL":   "https://env.example.com",
		"INSPECT_CUSTOM_ENV":   "from-constructor",
	})
	inspection := inspectExec(t, inspectExecOptions{
		exec: exec,
		env: map[string]string{
			"INSPECT_CUSTOM_ENV": "from-run",
		},
	})
	assertPtrEqual(t, inspection.Env.AnthropicAPIKey, "env-key")
	assertPtrEqual(t, inspection.Env.AnthropicAuthToken, "env-token")
	assertPtrEqual(t, inspection.Env.AnthropicBaseURL, "https://env.example.com")
	assertPtrEqual(t, inspection.Env.InspectCustomEnv, "from-run")
}

func findResultLine(t *testing.T, lines []string) string {
	t.Helper()
	for _, line := range lines {
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		if payload["type"] == "result" {
			return line
		}
	}
	t.Fatal("missing result line")
	return ""
}

func getFlagValues(args []string, flag string) []string {
	values := make([]string, 0)
	for index := 0; index < len(args); index++ {
		if args[index] == flag && index+1 < len(args) {
			values = append(values, args[index+1])
		}
	}
	return values
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func assertStringsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected values: got %#v want %#v", got, want)
	}
}

func intPtr(value int) *int {
	return &value
}

func saveEnv(t *testing.T, key, value string) func() {
	t.Helper()
	original, existed := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	return func() {
		if !existed {
			_ = os.Unsetenv(key)
			return
		}
		_ = os.Setenv(key, original)
	}
}

func assertPtrEqual(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("unexpected pointer value: %#v want %q", got, want)
	}
}

func assertNilPtr(t *testing.T, value *string) {
	t.Helper()
	if value != nil {
		t.Fatalf("expected nil pointer, got %#v", value)
	}
}

func assertEventTypePresent(t *testing.T, types []RawClaudeEventType, want RawClaudeEventType) {
	t.Helper()
	for _, eventType := range types {
		if eventType == want {
			return
		}
	}
	t.Fatalf("missing event type %q in %#v", want, types)
}

func findRawEvent(t *testing.T, events []RawClaudeEvent, want RawClaudeEventType) RawClaudeEvent {
	t.Helper()
	for _, event := range events {
		if event.Type == want {
			return event
		}
	}
	t.Fatalf("missing raw event type %q", want)
	return RawClaudeEvent{}
}
