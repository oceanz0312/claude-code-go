package claudecodego

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"claude-code-go/parser"
)

type abortSignalBinding struct {
	signal  context.Context
	cleanup func()
}

type abortSignalCleanupRecorder interface {
	markCleanup()
}

type execRunner interface {
	Run(ctx context.Context, request ExecRequest) error
}

type Session struct {
	exec           execRunner
	globalOptions  ClaudeCodeOptions
	sessionOptions SessionOptions
	id             string
	continueMode   bool
	hasRun         bool
}

type streamState struct {
	activeMessageID        string
	lastCompletedMessageID string
	messages               map[string]*streamedMessageState
}

type streamedMessageState struct {
	textStreamed     bool
	thinkingStreamed bool
}

func NewSession(exec execRunner, globalOptions ClaudeCodeOptions, sessionOptions SessionOptions, sessionID string, continueMode bool) *Session {
	return &Session{
		exec:           exec,
		globalOptions:  globalOptions,
		sessionOptions: sessionOptions,
		id:             sessionID,
		continueMode:   continueMode,
	}
}

func (session *Session) ID() string {
	return session.id
}

func (session *Session) Run(ctx context.Context, input Input, options ...TurnOptions) (*Turn, error) {
	turnOptions := mergeTurnOptions(options)
	events := make([]parser.RelayEvent, 0)
	finalResponse := ""
	var usage *TurnUsage
	sessionID := session.id
	var streamError *parser.ErrorEvent
	var structuredOutput any

	err := session.processStream(ctx, input, turnOptions, func(event parser.RelayEvent) {
		events = append(events, event)
		switch typed := event.(type) {
		case parser.TextDeltaEvent:
			finalResponse += typed.Content
		case parser.TurnCompleteEvent:
			if typed.SessionID != "" {
				sessionID = typed.SessionID
			}
			usage = &TurnUsage{}
			if typed.CostUSD != nil {
				usage.CostUSD = *typed.CostUSD
			}
			if typed.InputTokens != nil {
				usage.InputTokens = *typed.InputTokens
			}
			if typed.OutputTokens != nil {
				usage.OutputTokens = *typed.OutputTokens
			}
			if typed.ContextWindow != nil {
				usage.ContextWindow = *typed.ContextWindow
			}
		case parser.ErrorEvent:
			copy := typed
			streamError = &copy
			if typed.SessionID != "" {
				sessionID = typed.SessionID
			}
		}
	}, func(value any) {
		structuredOutput = value
	})
	if err != nil {
		return nil, err
	}
	if streamError != nil {
		return nil, errors.New(streamError.Message)
	}

	return &Turn{
		Events:           events,
		FinalResponse:    finalResponse,
		Usage:            usage,
		SessionID:        sessionID,
		StructuredOutput: structuredOutput,
	}, nil
}

func (session *Session) RunStreamed(ctx context.Context, input Input, options ...TurnOptions) (*StreamedTurn, error) {
	turnOptions := mergeTurnOptions(options)
	events := make(chan streamItem, 16)
	done := make(chan struct{})

	stream := &StreamedTurn{next: events, done: done}

	go func() {
		defer close(done)
		defer close(events)

		err := session.processStream(ctx, input, turnOptions, func(event parser.RelayEvent) {
			events <- streamItem{event: event, ok: true}
		}, func(any) {})
		if err != nil {
			stream.err = err
			events <- streamItem{err: err}
		}
	}()

	return stream, nil
}

