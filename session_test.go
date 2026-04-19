package claudecodego

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	stderrAPIErrorPrompt       = "__stderr_api_error__"
	stdoutAPIRetryAuthPrompt   = "__stdout_api_retry_auth__"
	defaultFakeClaudeSessionID = "test-session-001"
)

func createTestClient(options ClaudeCodeOptions) *ClaudeCode {
	options.CLIPath = fakeClaudePath
	return NewClaudeCode(options)
}

func TestSessionRunReturnsCompleteTurnWithFinalResponseAndUsage(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	turn, err := session.Run(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if turn.FinalResponse != "Here is my response." {
		t.Fatalf("unexpected final response: %q", turn.FinalResponse)
	}
	if len(turn.Events) == 0 {
		t.Fatal("expected relay events")
	}
	if turn.SessionID != defaultFakeClaudeSessionID {
		t.Fatalf("unexpected session id: %q", turn.SessionID)
	}
	if turn.Usage == nil {
		t.Fatal("expected usage")
	}
	if turn.Usage.CostUSD <= 0 {
		t.Fatalf("unexpected cost: %#v", turn.Usage)
	}
	if turn.Usage.InputTokens <= 0 || turn.Usage.OutputTokens <= 0 {
		t.Fatalf("unexpected usage tokens: %#v", turn.Usage)
	}
}

func TestSessionRunCapturesSessionIDFromSessionMeta(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	if _, err := session.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.ID() != defaultFakeClaudeSessionID {
		t.Fatalf("unexpected session id: %q", session.ID())
	}
}

func TestSessionRunThrowsOnErrorResponse(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	_, err := session.Run(context.Background(), "force-error")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Something went wrong" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSessionRunCanFailFastOnFatalCLIAPIErrorsWrittenToStderr(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	start := time.Now()
	_, err := session.Run(context.Background(), stderrAPIErrorPrompt, TurnOptions{FailFastOnCLIAPIError: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if since := time.Since(start); since >= time.Second {
		t.Fatalf("expected fail-fast under 1s, got %v", since)
	}
	if got := err.Error(); got == "" || !containsAll(got, []string{"API Error:", "502"}) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSessionRunCanFailFastOnFatalCLIAPIRetryEventsWrittenToStdout(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	start := time.Now()
	_, err := session.Run(context.Background(), stdoutAPIRetryAuthPrompt, TurnOptions{FailFastOnCLIAPIError: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if since := time.Since(start); since >= time.Second {
		t.Fatalf("expected fail-fast under 1s, got %v", since)
	}
	if got := err.Error(); got == "" || !containsAll(got, []string{"authentication_failed", "status 401"}) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSessionRunSupportsMultiTurnViaAutomaticResume(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	first, err := session.Run(context.Background(), "__inspect_session_flags__")
	if err != nil {
		t.Fatalf("unexpected first run error: %v", err)
	}
	if first.SessionID != defaultFakeClaudeSessionID {
		t.Fatalf("unexpected first session id: %q", first.SessionID)
	}
	if session.ID() != defaultFakeClaudeSessionID {
		t.Fatalf("unexpected session id: %q", session.ID())
	}
	assertSessionFlagsJSON(t, first.FinalResponse, nil, false)

	second, err := session.Run(context.Background(), "__inspect_session_flags__")
	if err != nil {
		t.Fatalf("unexpected second run error: %v", err)
	}
	assertSessionFlagsJSON(t, second.FinalResponse, stringPtr(defaultFakeClaudeSessionID), false)
}

func TestSessionRunStreamedYieldsRelayEventsAsStream(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	stream, err := session.RunStreamed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := collectStreamEvents(t, stream)
	if len(events) == 0 {
		t.Fatal("expected events")
	}
	if !hasRelayEventType(events, "session_meta") {
		t.Fatalf("expected session_meta in %#v", relayEventTypes(events))
	}
	if !hasRelayEventType(events, "turn_complete") {
		t.Fatalf("expected turn_complete in %#v", relayEventTypes(events))
	}
}

func TestSessionRunStreamedStreamsTextDeltaEventsIncrementally(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	stream, err := session.RunStreamed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	textDeltas := make([]string, 0)
	for _, event := range collectStreamEvents(t, stream) {
		if text, ok := event.(TextDeltaEvent); ok {
			textDeltas = append(textDeltas, text.Content)
		}
	}
	assertStringsEqual(t, textDeltas, []string{"Here is ", "my response."})
}

func TestSessionRunStreamedStreamsToolUseAndToolResultEvents(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	stream, err := session.RunStreamed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasToolUse bool
	var hasToolResult bool
	for _, event := range collectStreamEvents(t, stream) {
		switch event.(type) {
		case ToolUseEvent:
			hasToolUse = true
		case ToolResultEvent:
			hasToolResult = true
		}
	}
	if !hasToolUse {
		t.Fatal("expected tool_use event")
	}
	if !hasToolResult {
		t.Fatal("expected tool_result event")
	}
}

func TestSessionRunStreamedCanSurfaceFatalCLIAPIStderrAsRelayEventError(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	start := time.Now()
	stream, err := session.RunStreamed(context.Background(), stderrAPIErrorPrompt, TurnOptions{FailFastOnCLIAPIError: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := collectStreamEvents(t, stream)
	if since := time.Since(start); since >= time.Second {
		t.Fatalf("expected fail-fast under 1s, got %v", since)
	}
	errorEvent := findErrorEvent(t, events)
	if !containsAll(errorEvent.Message, []string{"API Error:", "502"}) {
		t.Fatalf("unexpected error event: %#v", errorEvent)
	}
	if errorEvent.SessionID != defaultFakeClaudeSessionID {
		t.Fatalf("unexpected session id: %q", errorEvent.SessionID)
	}
}

func TestSessionRunStreamedCanSurfaceFatalCLIAPIRetryStdoutEventsAsRelayEventError(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	start := time.Now()
	stream, err := session.RunStreamed(context.Background(), stdoutAPIRetryAuthPrompt, TurnOptions{FailFastOnCLIAPIError: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := collectStreamEvents(t, stream)
	if since := time.Since(start); since >= time.Second {
		t.Fatalf("expected fail-fast under 1s, got %v", since)
	}
	errorEvent := findErrorEvent(t, events)
	if !containsAll(errorEvent.Message, []string{"authentication_failed", "status 401"}) {
		t.Fatalf("unexpected error event: %#v", errorEvent)
	}
	if errorEvent.SessionID != defaultFakeClaudeSessionID {
		t.Fatalf("unexpected session id: %q", errorEvent.SessionID)
	}
}

func TestResumeSessionResumesWithGivenSessionID(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.ResumeSession("my-custom-session", SessionOptions{DangerouslySkipPermissions: true})

	turn, err := session.Run(context.Background(), "continue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if turn.SessionID != "my-custom-session" {
		t.Fatalf("unexpected session id: %q", turn.SessionID)
	}
}

func TestContinueSessionUsesContinueFlag(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.ContinueSession(SessionOptions{DangerouslySkipPermissions: true})

	turn, err := session.Run(context.Background(), "__inspect_session_flags__")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSessionFlagsJSON(t, turn.FinalResponse, nil, true)
}

func TestStructuredInputAcceptsUserInputArrayWithText(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	turn, err := session.Run(context.Background(), []InputItem{{Type: InputTypeText, Text: "First part"}, {Type: InputTypeText, Text: "Second part"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if turn.FinalResponse == "" {
		t.Fatal("expected final response")
	}
}

func TestStructuredInputSendsLocalImageItemsThroughStreamJSONStdinInsteadOfImageFlag(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	rawStdoutLines := make([]string, 0)
	_, err := session.Run(context.Background(), []InputItem{{Type: InputTypeText, Text: inspectPrompt}, {Type: InputTypeLocalImage, Path: redSquareImage}}, TurnOptions{OnRawEvent: func(event RawClaudeEvent) {
		if event.Type == RawEventStdoutLine {
			rawStdoutLines = append(rawStdoutLines, event.Line)
		}
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resultLine := findResultLine(t, rawStdoutLines)
	var payload struct {
		Inspection execInspection `json:"inspection"`
	}
	if err := json.Unmarshal([]byte(resultLine), &payload); err != nil {
		t.Fatalf("failed to decode inspection payload: %v", err)
	}
	if !hasFlag(payload.Inspection.Args, "--input-format") {
		t.Fatalf("expected --input-format in %#v", payload.Inspection.Args)
	}
	if hasFlag(payload.Inspection.Args, "--image") {
		t.Fatalf("did not expect --image in %#v", payload.Inspection.Args)
	}
	if payload.Inspection.Input.InputFormat == nil || *payload.Inspection.Input.InputFormat != "stream-json" {
		t.Fatalf("unexpected input format: %#v", payload.Inspection.Input.InputFormat)
	}
	if payload.Inspection.Input.ImageCount != 1 {
		t.Fatalf("unexpected image count: %d", payload.Inspection.Input.ImageCount)
	}
	if payload.Inspection.Input.Prompt != inspectPrompt {
		t.Fatalf("unexpected prompt: %q", payload.Inspection.Input.Prompt)
	}
}

func TestAbortContextAbortsARunningSession(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := session.Run(ctx, "slow-run")
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestSessionGlobalOptionsPassOnlyExplicitEnvFromClaudeCodeOptionsIntoTheCLIProcess(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{APIKey: "global-key", AuthToken: "global-token", BaseURL: "https://global.example.com"})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	rawStdoutLines := make([]string, 0)
	stream, err := session.RunStreamed(context.Background(), inspectPrompt, TurnOptions{OnRawEvent: func(event RawClaudeEvent) {
		if event.Type == RawEventStdoutLine {
			rawStdoutLines = append(rawStdoutLines, event.Line)
		}
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = collectStreamEvents(t, stream)

	resultLine := findResultLine(t, rawStdoutLines)
	var payload struct {
		Inspection execInspection `json:"inspection"`
	}
	if err := json.Unmarshal([]byte(resultLine), &payload); err != nil {
		t.Fatalf("failed to decode inspection payload: %v", err)
	}
	assertPtrEqual(t, payload.Inspection.Env.AnthropicAPIKey, "global-key")
	assertPtrEqual(t, payload.Inspection.Env.AnthropicAuthToken, "global-token")
	assertPtrEqual(t, payload.Inspection.Env.AnthropicBaseURL, "https://global.example.com")
}

func TestRawClaudeEventsForwardTurnOptionsOnRawEventThroughRunStreamed(t *testing.T) {
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true})

	rawEvents := make([]RawClaudeEvent, 0)
	stream, err := session.RunStreamed(context.Background(), rawEventsPrompt, TurnOptions{OnRawEvent: func(event RawClaudeEvent) {
		rawEvents = append(rawEvents, event)
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = collectStreamEvents(t, stream)

	if !rawEventTypePresent(rawEvents, RawEventStdoutLine) {
		t.Fatalf("expected stdout_line in %#v", rawEvents)
	}
	if !rawEventTypePresent(rawEvents, RawEventStderrLine) {
		t.Fatalf("expected stderr_line in %#v", rawEvents)
	}
}

func TestRawClaudeEventsWriteRawEventLogsAsNDJSONWhenEnabled(t *testing.T) {
	logDir := t.TempDir()
	client := createTestClient(ClaudeCodeOptions{})
	session := client.StartSession(SessionOptions{DangerouslySkipPermissions: true, RawEventLog: logDir})

	stream, err := session.RunStreamed(context.Background(), rawEventsPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = collectStreamEvents(t, stream)

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(entries))
	}
	logPath := filepath.Join(logDir, entries[0].Name())
	logText, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	lines := splitNonEmptyLines(string(logText))
	if len(lines) == 0 {
		t.Fatal("expected log records")
	}
	types := make([]RawClaudeEventType, 0, len(lines))
	for _, line := range lines {
		var record struct {
			Timestamp string         `json:"timestamp"`
			Event     RawClaudeEvent `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("failed to decode record: %v", err)
		}
		if record.Timestamp == "" {
			t.Fatalf("expected timestamp in %q", line)
		}
		types = append(types, record.Event.Type)
	}
	assertEventTypePresent(t, types, RawEventSpawn)
	assertEventTypePresent(t, types, RawEventStdoutLine)
	assertEventTypePresent(t, types, RawEventStderrChunk)
	assertEventTypePresent(t, types, RawEventStderrLine)
	assertEventTypePresent(t, types, RawEventExit)
}

type delayedErrorExec struct {
	err   error
	delay time.Duration
}

func (runner delayedErrorExec) Run(context.Context, ExecRequest) error {
	time.Sleep(runner.delay)
	return runner.err
}

type awaitAbortExec struct {
	started chan struct{}
	signal  context.Context
}

func newAwaitAbortExec() *awaitAbortExec {
	return &awaitAbortExec{started: make(chan struct{})}
}

func (runner *awaitAbortExec) Run(_ context.Context, request ExecRequest) error {
	runner.signal = request.Signal
	close(runner.started)
	if request.Signal == nil {
		return nil
	}
	<-request.Signal.Done()
	return nil
}

type manualAbortSignal struct {
	context.Context
	ctx         context.Context
	cancel      context.CancelCauseFunc
	addCount    int
	removeCount int
}

func newManualAbortSignal() *manualAbortSignal {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &manualAbortSignal{Context: ctx, ctx: ctx, cancel: cancel}
}

func (signal *manualAbortSignal) Done() <-chan struct{} {
	signal.addCount++
	return signal.ctx.Done()
}

func (signal *manualAbortSignal) Err() error {
	return signal.ctx.Err()
}

func (signal *manualAbortSignal) Value(key any) any {
	return signal.ctx.Value(key)
}

func (signal *manualAbortSignal) abort(reason error) {
	signal.cancel(reason)
}

func (signal *manualAbortSignal) markCleanup() {
	signal.removeCount++
}

func TestSessionInternalBranchesRejectsAPendingStreamedIteratorWhenProcessingFails(t *testing.T) {
	session := NewSession(delayedErrorExec{err: errors.New("delayed stream failure"), delay: 10 * time.Millisecond}, ClaudeCodeOptions{}, SessionOptions{}, "", false)

	stream, err := session.RunStreamed(context.Background(), "hello")
	if err != nil {
		fatalUnexpected(t, err)
	}

	_, _, nextErr := stream.Next(context.Background())
	if nextErr == nil || nextErr.Error() != "delayed stream failure" {
		t.Fatalf("unexpected next error: %v", nextErr)
	}
}

func TestSessionInternalBranchesMergesAbortSignalsWithoutAReasonAndCleansUpListeners(t *testing.T) {
	exec := newAwaitAbortExec()
	session := NewSession(exec, ClaudeCodeOptions{}, SessionOptions{}, "", false)
	externalSignal := newManualAbortSignal()

	runDone := make(chan struct{})
	var turn *Turn
	var runErr error
	go func() {
		defer close(runDone)
		turn, runErr = session.Run(context.Background(), "hello", TurnOptions{Signal: externalSignal, FailFastOnCLIAPIError: true})
	}()

	<-exec.started
	if externalSignal.addCount != 1 {
		t.Fatalf("unexpected add count: %d", externalSignal.addCount)
	}

	externalSignal.abort(nil)
	<-runDone
	if runErr != nil {
		fatalUnexpected(t, runErr)
	}
	if turn == nil || turn.FinalResponse != "" || len(turn.Events) != 0 {
		t.Fatalf("unexpected turn: %#v", turn)
	}
	if exec.signal == nil || exec.signal.Err() == nil {
		t.Fatal("expected merged signal to be aborted")
	}
	if externalSignal.removeCount != 1 {
		t.Fatalf("unexpected remove count: %d", externalSignal.removeCount)
	}
}

func TestSessionInternalBranchesPreservesAbortReasonsWhenMergingAbortSignals(t *testing.T) {
	exec := newAwaitAbortExec()
	session := NewSession(exec, ClaudeCodeOptions{}, SessionOptions{}, "", false)
	externalSignal := newManualAbortSignal()

	runDone := make(chan struct{})
	var runErr error
	go func() {
		defer close(runDone)
		_, runErr = session.Run(context.Background(), "hello", TurnOptions{Signal: externalSignal, FailFastOnCLIAPIError: true})
	}()

	<-exec.started
	reason := errors.New("manual-stop")
	externalSignal.abort(reason)
	<-runDone
	if runErr != nil {
		fatalUnexpected(t, runErr)
	}
	if got := context.Cause(exec.signal); got == nil || got.Error() != "manual-stop" {
		t.Fatalf("unexpected merged signal cause: %v", got)
	}
}

func fatalUnexpected(t *testing.T, err error) {
	t.Helper()
	t.Fatalf("unexpected error: %v", err)
}

func collectStreamEvents(t *testing.T, stream *StreamedTurn) []RelayEvent {
	t.Helper()
	events := make([]RelayEvent, 0)
	for {
		event, ok, err := stream.Next(context.Background())
		if err != nil {
			t.Fatalf("stream next failed: %v", err)
		}
		if !ok {
			break
		}
		events = append(events, event)
	}
	if err := stream.Wait(); err != nil {
		t.Fatalf("stream wait failed: %v", err)
	}
	return events
}

func hasRelayEventType(events []RelayEvent, want string) bool {
	for _, event := range events {
		if event.EventType() == want {
			return true
		}
	}
	return false
}

func relayEventTypes(events []RelayEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.EventType())
	}
	return types
}

func findErrorEvent(t *testing.T, events []RelayEvent) ErrorEvent {
	t.Helper()
	for _, event := range events {
		if errorEvent, ok := event.(ErrorEvent); ok {
			return errorEvent
		}
	}
	t.Fatal("missing error event")
	return ErrorEvent{}
}

func assertSessionFlagsJSON(t *testing.T, raw string, resumeSessionID *string, continueSession bool) {
	t.Helper()
	var flags struct {
		ResumeSessionID *string `json:"resumeSessionId"`
		ContinueSession bool    `json:"continueSession"`
	}
	if err := json.Unmarshal([]byte(raw), &flags); err != nil {
		t.Fatalf("failed to decode flags: %v", err)
	}
	if continueSession != flags.ContinueSession {
		t.Fatalf("unexpected continue flag: %#v", flags)
	}
	if !equalStringPointer(flags.ResumeSessionID, resumeSessionID) {
		t.Fatalf("unexpected resume session id: %#v want %#v", flags.ResumeSessionID, resumeSessionID)
	}
}

func equalStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func stringPtr(value string) *string {
	return &value
}

func rawEventTypePresent(events []RawClaudeEvent, want RawClaudeEventType) bool {
	for _, event := range events {
		if event.Type == want {
			return true
		}
	}
	return false
}

func splitNonEmptyLines(text string) []string {
	parts := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	return parts
}

func containsAll(text string, wants []string) bool {
	for _, want := range wants {
		if !strings.Contains(text, want) {
			return false
		}
	}
	return true
}
