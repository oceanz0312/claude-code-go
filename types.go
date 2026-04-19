package claudecodego

import (
	"context"
	"errors"
	"reflect"

	"github.com/oceanz0312/claude-code-go/parser"
)

type PermissionMode string

const (
	PermissionModeDefault           PermissionMode = "default"
	PermissionModeAcceptEdits       PermissionMode = "acceptEdits"
	PermissionModePlan              PermissionMode = "plan"
	PermissionModeAuto              PermissionMode = "auto"
	PermissionModeDontAsk           PermissionMode = "dontAsk"
	PermissionModeBypassPermissions PermissionMode = "bypassPermissions"
)

type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

type RawClaudeEventType string

const (
	RawEventSpawn        RawClaudeEventType = "spawn"
	RawEventStdinClosed  RawClaudeEventType = "stdin_closed"
	RawEventStdoutLine   RawClaudeEventType = "stdout_line"
	RawEventStderrChunk  RawClaudeEventType = "stderr_chunk"
	RawEventStderrLine   RawClaudeEventType = "stderr_line"
	RawEventProcessError RawClaudeEventType = "process_error"
	RawEventExit         RawClaudeEventType = "exit"
)

type SerializedError struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
	Stack   string `json:"stack,omitempty"`
}

type RawClaudeEvent struct {
	Type    RawClaudeEventType `json:"type"`
	Command string             `json:"command,omitempty"`
	Args    []string           `json:"args,omitempty"`
	CWD     string             `json:"cwd,omitempty"`
	Chunk   string             `json:"chunk,omitempty"`
	Line    string             `json:"line,omitempty"`
	Code    *int               `json:"code,omitempty"`
	Signal  string             `json:"signal,omitempty"`
	Error   *SerializedError   `json:"error,omitempty"`
}

func newProcessErrorEvent(err error) RawClaudeEvent {
	name := "Error"
	if err != nil {
		if typeOf := reflect.TypeOf(err); typeOf != nil {
			if value := typeOf.Name(); value != "" {
				name = value
			}
		}
	}

	message := ""
	if err != nil {
		message = err.Error()
	}

	return RawClaudeEvent{
		Type: RawEventProcessError,
		Error: &SerializedError{
			Name:    name,
			Message: message,
			Stack:   message,
		},
	}
}

type ClaudeCodeOptions struct {
	CLIPath   string
	Env       map[string]string
	APIKey    string
	AuthToken string
	BaseURL   string
}

type AgentDefinition struct {
	Description     string         `json:"description,omitempty"`
	Prompt          string         `json:"prompt,omitempty"`
	Tools           []string       `json:"tools,omitempty"`
	AllowedTools    []string       `json:"allowedTools,omitempty"`
	DisallowedTools []string       `json:"disallowedTools,omitempty"`
	Model           string         `json:"model,omitempty"`
	Effort          Effort         `json:"effort,omitempty"`
	MaxTurns        *int           `json:"maxTurns,omitempty"`
	PermissionMode  PermissionMode `json:"permissionMode,omitempty"`
	Isolation       string         `json:"isolation,omitempty"`
	InitialPrompt   string         `json:"initialPrompt,omitempty"`
	MCPServers      map[string]any `json:"mcpServers,omitempty"`
}

type SessionOptions struct {
	Model                              string
	CWD                                string
	AdditionalDirectories              []string
	MaxTurns                           *int
	MaxBudgetUSD                       *float64
	SystemPrompt                       string
	SystemPromptFile                   string
	AppendSystemPrompt                 string
	AppendSystemPromptFile             string
	PermissionMode                     PermissionMode
	DangerouslySkipPermissions         bool
	AllowedTools                       []string
	DisallowedTools                    []string
	Tools                              string
	PermissionPromptTool               string
	MCPConfig                          any
	StrictMCPConfig                    bool
	Effort                             Effort
	FallbackModel                      string
	Bare                               bool
	NoSessionPersistence               bool
	Chrome                             *bool
	Agents                             any
	Agent                              string
	Name                               string
	Settings                           string
	SettingSources                     string
	Verbose                            *bool
	IncludePartialMessages             *bool
	IncludeHookEvents                  bool
	Betas                              string
	Worktree                           string
	DisableSlashCommands               bool
	PluginDir                          any
	ExcludeDynamicSystemPromptSections bool
	Debug                              any
	DebugFile                          string
	JSONSchema                         any
	SessionID                          string
	ForkSession                        bool
	RawEventLog                        any
}

type InputItemType string

const (
	InputTypeText       InputItemType = "text"
	InputTypeLocalImage InputItemType = "local_image"
)

type InputItem struct {
	Type InputItemType `json:"type"`
	Text string        `json:"text,omitempty"`
	Path string        `json:"path,omitempty"`
}

type Input any

type TurnUsage struct {
	CostUSD       float64 `json:"costUsd,omitempty"`
	InputTokens   int     `json:"inputTokens,omitempty"`
	OutputTokens  int     `json:"outputTokens,omitempty"`
	ContextWindow int     `json:"contextWindow,omitempty"`
}

type Turn struct {
	Events           []parser.RelayEvent `json:"events"`
	FinalResponse    string              `json:"finalResponse"`
	Usage            *TurnUsage          `json:"usage,omitempty"`
	SessionID        string              `json:"sessionId,omitempty"`
	StructuredOutput any                 `json:"structuredOutput,omitempty"`
}

type TurnOptions struct {
	OnRawEvent            func(RawClaudeEvent)
	FailFastOnCLIAPIError bool
	Signal                context.Context
}

type StreamedTurn struct {
	next <-chan streamItem
	err  error
	done chan struct{}
}

type streamItem struct {
	event parser.RelayEvent
	err   error
	ok    bool
}

func (turn *StreamedTurn) Next(ctx context.Context) (parser.RelayEvent, bool, error) {
	if turn == nil {
		return nil, false, nil
	}

	select {
	case <-ctx.Done():
		return nil, false, context.Cause(ctx)
	case item, ok := <-turn.next:
		if !ok {
			return nil, false, nil
		}
		if item.err != nil {
			return nil, false, item.err
		}
		return item.event, item.ok, nil
	}
}

func (turn *StreamedTurn) Wait() error {
	if turn == nil {
		return nil
	}
	<-turn.done
	return turn.err
}

type RawEventLogger interface {
	Log(RawClaudeEvent)
	Close() error
}

type noopRawEventLogger struct{}

func (noopRawEventLogger) Log(RawClaudeEvent) {}
func (noopRawEventLogger) Close() error       { return nil }

var errNilContext = errors.New("context is nil")

type RelayEvent = parser.RelayEvent
type TextDeltaEvent = parser.TextDeltaEvent
type ThinkingDeltaEvent = parser.ThinkingDeltaEvent
type ToolUseEvent = parser.ToolUseEvent
type ToolResultEvent = parser.ToolResultEvent
type SessionMetaEvent = parser.SessionMetaEvent
type TurnCompleteEvent = parser.TurnCompleteEvent
type ErrorEvent = parser.ErrorEvent
