// Model recommendations: a small built-in default list of cloud models,
// refreshed online (stale-while-revalidate) from ollama.com, snapshot-persisted
// under ~/.ollama-lite/cache. This mirrors the official Ollama
// server/model_recommendations.go, adapted to ollama-lite (cloud-only defaults,
// the ollama-lite cache directory, and config.CloudBaseURL() as the upstream).
//
// When the server has no explicitly configured model list, /api/tags and
// /v1/models advertise these recommendation names; the richer objects are also
// served verbatim at /api/experimental/model-recommendations, for parity with
// the official server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ollama-lite/internal/config"
)

// recommendationsEndpoint is the path appended to the cloud base URL. It matches
// the official Ollama endpoint.
const recommendationsEndpoint = "/api/experimental/model-recommendations"

var (
	recommendationsRefreshInterval     = 4 * time.Hour
	recommendationsFetchTimeout        = 3 * time.Second
	recommendationsReadRefreshCooldown = 5 * time.Second
	recommendationsBackoffSteps        = []time.Duration{
		5 * time.Minute,
		15 * time.Minute,
		time.Hour,
		4 * time.Hour,
	}
)

// Recommendation is a single recommended-model entry, mirroring the relevant
// fields of Ollama's api.ModelRecommendation.
type Recommendation struct {
	Model           string `json:"model"`
	Description     string `json:"description"`
	ContextLength   int    `json:"context_length,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	VRAMBytes       int64  `json:"vram_bytes,omitempty"`
	RequiredPlan    string `json:"required_plan,omitempty"`
}

type recommendationsResponse struct {
	Recommendations []Recommendation `json:"recommendations"`
}

// defaultRecommendations is the built-in cloud-only default list, used before
// the first online refresh succeeds and as a fallback if it never does. The
// official Ollama also lists local models (gemma4, qwen3.5) here; ollama-lite
// is cloud-only, so those are intentionally omitted — advertising a model the
// upstream cannot run would only produce 404s.
var defaultRecommendations = []Recommendation{
	{
		Model:           "kimi-k2.6:cloud",
		Description:     "State-of-the-art coding, long-horizon execution, and multimodal agent swarm capability",
		ContextLength:   262_144,
		MaxOutputTokens: 262_144,
	},
	{
		Model:           "glm-5.1:cloud",
		Description:     "Reasoning and code generation",
		ContextLength:   202_752,
		MaxOutputTokens: 131_072,
	},
	{
		Model:           "qwen3.5:cloud",
		Description:     "Reasoning, coding, and agentic tool use with vision",
		ContextLength:   262_144,
		MaxOutputTokens: 32_768,
	},
	{
		Model:           "minimax-m2.7:cloud",
		Description:     "Fast, efficient coding and real-world productivity",
		ContextLength:   204_800,
		MaxOutputTokens: 128_000,
	},
}

// recommendationsCache holds the current recommendation list and refreshes it
// from the cloud on a stale-while-revalidate basis. It is safe for concurrent
// use. Reads never block on the network: they return the cached list and, at
// most, trigger an asynchronous background refresh.
type recommendationsCache struct {
	mu                   sync.RWMutex
	recs                 []Recommendation
	refreshing           bool
	nextReadRefreshAfter time.Time

	once   sync.Once
	client *http.Client
}

func newRecommendationsCache() *recommendationsCache {
	return &recommendationsCache{
		recs:   cloneRecommendations(defaultRecommendations),
		client: &http.Client{Timeout: recommendationsFetchTimeout},
	}
}

// Start loads any persisted snapshot and begins the background refresh loop.
// It is idempotent.
func (c *recommendationsCache) Start(ctx context.Context) {
	c.once.Do(func() {
		c.loadSnapshot()
		go c.run(ctx)
	})
}

// Get returns a clone of the current recommendations.
func (c *recommendationsCache) Get() []Recommendation {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneRecommendations(c.recs)
}

// GetSWR returns the current recommendations and, if a refresh is not already
// running and the read cooldown has elapsed, triggers one in the background so
// the next read sees fresher data. The refresh runs on a detached context so
// it outlives the request that triggered it.
func (c *recommendationsCache) GetSWR(ctx context.Context) []Recommendation {
	recs := c.Get()
	c.triggerRefreshOnRead(ctx)
	return recs
}

// Names returns the recommendation model names in order, for /api/tags and
// /v1/models.
func (c *recommendationsCache) Names() []string {
	recs := c.Get()
	names := make([]string, 0, len(recs))
	for _, r := range recs {
		if r.Model != "" {
			names = append(names, r.Model)
		}
	}
	return names
}

func (c *recommendationsCache) set(recs []Recommendation) {
	c.mu.Lock()
	c.recs = cloneRecommendations(recs)
	c.mu.Unlock()
}

func (c *recommendationsCache) beginRefresh() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refreshing {
		return false
	}
	c.refreshing = true
	return true
}

func (c *recommendationsCache) endRefresh() {
	c.mu.Lock()
	c.refreshing = false
	c.mu.Unlock()
}

func (c *recommendationsCache) beginReadRefresh() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refreshing || time.Now().Before(c.nextReadRefreshAfter) {
		return false
	}
	c.refreshing = true
	return true
}

func (c *recommendationsCache) endReadRefresh() {
	c.mu.Lock()
	c.refreshing = false
	c.nextReadRefreshAfter = time.Now().Add(recommendationsReadRefreshCooldown)
	c.mu.Unlock()
}

func (c *recommendationsCache) refreshIfIdle(ctx context.Context) (bool, error) {
	if !c.beginRefresh() {
		return false, nil
	}
	defer c.endRefresh()
	return true, c.refresh(ctx)
}

// triggerRefreshOnRead starts an asynchronous refresh when the read cooldown
// has elapsed and no refresh is running.
func (c *recommendationsCache) triggerRefreshOnRead(ctx context.Context) {
	if !c.beginReadRefresh() {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		defer c.endReadRefresh()
		if err := c.refresh(detached); err != nil {
			log.Printf("ollama-lite: model recommendations refresh failed: %v", err)
		}
	}()
}

func (c *recommendationsCache) run(ctx context.Context) {
	failures := 0
	for {
		started, err := c.refreshIfIdle(ctx)
		switch {
		case !started:
			failures = 0
		case err == nil:
			failures = 0
		default:
			failures++
			log.Printf("ollama-lite: model recommendations refresh failed: %v", err)
		}

		var wait time.Duration
		if failures == 0 {
			wait = withJitter(recommendationsRefreshInterval)
		} else {
			wait = withJitter(recommendationsBackoffSteps[min(failures-1, len(recommendationsBackoffSteps)-1)])
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (c *recommendationsCache) refresh(ctx context.Context) error {
	endpoint := strings.TrimRight(config.CloudBaseURL(), "/") + recommendationsEndpoint

	reqCtx, cancel := context.WithTimeout(ctx, recommendationsFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("model recommendations: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload recommendationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
	}

	recs, err := validateRecommendations(payload.Recommendations)
	if err != nil {
		return err
	}

	c.set(recs)
	if err := c.persistSnapshot(recs); err != nil {
		log.Printf("ollama-lite: could not persist model recommendations snapshot: %v", err)
	}
	return nil
}

// validateRecommendations trims fields, rejects empty/duplicate model names,
// drops cloud entries that lack token limits, and rejects an empty result.
func validateRecommendations(recs []Recommendation) ([]Recommendation, error) {
	if len(recs) == 0 {
		return nil, errors.New("model recommendations: empty list")
	}

	seen := make(map[string]struct{}, len(recs))
	valid := make([]Recommendation, 0, len(recs))
	for _, rec := range recs {
		rec.Model = strings.TrimSpace(rec.Model)
		rec.Description = strings.TrimSpace(rec.Description)
		rec.RequiredPlan = strings.TrimSpace(rec.RequiredPlan)

		if rec.Model == "" {
			return nil, errors.New("model recommendations: entry missing model")
		}
		if _, ok := seen[rec.Model]; ok {
			return nil, fmt.Errorf("model recommendations: duplicate %q", rec.Model)
		}
		seen[rec.Model] = struct{}{}

		if isCloudRecommendation(rec.Model) && (rec.ContextLength <= 0 || rec.MaxOutputTokens <= 0) {
			log.Printf("ollama-lite: dropping cloud recommendation %q missing token limits", rec.Model)
			continue
		}
		valid = append(valid, rec)
	}

	if len(valid) == 0 {
		return nil, errors.New("model recommendations: no valid entries")
	}
	return valid, nil
}

func isCloudRecommendation(model string) bool {
	return strings.HasSuffix(model, ":cloud") || strings.HasSuffix(model, "-cloud")
}

// --- snapshot persistence ---------------------------------------------------

func recommendationsSnapshotPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ollama-lite", "cache", "model-recommendations.json"), nil
}

func (c *recommendationsCache) loadSnapshot() {
	path, err := recommendationsSnapshotPath()
	if err != nil {
		log.Printf("ollama-lite: could not resolve model recommendations snapshot path: %v", err)
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("ollama-lite: could not read model recommendations snapshot: %v", err)
		}
		return
	}

	var snap recommendationsResponse
	if err := json.Unmarshal(data, &snap); err != nil {
		log.Printf("ollama-lite: could not parse model recommendations snapshot: %v", err)
		return
	}

	recs, err := validateRecommendations(snap.Recommendations)
	if err != nil {
		log.Printf("ollama-lite: ignoring invalid model recommendations snapshot: %v", err)
		return
	}

	c.set(recs)
}

func (c *recommendationsCache) persistSnapshot(recs []Recommendation) error {
	path, err := recommendationsSnapshotPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(recommendationsResponse{Recommendations: recs}, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".model-recommendations-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// --- helpers ---------------------------------------------------------------

func cloneRecommendations(in []Recommendation) []Recommendation {
	out := make([]Recommendation, len(in))
	copy(out, in)
	return out
}

// withJitter scales d by a factor in [0.8, 1.2] so refreshes across machines
// don't land on the same instant.
func withJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	factor := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(d) * factor)
}