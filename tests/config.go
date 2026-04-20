package tests

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	claudecodego "github.com/oceanz0312/claude-code-go"
)

type AuthMode string

const (
	AuthModeAPIKey    AuthMode = "api-key"
	AuthModeAuthToken AuthMode = "auth-token"
)

type E2ESecrets struct {
	Model     string
	APIKey    string
	AuthToken string
	BaseURL   string
}

type E2EConfig struct {
	RepoRoot              string
	ArtifactRoot          string
	RealCLIPath           string
	Secrets               E2ESecrets
	Model                 string
	DefaultSessionOptions claudecodego.SessionOptions
}

var cachedConfig *E2EConfig

func LoadE2EConfig() (*E2EConfig, error) {
	if cachedConfig != nil {
		return cachedConfig, nil
	}

	repoRoot, err := filepath.Abs(filepath.Join(".", ".."))
	if err != nil {
		return nil, err
	}

	secrets, err := loadSecretsFromEnv()
	if err != nil {
		return nil, err
	}

	config := &E2EConfig{
		RepoRoot:     repoRoot,
		ArtifactRoot: filepath.Join(repoRoot, "tests", "e2e", "artifacts"),
		RealCLIPath:  filepath.Join("/Users/bytedance/Documents/ttls_repo/agent-sdk/node_modules/@anthropic-ai/claude-code", "cli.js"),
		Secrets:      secrets,
		Model:        firstNonEmptyString(strings.TrimSpace(secrets.Model), "sonnet"),
		DefaultSessionOptions: claudecodego.SessionOptions{
			Bare:                       true,
			SettingSources:             "",
			Verbose:                    boolPtr(true),
			IncludePartialMessages:     boolPtr(true),
			DangerouslySkipPermissions: true,
		},
	}

	cachedConfig = config
	return config, nil
}

func GetClientOptions(secrets E2ESecrets, authMode AuthMode) (claudecodego.ClaudeCodeOptions, error) {
	switch authMode {
	case AuthModeAPIKey:
		if strings.TrimSpace(secrets.APIKey) == "" {
			return claudecodego.ClaudeCodeOptions{}, fmt.Errorf("E2E requires E2E_API_KEY env var for api-key cases")
		}
		return claudecodego.ClaudeCodeOptions{
			CLIPath: "/Users/bytedance/Documents/ttls_repo/agent-sdk/node_modules/@anthropic-ai/claude-code/cli.js",
			APIKey:  secrets.APIKey,
		}, nil
	case AuthModeAuthToken:
		if strings.TrimSpace(secrets.AuthToken) == "" || strings.TrimSpace(secrets.BaseURL) == "" {
			return claudecodego.ClaudeCodeOptions{}, fmt.Errorf("E2E requires both E2E_AUTH_TOKEN and E2E_BASE_URL env vars for auth-token cases")
		}
		return claudecodego.ClaudeCodeOptions{
			CLIPath:   "/Users/bytedance/Documents/ttls_repo/agent-sdk/node_modules/@anthropic-ai/claude-code/cli.js",
			AuthToken: secrets.AuthToken,
			BaseURL:   secrets.BaseURL,
		}, nil
	default:
		return claudecodego.ClaudeCodeOptions{}, fmt.Errorf("unsupported auth mode: %s", authMode)
	}
}

func ListAvailableAuthModes() ([]AuthMode, error) {
	config, err := LoadE2EConfig()
	if err != nil {
		return nil, err
	}
	modes := make([]AuthMode, 0, 2)
	if strings.TrimSpace(config.Secrets.APIKey) != "" {
		modes = append(modes, AuthModeAPIKey)
	}
	if strings.TrimSpace(config.Secrets.AuthToken) != "" && strings.TrimSpace(config.Secrets.BaseURL) != "" {
		modes = append(modes, AuthModeAuthToken)
	}
	return modes, nil
}

// loadDotEnv reads a .env file and populates os environment. Existing env
// vars are not overwritten so shell-level exports always win.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// strip optional surrounding quotes
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

// findDotEnv walks up from dir looking for a .env file, stopping at the
// module root (where go.mod lives) or the filesystem root.
func findDotEnv(dir string) string {
	for {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func loadSecretsFromEnv() (E2ESecrets, error) {
	// Auto-load .env when running via `go test` without a prior `source .env`.
	if abs, err := filepath.Abs("."); err == nil {
		if p := findDotEnv(abs); p != "" {
			_ = loadDotEnv(p) // best-effort; missing file is fine
		}
	}

	secrets := E2ESecrets{
		Model:     getOptionalString(os.Getenv("E2E_MODEL")),
		APIKey:    getOptionalString(os.Getenv("E2E_API_KEY")),
		AuthToken: getOptionalString(os.Getenv("E2E_AUTH_TOKEN")),
		BaseURL:   getOptionalString(os.Getenv("E2E_BASE_URL")),
	}
	if secrets.AuthToken == "" && secrets.APIKey == "" {
		return E2ESecrets{}, fmt.Errorf("No E2E_AUTH_TOKEN or E2E_API_KEY env var found. Copy .env.example to .env, fill in values, and source it before running go test")
	}
	return secrets, nil
}

func getOptionalString(value string) string {
	return strings.TrimSpace(value)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func boolPtr(value bool) *bool {
	return &value
}
