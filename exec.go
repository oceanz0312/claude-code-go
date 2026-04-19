package claudecodego

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type ExecRequest struct {
	Input           string
	InputItems      []InputItem
	Images          []string
	ResumeSessionID string
	ContinueSession bool
	SessionOptions  SessionOptions
	CLIPath         string
	Env             map[string]string
	Signal          context.Context
	RawEventLogger  RawEventLogger
	OnRawEvent      func(RawClaudeEvent)
	OnLine          func(string)
}

type ClaudeCodeExec struct {
	cliPath     string
	envOverride map[string]string
}

func NewClaudeCodeExec(cliPath string, env map[string]string) *ClaudeCodeExec {
	if cliPath == "" {
		cliPath = "claude"
	}

	return &ClaudeCodeExec{
		cliPath:     cliPath,
		envOverride: cloneStringMap(env),
	}
}

func (runner *ClaudeCodeExec) Run(ctx context.Context, request ExecRequest) error {
	if ctx == nil {
		return errNilContext
	}

	runCtx, cleanup := bindExecContext(ctx, request.Signal)
	defer cleanup()

	stdinPayload, err := buildStdinPayload(request)
	if err != nil {
		return err
	}

	commandArgs, err := buildArgs(request, stdinPayload != "")
	if err != nil {
		return err
	}

	commandPath := request.CLIPath
	if commandPath == "" {
		commandPath = runner.cliPath
	}

	emitRawEvent := func(event RawClaudeEvent) {
		if request.RawEventLogger != nil {
			request.RawEventLogger.Log(event)
		}
		if request.OnRawEvent != nil {
			request.OnRawEvent(event)
		}
	}

	cmd := exec.CommandContext(runCtx, commandPath, commandArgs...)
	env := mergeExecEnv(runner.envOverride, request.Env)
	if request.SessionOptions.CWD != "" {
		env["PWD"] = request.SessionOptions.CWD
	}
	cmd.Env = formatEnv(env)
	cmd.Dir = request.SessionOptions.CWD

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		emitRawEvent(newProcessErrorEvent(err))
		return err
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		emitRawEvent(newProcessErrorEvent(err))
		return err
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		emitRawEvent(newProcessErrorEvent(err))
		return err
	}

	if err := cmd.Start(); err != nil {
		emitRawEvent(newProcessErrorEvent(err))
		return err
	}

	emitRawEvent(RawClaudeEvent{
		Type:    RawEventSpawn,
		Command: commandPath,
		Args:    append([]string(nil), commandArgs...),
		CWD:     request.SessionOptions.CWD,
	})

	func() {
		defer func() {
			_ = stdinPipe.Close()
			emitRawEvent(RawClaudeEvent{Type: RawEventStdinClosed})
		}()

		if stdinPayload == "" {
			return
		}

		_, _ = io.WriteString(stdinPipe, stdinPayload)
	}()

	var (
		stderrMu      sync.Mutex
		stderrBuilder strings.Builder
		stdoutErr     error
		stderrErr     error
		waitGroup     sync.WaitGroup
	)

	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		stdoutErr = readStdoutLines(stdoutPipe, emitRawEvent, request.OnLine)
	}()
	go func() {
		defer waitGroup.Done()
		stderrErr = readStderrEvents(stderrPipe, emitRawEvent, &stderrMu, &stderrBuilder)
	}()

	waitGroup.Wait()
	waitErr := cmd.Wait()

	if stdoutErr != nil {
		return stdoutErr
	}
	if stderrErr != nil {
		return stderrErr
	}

	exitCode, exitSignal := processExitDetails(cmd.ProcessState)
	emitRawEvent(RawClaudeEvent{
		Type:   RawEventExit,
		Code:   exitCode,
		Signal: exitSignal,
	})

	if waitErr == nil && exitSignal == "" && exitCode != nil && *exitCode == 0 {
		return nil
	}

	stderrMu.Lock()
	stderrText := stderrBuilder.String()
	stderrMu.Unlock()

	detail := fmt.Sprintf("code %d", 1)
	if exitSignal != "" {
		detail = "signal " + exitSignal
	} else if exitCode != nil {
		detail = fmt.Sprintf("code %d", *exitCode)
	}

	if stderrText != "" {
		return fmt.Errorf("Claude CLI exited with %s: %s", detail, stderrText)
	}
	if waitErr != nil {
		return fmt.Errorf("Claude CLI exited with %s", detail)
	}

	return fmt.Errorf("Claude CLI exited with %s", detail)
}

