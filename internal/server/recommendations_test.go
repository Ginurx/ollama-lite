package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempHome points HOME/USERPROFILE at a fresh temp dir for the test so the
// recommendations snapshot never touches the real ~/.ollama-lite.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestRecommendationsDefaultsServedBeforeFetch(t *testing.T) {
	c := newRecommendationsCache()
	recs := c.Get()
	if len(recs) != len(defaultRecommendations) {
		t.Fatalf("got %d default recs, want %d", len(recs), len(defaultRecommendations))
	}
	for i, want := range defaultRecommendations {
		if recs[i].Model != want.Model {
			t.Errorf("rec[%d].Model = %q, want %q", i, recs[i].Model, want.Model)
		}
	}
	if names := c.Names(); len(names) != len(defaultRecommendations) {
		t.Fatalf("Names() = %v, want %d entries", names, len(defaultRecommendations))
	}
}

func TestRecommendationsRefreshUpdatesAndPersistsSnapshot(t *testing.T) {
	withTempHome(t)

	payload := `{"recommendations":[
		{"model":"gpt-oss:120b","description":"coding","context_length":131072,"max_output_tokens":131072},
		{"model":"glm-5.2:cloud","description":"reasoning","context_length":202752,"max_output_tokens":131072}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != recommendationsEndpoint {
			t.Fatalf("path = %q, want %q", r.URL.Path, recommendationsEndpoint)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_CLOUD_BASE_URL", srv.URL)

	c := newRecommendationsCache()
	if err := c.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	recs := c.Get()
	if len(recs) != 2 || recs[0].Model != "gpt-oss:120b" || recs[1].Model != "glm-5.2:cloud" {
		t.Fatalf("recs = %#v", recs)
	}

	// Snapshot persisted to the temp home.
	home, _ := os.UserHomeDir()
	snapPath := filepath.Join(home, ".ollama-lite", "cache", "model-recommendations.json")
	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	var snap recommendationsResponse
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if len(snap.Recommendations) != 2 {
		t.Fatalf("snapshot recs = %d, want 2", len(snap.Recommendations))
	}
}

func TestValidateRecommendations(t *testing.T) {
	t.Run("drops cloud entry missing limits", func(t *testing.T) {
		in := []Recommendation{
			{Model: "glm-5.2:cloud", ContextLength: 100, MaxOutputTokens: 100},
			{Model: "broken:cloud", ContextLength: 0, MaxOutputTokens: 100},
		}
		out, err := validateRecommendations(in)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(out) != 1 || out[0].Model != "glm-5.2:cloud" {
			t.Fatalf("out = %#v", out)
		}
	})
	t.Run("rejects duplicate model", func(t *testing.T) {
		_, err := validateRecommendations([]Recommendation{
			{Model: "a:cloud", ContextLength: 1, MaxOutputTokens: 1},
			{Model: "a:cloud", ContextLength: 1, MaxOutputTokens: 1},
		})
		if err == nil {
			t.Fatal("want duplicate error")
		}
	})
	t.Run("rejects empty model", func(t *testing.T) {
		_, err := validateRecommendations([]Recommendation{{Model: " ", Description: "x"}})
		if err == nil {
			t.Fatal("want missing-model error")
		}
	})
	t.Run("rejects empty list", func(t *testing.T) {
		if _, err := validateRecommendations(nil); err == nil {
			t.Fatal("want empty-list error")
		}
	})
	t.Run("rejects when all cloud entries dropped", func(t *testing.T) {
		_, err := validateRecommendations([]Recommendation{{Model: "x:cloud"}})
		if err == nil {
			t.Fatal("want no-valid error")
		}
	})
}

func TestRecommendationsRefreshFailureLeavesDefaults(t *testing.T) {
	withTempHome(t)
	t.Setenv("OLLAMA_CLOUD_BASE_URL", "http://127.0.0.1:1") // unreachable

	c := newRecommendationsCache()
	if err := c.refresh(context.Background()); err == nil {
		t.Fatal("want refresh error on unreachable upstream, got nil")
	}
	if got := c.Get(); len(got) != len(defaultRecommendations) {
		t.Fatalf("after failed refresh, got %d recs, want the %d builtin defaults", len(got), len(defaultRecommendations))
	}
}

func TestRecommendationsRefreshNon200LeavesDefaults(t *testing.T) {
	withTempHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("OLLAMA_CLOUD_BASE_URL", srv.URL)

	c := newRecommendationsCache()
	if err := c.refresh(context.Background()); err == nil {
		t.Fatal("want error on non-200, got nil")
	}
	if got := c.Get(); len(got) != len(defaultRecommendations) {
		t.Fatalf("got %d recs, want builtin defaults", len(got))
	}
}

func TestRecommendationsSnapshotLoadedOnStart(t *testing.T) {
	home := withTempHome(t)
	snapPath := filepath.Join(home, ".ollama-lite", "cache", "model-recommendations.json")
	data, _ := json.Marshal(recommendationsResponse{Recommendations: []Recommendation{
		{Model: "kimi-k2.6:cloud", ContextLength: 262144, MaxOutputTokens: 262144},
		{Model: "glm-5.1:cloud", ContextLength: 202752, MaxOutputTokens: 131072},
	}})
	if err := os.MkdirAll(filepath.Dir(snapPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	c := newRecommendationsCache()
	c.loadSnapshot()
	got := c.Get()
	if len(got) != 2 || got[0].Model != "kimi-k2.6:cloud" {
		t.Fatalf("snapshot not loaded: %#v", got)
	}
}

// TestRecommendationsGetSWRDoesNotBlock ensures a read returns promptly even
// though it may schedule a background refresh.
func TestRecommendationsGetSWRDoesNotBlock(t *testing.T) {
	c := newRecommendationsCache()
	start := time.Now()
	recs := c.GetSWR(context.Background())
	if time.Since(start) > time.Second {
		t.Fatalf("GetSWR blocked too long: %v", time.Since(start))
	}
	if len(recs) != len(defaultRecommendations) {
		t.Fatalf("recs = %d, want %d", len(recs), len(defaultRecommendations))
	}
}

func TestIsCloudRecommendation(t *testing.T) {
	for model, want := range map[string]bool{
		"kimi-k2.6:cloud": true,
		"foo-cloud":       true,
		"bar:cloud":       true,
		"glm-5.2":         false,
		"":                false,
	} {
		if got := isCloudRecommendation(model); got != want {
			t.Errorf("isCloudRecommendation(%q) = %v, want %v", model, got, want)
		}
	}
}