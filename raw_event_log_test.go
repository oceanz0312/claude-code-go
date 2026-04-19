package claudecodego

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRawEventLoggerRejectsRelativeRawEventLogPaths(t *testing.T) {
	_, err := CreateRawEventLogger("relative/raw-events")
	if err == nil {
		t.Fatal("expected error")
	}
	if got, want := err.Error(), `rawEventLog path must be an absolute path, got: "relative/raw-events"`; got != want {
		t.Fatalf("unexpected error: %q want %q", got, want)
	}
}

func TestRawEventLoggerUsesDefaultAgentLogsDirectoryAndSerializesProcessErrors(t *testing.T) {
	tempDir := t.TempDir()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(originalWD) }()

	logger, err := CreateRawEventLogger(true)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	logger.Log(newProcessErrorEvent(errors.New("raw logger boom")))
	if err := logger.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	logger.Log(RawClaudeEvent{Type: RawEventSpawn, Command: "ignored", Args: []string{}})
	if err := logger.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}

	logDir := filepath.Join(tempDir, "agent_logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
	logText, err := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	lines := splitNonEmptyLines(string(logText))
	if len(lines) != 1 {
		t.Fatalf("expected 1 record, got %d", len(lines))
	}
	var record struct {
		Timestamp string `json:"timestamp"`
		Event     struct {
			Type  string `json:"type"`
			Error struct {
				Name    string `json:"name"`
				Message string `json:"message"`
				Stack   string `json:"stack"`
			} `json:"error"`
		} `json:"event"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("failed to decode record: %v", err)
	}
	if record.Timestamp == "" {
		t.Fatal("expected timestamp")
	}
	if record.Event.Type != string(RawEventProcessError) {
		t.Fatalf("unexpected event type: %q", record.Event.Type)
	}
	if record.Event.Error.Name != "Error" {
		t.Fatalf("unexpected error name: %q", record.Event.Error.Name)
	}
	if record.Event.Error.Message != "raw logger boom" {
		t.Fatalf("unexpected error message: %q", record.Event.Error.Message)
	}
	if record.Event.Error.Stack == "" {
		t.Fatal("expected error stack")
	}
}

func TestRawEventLoggerWaitsForDrainBeforeClosingAfterABackpressuredWrite(t *testing.T) {
	logDir := t.TempDir()
	logger, err := CreateRawEventLogger(logDir)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	logger.Log(RawClaudeEvent{Type: RawEventStderrChunk, Chunk: stringsRepeat("x", 1024*1024)})
	if err := logger.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
	logText, err := os.ReadFile(filepath.Join(logDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if !strings.Contains(string(logText), `"type":"stderr_chunk"`) {
		t.Fatalf("unexpected log contents: %s", string(logText[:minInt(len(logText), 200)]))
	}
	if len(logText) <= 1024*1024 {
		t.Fatalf("expected log file larger than 1MiB, got %d", len(logText))
	}
}

func TestRawEventLoggerRethrowsFatalStreamErrorsCapturedBeforeCloseCompletes(t *testing.T) {
	logDir := t.TempDir()
	originalNow := rawEventLogNow
	originalSuffix := rawEventLogRandSuffix
	defer func() {
		rawEventLogNow = originalNow
		rawEventLogRandSuffix = originalSuffix
	}()

	rawEventLogNow = func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 678000000, time.UTC)
	}
	rawEventLogRandSuffix = func() string { return "4fzzzx" }

	blockedPath := filepath.Join(logDir, fmt.Sprintf("claude-raw-events-2026-01-02T03-04-05-678Z-%d-4fzzzx.ndjson", os.Getpid()))
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("failed to create blocked path: %v", err)
	}

	logger, err := CreateRawEventLogger(logDir)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	logger.Log(RawClaudeEvent{Type: RawEventSpawn, Command: "claude", Args: []string{}})
	if err := logger.Close(); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("unexpected close error: %v", err)
	}
}

func TestRawEventLoggerThrowsCloseErrorsWhenTheUnderlyingStreamCannotOpen(t *testing.T) {
	logDir := t.TempDir()
	originalNow := rawEventLogNow
	originalSuffix := rawEventLogRandSuffix
	defer func() {
		rawEventLogNow = originalNow
		rawEventLogRandSuffix = originalSuffix
	}()
	rawEventLogNow = func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 678000000, time.UTC)
	}
	rawEventLogRandSuffix = func() string { return "existing" }

	blockedPath := filepath.Join(logDir, fmt.Sprintf("claude-raw-events-2026-01-02T03-04-05-678Z-%d-existing.ndjson", os.Getpid()))
	if err := os.MkdirAll(blockedPath, 0o755); err != nil {
		t.Fatalf("failed to create blocked path: %v", err)
	}

	logger, err := CreateRawEventLogger(logDir)
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	logger.Log(RawClaudeEvent{Type: RawEventSpawn, Command: "claude", Args: []string{}})
	if err := logger.Close(); err == nil {
		t.Fatal("expected close error")
	}
}

func stringsRepeat(value string, count int) string {
	buffer := make([]byte, len(value)*count)
	position := 0
	for index := 0; index < count; index++ {
		copy(buffer[position:], value)
		position += len(value)
	}
	return string(buffer)
}
func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
