package claudecodego

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type timestampedRawEvent struct {
	Timestamp string         `json:"timestamp"`
	Event     RawClaudeEvent `json:"event"`
}

type fileRawEventLogger struct {
	mu     sync.Mutex
	filePath string
	records [][]byte
	closed bool
	fatal  error
}

var (
	rawEventLogNow = func() time.Time {
		return time.Now().UTC()
	}
	rawEventLogRandSuffix = func() string {
		const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
		buf := make([]byte, 6)
		for index := range buf {
			buf[index] = alphabet[rand.Intn(len(alphabet))]
		}
		return string(buf)
	}
)

func CreateRawEventLogger(option any) (RawEventLogger, error) {
	if option == nil {
		return noopRawEventLogger{}, nil
	}

	switch value := option.(type) {
	case bool:
		if !value {
			return noopRawEventLogger{}, nil
		}
		return newFileRawEventLogger(filepath.Join(mustGetwd(), "agent_logs"))
	case string:
		if value == "" {
			return noopRawEventLogger{}, nil
		}
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("rawEventLog path must be an absolute path, got: %q", value)
		}
		return newFileRawEventLogger(value)
	default:
		return noopRawEventLogger{}, nil
	}
}

func newFileRawEventLogger(dir string) (RawEventLogger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &fileRawEventLogger{
		filePath: filepath.Join(dir, createRawEventLogFilename()),
		records:  make([][]byte, 0, 16),
	}, nil
}

func (logger *fileRawEventLogger) Log(event RawClaudeEvent) {
	logger.mu.Lock()
	defer logger.mu.Unlock()

	if logger.closed || logger.fatal != nil {
		return
	}

	record := timestampedRawEvent{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     serializeRawClaudeEvent(event),
	}
	bytes, err := json.Marshal(record)
	if err != nil {
		logger.fatal = err
		return
	}
	logger.records = append(logger.records, append(bytes, '\n'))
}

func (logger *fileRawEventLogger) Close() error {
	logger.mu.Lock()
	if logger.closed {
		logger.mu.Unlock()
		return logger.fatal
	}
	logger.closed = true
	filePath := logger.filePath
	records := append([][]byte(nil), logger.records...)
	logger.records = nil
	fatal := logger.fatal
	logger.mu.Unlock()

	if fatal == nil {
		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fatal = err
		} else {
			writer := bufio.NewWriter(file)
			for _, record := range records {
				if _, err := writer.Write(record); err != nil {
					fatal = err
					break
				}
			}
			if fatal == nil {
				if err := writer.Flush(); err != nil {
					fatal = err
				}
			}
			if err := file.Close(); err != nil && fatal == nil {
				fatal = err
			}
		}
	}

	logger.mu.Lock()
	if logger.fatal == nil {
		logger.fatal = fatal
	}
	logger.mu.Unlock()

	return fatal
}

func serializeRawClaudeEvent(event RawClaudeEvent) RawClaudeEvent {
	if event.Type != RawEventProcessError || event.Error == nil {
		return event
	}

	clone := event
	clone.Error = &SerializedError{
		Name:    event.Error.Name,
		Message: event.Error.Message,
		Stack:   event.Error.Stack,
	}
	return clone
}

func createRawEventLogFilename() string {
	timestamp := rawEventLogNow().UTC().Format("2006-01-02T15:04:05.000Z")
	timestamp = strings.NewReplacer(":", "-", ".", "-").Replace(timestamp)
	return fmt.Sprintf("claude-raw-events-%s-%d-%s.ndjson", timestamp, os.Getpid(), rawEventLogRandSuffix())
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
