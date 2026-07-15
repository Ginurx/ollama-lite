package launch

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
)

// opencode configures OpenCode to use ollama-lite by passing an inline JSON
// config through the OPENCODE_CONFIG_CONTENT environment variable, so no config
// file is written. Mirrors D:\repo10\ollama\cmd\launch\opencode.go (minus the
// per-model capability enrichment).
type opencode struct{}

func (o *opencode) Display() string { return "OpenCode" }

func (o *opencode) FindBin() (string, bool) {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p, true
	}
	home, err := homeDir()
	if err != nil {
		return "", false
	}
	fallback := filepath.Join(home, ".opencode", "bin", exeName("opencode"))
	if _, err := os.Stat(fallback); err == nil {
		return fallback, true
	}
	return "", false
}

func (o *opencode) Prepare(model string, host *url.URL, extra []string, apiKey string) (args, env []string, err error) {
	content, err := opencodeConfig(model, host, apiKey)
	if err != nil {
		return nil, nil, err
	}
	env = []string{"OPENCODE_CONFIG_CONTENT=" + content}
	// OpenCode's model/provider come from the inline config; pass extra through.
	return extra, env, nil
}

// opencodeConfig builds the inline JSON for OPENCODE_CONFIG_CONTENT.
func opencodeConfig(model string, host *url.URL, apiKey string) (string, error) {
	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			"ollama": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "Ollama",
				"options": map[string]any{
					"baseURL": hostV1(host),
					"apiKey":  effectiveAPIKey(apiKey),
				},
				"models": map[string]any{
					model: map[string]any{"name": model},
				},
			},
		},
		"model": "ollama/" + model,
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
