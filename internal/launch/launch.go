// Package launch starts an AI coding tool (Claude Code, Codex, …) pre-wired to
// use the local ollama-lite server as its model backend. For each supported app
// it either injects the right environment variables or writes the app's config
// file, then execs the app.
//
// This is a deliberately trimmed port of the official `ollama launch`: no
// interactive menu, no model picker/inventory, no auto-install. The target app
// must already be installed; when it is not, launch prints an install hint and
// exits.
package launch

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// App configures and launches one AI tool against the ollama-lite server.
type App interface {
	// Display is the human-readable name, e.g. "Claude Code".
	Display() string
	// FindBin resolves the app's executable, returning ("", false) if not found.
	FindBin() (string, bool)
	// Prepare writes any config files the app needs and returns the complete
	// argument vector (app-specific flags composed with the user's pass-through
	// extra args) plus any extra environment variables (KEY=VALUE) to set.
	// apiKey is the shared secret resolved from --api-key / OLLAMA_LITE_API_KEY
	// (empty when unset); apps that always send a key should pass it through
	// effectiveAPIKey, which falls back to "ollama".
	Prepare(model string, host *url.URL, extra []string, apiKey string) (args, env []string, err error)
}

// spec is a registry entry describing one supported app.
type spec struct {
	name        string       // canonical name, e.g. "claude"
	aliases     []string     // alternate names, e.g. "copilot-cli"
	installHint string       // shown when the binary is missing
	app         App          // the runner
	unsupported func() error // returns non-nil when the app can't run here (e.g. pool on Windows)
}

// registry is the set of supported apps, in display order.
var registry = []spec{
	{name: "claude", installHint: "install from https://code.claude.com/docs/en/quickstart", app: &claude{}},
	{name: "codex", installHint: "install with: npm install -g @openai/codex", app: &codex{}},
	{name: "copilot", aliases: []string{"copilot-cli"}, installHint: "install from https://docs.github.com/en/copilot/how-tos/set-up/install-copilot-cli", app: &copilot{}},
	{name: "opencode", installHint: "install from https://opencode.ai", app: &opencode{}},
	{name: "qwen", installHint: "install from https://qwen.ai/qwencode", app: &qwen{}},
	{name: "droid", installHint: "install from https://docs.factory.ai/cli/getting-started/quickstart", app: &droid{}},
	{name: "cline", installHint: "install with: npm install -g cline@latest", app: &cline{}},
	{name: "pool", installHint: "install from https://github.com/poolsideai/pool", app: &pool{}, unsupported: poolUnsupported},
}

// lookup resolves a canonical name or alias (case-insensitive) to its spec.
func lookup(name string) (spec, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	for _, s := range registry {
		if s.name == key {
			return s, true
		}
		for _, alias := range s.aliases {
			if alias == key {
				return s, true
			}
		}
	}
	return spec{}, false
}

// Resolve returns the canonical app name for a supported name or alias.
func Resolve(name string) (string, bool) {
	s, ok := lookup(name)
	return s.name, ok
}

// Supported returns the canonical app names in display order.
func Supported() []string {
	names := make([]string, 0, len(registry))
	for _, s := range registry {
		names = append(names, s.name)
	}
	return names
}

// SupportedList returns "name — Display Name" lines for help text.
func SupportedList() []string {
	lines := make([]string, 0, len(registry))
	for _, s := range registry {
		lines = append(lines, fmt.Sprintf("%-10s %s", s.name, s.app.Display()))
	}
	return lines
}

// Launch configures the named app to use host as its backend and execs it,
// passing model plus any extra pass-through args. apiKey is the shared secret
// (empty when unset) written into the app so it authenticates against an
// auth-enabled server.
func Launch(name, model string, extra []string, host *url.URL, apiKey string) error {
	s, ok := lookup(name)
	if !ok {
		return fmt.Errorf("unknown app %q\n\nSupported apps: %s", name, strings.Join(Supported(), ", "))
	}

	if s.unsupported != nil {
		if err := s.unsupported(); err != nil {
			return err
		}
	}

	bin, found := s.app.FindBin()
	if !found {
		return fmt.Errorf("%s is not installed, %s", s.name, s.installHint)
	}

	args, env, err := s.app.Prepare(model, host, extra, apiKey)
	if err != nil {
		return err
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run()
}

// hostRoot returns the base URL string without a trailing slash,
// e.g. "http://127.0.0.1:11434".
func hostRoot(host *url.URL) string {
	return strings.TrimRight(host.String(), "/")
}

// hostV1 returns the OpenAI-compatible base, e.g. "http://127.0.0.1:11434/v1".
func hostV1(host *url.URL) string {
	return hostRoot(host) + "/v1"
}

// effectiveAPIKey returns the API key an app should send: the resolved key when one
// was provided (--api-key / OLLAMA_LITE_API_KEY), or the historical literal
// "ollama" when unset, preserving the pre-flag behavior against an open server.
func effectiveAPIKey(apiKey string) string {
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		return apiKey
	}
	return "ollama"
}
