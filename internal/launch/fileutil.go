package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// writeWithBackup writes data to path, first copying any existing file to a
// sibling "<path>.ollama-lite.bak" so the user's original config is recoverable.
func writeWithBackup(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".ollama-lite.bak", existing, perm)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// readJSONMap reads a JSON object from path into a map. A missing file yields an
// empty map (not an error) so callers can merge into a fresh config.
func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// writeJSON marshals v with indentation and writes it with a backup.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeWithBackup(path, data, 0o644)
}

// asMap coerces a value read from JSON into a map, returning a fresh map when the
// value is absent or the wrong type.
func asMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// homeDir returns the user's home directory.
func homeDir() (string, error) {
	return os.UserHomeDir()
}

// lookInstalled resolves an executable on PATH, returning its path and whether
// it was found.
func lookInstalled(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

// exeName appends ".exe" on Windows.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
