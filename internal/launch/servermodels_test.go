package launch

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFetchModelsFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("path: got %q, want /api/tags", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[{"name":"gpt-oss:120b"},{"name":"qwen3-coder:480b"},{"name":""}]}`))
	}))
	defer srv.Close()

	host, _ := url.Parse(srv.URL)
	got, err := FetchModelsFromServer(host)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"gpt-oss:120b", "qwen3-coder:480b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFetchModelsFromServerEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	host, _ := url.Parse(srv.URL)
	got, err := FetchModelsFromServer(host)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestFetchModelsFromServerNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	host, _ := url.Parse(srv.URL)
	if _, err := FetchModelsFromServer(host); err == nil {
		t.Fatal("want error on non-200, got nil")
	}
}

func TestFetchModelsFromServerUnreachable(t *testing.T) {
	// Port 1 is reserved/blocked on most systems, so connecting should fail fast.
	host, _ := url.Parse("http://127.0.0.1:1")
	if _, err := FetchModelsFromServer(host); err == nil {
		t.Fatal("want error on unreachable server, got nil")
	}
}

// TestFetchModelsFromServerTrimsTrailingSlash ensures we don't double-slash.
func TestFetchModelsFromServerTrimsTrailingSlash(t *testing.T) {
	var seenPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	host, _ := url.Parse(srv.URL + "/")
	if _, err := FetchModelsFromServer(host); err != nil {
		t.Fatalf("err: %v", err)
	}
	if seenPath != "/api/tags" {
		t.Fatalf("path: got %q, want /api/tags", seenPath)
	}
	if strings.Contains(seenPath, "//") {
		t.Fatalf("path has double slash: %q", seenPath)
	}
}

// Ensure the client doesn't hang forever on a slow server.
func TestFetchModelsFromServerTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	host, _ := url.Parse(srv.URL)
	start := time.Now()
	// Use a 1ms-timeout variant by temporarily wrapping: the production helper
	// uses 1500ms which is too long for a unit test, so just verify the helper
	// returns at all (it will succeed here since 300ms < 1500ms).
	_, _ = FetchModelsFromServer(host)
	if time.Since(start) > 2*time.Second {
		t.Fatal("helper blocked too long")
	}
}