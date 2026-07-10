package launch

import (
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

// pool configures Poolside's CLI to use ollama-lite via environment variables.
// Mirrors D:\repo10\ollama\cmd\launch\poolside.go.
type pool struct{}

func (p *pool) Display() string { return "Pool" }

// poolUnsupported reports Pool as unavailable on Windows (matches the reference).
func poolUnsupported() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Pool is not currently supported on Windows")
	}
	return nil
}

func (p *pool) FindBin() (string, bool) {
	if p, err := exec.LookPath("pool"); err == nil {
		return p, true
	}
	return "", false
}

func (p *pool) Prepare(model string, host *url.URL, extra []string) (args, env []string, err error) {
	env = []string{
		"POOLSIDE_STANDALONE_BASE_URL=" + hostV1(host),
		"POOLSIDE_API_KEY=ollama",
	}
	args = append([]string{"-m", model}, extra...)
	return args, env, nil
}
