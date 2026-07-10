package launch

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
)

// copilot configures GitHub Copilot CLI to use ollama-lite via environment
// variables. Mirrors D:\repo10\ollama\cmd\launch\copilot.go.
type copilot struct{}

func (c *copilot) Display() string { return "Copilot CLI" }

func (c *copilot) FindBin() (string, bool) {
	if p, err := exec.LookPath("copilot"); err == nil {
		return p, true
	}
	home, err := homeDir()
	if err != nil {
		return "", false
	}
	fallback := filepath.Join(home, ".local", "bin", exeName("copilot"))
	if _, err := os.Stat(fallback); err == nil {
		return fallback, true
	}
	return "", false
}

func (c *copilot) Prepare(model string, host *url.URL, extra []string) (args, env []string, err error) {
	env = []string{
		"COPILOT_PROVIDER_BASE_URL=" + hostV1(host),
		"COPILOT_PROVIDER_API_KEY=",
		"COPILOT_PROVIDER_WIRE_API=responses",
		"COPILOT_MODEL=" + model,
	}
	args = append([]string{"--model", model}, extra...)
	return args, env, nil
}
