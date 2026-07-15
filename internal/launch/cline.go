package launch

import (
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// cline configures the Cline CLI by writing its providers.json and legacy
// globalState.json. Mirrors D:\repo10\ollama\cmd\launch\cline.go.
type cline struct{}

const clineProvider = "ollama"

func (c *cline) Display() string { return "Cline" }

func (c *cline) FindBin() (string, bool) { return lookInstalled("cline") }

func (c *cline) Prepare(model string, host *url.URL, extra []string, apiKey string) (args, env []string, err error) {
	home, err := homeDir()
	if err != nil {
		return nil, nil, err
	}

	providersPath := filepath.Join(home, ".cline", "data", "settings", "providers.json")
	legacyPath := filepath.Join(home, ".cline", "data", "globalState.json")

	if err := writeClineProviders(providersPath, model, host, apiKey); err != nil {
		return nil, nil, err
	}
	if err := writeClineLegacyState(legacyPath, model, host); err != nil {
		return nil, nil, err
	}
	// Cline reads its provider/model from these files; only pass extra through.
	return extra, nil, nil
}

func writeClineProviders(path, model string, host *url.URL, apiKey string) error {
	config, err := readJSONMap(path)
	if err != nil {
		return err
	}

	providers := asMap(config["providers"])
	provider := asMap(providers[clineProvider])
	settings := asMap(provider["settings"])

	baseURL := hostV1(host)
	previousModel, _ := settings["model"].(string)
	previousBaseURL, _ := settings["baseUrl"].(string)
	previousTokenSource, _ := provider["tokenSource"].(string)

	settings["provider"] = clineProvider
	settings["model"] = model
	settings["baseUrl"] = baseURL
	// The default open server needs no key; only set one when the operator
	// configured a shared secret, otherwise leave apiKey absent as before.
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		settings["apiKey"] = apiKey
	} else {
		delete(settings, "apiKey")
	}
	provider["settings"] = settings

	if previousModel != model || previousBaseURL != baseURL || previousTokenSource != "manual" {
		provider["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	} else if _, ok := provider["updatedAt"].(string); !ok {
		provider["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	provider["tokenSource"] = "manual"
	providers[clineProvider] = provider

	config["version"] = float64(1)
	config["lastUsedProvider"] = clineProvider
	config["providers"] = providers

	return writeJSON(path, config)
}

func writeClineLegacyState(path, model string, host *url.URL) error {
	config, err := readJSONMap(path)
	if err != nil {
		return err
	}

	root := hostRoot(host)
	config["ollamaBaseUrl"] = root
	config["actModeApiProvider"] = clineProvider
	config["actModeOllamaModelId"] = model
	config["actModeOllamaBaseUrl"] = root
	config["planModeApiProvider"] = clineProvider
	config["planModeOllamaModelId"] = model
	config["planModeOllamaBaseUrl"] = root
	config["welcomeViewCompleted"] = true

	return writeJSON(path, config)
}
