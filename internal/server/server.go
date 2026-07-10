// Package server implements the Ollama-compatible HTTP server. Liveness and
// model-listing endpoints are answered locally; every other request is signed
// with the shared Ollama key and reverse-proxied (streaming) to ollama.com, so
// existing Ollama clients transparently run cloud models.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ollama-lite/internal/auth"
	"ollama-lite/internal/config"
)

// Version is ollama-lite's own version.
const Version = "0.1.0"

// defaultOllamaVersion is reported on /api/version and sent as the client
// version header. It tracks a real Ollama release so clients enable the full
// feature set. Override with OLLAMA_LITE_OLLAMA_VERSION.
const defaultOllamaVersion = "0.31.2"

const clientVersionHeader = "X-Ollama-Client-Version"

// hopByHopHeaders are not forwarded across the proxy (per RFC 7230).
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"content-length":      {},
	"proxy-connection":    {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

func reportedOllamaVersion() string {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_LITE_OLLAMA_VERSION")); v != "" {
		return v
	}
	return defaultOllamaVersion
}

// Server serves the Ollama-compatible API.
type Server struct {
	models  []string
	origins []*regexp.Regexp
}

// New builds a Server that advertises the given model list on /api/tags.
func New(models []string) *Server {
	return &Server{
		models:  models,
		origins: compileOrigins(config.AllowedOrigins()),
	}
}

// Handler returns the root http.Handler (routes wrapped with CORS handling).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", handleRoot)
	mux.HandleFunc("HEAD /{$}", handleRoot)
	mux.HandleFunc("GET /api/version", handleVersion)
	mux.HandleFunc("HEAD /api/version", handleVersion)
	mux.HandleFunc("GET /api/tags", s.handleTags)
	mux.HandleFunc("HEAD /api/tags", s.handleTags)
	mux.HandleFunc("GET /v1/models", s.handleV1Models)

	// Everything else is signed and proxied to the cloud.
	mux.HandleFunc("/", s.handleProxy)

	return s.withCORS(mux)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("Ollama is running"))
	}
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": reportedOllamaVersion()})
}

// --- model listing --------------------------------------------------------

type modelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type listModelResponse struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    modelDetails `json:"details"`
}

