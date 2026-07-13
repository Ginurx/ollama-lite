package launch

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// claude configures Claude Code to use ollama-lite via environment variables.
// Mirrors D:\repo10\ollama\cmd\launch\claude.go, including the cloud-limit
// enrichment: for cloud models, CLAUDE_CODE_AUTO_COMPACT_WINDOW is set to the
// model's context length so auto-compact triggers near the real limit instead
// of Claude Code's smaller default. The limit is fetched over HTTP from the
// running server's /api/experimental/model-recommendations (ollama-lite's model
// list is dynamic, so a static table like ollama's would drift).
type claude struct{}

func (c *claude) Display() string { return "Claude Code" }

func (c *claude) FindBin() (string, bool) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, true
	}
	home, err := homeDir()
	if err != nil {
		return "", false
	}
	name := exeName("claude")
	for _, fallback := range []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".claude", "local", name),
	} {
		if _, err := os.Stat(fallback); err == nil {
			return fallback, true
		}
	}
	return "", false
}

func (c *claude) Prepare(model string, host *url.URL, extra []string) (args, env []string, err error) {
	env = []string{
		"ANTHROPIC_BASE_URL=" + host.String(),
		"ANTHROPIC_API_KEY=",
		"ANTHROPIC_AUTH_TOKEN=ollama",
		"CLAUDE_CODE_ATTRIBUTION_HEADER=0",
		"DISABLE_TELEMETRY=1",
		"DISABLE_ERROR_REPORTING=1",
		"DISABLE_FEEDBACK_COMMAND=1",
		"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=" + model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model,
		"CLAUDE_CODE_SUBAGENT_MODEL=" + model,
	}
	if isCloudModelName(model) {
		if w, ok := fetchContextWindow(host, model); ok {
			env = append(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+strconv.Itoa(w))
		}
	}
	args = append([]string{"--model", model}, extra...)
	return args, env, nil
}

// isCloudModelName reports whether model is an explicit cloud model, mirroring
// internal/server.isCloudRecommendation. Used to gate the auto-compact-window
// enrichment the same way ollama's cmd/launch gates it with isCloudModelName.
func isCloudModelName(model string) bool {
	return strings.HasSuffix(model, ":cloud") || strings.HasSuffix(model, "-cloud")
}

// fetchContextWindow looks up the model's context length from the running
// ollama-lite server's /api/experimental/model-recommendations. It is
// best-effort: any transport error, non-200 response, decode failure, missing
// entry, or zero context length returns (0, false) and the caller simply omits
// the env var (Claude Code then keeps its default), matching ollama's "if ok".
func fetchContextWindow(host *url.URL, model string) (int, bool) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(strings.TrimRight(host.String(), "/") + "/api/experimental/model-recommendations")
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var out struct {
		Recommendations []struct {
			Model         string `json:"model"`
			ContextLength int    `json:"context_length"`
		} `json:"recommendations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, false
	}
	for _, r := range out.Recommendations {
		if r.Model == model && r.ContextLength > 0 {
			return r.ContextLength, true
		}
	}
	return 0, false
}