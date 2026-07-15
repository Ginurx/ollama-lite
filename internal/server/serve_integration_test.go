package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestServeAdvertisesOnlineRecommendations drives the full Serve loop: with no
// configured model list, /api/tags and /api/experimental/model-recommendations
// reflect the recommendations fetched online from the cloud base URL.
func TestServeAdvertisesOnlineRecommendations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	payload := `{"recommendations":[
		{"model":"gpt-oss:120b","description":"coding","context_length":131072,"max_output_tokens":131072},
		{"model":"glm-5.2:cloud","description":"reasoning","context_length":202752,"max_output_tokens":131072}
	]}`
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer cloud.Close()
	t.Setenv("OLLAMA_CLOUD_BASE_URL", cloud.URL)

	// Grab a free port, then hand it to Serve.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, addr, nil, "") }()

	base := "http://" + addr
	check := func() []string {
		resp, err := http.Get(base + "/api/tags")
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		var out struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		names := make([]string, 0, len(out.Models))
		for _, m := range out.Models {
			names = append(names, m.Name)
		}
		return names
	}

	// The cache starts with builtin defaults and the background refresh swaps
	// them for the mock payload once it succeeds. Poll until that happens.
	var names []string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		names = check()
		if len(names) == 2 && names[0] == "gpt-oss:120b" && names[1] == "glm-5.2:cloud" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(names) != 2 || names[0] != "gpt-oss:120b" || names[1] != "glm-5.2:cloud" {
		t.Fatalf("/api/tags = %v, want [gpt-oss:120b glm-5.2:cloud]", names)
	}

	// /api/experimental/model-recommendations returns the rich objects.
	resp, err := http.Get(base + "/api/experimental/model-recommendations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var recs recommendationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&recs); err != nil {
		t.Fatal(err)
	}
	if len(recs.Recommendations) != 2 || recs.Recommendations[0].ContextLength != 131072 {
		t.Fatalf("recommendations = %#v", recs)
	}

	// Snapshot was persisted to the temp home.
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(home + "/.ollama-lite/cache/model-recommendations.json"); err != nil {
		t.Fatalf("snapshot not persisted: %v", err)
	}
}

// TestServeConfiguredModelsOverridesRecommendations: with --models set, /api/tags
// returns exactly that list even though recommendations would otherwise be
// advertised.
func TestServeConfiguredModelsOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"recommendations":[{"model":"should-not-appear:cloud","context_length":1,"max_output_tokens":1}]}`)
	}))
	defer cloud.Close()
	t.Setenv("OLLAMA_CLOUD_BASE_URL", cloud.URL)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Serve(ctx, addr, []string{"gpt-oss:120b"}, "") }()

	base := "http://" + addr
	var names []string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/api/tags")
		if err == nil {
			var out struct {
				Models []struct {
					Name string `json:"name"`
				} `json:"models"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
			names = make([]string, 0, len(out.Models))
			for _, m := range out.Models {
				names = append(names, m.Name)
			}
			if len(names) == 1 && names[0] == "gpt-oss:120b" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(names) != 1 || names[0] != "gpt-oss:120b" {
		t.Fatalf("/api/tags = %v, want [gpt-oss:120b]", names)
	}
}