func (session *Session) processStream(
	ctx context.Context,
	input Input,
	turnOptions TurnOptions,
	onEvent func(parser.RelayEvent),
	onStructuredOutput func(any),
) error {
	if ctx == nil {
		return errNilContext
	}

	prompt, inputItems := normalizeInput(input)
	translator := parser.NewTranslator()
	state := newStreamState()
	rawEventLogger, err := CreateRawEventLogger(session.sessionOptions.RawEventLog)
	if err != nil {
		return err
	}
	defer func() {
		if rawEventLogger != nil {
			_ = rawEventLogger.Close()
		}
	}()

	cliSignalCtx, cliSignalCancel := context.WithCancelCause(context.Background())
	defer cliSignalCancel(nil)
	abortBinding := createAbortSignalBinding(turnOptions.Signal, cliSignalCtx)
	defer abortBinding.cleanup()
	defer func() {
		session.hasRun = true
	}()

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var (
		fatalMu       sync.Mutex
		fatalCLIError string
		stderrText    string
		emittedError  bool
	)

	setFatalError := func(value string) {
		fatalMu.Lock()
		if fatalCLIError == "" {
			fatalCLIError = value
		}
		fatalMu.Unlock()
	}
	getFatalError := func() string {
		fatalMu.Lock()
		defer fatalMu.Unlock()
		return fatalCLIError
	}

	onRawEvent := turnOptions.OnRawEvent
	if turnOptions.FailFastOnCLIAPIError {
		onRawEvent = func(event RawClaudeEvent) {
			fatalMu.Lock()
			stderrText = appendStderrText(stderrText, event)
			detected := extractFatalCLIAPIError(stderrText)
			fatalMu.Unlock()
			if detected != "" {
				setFatalError(detected)
				cliSignalCancel(errors.New(detected))
			}
			if turnOptions.OnRawEvent != nil {
				turnOptions.OnRawEvent(event)
			}
		}
	}

	err = session.exec.Run(runCtx, ExecRequest{
		Input:           prompt,
		InputItems:      inputItems,
		ResumeSessionID: session.resumeSessionID(),
		ContinueSession: !session.hasRun && session.continueMode,
		SessionOptions:  session.sessionOptions,
		CLIPath:         session.globalOptions.CLIPath,
		Env:             session.globalOptions.Env,
		Signal:          abortBinding.signal,
		RawEventLogger:  rawEventLogger,
		OnRawEvent:      onRawEvent,
		OnLine: func(line string) {
			parsed := parser.ParseLine(line)
			if parsed == nil {
				return
			}

			if parsed.Type == "result" {
				if structured, ok := parsed.Raw["structured_output"]; ok {
					onStructuredOutput(structured)
				}
			}

			if turnOptions.FailFastOnCLIAPIError {
				if detected := extractFatalCLIAPIErrorFromStdoutEvent(*parsed); detected != "" {
					setFatalError(detected)
					if !session.hasID() && parsed.SessionID != "" {
						session.id = parsed.SessionID
					}
					cliSignalCancel(errors.New(detected))
					emittedError = true
					onEvent(parser.NewError(detected, session.currentSessionID(translator.SessionID())))
					return
				}
			}

			relayEvents := translateRelayEvents(*parsed, translator, state)
			if translator.SessionID() != "" && !session.hasID() {
				session.id = translator.SessionID()
			}
			for _, relayEvent := range relayEvents {
				if turnComplete, ok := relayEvent.(parser.TurnCompleteEvent); ok {
					if turnComplete.SessionID != "" && !session.hasID() {
						session.id = turnComplete.SessionID
					}
				}
				onEvent(relayEvent)
			}
		},
	})
	closeErr := rawEventLogger.Close()
	rawEventLogger = nil

	if fatal := getFatalError(); fatal != "" {
		if turnOptions.FailFastOnCLIAPIError {
			if !emittedError {
				emittedError = true
				onEvent(parser.NewError(fatal, session.currentSessionID(translator.SessionID())))
			}
			if closeErr != nil {
				return closeErr
			}
			return nil
		}
	}
	if closeErr != nil {
		return closeErr
	}

	if err != nil {
		if cause := context.Cause(runCtx); cause != nil && fatalCLIError != "" {
			return nil
		}
		return err
	}

	return nil
}

func (session *Session) resumeSessionID() string {
	if session.hasRun {
		return session.id
	}
	if session.continueMode {
		return ""
	}
	return session.id
}

func (session *Session) hasID() bool {
	return session.id != ""
}

