package engine

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewEmbeddingsGeneratorWithOllamaConfig_AppliesConfig(t *testing.T) {
	cfg := OllamaConfig{
		URL:          "http://example.test/api/embeddings",
		Model:        "test-model",
		Timeout:      5 * time.Second,
		RetryCount:   3,
		RetryBackoff: 100 * time.Millisecond,
	}
	eg := NewEmbeddingsGeneratorWithOllamaConfig(cfg)
	eg.sleepFn = func(time.Duration) {}
	if eg.OllamaURL != cfg.URL {
		t.Errorf("expected URL %q, got %q", cfg.URL, eg.OllamaURL)
	}
	if eg.Model != cfg.Model {
		t.Errorf("expected Model %q, got %q", cfg.Model, eg.Model)
	}
	if eg.Timeout != cfg.Timeout {
		t.Errorf("expected Timeout %v, got %v", cfg.Timeout, eg.Timeout)
	}
	if eg.RetryCount != cfg.RetryCount {
		t.Errorf("expected RetryCount %d, got %d", cfg.RetryCount, eg.RetryCount)
	}
	if eg.RetryBackoff != cfg.RetryBackoff {
		t.Errorf("expected RetryBackoff %v, got %v", cfg.RetryBackoff, eg.RetryBackoff)
	}
	if eg.httpClient.Timeout != cfg.Timeout {
		t.Errorf("expected httpClient.Timeout %v, got %v", cfg.Timeout, eg.httpClient.Timeout)
	}
}

func TestNewEmbeddingsGeneratorWithOllamaConfig_ZeroValuesGetDefaults(t *testing.T) {
	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{})
	if eg.OllamaURL == "" {
		t.Error("expected default OllamaURL")
	}
	if eg.Model == "" {
		t.Error("expected default Model")
	}
	if eg.Timeout != defaultOllamaTimeout {
		t.Errorf("expected default Timeout %v, got %v", defaultOllamaTimeout, eg.Timeout)
	}
	if eg.RetryCount != defaultOllamaRetries {
		t.Errorf("expected default RetryCount %d, got %d", defaultOllamaRetries, eg.RetryCount)
	}
	if eg.RetryBackoff != defaultOllamaBackoff {
		t.Errorf("expected default RetryBackoff %v, got %v", defaultOllamaBackoff, eg.RetryBackoff)
	}
}

func TestNewEmbeddingsGeneratorWithOllamaConfig_NegativeRetryClampedToDefault(t *testing.T) {
	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{RetryCount: -5})
	if eg.RetryCount != defaultOllamaRetries {
		t.Errorf("expected negative RetryCount clamped to default %d, got %d", defaultOllamaRetries, eg.RetryCount)
	}
}

func TestNewEmbeddingsGeneratorWithOllamaConfig_PositiveRetryCountHonored(t *testing.T) {
	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{RetryCount: 5})
	if eg.RetryCount != 5 {
		t.Errorf("expected RetryCount 5, got %d", eg.RetryCount)
	}
}

func TestDoOllamaRequest_SuccessOnFirstTry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"embedding": []float32{0.1, 0.2, 0.3},
		})
	}))
	defer srv.Close()

	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:          srv.URL,
		Model:        "test",
		RetryCount:   2,
		RetryBackoff: 10 * time.Millisecond,
	})
	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := eg.doOllamaRequest(srv.URL, []byte(`{}`), &res); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 call, got %d", got)
	}
	if len(res.Embedding) != 3 {
		t.Errorf("expected 3-dim embedding, got %d", len(res.Embedding))
	}
}

func TestDoOllamaRequest_RetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, "transient")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"embedding": []float32{0.5},
		})
	}))
	defer srv.Close()

	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:          srv.URL,
		Model:        "test",
		RetryCount:   2,
		RetryBackoff: 1 * time.Millisecond,
	})
	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := eg.doOllamaRequest(srv.URL, []byte(`{}`), &res); err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 calls (2 retries), got %d", got)
	}
}

func TestDoOllamaRequest_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:          srv.URL,
		Model:        "test",
		RetryCount:   2,
		RetryBackoff: 1 * time.Millisecond,
	})
	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := eg.doOllamaRequest(srv.URL, []byte(`{}`), &res); err == nil {
		t.Fatal("expected exhaustion error")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 calls (1 initial + 2 retries), got %d", got)
	}
}

func TestDoOllamaRequest_DoesNotRetry4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "client error")
	}))
	defer srv.Close()

	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:          srv.URL,
		Model:        "test",
		RetryCount:   3,
		RetryBackoff: 1 * time.Millisecond,
	})
	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	err := eg.doOllamaRequest(srv.URL, []byte(`{}`), &res)
	if err == nil {
		t.Fatal("expected error on 4xx")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 call (no retry on 4xx), got %d", got)
	}
}

func TestDoOllamaRequest_RetriesOnNetworkError(t *testing.T) {
	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:          "http://127.0.0.1:1/api/embeddings",
		Model:        "test",
		RetryCount:   1,
		RetryBackoff: 1 * time.Millisecond,
		Timeout:      200 * time.Millisecond,
	})
	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := eg.doOllamaRequest(eg.OllamaURL, []byte(`{}`), &res); err == nil {
		t.Fatal("expected connection error")
	}
}

func TestQueryOllama_Retries5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"embedding": []float32{0.7, 0.8, 0.9},
		})
	}))
	defer srv.Close()

	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:          srv.URL,
		Model:        "m",
		RetryCount:   2,
		RetryBackoff: 1 * time.Millisecond,
	})
	vec, err := eg.queryOllama("hello")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(vec) != 3 {
		t.Errorf("expected 3-dim vector, got %d", len(vec))
	}
}

