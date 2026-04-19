package claudecodego

type ClaudeCode struct {
	exec    *ClaudeCodeExec
	options ClaudeCodeOptions
}

func NewClaudeCode(options ClaudeCodeOptions) *ClaudeCode {
	normalizedEnv := mergeClaudeEnv(options)
	normalizedOptions := options
	normalizedOptions.Env = normalizedEnv

	return &ClaudeCode{
		exec:    NewClaudeCodeExec(options.CLIPath, normalizedEnv),
		options: normalizedOptions,
	}
}

func (client *ClaudeCode) StartSession(options SessionOptions) *Session {
	return NewSession(client.exec, client.options, options, "", false)
}

func (client *ClaudeCode) ResumeSession(sessionID string, options SessionOptions) *Session {
	return NewSession(client.exec, client.options, options, sessionID, false)
}

func (client *ClaudeCode) ContinueSession(options SessionOptions) *Session {
	return NewSession(client.exec, client.options, options, "", true)
}

func mergeClaudeEnv(options ClaudeCodeOptions) map[string]string {
	env := cloneStringMap(options.Env)
	if options.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = options.APIKey
	}
	if options.AuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = options.AuthToken
	}
	if options.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = options.BaseURL
	}
	return env
}
