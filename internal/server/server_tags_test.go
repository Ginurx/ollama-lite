package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeTags(t *testing.T, body []byte) []string {
	t.Helper()
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal /api/tags: %v", err)
	}
	names := make([]string, 0, len(resp.Models))
	for _, m := range resp.Models {
		names = append(names, m.Name)
	}
	return names
}

func TestHandleTagsConfigured(t *testing.T) {
	s := New([]string{"gpt-oss:120b", "qwen3-coder:480b"}, nil)

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/tags", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got := decodeTags(t, rr.Body.Bytes())
	want := []string{"gpt-oss:120b", "qwen3-coder:480b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}

func TestHandleTagsRecommendationsWhenUnconfigured(t *testing.T) {
	s := New(nil, newRecommendationsCache())

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/tags", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got := decodeTags(t, rr.Body.Bytes())
	if len(got) != len(defaultRecommendations) {
		t.Fatalf("tags = %v, want %d recommendation names", got, len(defaultRecommendations))
	}
	// First entry must be the first builtin default.
	if got[0] != defaultRecommendations[0].Model {
		t.Fatalf("first tag = %q, want %q", got[0], defaultRecommendations[0].Model)
	}
}

func TestHandleRecommendationsEndpoint(t *testing.T) {
	s := New(nil, newRecommendationsCache())

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/experimental/model-recommendations", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp recommendationsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Recommendations) != len(defaultRecommendations) {
		t.Fatalf("recommendations = %d, want %d", len(resp.Recommendations), len(defaultRecommendations))
	}
	// Rich fields are preserved, not just names.
	if resp.Recommendations[0].ContextLength != defaultRecommendations[0].ContextLength {
		t.Errorf("first rec context_length = %d, want %d",
			resp.Recommendations[0].ContextLength, defaultRecommendations[0].ContextLength)
	}
}

func TestHandleV1ModelsRecommendations(t *testing.T) {
	s := New(nil, newRecommendationsCache())

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal /v1/models: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("object = %q, want list", resp.Object)
	}
	if len(resp.Data) != len(defaultRecommendations) {
		t.Fatalf("data = %d, want %d", len(resp.Data), len(defaultRecommendations))
	}
	if resp.Data[0].ID != defaultRecommendations[0].Model {
		t.Errorf("data[0].id = %q, want %q", resp.Data[0].ID, defaultRecommendations[0].Model)
	}
	if resp.Data[0].Object != "model" {
		t.Errorf("data[0].object = %q, want model", resp.Data[0].Object)
	}
}