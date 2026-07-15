package launch

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// qwen configures Qwen Code by writing ~/.qwen/settings.json plus OpenAI-style
// environment variables. Mirrors D:\repo10\ollama\cmd\launch\qwen.go.
type qwen struct{}

const qwenEnvKey = "OLLAMA_API_KEY"

func (q *qwen) Display() string { return "Qwen Code" }

func (q *qwen) FindBin() (string, bool) {
	if p, err := exec.LookPath("qwen"); err == nil {
		return p, true
	}
	home, err := homeDir()
	if err != nil {
		return "", false
	}

	var candidates []string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		candidates = []string{
			filepath.Join(appData, "npm", "qwen.cmd"),
			filepath.Join(appData, "npm", "qwen.exe"),
			filepath.Join(localAppData, "npm", "qwen.cmd"),
			filepath.Join(localAppData, "npm", "qwen.exe"),
		}
	default:
		candidates = []string{
			filepath.Join(home, ".npm-global", "bin", "qwen"),
			filepath.Join(home, ".local", "bin", "qwen"),
			"/usr/local/bin/qwen",
		}
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func (q *qwen) Prepare(model string, host *url.URL, extra []string, apiKey string) (args, env []string, err error) {
	if err := q.writeConfig(model, host, apiKey); err != nil {
		return nil, nil, err
	}

	env = []string{
		"OPENAI_API_KEY=" + effectiveAPIKey(apiKey),
		"OPENAI_BASE_URL=" + hostV1(host),
		"OPENAI_MODEL=" + model,
	}
	args = qwenArgs(model, extra)
	return args, env, nil
}

func (q *qwen) writeConfig(model string, host *url.URL, apiKey string) error {
	home, err := homeDir()
	if err != nil {
		return err
	}
	configPath := filepath.Join(home, ".qwen", "settings.json")

	cfg, err := readJSONMap(configPath)
	if err != nil {
		return err
	}
	applyQwenConfig(cfg, model, host, apiKey)
	return writeJSON(configPath, cfg)
}

func applyQwenConfig(cfg map[string]any, model string, host *url.URL, apiKey string) {
	baseURL := hostV1(host)

	envCfg := asMap(cfg["env"])
	envCfg[qwenEnvKey] = effectiveAPIKey(apiKey)
	cfg["env"] = envCfg

	provider := map[string]any{
		"id":      model,
		"name":    fmt.Sprintf("%s (Ollama)", model),
		"baseUrl": baseURL,
		"envKey":  qwenEnvKey,
	}
	modelProviders := asMap(cfg["modelProviders"])
	modelProviders["openai"] = mergeQwenProviders(modelProviders["openai"], provider, baseURL)
	cfg["modelProviders"] = modelProviders

	security := asMap(cfg["security"])
	auth := asMap(security["auth"])
	auth["selectedType"] = "openai"
	auth["baseUrl"] = baseURL
	security["auth"] = auth
	cfg["security"] = security

	modelCfg := asMap(cfg["model"])
	modelCfg["name"] = model
	cfg["model"] = modelCfg
}

// mergeQwenProviders puts the Ollama provider first and keeps any existing
// non-Ollama providers.
func mergeQwenProviders(existing any, provider map[string]any, baseURL string) []any {
	merged := []any{provider}
	list, _ := existing.([]any)
	for _, entry := range list {
		if isQwenOllamaProvider(entry, baseURL) {
			continue
		}
		merged = append(merged, entry)
	}
	return merged
}

func isQwenOllamaProvider(value any, baseURL string) bool {
	provider, ok := value.(map[string]any)
	if !ok {
		return false
	}
	envKey, _ := provider["envKey"].(string)
	url, _ := provider["baseUrl"].(string)
	return envKey == qwenEnvKey && strings.TrimRight(url, "/") == strings.TrimRight(baseURL, "/")
}

func qwenArgs(model string, extra []string) []string {
	args := append([]string{}, extra...)
	if !hasFlag(args, "--auth-type") {
		args = append([]string{"--auth-type", "openai"}, args...)
	}
	if model != "" && !hasFlag(args, "--model", "-m") {
		args = append([]string{"--model", model}, args...)
	}
	return args
}

// hasFlag reports whether args contains any of the given flag names, either as a
// bare token or in "flag=value" form.
func hasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}
