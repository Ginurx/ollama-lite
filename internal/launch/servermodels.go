package launch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FetchModelsFromServer GETs {host}/api/tags from the running ollama-lite
// server and returns the advertised model names. Returns an error if the
// server is unreachable or returns a non-200 status. The response shape is a
// minimal local subset of Ollama's api.ListResponse (only the name field is
// needed), intentionally not shared with internal/server to keep this package
// lite.
func FetchModelsFromServer(host *url.URL) ([]string, error) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(strings.TrimRight(host.String(), "/") + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/api/tags: %s", resp.Status)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}