func (session *Session) currentSessionID(translatorSessionID string) string {
	if session.id != "" {
		return session.id
	}
	return translatorSessionID
}

func mergeTurnOptions(options []TurnOptions) TurnOptions {
	if len(options) == 0 {
		return TurnOptions{}
	}
	return options[0]
}

func normalizeInput(input Input) (string, []InputItem) {
	switch typed := input.(type) {
	case string:
		return typed, nil
	case []InputItem:
		promptParts := make([]string, 0, len(typed))
		for _, item := range typed {
			if item.Type == InputTypeText {
				promptParts = append(promptParts, item.Text)
			}
		}
		return strings.Join(promptParts, "\n\n"), typed
	default:
		return "", nil
	}
}

func newStreamState() *streamState {
	return &streamState{messages: make(map[string]*streamedMessageState)}
}

func createAbortSignalBinding(signals ...context.Context) abortSignalBinding {
	activeSignals := make([]context.Context, 0, len(signals))
	for _, signal := range signals {
		if signal != nil {
			activeSignals = append(activeSignals, signal)
		}
	}

	if len(activeSignals) == 0 {
		return abortSignalBinding{cleanup: func() {}}
	}
	if len(activeSignals) == 1 {
		return abortSignalBinding{signal: activeSignals[0], cleanup: func() {}}
	}

	merged, mergedCancel := context.WithCancelCause(context.Background())
	stopChans := make([]chan struct{}, 0, len(activeSignals))

	abort := func(source context.Context) {
		if merged.Err() != nil {
			return
		}
		mergedCancel(context.Cause(source))
	}

	for _, signal := range activeSignals {
		if signal.Err() != nil {
			abort(signal)
			return abortSignalBinding{signal: merged, cleanup: func() {}}
		}
		sig := signal
		done := sig.Done()
		stop := make(chan struct{})
		stopChans = append(stopChans, stop)
		go func() {
			select {
			case <-done:
				abort(sig)
			case <-stop:
			case <-merged.Done():
			}
		}()
	}

	return abortSignalBinding{
		signal: merged,
		cleanup: func() {
			for _, stop := range stopChans {
				close(stop)
			}
			for _, signal := range activeSignals {
				if recorder, ok := signal.(abortSignalCleanupRecorder); ok {
					recorder.markCleanup()
				}
			}
		},
	}
}

func ensureStreamedMessageState(state *streamState, messageID string) *streamedMessageState {
	if messageID == "" {
		return nil
	}
	if existing, ok := state.messages[messageID]; ok {
		return existing
	}
	next := &streamedMessageState{}
	state.messages[messageID] = next
	return next
}

func appendStderrText(stderrText string, event RawClaudeEvent) string {
	switch event.Type {
	case RawEventStderrChunk:
		return truncateTail(stderrText + event.Chunk)
	case RawEventStderrLine:
		return truncateTail(stderrText + event.Line + "\n")
	default:
		return stderrText
	}
}

func truncateTail(text string) string {
	const maxLen = 16384
	if len(text) <= maxLen {
		return text
	}
	return text[len(text)-maxLen:]
}

func extractFatalCLIAPIError(stderrText string) string {
	index := strings.Index(strings.ToLower(stderrText), strings.ToLower("API Error:"))
	if index < 0 {
		return ""
	}
	tail := strings.TrimSpace(stderrText[index:])
	if tail == "" {
		return ""
	}
	parts := strings.SplitN(tail, "\n", 2)
	return strings.TrimSpace(parts[0])
}

func extractFatalCLIAPIErrorFromStdoutEvent(parsed parser.ClaudeEvent) string {
	if parsed.Type != "system" || parsed.Subtype != "api_retry" {
		return ""
	}
	if parsed.ErrorStatus == nil && parsed.Error == "" {
		return ""
	}
	parts := []string{"API retry aborted"}
	if parsed.ErrorStatus != nil {
		parts = append(parts, "status "+strconv.Itoa(*parsed.ErrorStatus))
	}
	if parsed.Error != "" {
		parts = append(parts, strings.TrimSpace(parsed.Error))
	}
	if parsed.Attempt != nil && parsed.MaxRetries != nil {
		parts = append(parts, fmt.Sprintf("attempt %d/%d", *parsed.Attempt, *parsed.MaxRetries))
	}
	if parsed.RetryDelayMS != nil {
		parts = append(parts, fmt.Sprintf("next retry in %dms", *parsed.RetryDelayMS))
	}
	return strings.Join(parts, " | ")
}

