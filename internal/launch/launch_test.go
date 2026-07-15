package launch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func testHost() *url.URL {
	return &url.URL{Scheme: "http", Host: "127.0.0.1:11434"}
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// setHome points os.UserHomeDir at a temp dir on both Unix and Windows.
func setHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestLookupAndAliases(t *testing.T) {
	if s, ok := lookup("copilot-cli"); !ok || s.name != "copilot" {
		t.Fatalf("copilot-cli should resolve to copilot, got %q ok=%v", s.name, ok)
	}
	if s, ok := lookup("CLAUDE"); !ok || s.name != "claude" {
		t.Fatalf("lookup should be case-insensitive, got %q ok=%v", s.name, ok)
	}
	if _, ok := lookup("nope"); ok {
		t.Fatal("unknown app should not resolve")
	}
}

func TestClaudePrepare(t *testing.T) {
	args, env, err := (&claude{}).Prepare("gpt-oss:120b", testHost(), []string{"--resume"}, "")
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	if m["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:11434" {
		t.Errorf("ANTHROPIC_BASE_URL = %q", m["ANTHROPIC_BASE_URL"])
	}
	if m["ANTHROPIC_AUTH_TOKEN"] != "ollama" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q", m["ANTHROPIC_AUTH_TOKEN"])
	}
	if _, ok := m["ANTHROPIC_API_KEY"]; !ok {
		t.Error("ANTHROPIC_API_KEY should be set (empty)")
	}
	if m["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "gpt-oss:120b" || m["CLAUDE_CODE_SUBAGENT_MODEL"] != "gpt-oss:120b" {
		t.Error("model env vars not set")
	}
	if _, ok := m["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; ok {
		t.Error("CLAUDE_CODE_AUTO_COMPACT_WINDOW should be absent for non-cloud models")
	}
	if !slices.Equal(args, []string{"--model", "gpt-oss:120b", "--resume"}) {
		t.Errorf("args = %v", args)
	}
}

// TestClaudePrepareCloudAutoCompact asserts that for a cloud model the
// CLAUDE_CODE_AUTO_COMPACT_WINDOW env var is enriched from the running server's
// /api/experimental/model-recommendations.
func TestClaudePrepareCloudAutoCompact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/experimental/model-recommendations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"recommendations":[
			{"model":"gpt-oss:120b","context_length":131072},
			{"model":"glm-5.2:cloud","context_length":202752}
		]}`)
	}))
	defer srv.Close()

	host := mustURL(t, srv.URL)
	args, env, err := (&claude{}).Prepare("glm-5.2:cloud", host, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	if m["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "202752" {
		t.Errorf("CLAUDE_CODE_AUTO_COMPACT_WINDOW = %q, want 202752", m["CLAUDE_CODE_AUTO_COMPACT_WINDOW"])
	}
	if m["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "glm-5.2:cloud" {
		t.Errorf("OPUS model = %q", m["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	if !slices.Equal(args, []string{"--model", "glm-5.2:cloud"}) {
		t.Errorf("args = %v", args)
	}
}

// TestClaudePrepareCloudNoServer asserts the launch is not broken when the
// server is unreachable: the auto-compact window is omitted and Prepare succeeds.
func TestClaudePrepareCloudNoServer(t *testing.T) {
	host := &url.URL{Scheme: "http", Host: "127.0.0.1:1"} // nothing listening
	_, env, err := (&claude{}).Prepare("kimi-k2.6:cloud", host, nil, "")
	if err != nil {
		t.Fatalf("Prepare should succeed even when server is down: %v", err)
	}
	if _, ok := envMap(env)["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; ok {
		t.Error("CLAUDE_CODE_AUTO_COMPACT_WINDOW should be absent when server is unreachable")
	}
}

func TestCopilotPrepare(t *testing.T) {
	args, env, err := (&copilot{}).Prepare("m", testHost(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	if m["COPILOT_PROVIDER_BASE_URL"] != "http://127.0.0.1:11434/v1" {
		t.Errorf("COPILOT_PROVIDER_BASE_URL = %q", m["COPILOT_PROVIDER_BASE_URL"])
	}
	if m["COPILOT_MODEL"] != "m" {
		t.Errorf("COPILOT_MODEL = %q", m["COPILOT_MODEL"])
	}
	if !slices.Equal(args, []string{"--model", "m"}) {
		t.Errorf("args = %v", args)
	}
}

func TestPoolPrepare(t *testing.T) {
	args, env, err := (&pool{}).Prepare("m", testHost(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	if m["POOLSIDE_STANDALONE_BASE_URL"] != "http://127.0.0.1:11434/v1" {
		t.Errorf("POOLSIDE_STANDALONE_BASE_URL = %q", m["POOLSIDE_STANDALONE_BASE_URL"])
	}
	if m["POOLSIDE_API_KEY"] != "ollama" {
		t.Errorf("POOLSIDE_API_KEY = %q", m["POOLSIDE_API_KEY"])
	}
	if !slices.Equal(args, []string{"-m", "m"}) {
		t.Errorf("args = %v", args)
	}
}

func TestPoolUnsupportedMatchesPlatform(t *testing.T) {
	err := poolUnsupported()
	if runtime.GOOS == "windows" && err == nil {
		t.Error("pool should be unsupported on Windows")
	}
	if runtime.GOOS != "windows" && err != nil {
		t.Errorf("pool should be supported off Windows, got %v", err)
	}
}

func TestOpenCodePrepare(t *testing.T) {
	_, env, err := (&opencode{}).Prepare("gpt-oss:120b", testHost(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	content := envMap(env)["OPENCODE_CONFIG_CONTENT"]
	if content == "" {
		t.Fatal("OPENCODE_CONFIG_CONTENT not set")
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("config content is not valid JSON: %v", err)
	}
	if cfg["model"] != "ollama/gpt-oss:120b" {
		t.Errorf("model = %v", cfg["model"])
	}
	base := cfg["provider"].(map[string]any)["ollama"].(map[string]any)["options"].(map[string]any)["baseURL"]
	if base != "http://127.0.0.1:11434/v1" {
		t.Errorf("baseURL = %v", base)
	}
}

func TestCodexBuildingBlocks(t *testing.T) {
	if got := codexBaseURL(testHost()); got != "http://127.0.0.1:11434/v1/" {
		t.Errorf("codexBaseURL = %q", got)
	}

	overrides := codexOverrides("/home/u/.codex/model.json", "http://127.0.0.1:11434/v1/")
	if !slices.Contains(overrides, `model_provider="ollama-launch"`) {
		t.Errorf("overrides missing model_provider: %v", overrides)
	}

	dir := t.TempDir()
	profilePath := filepath.Join(dir, "ollama-launch.config.toml")
	if err := writeCodexProfile(profilePath, "gpt-oss:120b", filepath.Join(dir, "model.json"), "http://127.0.0.1:11434/v1/"); err != nil {
		t.Fatal(err)
	}
	profile, _ := os.ReadFile(profilePath)
	for _, want := range []string{`model = "gpt-oss:120b"`, `[model_providers.ollama-launch]`, `base_url = "http://127.0.0.1:11434/v1/"`, `wire_api = "responses"`} {
		if !strings.Contains(string(profile), want) {
			t.Errorf("profile missing %q\n%s", want, profile)
		}
	}

	catalogPath := filepath.Join(dir, "model.json")
	if err := writeCodexCatalog(catalogPath, "gpt-oss:120b"); err != nil {
		t.Fatal(err)
	}
	catalog, _ := readJSONMap(catalogPath)
	models := catalog["models"].([]any)
	if models[0].(map[string]any)["slug"] != "gpt-oss:120b" {
		t.Errorf("catalog slug = %v", models[0])
	}
}

func TestCodexValidateExtra(t *testing.T) {
	if err := codexValidateExtra([]string{"--model", "x"}); err == nil {
		t.Error("--model should conflict")
	}
	if err := codexValidateExtra([]string{"-c", "model_providers.ollama-launch.base_url=x"}); err == nil {
		t.Error("provider override should conflict")
	}
	if err := codexValidateExtra([]string{"--sandbox", "workspace-write"}); err != nil {
		t.Errorf("harmless extra args rejected: %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	if compareVersions("0.87.0", "0.134.0") >= 0 {
		t.Error("0.87.0 < 0.134.0")
	}
	if compareVersions("0.134.0", "0.134.0") != 0 {
		t.Error("equal versions")
	}
	if compareVersions("1.2.0", "0.134.0") <= 0 {
		t.Error("1.2.0 > 0.134.0")
	}
	if compareVersions("v0.134.0", "0.134.0") != 0 {
		t.Error("leading v should be ignored")
	}
}

func TestQwenPrepareMergesConfig(t *testing.T) {
	home := setHome(t)
	// Pre-existing config with a non-Ollama provider that must be preserved.
	configPath := filepath.Join(home, ".qwen", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"modelProviders":{"openai":[{"id":"gpt-4o","envKey":"OPENAI_API_KEY","baseUrl":"https://api.openai.com/v1"}]}}`
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	args, env, err := (&qwen{}).Prepare("gpt-oss:120b", testHost(), nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if m := envMap(env); m["OPENAI_BASE_URL"] != "http://127.0.0.1:11434/v1" || m["OPENAI_API_KEY"] != "ollama" {
		t.Errorf("env = %v", m)
	}
	if !hasFlag(args, "--auth-type") || !hasFlag(args, "--model") {
		t.Errorf("args missing flags: %v", args)
	}

	cfg, _ := readJSONMap(configPath)
	if asMap(cfg["env"])["OLLAMA_API_KEY"] != "ollama" {
		t.Error("env.OLLAMA_API_KEY not set")
	}
	providers := asMap(cfg["modelProviders"])["openai"].([]any)
	first := providers[0].(map[string]any)
	if first["baseUrl"] != "http://127.0.0.1:11434/v1" || first["id"] != "gpt-oss:120b" {
		t.Errorf("first provider = %v", first)
	}
	// The pre-existing non-Ollama provider must still be present.
	found := false
	for _, p := range providers {
		if p.(map[string]any)["id"] == "gpt-4o" {
			found = true
		}
	}
	if !found {
		t.Error("non-Ollama provider was dropped")
	}
	if asMap(asMap(cfg["security"])["auth"])["baseUrl"] != "http://127.0.0.1:11434/v1" {
		t.Error("security.auth.baseUrl not set")
	}
}

