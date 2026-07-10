package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setHome points the user's home directory at a temp dir for the duration of a
// test, so config reads/writes stay isolated.
func setHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // Windows
	return dir
}

func writeLiteConfig(t *testing.T, home, contents string) string {
	t.Helper()
	path := filepath.Join(home, ".ollama-lite", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func readLiteConfig(t *testing.T, home string) launchConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".ollama-lite", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg launchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return cfg
}

func TestLaunchDefaultModel(t *testing.T) {
	home := setHome(t)

	// No file → empty.
	if got := LaunchDefaultModel("claude"); got != "" {
		t.Fatalf("missing file: got %q, want \"\"", got)
	}

	// Per-app model wins over last_model.
	writeLiteConfig(t, home, `{
		"integrations": {"claude": {"models": ["gpt-oss:120b"]}},
		"last_model": "qwen3-coder:480b"
	}`)
	if got := LaunchDefaultModel("claude"); got != "gpt-oss:120b" {
		t.Fatalf("per-app: got %q, want gpt-oss:120b", got)
	}
	// Case-insensitive app name.
	if got := LaunchDefaultModel("Claude"); got != "gpt-oss:120b" {
		t.Fatalf("case-insensitive: got %q, want gpt-oss:120b", got)
	}
	// App without an entry falls back to last_model.
	if got := LaunchDefaultModel("codex"); got != "qwen3-coder:480b" {
		t.Fatalf("last_model fallback: got %q, want qwen3-coder:480b", got)
	}

	// Only last_model set.
	writeLiteConfig(t, home, `{"last_model": "deepseek-v3.1:671b"}`)
	if got := LaunchDefaultModel("claude"); got != "deepseek-v3.1:671b" {
		t.Fatalf("last_model only: got %q, want deepseek-v3.1:671b", got)
	}

	// Empty config → empty.
	writeLiteConfig(t, home, `{}`)
	if got := LaunchDefaultModel("claude"); got != "" {
		t.Fatalf("empty config: got %q, want \"\"", got)
	}
}

func TestSaveLaunchModelPreservesFields(t *testing.T) {
	home := setHome(t)

	writeLiteConfig(t, home, `{
		"integrations": {
			"claude": {"models": ["old-model"], "onboarded": true, "aliases": {"a": "b"}},
			"codex":  {"models": ["qwen3-coder:480b"]}
		},
		"last_model": "old-model",
		"last_selection": "claude"
	}`)

	if err := SaveLaunchModel("claude", "glm-5.2"); err != nil {
		t.Fatalf("SaveLaunchModel: %v", err)
	}

	cfg := readLiteConfig(t, home)
	claude := cfg.Integrations["claude"]
	if claude == nil || len(claude.Models) != 1 || claude.Models[0] != "glm-5.2" {
		t.Fatalf("claude.models = %+v, want [glm-5.2]", claude)
	}
	if cfg.LastModel != "glm-5.2" {
		t.Fatalf("last_model = %q, want glm-5.2", cfg.LastModel)
	}
	// Preserved fields on the edited entry.
	if !claude.Onboarded {
		t.Fatal("claude.onboarded not preserved")
	}
	if claude.Aliases["a"] != "b" {
		t.Fatalf("claude.aliases not preserved: %+v", claude.Aliases)
	}
	// Unrelated entry and top-level field preserved.
	if codex := cfg.Integrations["codex"]; codex == nil || len(codex.Models) != 1 || codex.Models[0] != "qwen3-coder:480b" {
		t.Fatalf("codex entry not preserved: %+v", codex)
	}
	if cfg.LastSelection != "claude" {
		t.Fatalf("last_selection = %q, want claude", cfg.LastSelection)
	}

	// Backup of the pre-write file exists.
	if _, err := os.Stat(filepath.Join(home, ".ollama-lite", "config.json.ollama-lite.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	// The official Ollama config must never be touched.
	if _, err := os.Stat(filepath.Join(home, ".ollama", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("~/.ollama/config.json should not exist, stat err = %v", err)
	}
}

func TestSaveLaunchModelCreatesFileAndRoundTrips(t *testing.T) {
	setHome(t)

	if err := SaveLaunchModel("droid", "gpt-oss:120b"); err != nil {
		t.Fatalf("SaveLaunchModel: %v", err)
	}
	if got := LaunchDefaultModel("droid"); got != "gpt-oss:120b" {
		t.Fatalf("round-trip: got %q, want gpt-oss:120b", got)
	}
}

func TestSaveLaunchModelNoOpWhenUnchanged(t *testing.T) {
	home := setHome(t)

	if err := SaveLaunchModel("claude", "gpt-oss:120b"); err != nil {
		t.Fatalf("first save: %v", err)
	}
	path := filepath.Join(home, ".ollama-lite", "config.json")
	bak := path + ".ollama-lite.bak"
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatalf("no backup expected after first write, stat err = %v", err)
	}

	// Identical save must not rewrite (so no backup is produced).
	if err := SaveLaunchModel("claude", "gpt-oss:120b"); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if _, err := os.Stat(bak); !os.IsNotExist(err) {
		t.Fatal("identical SaveLaunchModel should have been a no-op (backup found)")
	}
}
