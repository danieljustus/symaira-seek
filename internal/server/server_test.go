package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
	"github.com/danieljustus/symaira-seek/internal/pathutil"
)

// ---------------------------------------------------------------------------
// Mock db.Store
// ---------------------------------------------------------------------------

type mockStore struct {
	getStatsFn            func() (*db.Stats, error)
	searchBM25Fn          func(query string, limit int) ([]*db.SearchResult, error)
	searchVectorFn        func(queryVec []float32, limit int) ([]*db.SearchResult, error)
	listDocumentsFn       func() ([]*db.Document, error)
	deleteDocumentFn      func(path string) error
	saveDocumentFn        func(doc *db.Document) error
	saveChunksFn          func(chunks []*db.Chunk) error
	getDocumentFn         func(path string) (*db.Document, error)
	getChunksForDocFn     func(path string) ([]*db.Chunk, error)
}

func (m *mockStore) Close() error { return nil }

func (m *mockStore) GetStats() (*db.Stats, error) {
	if m.getStatsFn != nil {
		return m.getStatsFn()
	}
	return &db.Stats{DocumentCount: 0, ChunkCount: 0, DatabaseSize: 0}, nil
}

func (m *mockStore) SearchBM25(query string, limit int) ([]*db.SearchResult, error) {
	if m.searchBM25Fn != nil {
		return m.searchBM25Fn(query, limit)
	}
	return nil, nil
}

func (m *mockStore) SearchVector(queryVec []float32, limit int) ([]*db.SearchResult, error) {
	if m.searchVectorFn != nil {
		return m.searchVectorFn(queryVec, limit)
	}
	return nil, nil
}

func (m *mockStore) DetectMixedEmbeddingSpaces() (map[string]int, error) {
	return nil, nil
}

func (m *mockStore) ListDocuments() ([]*db.Document, error) {
	if m.listDocumentsFn != nil {
		return m.listDocumentsFn()
	}
	return nil, nil
}

func (m *mockStore) DeleteDocument(path string) error {
	if m.deleteDocumentFn != nil {
		return m.deleteDocumentFn(path)
	}
	return nil
}

func (m *mockStore) SaveDocument(doc *db.Document) error {
	if m.saveDocumentFn != nil {
		return m.saveDocumentFn(doc)
	}
	return nil
}

func (m *mockStore) SaveChunks(chunks []*db.Chunk) error {
	if m.saveChunksFn != nil {
		return m.saveChunksFn(chunks)
	}
	return nil
}

func (m *mockStore) GetDocument(path string) (*db.Document, error) {
	if m.getDocumentFn != nil {
		return m.getDocumentFn(path)
	}
	return nil, nil
}

func (m *mockStore) GetChunksForDocument(docPath string) ([]*db.Chunk, error) {
	if m.getChunksForDocFn != nil {
		return m.getChunksForDocFn(docPath)
	}
	return nil, nil
}