type listResponse struct {
	Models []listModelResponse `json:"models"`
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	resp := listResponse{Models: make([]listModelResponse, 0, len(s.models))}
	for _, m := range s.models {
		resp.Models = append(resp.Models, listModelResponse{
			Name:       m,
			Model:      m,
			ModifiedAt: now,
			Details:    modelDetails{Format: "gguf"},
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (s *Server) handleV1Models(w http.ResponseWriter, r *http.Request) {
	created := time.Now().Unix()
	data := make([]openAIModel, 0, len(s.models))
	for _, m := range s.models {
		data = append(data, openAIModel{ID: m, Object: "model", Created: created, OwnedBy: "library"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// --- cloud proxy ----------------------------------------------------------

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	body = maybeNormalizeModel(r, body)

	base, err := url.Parse(config.CloudBaseURL())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	target := *base
	target.Path = r.URL.Path
	target.RawQuery = r.URL.RawQuery

	outReq, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	copyHeaders(outReq.Header, r.Header)
	outReq.Header.Del("Authorization") // replaced by our signature below
	if outReq.Header.Get("Content-Type") == "" && len(body) > 0 {
		outReq.Header.Set("Content-Type", "application/json")
	}

	if err := signRequest(ctx, outReq); err != nil {
		s.writeUnauthorized(w)
		return
	}

	resp, err := http.DefaultClient.Do(outReq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		s.writeUnauthorized(w)
		return
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	streamCopy(w, resp.Body)
}

// signRequest sets a fresh ts query parameter and the Ollama-compatible
// Authorization header, matching signCloudProxyRequest / the api client.
func signRequest(ctx context.Context, req *http.Request) error {
	q := req.URL.Query()
	q.Set("ts", strconv.FormatInt(time.Now().Unix(), 10))
	req.URL.RawQuery = q.Encode()

	challenge := fmt.Sprintf("%s,%s", req.Method, req.URL.RequestURI())
	signature, err := auth.Sign(ctx, []byte(challenge))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", signature)
	req.Header.Set(clientVersionHeader, reportedOllamaVersion())
	return nil
}

// SignedRequest performs a signed request to the cloud (used by the whoami and
// signout CLI commands). The caller must close the returned response body.
func SignedRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	base, err := url.Parse(config.CloudBaseURL())
	if err != nil {
		return nil, err
	}
	base.Path = path

	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := signRequest(ctx, req); err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func (s *Server) writeUnauthorized(w http.ResponseWriter) {
	body := map[string]string{"error": "unauthorized"}
	if signinURL, err := auth.SigninURL(); err == nil {
		body["signin_url"] = signinURL
	}
	writeJSON(w, http.StatusUnauthorized, body)
}

// --- model normalization --------------------------------------------------

// maybeNormalizeModel strips a :cloud / -cloud source suffix from the JSON
// "model" field so requests for "glm-5.2:cloud" reach the cloud as "glm-5.2"
// (mirrors replaceJSONModelField + parseSourceSuffix in Ollama).
func maybeNormalizeModel(r *http.Request, body []byte) []byte {
	if r.Method != http.MethodPost || len(body) == 0 {
		return body
	}
	if r.Header.Get("Content-Encoding") != "" {
		return body
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "json") {
		return body
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	raw, ok := payload["model"]
	if !ok {
		return body
	}
	var model string
	if err := json.Unmarshal(raw, &model); err != nil {
		return body
	}

	base := stripCloudSuffix(model)
	if base == model {
		return body
	}

	encoded, err := json.Marshal(base)
	if err != nil {
		return body
	}
	payload["model"] = encoded
	normalized, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return normalized
}

// stripCloudSuffix removes an explicit cloud source suffix, matching
// modelref.parseSourceSuffix for the cloud cases.
func stripCloudSuffix(raw string) string {
	idx := strings.LastIndex(raw, ":")
	if idx < 0 {
		return raw
	}
	suffixRaw := strings.TrimSpace(raw[idx+1:])
	suffix := strings.ToLower(suffixRaw)
	switch suffix {
	case "cloud":
		return raw[:idx]
	}
	if !strings.Contains(suffixRaw, "/") && strings.HasSuffix(suffix, "-cloud") {
		return raw[:idx+1] + suffixRaw[:len(suffixRaw)-len("-cloud")]
	}
	return raw
}

// --- helpers --------------------------------------------------------------

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopByHop(key) || strings.EqualFold(key, "Host") {
			continue
		}
		dst.Del(key)
		for _, v := range values {
			dst.Add(key, v)
		}
	}
}

func isHopByHop(name string) bool {
	_, ok := hopByHopHeaders[strings.ToLower(name)]
	return ok
}

func streamCopy(w http.ResponseWriter, src io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("ollama-lite: write response: %v", err)
	}
}

// --- CORS -----------------------------------------------------------------

func compileOrigins(patterns []string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		escaped := regexp.QuoteMeta(strings.TrimSpace(p))
		escaped = strings.ReplaceAll(escaped, `\*`, `.*`)
		if re, err := regexp.Compile("(?i)^" + escaped + "$"); err == nil {
			res = append(res, re)
		}
	}
	return res
}

func (s *Server) originAllowed(origin string) bool {
	for _, re := range s.origins {
		if re.MatchString(origin) {
			return true
		}
	}
	return false
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == http.MethodOptions {
			h := w.Header()
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			reqHeaders := r.Header.Get("Access-Control-Request-Headers")
			if reqHeaders == "" {
				reqHeaders = "Authorization, Content-Type, Accept"
			}
			h.Set("Access-Control-Allow-Headers", reqHeaders)
			h.Set("Access-Control-Max-Age", "43200")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Serve starts the HTTP server on addr and blocks.
func Serve(ctx context.Context, addr string, models []string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: New(models).Handler(),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
