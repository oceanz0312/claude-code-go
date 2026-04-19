package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	claudecodego "claude-code-go"
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

func loadSecretsFromEnv() (E2ESecrets, error) {
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