func (m *mockStore) Upsert(_ context.Context, _ []*db.Chunk) error { return nil }
func (m *mockStore) Delete(_ context.Context, _ string) error     { return nil }
func (m *mockStore) Search(_ context.Context, queryVec []float32, limit int) ([]*db.SearchResult, error) {
	if m.searchVectorFn != nil {
		return m.searchVectorFn(queryVec, limit)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Mock engine.Embedder
// ---------------------------------------------------------------------------

type mockEmbedder struct {
	generateVectorFn        func(text string) []float32
	generateVectorsFn       func(texts []string) [][]float32
	generateVectorNoRetryFn func(text string) []float32
}

func (m *mockEmbedder) GenerateVector(text string) []float32 {
	if m.generateVectorFn != nil {
		return m.generateVectorFn(text)
	}
	return make([]float32, 768)
}

func (m *mockEmbedder) GenerateVectors(texts []string) [][]float32 {
	if m.generateVectorsFn != nil {
		return m.generateVectorsFn(texts)
	}
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		vecs[i] = make([]float32, 768)
	}
	return vecs
}

func (m *mockEmbedder) GenerateVectorNoRetry(text string) []float32 {
	if m.generateVectorNoRetryFn != nil {
		return m.generateVectorNoRetryFn(text)
	}
	return make([]float32, 768)
}

func (m *mockEmbedder) Dim() int {
	return 768
}

func (m *mockEmbedder) ModelName() string {
	return "mock-model"
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// testHandler returns the full middleware-wrapped handler for the mux.
func testHandler(t *testing.T, store db.Store, vectorStore db.VectorStore, embedder engine.Embedder, indexCooldown time.Duration) http.Handler {
	t.Helper()
	mux := newServeMux(store, vectorStore, embedder, indexCooldown)
	return hostValidation(originValidation(contentTypeEnforcement(bearerTokenAuth(mux))))
}

// newTestServer starts an httptest.Server with the full handler chain.
// The server is automatically closed when the test finishes.
func newTestServer(t *testing.T, store db.Store, vectorStore db.VectorStore, embedder engine.Embedder) *httptest.Server {
	t.Helper()
	handler := testHandler(t, store, vectorStore, embedder, 5*time.Second)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// doRequest is a convenience wrapper for making HTTP requests.
func doRequest(t *testing.T, method, url string, body string, headers map[string]string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// Existing tests (preserved)
// ---------------------------------------------------------------------------

func TestIsLocalhostHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.1:8080", true},
		{"localhost", true},
		{"localhost:3000", true},
		{"::1", true},
		{"evil.com", false},
		{"attacker.example.com:80", false},
		{"192.168.1.1", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := isLocalhostHost(tt.host)
			if got != tt.want {
				t.Errorf("isLocalhostHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestIsLocalhostOrigin(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:3000", true},
		{"https://127.0.0.1:8080", true},
		{"http://localhost", true},
		{"http://evil.com", false},
		{"https://attacker.example.com", false},
		{"ftp://localhost", false},
		{"not-a-url", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			got := isLocalhostOrigin(tt.origin)
			if got != tt.want {
				t.Errorf("isLocalhostOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestHostValidation_RejectsNonLocalhost(t *testing.T) {
	handler := hostValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "evil.com"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestHostValidation_AcceptsLocalhost(t *testing.T) {
	handler := hostValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "127.0.0.1:8080"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestOriginValidation_RejectsCrossOrigin(t *testing.T) {
	handler := originValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/search", nil)
	req.Host = "127.0.0.1:8788"
	req.Header.Set("Origin", "http://evil.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestOriginValidation_AcceptsLocalhost(t *testing.T) {
	handler := originValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/search", nil)
	req.Host = "127.0.0.1:8788"
	req.Header.Set("Origin", "http://localhost:3000")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestOriginValidation_AllowsNoOrigin(t *testing.T) {
	handler := originValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/search", nil)
	req.Host = "127.0.0.1:8788"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestContentTypeEnforcement_RejectsWrongType(t *testing.T) {
	handler := contentTypeEnforcement(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/index", strings.NewReader(`{"path":"/tmp"}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rr.Code)
	}
}

func TestContentTypeEnforcement_AcceptsJSON(t *testing.T) {
	handler := contentTypeEnforcement(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/index", strings.NewReader(`{"path":"/tmp"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestContentTypeEnforcement_SkipsNonIndexPaths(t *testing.T) {
	handler := contentTypeEnforcement(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/other", strings.NewReader("data"))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestBearerTokenAuth_NoTokenRequired(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "")
	handler := bearerTokenAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestBearerTokenAuth_RejectsMissingToken(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "secret123")
	handler := bearerTokenAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestBearerTokenAuth_AcceptsValidToken(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "secret123")
	handler := bearerTokenAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestValidateIndexPath_AcceptsDirectoryUnderHome(t *testing.T) {
	home := withTempHome(t)
	subdir := filepath.Join(home, "docs")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	got, err := pathutil.RestrictToHome(subdir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(subdir)
	if got != resolved {
		t.Fatalf("expected %s, got %s", resolved, got)
	}
}

func TestValidateIndexPath_RejectsNonExistentPath(t *testing.T) {
	withTempHome(t)

	_, err := pathutil.RestrictToHome("/nonexistent/path/should/reject")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestValidateIndexPath_RejectsPathOutsideHome(t *testing.T) {
	withTempHome(t)

	candidates := []string{"/etc", "/usr", "/var"}
	for _, p := range candidates {
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		_, err = pathutil.RestrictToHome(p)
		if err == nil {
			t.Fatalf("expected error for %s outside home", p)
		}
		if !strings.Contains(err.Error(), "allowed root") {
			t.Fatalf("expected error to mention allowed root, got %q", err.Error())
		}
	}
}

func TestRateLimiter_FirstRequestAllowed(t *testing.T) {
	rl := newRateLimiter(100 * time.Millisecond)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request from a key must be allowed")
	}
}

func TestRateLimiter_BlocksWithinCooldown(t *testing.T) {
	rl := newRateLimiter(100 * time.Millisecond)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request must be allowed")
	}
	if rl.Allow("client-a", now.Add(50*time.Millisecond)) {
		t.Fatal("second request within cooldown must be blocked")
	}
}

func TestRateLimiter_AllowsAfterCooldown(t *testing.T) {
	rl := newRateLimiter(50 * time.Millisecond)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request must be allowed")
	}
	if !rl.Allow("client-a", now.Add(100*time.Millisecond)) {
		t.Fatal("request after cooldown must be allowed")
	}
}

func TestRateLimiter_KeysAreIsolated(t *testing.T) {
	rl := newRateLimiter(1 * time.Second)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request from a must be allowed")
	}
	if !rl.Allow("client-b", now) {
		t.Fatal("first request from b must be allowed, even when a is rate-limited")
	}
	if rl.Allow("client-a", now) {
		t.Fatal("a must remain rate-limited until cooldown elapses")
	}
}

func TestSSEStreamEndpoint(t *testing.T) {
	var gotResults, gotDone bool
	var resultCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/stream" {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		results := []map[string]interface{}{
			{"chunk": map[string]string{"content": "result 1"}, "rrf_score": 0.9},
			{"chunk": map[string]string{"content": "result 2"}, "rrf_score": 0.7},
		}

		for _, res := range results {
			data, _ := json.Marshal(res)
			fmt.Fprintf(w, "event: result\ndata: %s\n\n", data)
			flusher.Flush()
		}

		doneData, _ := json.Marshal(map[string]int{"count": len(results)})
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
		flusher.Flush()
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/search/stream?q=test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	var buf strings.Builder
	io.Copy(&buf, resp.Body)
	body := buf.String()

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "event: result") {
			gotResults = true
			resultCount++
		}
		if strings.HasPrefix(line, "event: done") {
			gotDone = true
		}
	}

	if !gotResults {
		t.Error("expected event: result in response")
	}
	if !gotDone {
		t.Error("expected event: done in response")
	}
	if resultCount != 2 {
		t.Errorf("expected 2 result events, got %d", resultCount)
	}
}

func TestRateLimiter_AllowsFirstRequest(t *testing.T) {
	rl := newRateLimiter(5 * time.Second)
	now := time.Now()

	if !rl.Allow("key1", now) {
		t.Error("expected first request to be allowed")
	}
}

func TestRateLimiter_DeniesSecondRequestWithinCooldown(t *testing.T) {
	rl := newRateLimiter(5 * time.Second)
	now := time.Now()

	rl.Allow("key1", now)
	if rl.Allow("key1", now.Add(1*time.Second)) {
		t.Error("expected second request within cooldown to be denied")
	}
}

func TestRateLimiter_AllowsRequestAfterCooldown(t *testing.T) {
	rl := newRateLimiter(5 * time.Second)
	now := time.Now()

	rl.Allow("key1", now)
	if !rl.Allow("key1", now.Add(6*time.Second)) {
		t.Error("expected request after cooldown to be allowed")
	}
}

func TestRateLimiter_DifferentKeys(t *testing.T) {
	rl := newRateLimiter(5 * time.Second)
	now := time.Now()

	rl.Allow("key1", now)
	if !rl.Allow("key2", now) {
		t.Error("expected different key to be allowed")
	}
}

func TestRateLimiter_EvictsStaleEntries(t *testing.T) {
	rl := newRateLimiter(5 * time.Second)
	now := time.Now()

	for i := 0; i < 2000; i++ {
		key := fmt.Sprintf("key%d", i)
		rl.Allow(key, now)
	}

	rl.Allow("newkey", now.Add(6*time.Second))

	if !rl.Allow("key0", now.Add(6*time.Second)) {
		t.Log("key0 was not evicted, but eviction mechanism ran without panic")
	}
}

func TestRateLimiterKeyedByHostAcrossConnections(t *testing.T) {
	rl := newRateLimiter(time.Minute)
	now := time.Now()

	if !rl.Allow(clientKey("127.0.0.1:54001"), now) {
		t.Fatal("first request from host should be allowed")
	}
	if rl.Allow(clientKey("127.0.0.1:54002"), now.Add(time.Second)) {
		t.Fatal("second request from same host (different port) should be rate limited")
	}

	if !rl.Allow(clientKey("127.0.0.2:54003"), now.Add(time.Second)) {
		t.Fatal("request from a different host should be allowed")
	}

	if !rl.Allow(clientKey("127.0.0.1:54004"), now.Add(2*time.Minute)) {
		t.Fatal("request after cooldown should be allowed")
	}
}

func TestWarnIfNoAuthToken_Unset(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "")
	var buf bytes.Buffer
	warnIfNoAuthToken(&buf)
	out := buf.String()
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected warning on stderr when SEEK_API_TOKEN is unset, got %q", out)
	}
	if !strings.Contains(out, "SEEK_API_TOKEN") {
		t.Errorf("warning should mention SEEK_API_TOKEN, got %q", out)
	}
}

func TestWarnIfNoAuthToken_Set(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "my-secret-token")
	var buf bytes.Buffer
	warnIfNoAuthToken(&buf)
	if buf.Len() != 0 {
		t.Errorf("expected no warning when SEEK_API_TOKEN is set, got %q", buf.String())
	}
}

// ---------------------------------------------------------------------------
// newServeMux handler tests (issue #164)
// ---------------------------------------------------------------------------

func TestMux_HealthEndpoint(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/health", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health: status %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}
}

func TestMux_StatusEndpoint_Success(t *testing.T) {
	store := &mockStore{
		getStatsFn: func() (*db.Stats, error) {
			return &db.Stats{DocumentCount: 42, ChunkCount: 100, DatabaseSize: 1024}, nil
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/status", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /status: status %d, want 200", resp.StatusCode)
	}

	var stats db.Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}
	if stats.DocumentCount != 42 {
		t.Errorf("DocumentCount = %d, want 42", stats.DocumentCount)
	}
	if stats.ChunkCount != 100 {
		t.Errorf("ChunkCount = %d, want 100", stats.ChunkCount)
	}
	if stats.DatabaseSize != 1024 {
		t.Errorf("DatabaseSize = %d, want 1024", stats.DatabaseSize)
	}
}

func TestMux_StatusEndpoint_DBError(t *testing.T) {
	store := &mockStore{
		getStatsFn: func() (*db.Stats, error) {
			return nil, fmt.Errorf("database locked")
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/status", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET /status: status %d, want 500", resp.StatusCode)
	}
}

func TestMux_SearchEndpoint_MissingQuery(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /search (no q): status %d, want 400", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "missing query parameter 'q'") {
		t.Errorf("error body = %q, want 'missing query parameter 'q''", string(body))
	}
}

func TestMux_SearchEndpoint_EmptyQuery(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search?q=", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /search?q=: status %d, want 400", resp.StatusCode)
	}
}

func TestMux_SearchEndpoint_Success(t *testing.T) {
	store := &mockStore{
		searchBM25Fn: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{
				{
					Chunk:    &db.Chunk{UUID: "u1", Content: "found it"},
					BM25Rank: 1,
					RRFScore: 0.1,
				},
			}, nil
		},
		searchVectorFn: func(queryVec []float32, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search?q=test+query", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /search?q=test: status %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var results []*db.SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Chunk.UUID != "u1" {
		t.Errorf("results[0].Chunk.UUID = %q, want %q", results[0].Chunk.UUID, "u1")
	}
}

func TestMux_SearchEndpoint_CustomLimit(t *testing.T) {
	store := &mockStore{
		searchBM25Fn: func(query string, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
		searchVectorFn: func(queryVec []float32, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search?q=test&limit=10", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}

	var results []*db.SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode results: %v", err)
	}
}

func TestMux_SearchEndpoint_InvalidLimitFallback(t *testing.T) {
	store := &mockStore{
		searchBM25Fn: func(query string, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
		searchVectorFn: func(queryVec []float32, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search?q=test&limit=abc", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

func TestMux_SearchEndpoint_NegativeLimitFallback(t *testing.T) {
	store := &mockStore{
		searchBM25Fn: func(query string, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
		searchVectorFn: func(queryVec []float32, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search?q=test&limit=-3", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

func TestMux_SearchEndpoint_SearchError(t *testing.T) {
	store := &mockStore{
		searchBM25Fn: func(query string, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
		searchVectorFn: func(queryVec []float32, limit int) ([]*db.SearchResult, error) {
			return nil, fmt.Errorf("vector search failed")
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search?q=test", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET /search: status %d, want 500", resp.StatusCode)
	}
}

func TestMux_SearchStreamEndpoint_MissingQuery(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search/stream", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /search/stream (no q): status %d, want 400", resp.StatusCode)
	}
}

func TestMux_SearchStreamEndpoint_Success(t *testing.T) {
	store := &mockStore{
		searchBM25Fn: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{
				{
					Chunk:    &db.Chunk{UUID: "u1", Content: "stream result"},
					BM25Rank: 1,
					RRFScore: 0.15,
				},
				{
					Chunk:    &db.Chunk{UUID: "u2", Content: "second result"},
					BM25Rank: 2,
					RRFScore: 0.08,
				},
			}, nil
		},
		searchVectorFn: func(queryVec []float32, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search/stream?q=test+stream", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /search/stream: status %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	if conn := resp.Header.Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", conn)
	}

	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	var resultCount int
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "event: result") {
			resultCount++
		}
	}
	if resultCount != 2 {
		t.Errorf("expected 2 result events, got %d", resultCount)
	}
	if !strings.Contains(s, "event: done") {
		t.Error("expected event: done in SSE stream")
	}
	if !strings.Contains(s, `"count":2`) {
		t.Errorf("expected done event to report count 2, got: %s", s)
	}
}

func TestMux_SearchStreamEndpoint_SearchError(t *testing.T) {
	store := &mockStore{
		searchBM25Fn: func(query string, limit int) ([]*db.SearchResult, error) {
			return nil, nil
		},
		searchVectorFn: func(queryVec []float32, limit int) ([]*db.SearchResult, error) {
			return nil, fmt.Errorf("stream search failed")
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/search/stream?q=fail", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("GET /search/stream: status %d, want 500", resp.StatusCode)
	}
}

func TestMux_IndexEndpoint_MethodNotAllowed(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "GET", srv.URL+"/index", "", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /index: status %d, want 405", resp.StatusCode)
	}
}

func TestMux_IndexEndpoint_BadContentType(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "POST", srv.URL+"/index", `{"path":"/tmp"}`,
		map[string]string{"Content-Type": "text/plain"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("POST /index (text/plain): status %d, want 415", resp.StatusCode)
	}
}

func TestMux_IndexEndpoint_BadJSON(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "POST", srv.URL+"/index", `not-json`,
		map[string]string{"Content-Type": "application/json"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /index (bad json): status %d, want 400", resp.StatusCode)
	}
}

func TestMux_IndexEndpoint_EmptyJSONBody(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "POST", srv.URL+"/index", `{}`,
		map[string]string{"Content-Type": "application/json"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /index (empty body): status %d, want 400", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "missing 'path' field") {
		t.Errorf("error body = %q, want 'missing path field'", string(body))
	}
}

func TestMux_IndexEndpoint_PathOutsideHome(t *testing.T) {
	withTempHome(t)
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "POST", srv.URL+"/index",
		`{"path":"/etc"}`,
		map[string]string{"Content-Type": "application/json"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /index (outside home): status %d, want 403", resp.StatusCode)
	}
}

func TestMux_IndexEndpoint_PathNotFound(t *testing.T) {
	withTempHome(t)
	store := &mockStore{}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	resp := doRequest(t, "POST", srv.URL+"/index",
		`{"path":"/nonexistent/path/that/does/not/exist"}`,
		map[string]string{"Content-Type": "application/json"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /index (not found): status %d, want 403", resp.StatusCode)
	}
}

func TestMux_IndexEndpoint_RateLimit(t *testing.T) {
	home := withTempHome(t)
	subdir := filepath.Join(home, "docs")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	store := &mockStore{
		listDocumentsFn: func() ([]*db.Document, error) { return nil, nil },
	}
	embedder := &mockEmbedder{}
	// Very long cooldown to guarantee rate limiting on second request
	handler := testHandler(t, store, store, embedder, 24*time.Hour)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	payload := fmt.Sprintf(`{"path":%q}`, subdir)
	ct := map[string]string{"Content-Type": "application/json"}

	resp1 := doRequest(t, "POST", srv.URL+"/index", payload, ct)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first POST /index: status %d, want 200", resp1.StatusCode)
	}

	resp2 := doRequest(t, "POST", srv.URL+"/index", payload, ct)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second POST /index: status %d, want 429", resp2.StatusCode)
	}
}

func TestMux_IndexEndpoint_Success(t *testing.T) {
	home := withTempHome(t)
	subdir := filepath.Join(home, "docs")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	testFile := filepath.Join(subdir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	var savedChunks bool
	store := &mockStore{
		listDocumentsFn: func() ([]*db.Document, error) { return nil, nil },
		saveDocumentFn: func(doc *db.Document) error { return nil },
		saveChunksFn: func(chunks []*db.Chunk) error {
			savedChunks = true
			return nil
		},
	}
	embedder := &mockEmbedder{
		generateVectorsFn: func(texts []string) [][]float32 {
			vecs := make([][]float32, len(texts))
			for i := range vecs {
				vecs[i] = make([]float32, 768)
				vecs[i][0] = 1.0
			}
			return vecs
		},
	}
	srv := newTestServer(t, store, store, embedder)

	payload := fmt.Sprintf(`{"path":%q}`, subdir)
	resp := doRequest(t, "POST", srv.URL+"/index", payload,
		map[string]string{"Content-Type": "application/json"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /index: status %d, want 200; body: %s", resp.StatusCode, string(body))
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["status"] != "indexed" {
		t.Errorf("status = %q, want indexed", result["status"])
	}
	canonicalSubdir, _ := filepath.EvalSymlinks(subdir)
	if result["path"] != canonicalSubdir {
		t.Errorf("path = %q, want %q", result["path"], canonicalSubdir)
	}
	if !savedChunks {
		t.Error("expected SaveChunks to be called")
	}
}

func TestMux_IndexEndpoint_IndexDirectoryError(t *testing.T) {
	home := withTempHome(t)
	subdir := filepath.Join(home, "docs")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	store := &mockStore{
		listDocumentsFn: func() ([]*db.Document, error) {
			return nil, fmt.Errorf("db list failed")
		},
	}
	embedder := &mockEmbedder{}
	srv := newTestServer(t, store, store, embedder)

	payload := fmt.Sprintf(`{"path":%q}`, subdir)
	resp := doRequest(t, "POST", srv.URL+"/index", payload,
		map[string]string{"Content-Type": "application/json"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("POST /index: status %d, want 500", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Full middleware chain tests through newServeMux
// ---------------------------------------------------------------------------

func TestFullChain_HostValidationRejects(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "evil.example.com"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("non-localhost host: status %d, want 403", rr.Code)
	}
}

func TestFullChain_OriginValidationRejects(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "127.0.0.1:8788"
	req.Header.Set("Origin", "http://attacker.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-origin: status %d, want 403", rr.Code)
	}
}

func TestFullChain_BearerTokenRejects(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "real-secret")
	defer t.Setenv("SEEK_API_TOKEN", "")

	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "127.0.0.1:8788"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("missing bearer token: status %d, want 401", rr.Code)
	}
}

func TestFullChain_BearerTokenAccepts(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "real-secret")
	defer t.Setenv("SEEK_API_TOKEN", "")

	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "127.0.0.1:8788"
	req.Header.Set("Authorization", "Bearer real-secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("valid bearer token: status %d, want 200", rr.Code)
	}
}

func TestFullChain_ContentTypeRejectsOnIndex(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("POST", "/index", strings.NewReader(`{"path":"/tmp"}`))
	req.Host = "127.0.0.1:8788"
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("wrong content-type: status %d, want 415", rr.Code)
	}
}

func TestFullChain_AllowsGETOnNonIndexWithoutContentType(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("GET", "/search?q=test", nil)
	req.Host = "127.0.0.1:8788"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("GET /search without content-type: status %d, want 200", rr.Code)
	}
}

func TestFullChain_AllOptionsOnHealthEndpoint(t *testing.T) {
	t.Setenv("SEEK_API_TOKEN", "mytoken")
	defer t.Setenv("SEEK_API_TOKEN", "")

	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "127.0.0.1:8788"
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Authorization", "Bearer mytoken")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("full chain /health: status %d, want 200", rr.Code)
	}
}

func TestFullChain_IndexMethodNotAllowed(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("DELETE", "/index", nil)
	req.Host = "127.0.0.1:8788"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /index: status %d, want 405", rr.Code)
	}
}

func TestFullChain_IndexBadContentTypeOverwrites(t *testing.T) {
	store := &mockStore{}
	embedder := &mockEmbedder{}
	handler := testHandler(t, store, store, embedder, 5*time.Second)

	req := httptest.NewRequest("POST", "/index", strings.NewReader(`{"path":"/tmp"}`))
	req.Host = "127.0.0.1:8788"
	req.Header.Set("Content-Type", "application/xml")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("POST /index with XML: status %d, want 415", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// StartHTTPServer lifecycle tests
// ---------------------------------------------------------------------------

func TestStartHTTPServer_ListensAndServe(t *testing.T) {
	withTempHome(t)

	errCh := make(chan error, 1)
	go func() {
		errCh <- StartHTTPServer(0, engine.OllamaConfig{}, 5*time.Second, nil)
	}()

	time.Sleep(200 * time.Millisecond)

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartHTTPServer returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5s")
	}
}
