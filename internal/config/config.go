// Package config resolves configuration that ollama-lite shares with the
// official Ollama (the ~/.ollama directory, OLLAMA_HOST, OLLAMA_ORIGINS) plus a
// small amount of ollama-lite-specific configuration (the cloud endpoint and the
// advertised model list).
package config

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultCloudBaseURL is the upstream Ollama cloud endpoint.
const DefaultCloudBaseURL = "https://ollama.com"

// builtinModels is the fallback model list advertised on /api/tags when the
// user has not configured one. These are examples; edit ~/.ollama-lite/models.json
// or pass --models to change them.
var builtinModels = []string{
	"gpt-oss:20b",
	"gpt-oss:120b",
	"qwen3-coder:480b",
	"deepseek-v3.1:671b",
}

// Host mirrors Ollama's OLLAMA_HOST parsing. Default is http://127.0.0.1:11434.
func Host() *url.URL {
	return hostURL(strings.TrimSpace(os.Getenv("OLLAMA_HOST")))
}

// hostURL parses a host string in OLLAMA_HOST form (e.g. "127.0.0.1:11435",
// ":11434", "0.0.0.0", or "https://host:443") into a URL.
func hostURL(s string) *url.URL {
	defaultPort := "11434"

	scheme, hostport, ok := strings.Cut(s, "://")
	switch {
	case !ok:
		scheme, hostport = "http", s
	case scheme == "http":
		defaultPort = "80"
	case scheme == "https":
		defaultPort = "443"
	}

	hostport, path, _ := strings.Cut(hostport, "/")
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = "127.0.0.1", defaultPort
		if ip := net.ParseIP(strings.Trim(hostport, "[]")); ip != nil {
			host = ip.String()
		} else if hostport != "" {
			host = hostport
		}
	}

	if n, err := strconv.ParseInt(port, 10, 32); err != nil || n > 65535 || n < 0 {
		port = defaultPort
	}

	return &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, port),
		Path:   path,
	}
}

// BindAddress returns the host:port ollama-lite should listen on (from OLLAMA_HOST).
func BindAddress() string {
	return Host().Host
}

// BindAddressFrom returns the host:port to listen on, preferring an explicit
// override (e.g. a --host flag) over the OLLAMA_HOST environment variable.
func BindAddressFrom(override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return hostURL(override).Host
	}
	return BindAddress()
}

// CloudBaseURL returns the upstream cloud endpoint, overridable via
// OLLAMA_CLOUD_BASE_URL (matching the official Ollama env var).
func CloudBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_CLOUD_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultCloudBaseURL
}

// AllowedOrigins mirrors Ollama's OLLAMA_ORIGINS handling plus its built-in
// defaults, so browser UIs (Open WebUI, Tauri/VS Code webviews, etc.) can reach
// the server on :11434.
func AllowedOrigins() []string {
	var origins []string
	if s := strings.TrimSpace(os.Getenv("OLLAMA_ORIGINS")); s != "" {
		origins = strings.Split(s, ",")
	}

	for _, origin := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		origins = append(origins,
			"http://"+origin,
			"https://"+origin,
			"http://"+net.JoinHostPort(origin, "*"),
			"https://"+net.JoinHostPort(origin, "*"),
		)
	}

	origins = append(origins,
		"app://*",
		"file://*",
		"tauri://*",
		"vscode-webview://*",
		"vscode-file://*",
	)

	return origins
}

// Models returns the list of model names advertised on /api/tags and /v1/models.
//
// Resolution order:
//  1. the --models flag (comma-separated), if non-empty;
//  2. ~/.ollama-lite/models.json (a JSON array of strings), if present;
//  3. models found in ~/.ollama/config.json integrations, merged with a small
//     built-in default list.
func Models(flagValue string) []string {
	if flagValue = strings.TrimSpace(flagValue); flagValue != "" {
		return dedupe(splitList(flagValue))
	}

	if models, ok := modelsFromFile(); ok {
		return dedupe(models)
	}

	models := modelsFromOllamaConfig()
	models = append(models, builtinModels...)
	return dedupe(models)
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// modelsFile is the path to the ollama-lite model list (~/.ollama-lite/models.json).
func modelsFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ollama-lite", "models.json"), nil
}

func modelsFromFile() ([]string, bool) {
	path, err := modelsFile()
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var models []string
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, false
	}
	return models, len(models) > 0
}

// modelsFromOllamaConfig reads any model names configured under
// "integrations.*.models" in the official ~/.ollama/config.json.
func modelsFromOllamaConfig() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".ollama", "config.json"))
	if err != nil {
		return nil
	}

	var cfg struct {
		Integrations map[string]struct {
			Models []string `json:"models"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}

	var models []string
	for _, integration := range cfg.Integrations {
		models = append(models, integration.Models...)
	}
	return models
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