// TestIsStopWordUsesPackageMap is a regression test for issue #47.
// The stop-word set must be consulted from the package-level map
// rather than rebuilt on every call. The set must still classify
// known stop words (English and German) and reject ordinary tokens.
// TestBatchURLDerivation verifies that batch embedding requests target
// /api/embed when the configured URL is /api/embeddings (issue #67).
func TestBatchURLDerivation(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		expected string
	}{
		{
			name:     "standard endpoint",
			config:   "http://localhost:11434/api/embeddings",
			expected: "http://localhost:11434/api/embed",
		},
		{
			name:     "non-standard endpoint unchanged",
			config:   "http://localhost:11434/custom",
			expected: "http://localhost:11434/custom",
		},
		{
			name:     "already embed endpoint",
			config:   "http://localhost:11434/api/embed",
			expected: "http://localhost:11434/api/embed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{URL: tt.config})
			if got := eg.batchURL(); got != tt.expected {
				t.Errorf("batchURL() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestGenerateVectors_BatchUsesCorrectEndpoint verifies that batch embedding
// requests go to /api/embed, not /api/embeddings (issue #67).
func TestGenerateVectors_BatchUsesCorrectEndpoint(t *testing.T) {
	var singleHits, batchHits int32

	// Create 768-dim vectors for the test
	vec768 := make([]float32, 768)
	for i := range vec768 {
		vec768[i] = float32(i) / 768.0
	}
	vec768b := make([]float32, 768)
	for i := range vec768b {
		vec768b[i] = float32(i+1) / 768.0
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embeddings":
			atomic.AddInt32(&singleHits, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"embedding": vec768,
			})
		case "/api/embed":
			atomic.AddInt32(&batchHits, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"embeddings": [][]float32{vec768, vec768b},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:   srv.URL + "/api/embeddings",
		Model: "test",
	})

	texts := []string{"alpha", "beta"}
	vecs := eg.GenerateVectors(texts)

	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 768 {
			t.Errorf("vector %d: expected 768 dims, got %d", i, len(v))
		}
	}

	if got := atomic.LoadInt32(&batchHits); got != 1 {
		t.Errorf("expected 1 batch request to /api/embed, got %d", got)
	}
	if got := atomic.LoadInt32(&singleHits); got != 0 {
		t.Errorf("expected 0 single requests to /api/embeddings, got %d", got)
	}
}

// TestAllConstructorsHaveRedirectProtection verifies that every public
// constructor produces an HTTP client that refuses to follow redirects
// (issue #68).
func TestAllConstructorsHaveRedirectProtection(t *testing.T) {
	constructors := []struct {
		name string
		eg   *EmbeddingsGenerator
	}{
		{"NewEmbeddingsGenerator", NewEmbeddingsGenerator()},
		{"NewEmbeddingsGeneratorWithConfig", NewEmbeddingsGeneratorWithConfig("http://localhost:11434/api/embeddings", "test")},
		{"NewEmbeddingsGeneratorWithOllamaConfig", NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{})},
	}

	for _, tc := range constructors {
		t.Run(tc.name, func(t *testing.T) {
			if tc.eg.httpClient.CheckRedirect == nil {
				t.Error("CheckRedirect is nil — redirect protection is missing")
			}
			// Verify it returns ErrUseLastResponse
			err := tc.eg.httpClient.CheckRedirect(nil, nil)
			if err != http.ErrUseLastResponse {
				t.Errorf("expected ErrUseLastResponse, got %v", err)
			}
		})
	}
}

// TestRedirectNotFollowed verifies that the embedder does not follow
// a 301 redirect (issue #68).
func TestRedirectNotFollowed(t *testing.T) {
	var redirectHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&redirectHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	var redirectorHits int32
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&redirectorHits, 1)
		http.Redirect(w, r, target.URL, http.StatusMovedPermanently)
	}))
	defer redirector.Close()

	eg := NewEmbeddingsGeneratorWithOllamaConfig(OllamaConfig{
		URL:          redirector.URL + "/api/embeddings",
		Model:        "test",
		RetryCount:   0,
		RetryBackoff: 1 * time.Millisecond,
	})

	_, err := eg.queryOllama("test")
	if err == nil {
		t.Fatal("expected error from redirect response")
	}

	if got := atomic.LoadInt32(&redirectorHits); got != 1 {
		t.Errorf("expected 1 hit on redirector, got %d", got)
	}
	if got := atomic.LoadInt32(&redirectHits); got != 0 {
		t.Errorf("expected 0 hits on redirect target (redirect was followed!), got %d", got)
	}
}

func TestIsStopWordUsesPackageMap(t *testing.T) {
	stopSamples := []string{
		"and", "the", "a", "an", "of", "to", "in", "is", "it", "that",
		"und", "der", "die", "das", "ein", "eine", "ist", "es", "dass",
		"von", "zu", "mit", "auf", "für", "den", "dem", "des", "im", "am",
	}
	for _, w := range stopSamples {
		if !isStopWord(w) {
			t.Errorf("expected %q to be classified as a stop word", w)
		}
	}

	nonStop := []string{"falcon", "database", "golang", "symaira", "search"}
	for _, w := range nonStop {
		if isStopWord(w) {
			t.Errorf("expected %q to NOT be classified as a stop word", w)
		}
	}
}