func buildArgs(request ExecRequest, useStreamJSONInput bool) ([]string, error) {
	opts := request.SessionOptions
	args := make([]string, 0, 64)

	args = append(args, "-p")
	if !useStreamJSONInput {
		args = append(args, request.Input)
	}
	if useStreamJSONInput {
		args = append(args, "--input-format", "stream-json")
	}

	args = append(args, "--output-format", "stream-json")

	if opts.Verbose == nil || *opts.Verbose {
		args = append(args, "--verbose")
	}

	if opts.IncludePartialMessages == nil || *opts.IncludePartialMessages {
		args = append(args, "--include-partial-messages")
	}

	if request.ContinueSession {
		args = append(args, "--continue")
	} else if request.ResumeSessionID != "" {
		args = append(args, "--resume", request.ResumeSessionID)
	}

	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}
	if opts.ForkSession {
		args = append(args, "--fork-session")
	}

	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}

	for _, dir := range opts.AdditionalDirectories {
		args = append(args, "--add-dir", dir)
	}

	if opts.MaxTurns != nil {
		args = append(args, "--max-turns", strconv.Itoa(*opts.MaxTurns))
	}

	if opts.MaxBudgetUSD != nil {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(*opts.MaxBudgetUSD, 'f', -1, 64))
	}

	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	} else if opts.SystemPromptFile != "" {
		args = append(args, "--system-prompt-file", opts.SystemPromptFile)
	}

	if opts.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendSystemPrompt)
	}
	if opts.AppendSystemPromptFile != "" {
		args = append(args, "--append-system-prompt-file", opts.AppendSystemPromptFile)
	}

	if opts.DangerouslySkipPermissions {
		args = append(args, "--dangerously-skip-permissions")
	} else if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", string(opts.PermissionMode))
	}

	for _, tool := range opts.AllowedTools {
		args = append(args, "--allowedTools", tool)
	}

	for _, tool := range opts.DisallowedTools {
		args = append(args, "--disallowedTools", tool)
	}

	if opts.Tools != "" {
		args = append(args, "--tools", opts.Tools)
	}

	if opts.PermissionPromptTool != "" {
		args = append(args, "--permission-prompt-tool", opts.PermissionPromptTool)
	}

	for _, config := range stringSliceValue(opts.MCPConfig) {
		args = append(args, "--mcp-config", config)
	}
	if opts.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}

	if opts.Effort != "" {
		args = append(args, "--effort", string(opts.Effort))
	}

	if opts.FallbackModel != "" {
		args = append(args, "--fallback-model", opts.FallbackModel)
	}

	if opts.Bare {
		args = append(args, "--bare")
	}

	if opts.NoSessionPersistence {
		args = append(args, "--no-session-persistence")
	}

	if opts.Chrome != nil {
		if *opts.Chrome {
			args = append(args, "--chrome")
		} else {
			args = append(args, "--no-chrome")
		}
	}

	if opts.Agents != nil {
		agentsValue, err := jsonFlagValue(opts.Agents)
		if err != nil {
			return nil, err
		}
		args = append(args, "--agents", agentsValue)
	}
	if opts.Agent != "" {
		args = append(args, "--agent", opts.Agent)
	}

	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}

	if opts.Settings != "" {
		args = append(args, "--settings", opts.Settings)
	}

	args = append(args, "--setting-sources", opts.SettingSources)

	if opts.IncludeHookEvents {
		args = append(args, "--include-hook-events")
	}

	if opts.Betas != "" {
		args = append(args, "--betas", opts.Betas)
	}

	if opts.Worktree != "" {
		args = append(args, "--worktree", opts.Worktree)
	}

	if opts.DisableSlashCommands {
		args = append(args, "--disable-slash-commands")
	}

	for _, dir := range stringSliceValue(opts.PluginDir) {
		args = append(args, "--plugin-dir", dir)
	}

	if opts.ExcludeDynamicSystemPromptSections {
		args = append(args, "--exclude-dynamic-system-prompt-sections")
	}

	switch value := opts.Debug.(type) {
	case bool:
		if value {
			args = append(args, "--debug")
		}
	case string:
		if value != "" {
			args = append(args, "--debug", value)
		}
	}

	if opts.DebugFile != "" {
		args = append(args, "--debug-file", opts.DebugFile)
	}

	if opts.JSONSchema != nil {
		schemaValue, err := jsonFlagValue(opts.JSONSchema)
		if err != nil {
			return nil, err
		}
		args = append(args, "--json-schema", schemaValue)
	}

	return args, nil
}

func buildStdinPayload(request ExecRequest) (string, error) {
	items := structuredInputItems(request)
	if len(items) == 0 {
		return "", nil
	}

	content := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case InputTypeText:
			if item.Text == "" {
				continue
			}
			content = append(content, map[string]any{
				"type": "text",
				"text": item.Text,
			})
		case InputTypeLocalImage:
			imageData, err := os.ReadFile(item.Path)
			if err != nil {
				return "", err
			}
			content = append(content, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": detectImageMediaType(imageData, item.Path),
					"data":       base64.StdEncoding.EncodeToString(imageData),
				},
			})
		}
	}

	if len(content) == 0 {
		return "", nil
	}

	payload := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": content,
		},
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return string(encoded) + "\n", nil
}

func structuredInputItems(request ExecRequest) []InputItem {
	if containsLocalImage(request.InputItems) {
		return mergeTextItems(request.InputItems)
	}

	if len(request.Images) == 0 {
		return nil
	}

	items := make([]InputItem, 0, len(request.Images)+1)
	if request.Input != "" {
		items = append(items, InputItem{Type: InputTypeText, Text: request.Input})
	}
	for _, imagePath := range request.Images {
		items = append(items, InputItem{Type: InputTypeLocalImage, Path: imagePath})
	}
	return items
}

