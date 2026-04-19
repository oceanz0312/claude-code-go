package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	claudecodego "claude-code-go"
)

type TimestampedRawEvent struct {
	Timestamp string                      `json:"timestamp"`
	Event     claudecodego.RawClaudeEvent `json:"event"`
}

type CaseArtifactPayload struct {
	CaseName              string
	AuthMode              string
	ArtifactDir           string
	InputSummary          map[string]any
	SessionOptionsSummary map[string]any
	RawEvents             []TimestampedRawEvent
	RelayEvents           []map[string]any
	FinalResponse         string
	Metadata              map[string]any
}

var (
	runIDOnce sync.Once
	runID     string
)

func CreateArtifactDir(artifactRoot, caseName string) (string, error) {
	runIDOnce.Do(func() {
		runID = createRunID()
	})
	dir := filepath.Join(artifactRoot, runID, sanitizeCaseName(caseName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func WriteCaseArtifacts(payload CaseArtifactPayload) error {
	if err := os.MkdirAll(payload.ArtifactDir, 0o755); err != nil {
		return err
	}

	rawEventLogFiles, err := collectRawEventLogFiles(payload.ArtifactDir)
	if err != nil {
		return err
	}

	if err := writePrettyJSON(filepath.Join(payload.ArtifactDir, "input.json"), payload.InputSummary); err != nil {
		return err
	}
	if err := writePrettyJSON(filepath.Join(payload.ArtifactDir, "relay-events.json"), payload.RelayEvents); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(payload.ArtifactDir, "raw-events.ndjson"), payload.RawEvents); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(payload.ArtifactDir, "final-response.txt"), []byte(payload.FinalResponse), 0o644); err != nil {
		return err
	}

	summary := map[string]any{
		"caseName":              payload.CaseName,
		"authMode":              payload.AuthMode,
		"artifactDir":           payload.ArtifactDir,
		"rawEventCount":         len(payload.RawEvents),
		"relayEventCount":       len(payload.RelayEvents),
		"sessionOptionsSummary": payload.SessionOptionsSummary,
		"inputSummary":          payload.InputSummary,
		"rawEventLogFiles":      rawEventLogFiles,
		"metadata":              payload.Metadata,
	}
	if err := writePrettyJSON(filepath.Join(payload.ArtifactDir, "summary.json"), summary); err != nil {
		return err
	}

	transcript := buildTerminalTranscript(payload, rawEventLogFiles)
	if err := os.WriteFile(filepath.Join(payload.ArtifactDir, "terminal-transcript.txt"), []byte(transcript), 0o644); err != nil {
		return err
	}
	return nil
}

func buildTerminalTranscript(payload CaseArtifactPayload, rawEventLogFiles []string) string {
	lines := []string{
		fmt.Sprintf("[E2E] case=%s", payload.CaseName),
		fmt.Sprintf("[E2E] auth_mode=%s", payload.AuthMode),
		fmt.Sprintf("[E2E] options=%s", mustJSON(payload.SessionOptionsSummary)),
		fmt.Sprintf("[E2E] input=%s", mustJSON(payload.InputSummary)),
		fmt.Sprintf("[E2E] raw_event_count=%d", len(payload.RawEvents)),
		fmt.Sprintf("[E2E] relay_event_count=%d", len(payload.RelayEvents)),
		fmt.Sprintf("[E2E] raw_event_log_files=%s", firstNonEmptyString(strings.Join(rawEventLogFiles, ","), "<none>")),
		fmt.Sprintf("[E2E] final_response=%s", payload.FinalResponse),
		fmt.Sprintf("[E2E] artifact_dir=%s", payload.ArtifactDir),
	}
	return strings.Join(lines, "\n") + "\n"
}

func PrintCaseSummary(caseName, authMode, artifactDir string, rawEvents []TimestampedRawEvent, relayEvents []map[string]any, finalResponse string, inputSummary, sessionOptionsSummary map[string]any) {
	lines := []string{
		fmt.Sprintf("[E2E] case=%s", caseName),
		fmt.Sprintf("[E2E] auth_mode=%s", authMode),
		fmt.Sprintf("[E2E] options=%s", mustJSON(sessionOptionsSummary)),
		fmt.Sprintf("[E2E] input=%s", mustJSON(inputSummary)),
		fmt.Sprintf("[E2E] raw_event_count=%d", len(rawEvents)),
		fmt.Sprintf("[E2E] relay_event_count=%d", len(relayEvents)),
		fmt.Sprintf("[E2E] final_response=%s", finalResponse),
		fmt.Sprintf("[E2E] artifact_dir=%s", artifactDir),
	}
	for _, line := range lines {
		fmt.Println(line)
	}
}

func writePrettyJSON(path string, value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	return os.WriteFile(path, bytes, 0o644)
}

func writeNDJSON(path string, values any) error {
	bytes, err := json.Marshal(values)
	if err != nil {
		return err
	}
	var decoded []json.RawMessage
	if err := json.Unmarshal(bytes, &decoded); err != nil {
		return err
	}
	if len(decoded) == 0 {
		return os.WriteFile(path, []byte{}, 0o644)
	}
	lines := make([]string, 0, len(decoded))
	for _, item := range decoded {
		lines = append(lines, string(item))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func collectRawEventLogFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "claude-raw-events-") && strings.HasSuffix(name, ".ndjson") {
			files = append(files, name)
		}
	}
	return files, nil
}

func createRunID() string {
	return fmt.Sprintf("%s-%d", time.Now().UTC().Format("2006-01-02T15-04-05-000Z07-00"), os.Getpid())
}

func sanitizeCaseName(caseName string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	return re.ReplaceAllString(caseName, "-")
}

func mustJSON(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}
