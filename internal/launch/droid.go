package launch

import (
	"net/url"
	"path/filepath"
	"strings"
)

// droid configures Factory's Droid CLI by writing ~/.factory/settings.json.
// Mirrors D:\repo10\ollama\cmd\launch\droid.go (minus capability enrichment).
type droid struct{}

func (d *droid) Display() string { return "Droid" }

func (d *droid) FindBin() (string, bool) { return lookInstalled("droid") }

func (d *droid) Prepare(model string, host *url.URL, extra []string, apiKey string) (args, env []string, err error) {
	home, err := homeDir()
	if err != nil {
		return nil, nil, err
	}
	settingsPath := filepath.Join(home, ".factory", "settings.json")

	settings, err := readJSONMap(settingsPath)
	if err != nil {
		return nil, nil, err
	}

	updateDroidSettings(settings, model, host, apiKey)

	if err := writeJSON(settingsPath, settings); err != nil {
		return nil, nil, err
	}
	// Droid reads its provider/model from settings.json; only pass extra through.
	return extra, nil, nil
}

// updateDroidSettings rewrites the Ollama custom model in place, preserving any
// non-Ollama custom models and unknown fields.
func updateDroidSettings(settings map[string]any, model string, host *url.URL, apiKey string) {
	// Keep only custom models pointing at a different base URL (preserving extra
	// fields). Identifying "ours" by baseUrl rather than apiKey avoids dropping or
	// duplicating a prior ollama-lite entry once a custom key is in use.
	baseURL := hostV1(host)
	var nonOllama []any
	if raw, ok := settings["customModels"].([]any); ok {
		for _, entry := range raw {
			m, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if u, _ := m["baseUrl"].(string); strings.TrimRight(u, "/") == strings.TrimRight(baseURL, "/") {
				continue
			}
			nonOllama = append(nonOllama, entry)
		}
	}

	modelID := "custom:" + model + "-0"
	ollamaModel := map[string]any{
		"model":           model,
		"displayName":     model,
		"baseUrl":         baseURL,
		"apiKey":          effectiveAPIKey(apiKey),
		"provider":        "generic-chat-completion-api",
		"maxOutputTokens": 64000,
		"supportsImages":  false,
		"id":              modelID,
		"index":           0,
	}
	settings["customModels"] = append([]any{ollamaModel}, nonOllama...)

	session := asMap(settings["sessionDefaultSettings"])
	session["model"] = modelID
	if !validReasoningEffort(session["reasoningEffort"]) {
		session["reasoningEffort"] = "none"
	}
	settings["sessionDefaultSettings"] = session
}

func validReasoningEffort(value any) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	switch s {
	case "high", "medium", "low", "none":
		return true
	}
	return false
}
