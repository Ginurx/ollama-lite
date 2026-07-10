package launch

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
)

// claude configures Claude Code to use ollama-lite via environment variables.
// Mirrors D:\repo10\ollama\cmd\launch\claude.go (minus cloud-limit enrichment).
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
	args = append([]string{"--model", model}, extra...)
	return args, env, nil
}
