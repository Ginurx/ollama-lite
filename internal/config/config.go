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

// ConnectableHost returns Host() with an unspecified bind address (0.0.0.0, ::)
// rewritten to a loopback address, so an app launched by `ollama-lite launch`
// dials a reachable address instead of 0.0.0.0. Mirrors envconfig.ConnectableHost.
func ConnectableHost() *url.URL {
	u := Host()
	connectable(u)
	return u
}

// ConnectableHostFrom returns the connectable host URL, preferring an explicit
// override (e.g. a --host flag) over the OLLAMA_HOST environment variable. When
// an explicit override is an unspecified bind address (0.0.0.0, ::, or an empty
// host like ":11434"), it is rewritten to loopback and the original override is
// returned as bindOverride so the caller can warn the user that a bind address is
// not a connectable one. The OLLAMA_HOST path never returns a bindOverride — it
// stays silent, mirroring the official Ollama.
func ConnectableHostFrom(override string) (host *url.URL, bindOverride string) {
	if override = strings.TrimSpace(override); override != "" {
		u := hostURL(override)
		if connectable(u) {
			bindOverride = override
		}
		return u, bindOverride
	}
	return ConnectableHost(), ""
}

// connectable rewrites an unspecified or empty host (0.0.0.0, ::, ":port") to a
// loopback address so the launched app dials a reachable address. It reports
// whether it made a change, so an explicit --host that was a bind address can be
// warned about rather than silently misrouted.
func connectable(u *url.URL) bool {
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return false
	}
	var loopback string
	switch {
	case host == "":
		loopback = "127.0.0.1"
	default:
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			if ip.To4() != nil {
				loopback = "127.0.0.1"
			} else {
				loopback = "::1"
			}
		}
	}
	if loopback == "" {
		return false
	}
	u.Host = net.JoinHostPort(loopback, port)
	return true
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

// Models returns the explicitly configured list of model names to advertise on
// /api/tags and /v1/models.
//
// Resolution order:
//  1. the --models flag (comma-separated), if non-empty;
//  2. ~/.ollama-lite/models.json (a JSON array of strings), if present;
//  3. empty — in which case the server advertises the online-refreshed model
//     recommendation list instead (see internal/server/recommendations.go).
func Models(flagValue string) []string {
	if flagValue = strings.TrimSpace(flagValue); flagValue != "" {
		return dedupe(splitList(flagValue))
	}

	if models, ok := modelsFromFile(); ok {
		return dedupe(models)
	}

	return nil
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
