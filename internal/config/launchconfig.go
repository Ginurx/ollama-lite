package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// launchConfig is ollama-lite's own launch configuration, stored at
// ~/.ollama-lite/config.json. It deliberately mirrors the structure of the
// official Ollama ~/.ollama/config.json (integrations.<app>.models, last_model,
// last_selection) so the file is familiar and an existing Ollama config can be
// copied over. ollama-lite never writes the official file.
type launchConfig struct {
	Integrations  map[string]*integration `json:"integrations"`
	LastModel     string                  `json:"last_model,omitempty"`
	LastSelection string                  `json:"last_selection,omitempty"`
}

// integration is the per-app entry within launchConfig, matching Ollama's schema.
type integration struct {
	Models    []string          `json:"models"`
	Aliases   map[string]string `json:"aliases,omitempty"`
	Onboarded bool              `json:"onboarded,omitempty"`
}

// liteConfigPath is the path to ollama-lite's launch config (~/.ollama-lite/config.json).
func liteConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ollama-lite", "config.json"), nil
}

// loadLaunchConfig reads ~/.ollama-lite/config.json. A missing file yields an
// empty config (with an initialized Integrations map), not an error.
func loadLaunchConfig() (*launchConfig, error) {
	path, err := liteConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &launchConfig{Integrations: map[string]*integration{}}, nil
		}
		return nil, err
	}
	var cfg launchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Integrations == nil {
		cfg.Integrations = map[string]*integration{}
	}
	return &cfg, nil
}

// saveLaunchConfig writes cfg to ~/.ollama-lite/config.json, backing up any
// existing file to a sibling ".ollama-lite.bak" first.
func saveLaunchConfig(cfg *launchConfig) error {
	path, err := liteConfigPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".ollama-lite.bak", existing, 0o644)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LaunchDefaultModel returns the configured default model for launching app
// `name`, read from ~/.ollama-lite/config.json. It prefers the per-app
// integrations.<name>.models[0], then the top-level last_model. Returns "" when
// neither is set.
func LaunchDefaultModel(name string) string {
	cfg, err := loadLaunchConfig()
	if err != nil {
		return ""
	}
	if entry := cfg.Integrations[strings.ToLower(name)]; entry != nil {
		for _, m := range entry.Models {
			if m = strings.TrimSpace(m); m != "" {
				return m
			}
		}
	}
	return strings.TrimSpace(cfg.LastModel)
}

// SaveLaunchModel records `model` as the launch default for app `name` in
// ~/.ollama-lite/config.json, setting integrations.<name>.models to [model] and
// the top-level last_model while preserving the entry's other fields. It is a
// no-op when both already hold `model`. Callers treat the error as non-fatal so a
// read-only home directory never blocks a launch.
func SaveLaunchModel(name, model string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	model = strings.TrimSpace(model)
	if name == "" || model == "" {
		return nil
	}

	cfg, err := loadLaunchConfig()
	if err != nil {
		return err
	}

	entry := cfg.Integrations[name]
	if entry != nil && cfg.LastModel == model &&
		len(entry.Models) == 1 && entry.Models[0] == model {
		return nil // nothing would change
	}

	if entry == nil {
		entry = &integration{}
		cfg.Integrations[name] = entry
	}
	entry.Models = []string{model}
	cfg.LastModel = model
	return saveLaunchConfig(cfg)
}