func translateRelayEvents(parsed parser.ClaudeEvent, translator *parser.Translator, state *streamState) []parser.RelayEvent {
	if parsed.Type == "stream_event" {
		return translateStreamEvent(parsed, state)
	}
	relayEvents := translator.Translate(parsed)
	if parsed.Type == "assistant" {
		return suppressDuplicateAssistantSnapshot(parsed, relayEvents, state)
	}
	return relayEvents
}

func translateStreamEvent(parsed parser.ClaudeEvent, state *streamState) []parser.RelayEvent {
	event, ok := parsed.Event.(map[string]any)
	if !ok || event == nil {
		return nil
	}

	switch eventType, _ := event["type"].(string); eventType {
	case "message_start":
		messageID := getMessageID(event["message"])
		if messageID != "" {
			state.activeMessageID = messageID
			ensureStreamedMessageState(state, messageID)
		}
		return nil
	case "message_stop":
		messageID := resolveStreamEventMessageID(event, state)
		if messageID != "" {
			ensureStreamedMessageState(state, messageID)
			state.lastCompletedMessageID = messageID
			if state.activeMessageID == messageID {
				state.activeMessageID = ""
			}
		}
		return nil
	case "content_block_delta":
		messageID := resolveStreamEventMessageID(event, state)
		messageState := ensureStreamedMessageState(state, messageID)
		delta, _ := event["delta"].(map[string]any)
		if delta == nil {
			return nil
		}
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "text_delta":
			text, _ := delta["text"].(string)
			if text == "" {
				return nil
			}
			if messageState != nil {
				messageState.textStreamed = true
			}
			return []parser.RelayEvent{parser.NewTextDelta(text)}
		case "thinking_delta":
			content, _ := delta["thinking"].(string)
			if content == "" {
				content, _ = delta["text"].(string)
			}
			if content == "" {
				return nil
			}
			if messageState != nil {
				messageState.thinkingStreamed = true
			}
			return []parser.RelayEvent{parser.NewThinkingDelta(content)}
		}
		return nil
	default:
		return nil
	}
}

func suppressDuplicateAssistantSnapshot(parsed parser.ClaudeEvent, relayEvents []parser.RelayEvent, state *streamState) []parser.RelayEvent {
	messageID := getMessageIDFromClaudeMessage(parsed.Message)
	if messageID == "" {
		messageID = state.lastCompletedMessageID
	}
	if messageID == "" {
		return relayEvents
	}
	messageState := state.messages[messageID]
	if messageState == nil {
		return relayEvents
	}

	filtered := make([]parser.RelayEvent, 0, len(relayEvents))
	for _, event := range relayEvents {
		switch event.(type) {
		case parser.TextDeltaEvent:
			if messageState.textStreamed {
				continue
			}
		case parser.ThinkingDeltaEvent:
			if messageState.thinkingStreamed {
				continue
			}
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func getMessageIDFromClaudeMessage(message *parser.ClaudeMessage) string {
	if message == nil {
		return ""
	}
	return message.ID
}

func getMessageID(raw any) string {
	message, ok := raw.(map[string]any)
	if !ok || message == nil {
		return ""
	}
	messageID, _ := message["id"].(string)
	return messageID
}

func getStreamEventMessageID(event map[string]any) string {
	if messageID, ok := event["message_id"].(string); ok {
		return messageID
	}
	return getMessageID(event["message"])
}

func resolveStreamEventMessageID(event map[string]any, state *streamState) string {
	if messageID := getStreamEventMessageID(event); messageID != "" {
		return messageID
	}
	return state.activeMessageID
}
