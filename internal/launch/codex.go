package launch

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// codex configures OpenAI's Codex CLI. It writes a dedicated launch profile
// (~/.codex/ollama-launch.config.toml) and model catalog (~/.codex/model.json)
// that it fully owns, and runs codex with --profile ollama-launch, so the user's
// own ~/.codex/config.toml is never modified. Mirrors
// D:\repo10\ollama\cmd\launch\codex.go (minus capability enrichment and the
// legacy in-place config migration).
type codex struct{}

const (
	codexProfileName      = "ollama-launch"
	codexProviderName     = "Ollama"
	codexContextWindow    = 128_000
	codexMinVersion       = "0.134.0"
	codexModelProviderKey = "model_provider"
	codexModelCatalogKey  = "model_catalog_json"
)

func (c *codex) Display() string { return "Codex" }

func (c *codex) FindBin() (string, bool) { return lookInstalled("codex") }

func (c *codex) Prepare(model string, host *url.URL, extra []string) (args, env []string, err error) {
	if err := checkCodexVersion(); err != nil {
		return nil, nil, err
	}
	if err := codexValidateExtra(extra); err != nil {
		return nil, nil, err
	}

	home, err := homeDir()
	if err != nil {
		return nil, nil, err
	}
	catalogPath := filepath.Join(home, ".codex", "model.json")
	profilePath := filepath.Join(home, ".codex", codexProfileName+".config.toml")
	baseURL := codexBaseURL(host)

	if err := writeCodexCatalog(catalogPath, model); err != nil {
		return nil, nil, err
	}
	if err := writeCodexProfile(profilePath, model, catalogPath, baseURL); err != nil {
		return nil, nil, err
	}

	args = []string{"--profile", codexProfileName}
	for _, override := range codexOverrides(catalogPath, baseURL) {
		args = append(args, "-c", override)
	}
	args = append(args, "-m", model)
	args = append(args, extra...)

	env = []string{"OPENAI_API_KEY=ollama"}
	return args, env, nil
}

func codexBaseURL(host *url.URL) string {
	return hostV1(host) + "/"
}

func codexOverrides(catalogPath, baseURL string) []string {
	return []string{
		fmt.Sprintf("%s=%q", codexModelProviderKey, codexProfileName),
		fmt.Sprintf("model_providers.%s.name=%q", codexProfileName, codexProviderName),
		fmt.Sprintf("model_providers.%s.base_url=%q", codexProfileName, baseURL),
		fmt.Sprintf("model_providers.%s.wire_api=%q", codexProfileName, "responses"),
		fmt.Sprintf("%s=%q", codexModelCatalogKey, catalogPath),
	}
}

// writeCodexProfile writes ~/.codex/ollama-launch.config.toml as plain TOML text.
func writeCodexProfile(path, model, catalogPath, baseURL string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "model = %q\n", model)
	fmt.Fprintf(&b, "%s = %q\n", codexModelProviderKey, codexProfileName)
	fmt.Fprintf(&b, "%s = %q\n\n", codexModelCatalogKey, catalogPath)
	fmt.Fprintf(&b, "[model_providers.%s]\n", codexProfileName)
	fmt.Fprintf(&b, "name = %q\n", codexProviderName)
	fmt.Fprintf(&b, "base_url = %q\n", baseURL)
	fmt.Fprintf(&b, "wire_api = %q\n", "responses")
	return writeWithBackup(path, []byte(b.String()), 0o644)
}

func writeCodexCatalog(path, model string) error {
	entry := map[string]any{
		"slug":                         model,
		"display_name":                 model,
		"context_window":               codexContextWindow,
		"shell_type":                   "default",
		"visibility":                   "list",
		"supported_in_api":             true,
		"priority":                     0,
		"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
		"input_modalities":             []string{"text"},
		"base_instructions":            "",
		"support_verbosity":            true,
		"default_verbosity":            "low",
		"supports_parallel_tool_calls": false,
		"supports_reasoning_summaries": false,
		"supported_reasoning_levels":   []any{},
		"experimental_supported_tools": []any{},
	}
	catalog := map[string]any{"models": []any{entry}}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return writeWithBackup(path, data, 0o644)
}

// codexValidateExtra rejects pass-through args that conflict with the launch
// profile, model, and provider config that ollama-lite manages.
func codexValidateExtra(args []string) error {
	for i, arg := range args {
		switch {
		case arg == "-p", strings.HasPrefix(arg, "-p"),
			arg == "--profile", strings.HasPrefix(arg, "--profile="):
			return fmt.Errorf("conflicting extra argument %q: ollama-lite launch codex manages --profile", arg)
		case arg == "-m", strings.HasPrefix(arg, "-m"),
			arg == "--model", strings.HasPrefix(arg, "--model="):
			return fmt.Errorf("conflicting extra argument %q: ollama-lite launch codex manages --model", arg)
		case arg == "-c", arg == "--config":
			if i+1 < len(args) && codexOverrideConflicts(args[i+1]) {
				return fmt.Errorf("conflicting extra config %q: ollama-lite launch codex manages provider and model catalog config", args[i+1])
			}
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			if codexOverrideConflicts(strings.TrimPrefix(arg, "-c")) {
				return fmt.Errorf("conflicting extra config %q: ollama-lite launch codex manages provider and model catalog config", arg)
			}
		case strings.HasPrefix(arg, "--config="):
			if codexOverrideConflicts(strings.TrimPrefix(arg, "--config=")) {
				return fmt.Errorf("conflicting extra config %q: ollama-lite launch codex manages provider and model catalog config", arg)
			}
		}
	}
	return nil
}

func codexOverrideConflicts(value string) bool {
	key, _, ok := strings.Cut(strings.TrimSpace(value), "=")
	if !ok {
		return false
	}
	key = strings.Trim(strings.TrimSpace(key), `"'`)
	switch {
	case key == "profile", key == "model", key == codexModelProviderKey, key == codexModelCatalogKey:
		return true
	case strings.HasPrefix(key, "model_providers."):
		return true
	}
	return false
}

// checkCodexVersion enforces a minimum Codex version so the responses wire API
// and profile support are present. A version that can't be parsed is allowed.
func checkCodexVersion() error {
	out, err := exec.Command("codex", "--version").Output()
	if err != nil {
		return nil // don't block on a flaky --version; the run will surface real errors
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return nil
	}
	got := fields[len(fields)-1]
	if compareVersions(got, codexMinVersion) < 0 {
		return fmt.Errorf("codex version %s is too old, minimum required is %s, update with: npm update -g @openai/codex", got, codexMinVersion)
	}
	return nil
}

// compareVersions compares dotted numeric versions (ignoring any pre-release or
// build suffix). Returns -1, 0, or 1. Unparseable parts compare as 0.
func compareVersions(a, b string) int {
	trim := func(s string) string {
		s = strings.TrimPrefix(strings.TrimSpace(s), "v")
		if i := strings.IndexAny(s, "-+"); i >= 0 {
			s = s[:i]
		}
		return s
	}
	ap := strings.Split(trim(a), ".")
	bp := strings.Split(trim(b), ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var an, bn int
		if i < len(ap) {
			an, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bn, _ = strconv.Atoi(bp[i])
		}
		if an < bn {
			return -1
		}
		if an > bn {
			return 1
		}
	}
	return 0
}