func containsLocalImage(items []InputItem) bool {
	for _, item := range items {
		if item.Type == InputTypeLocalImage {
			return true
		}
	}
	return false
}

func mergeTextItems(items []InputItem) []InputItem {
	merged := make([]InputItem, 0, len(items))
	pendingText := make([]string, 0)

	flushPendingText := func() {
		if len(pendingText) == 0 {
			return
		}
		merged = append(merged, InputItem{Type: InputTypeText, Text: strings.Join(pendingText, "\n\n")})
		pendingText = pendingText[:0]
	}

	for _, item := range items {
		if item.Type == InputTypeText {
			pendingText = append(pendingText, item.Text)
			continue
		}

		flushPendingText()
		merged = append(merged, item)
	}

	flushPendingText()
	return merged
}

func detectImageMediaType(data []byte, filePath string) string {
	if len(data) >= 8 &&
		data[0] == 0x89 &&
		data[1] == 0x50 &&
		data[2] == 0x4e &&
		data[3] == 0x47 {
		return "image/png"
	}

	if len(data) >= 3 &&
		data[0] == 0xff &&
		data[1] == 0xd8 &&
		data[2] == 0xff {
		return "image/jpeg"
	}

	if len(data) >= 6 &&
		data[0] == 0x47 &&
		data[1] == 0x49 &&
		data[2] == 0x46 {
		return "image/gif"
	}

	if len(data) >= 12 &&
		data[0] == 0x52 &&
		data[1] == 0x49 &&
		data[2] == 0x46 &&
		data[3] == 0x46 &&
		data[8] == 0x57 &&
		data[9] == 0x45 &&
		data[10] == 0x42 &&
		data[11] == 0x50 {
		return "image/webp"
	}

	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".png":
		fallthrough
	default:
		return "image/png"
	}
}

func readStdoutLines(reader io.Reader, emitRawEvent func(RawClaudeEvent), onLine func(string)) error {
	return readDelimited(reader, func(chunk string) {
		line := trimLineEnding(chunk)
		emitRawEvent(RawClaudeEvent{Type: RawEventStdoutLine, Line: line})
		if onLine != nil {
			onLine(line)
		}
	})
}

func readStderrEvents(reader io.Reader, emitRawEvent func(RawClaudeEvent), mu *sync.Mutex, builder *strings.Builder) error {
	buffer := make([]byte, 1024)
	pendingLine := make([]byte, 0, 1024)
	flushPendingLine := func() {
		if len(pendingLine) == 0 {
			return
		}
		emitRawEvent(RawClaudeEvent{Type: RawEventStderrLine, Line: trimLineEnding(string(pendingLine))})
		pendingLine = pendingLine[:0]
	}

	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			chunkBytes := append([]byte(nil), buffer[:n]...)
			chunk := string(chunkBytes)

			mu.Lock()
			builder.Write(chunkBytes)
			mu.Unlock()

			emitRawEvent(RawClaudeEvent{Type: RawEventStderrChunk, Chunk: chunk})

			pendingLine = append(pendingLine, chunkBytes...)
			for {
				newlineIndex := bytes.IndexByte(pendingLine, '\n')
				if newlineIndex < 0 {
					break
				}
				lineBytes := append([]byte(nil), pendingLine[:newlineIndex+1]...)
				emitRawEvent(RawClaudeEvent{Type: RawEventStderrLine, Line: trimLineEnding(string(lineBytes))})
				pendingLine = append([]byte(nil), pendingLine[newlineIndex+1:]...)
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			flushPendingLine()
			return nil
		}
		return err
	}
}

func readDelimited(reader io.Reader, handle func(string)) error {
	buffered := bufio.NewReader(reader)
	for {
		chunk, err := buffered.ReadString('\n')
		if chunk != "" {
			handle(chunk)
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			return nil
		}
		return err
	}
}

func trimLineEnding(value string) string {
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	return value
}

func bindExecContext(ctx context.Context, signal context.Context) (context.Context, context.CancelFunc) {
	if signal == nil {
		return ctx, func() {}
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-signal.Done():
			cancel(context.Cause(signal))
		case <-runCtx.Done():
		}
	}()
	return runCtx, func() { cancel(nil) }
}

func processExitDetails(state *os.ProcessState) (*int, string) {
	if state == nil {
		return nil, ""
	}

	if waitStatus, ok := state.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
		return nil, waitStatus.Signal().String()
	}

	exitCode := state.ExitCode()
	if exitCode < 0 {
		return nil, ""
	}

	return &exitCode, ""
}

func mergeExecEnv(base map[string]string, override map[string]string) map[string]string {
	merged := cloneStringMap(base)
	for key, value := range override {
		merged[key] = value
	}
	if _, ok := merged["PATH"]; !ok {
		if pathValue, ok := os.LookupEnv("PATH"); ok {
			merged["PATH"] = pathValue
		}
	}
	return merged
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func formatEnv(env map[string]string) []string {
	formatted := make([]string, 0, len(env))
	for key, value := range env {
		formatted = append(formatted, key+"="+value)
	}
	return formatted
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func jsonFlagValue(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