func TestDroidPreparePreservesOtherModels(t *testing.T) {
	home := setHome(t)
	configPath := filepath.Join(home, ".factory", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"customModels":[{"model":"other","apiKey":"real-key"}],"unknownField":true}`
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := (&droid{}).Prepare("gpt-oss:120b", testHost(), nil, ""); err != nil {
		t.Fatal(err)
	}

	cfg, _ := readJSONMap(configPath)
	if cfg["unknownField"] != true {
		t.Error("unknown field not preserved")
	}
	models := cfg["customModels"].([]any)
	first := models[0].(map[string]any)
	if first["baseUrl"] != "http://127.0.0.1:11434/v1" || first["apiKey"] != "ollama" || first["model"] != "gpt-oss:120b" {
		t.Errorf("ollama model entry = %v", first)
	}
	preserved := false
	for _, m := range models {
		if m.(map[string]any)["apiKey"] == "real-key" {
			preserved = true
		}
	}
	if !preserved {
		t.Error("non-ollama model dropped")
	}
	if asMap(cfg["sessionDefaultSettings"])["model"] != "custom:gpt-oss:120b-0" {
		t.Errorf("default model = %v", cfg["sessionDefaultSettings"])
	}
}

func TestClinePrepareWritesConfigs(t *testing.T) {
	home := setHome(t)
	if _, _, err := (&cline{}).Prepare("gpt-oss:120b", testHost(), nil, ""); err != nil {
		t.Fatal(err)
	}

	providers, _ := readJSONMap(filepath.Join(home, ".cline", "data", "settings", "providers.json"))
	if providers["lastUsedProvider"] != "ollama" {
		t.Errorf("lastUsedProvider = %v", providers["lastUsedProvider"])
	}
	settings := asMap(asMap(asMap(providers["providers"])["ollama"])["settings"])
	if settings["baseUrl"] != "http://127.0.0.1:11434/v1" || settings["model"] != "gpt-oss:120b" {
		t.Errorf("cline settings = %v", settings)
	}

	legacy, _ := readJSONMap(filepath.Join(home, ".cline", "data", "globalState.json"))
	if legacy["actModeOllamaBaseUrl"] != "http://127.0.0.1:11434" || legacy["welcomeViewCompleted"] != true {
		t.Errorf("cline globalState = %v", legacy)
	}
}

func TestWriteWithBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.json")
	if err := writeWithBackup(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeWithBackup(path, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "v2" {
		t.Errorf("content = %q", b)
	}
	if b, _ := os.ReadFile(path + ".ollama-lite.bak"); string(b) != "v1" {
		t.Errorf("backup = %q", b)
	}
}

// TestEffectiveAPIKey checks the fallback to the historical "ollama" literal when
// no key is configured, and the passthrough when one is.
func TestEffectiveAPIKey(t *testing.T) {
	if got := effectiveAPIKey(""); got != "ollama" {
		t.Errorf("effectiveAPIKey(\"\") = %q, want ollama", got)
	}
	if got := effectiveAPIKey("  "); got != "ollama" {
		t.Errorf("effectiveAPIKey(\"  \") = %q, want ollama", got)
	}
	if got := effectiveAPIKey("s3cret"); got != "s3cret" {
		t.Errorf("effectiveAPIKey(\"s3cret\") = %q, want s3cret", got)
	}
}

// TestPrepareCustomAPIKey asserts a custom --api-key threads through each app
// that writes a key into the target app, replacing the default "ollama" literal.
func TestPrepareCustomAPIKey(t *testing.T) {
	host := testHost()

	_, env, err := (&claude{}).Prepare("m", host, nil, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if m := envMap(env); m["ANTHROPIC_AUTH_TOKEN"] != "s3cret" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want s3cret", m["ANTHROPIC_AUTH_TOKEN"])
	}

	_, env, err = (&copilot{}).Prepare("m", host, nil, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if m := envMap(env); m["COPILOT_PROVIDER_API_KEY"] != "s3cret" {
		t.Errorf("COPILOT_PROVIDER_API_KEY = %q, want s3cret", m["COPILOT_PROVIDER_API_KEY"])
	}

	_, env, err = (&pool{}).Prepare("m", host, nil, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if m := envMap(env); m["POOLSIDE_API_KEY"] != "s3cret" {
		t.Errorf("POOLSIDE_API_KEY = %q, want s3cret", m["POOLSIDE_API_KEY"])
	}

	_, env, err = (&opencode{}).Prepare("m", host, nil, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(envMap(env)["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg["provider"].(map[string]any)["ollama"].(map[string]any)["options"].(map[string]any)["apiKey"]
	if got != "s3cret" {
		t.Errorf("opencode apiKey = %v, want s3cret", got)
	}
}

// TestQwenCustomAPIKey asserts the key flows into both the OpenAI env var and the
// settings.json env block written for Qwen.
func TestQwenCustomAPIKey(t *testing.T) {
	home := setHome(t)
	_, env, err := (&qwen{}).Prepare("gpt-oss:120b", testHost(), nil, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if m := envMap(env); m["OPENAI_API_KEY"] != "s3cret" {
		t.Errorf("OPENAI_API_KEY = %q, want s3cret", m["OPENAI_API_KEY"])
	}
	cfg, _ := readJSONMap(filepath.Join(home, ".qwen", "settings.json"))
	if asMap(cfg["env"])["OLLAMA_API_KEY"] != "s3cret" {
		t.Errorf("env.OLLAMA_API_KEY = %v, want s3cret", asMap(cfg["env"])["OLLAMA_API_KEY"])
	}
}

// TestDroidCustomAPIKeyReplacesPriorEntry asserts that with a custom key a prior
// ollama-lite entry (which now carries the custom key, not "ollama") is replaced
// rather than duplicated, and the new entry carries the custom key.
func TestDroidCustomAPIKeyReplacesPriorEntry(t *testing.T) {
	home := setHome(t)
	configPath := filepath.Join(home, ".factory", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// A prior ollama-lite launch pointing at the same base URL with the custom key.
	seed := `{"customModels":[{"model":"old","baseUrl":"http://127.0.0.1:11434/v1","apiKey":"s3cret"}]}`
	if err := os.WriteFile(configPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := (&droid{}).Prepare("gpt-oss:120b", testHost(), nil, "s3cret"); err != nil {
		t.Fatal(err)
	}

	cfg, _ := readJSONMap(configPath)
	models := cfg["customModels"].([]any)
	if len(models) != 1 {
		t.Fatalf("expected the prior same-baseUrl entry to be replaced, got %d entries: %v", len(models), models)
	}
	first := models[0].(map[string]any)
	if first["apiKey"] != "s3cret" || first["model"] != "gpt-oss:120b" {
		t.Errorf("ollama model entry = %v", first)
	}
}

// TestClineCustomAPIKey asserts cline writes the key when provided and omits it
// (preserving the old delete behavior) when not.
func TestClineCustomAPIKey(t *testing.T) {
	home := setHome(t)
	if _, _, err := (&cline{}).Prepare("gpt-oss:120b", testHost(), nil, "s3cret"); err != nil {
		t.Fatal(err)
	}
	providers, _ := readJSONMap(filepath.Join(home, ".cline", "data", "settings", "providers.json"))
	settings := asMap(asMap(asMap(providers["providers"])["ollama"])["settings"])
	if settings["apiKey"] != "s3cret" {
		t.Errorf("cline apiKey = %v, want s3cret", settings["apiKey"])
	}

	// Without a key, apiKey must be absent (the pre-flag delete behavior).
	home2 := setHome(t) // resets HOME to a fresh temp dir
	if _, _, err := (&cline{}).Prepare("gpt-oss:120b", testHost(), nil, ""); err != nil {
		t.Fatal(err)
	}
	providers2, _ := readJSONMap(filepath.Join(home2, ".cline", "data", "settings", "providers.json"))
	settings2 := asMap(asMap(asMap(providers2["providers"])["ollama"])["settings"])
	if _, ok := settings2["apiKey"]; ok {
		t.Errorf("cline apiKey should be absent when no key set, got %v", settings2["apiKey"])
	}
}
