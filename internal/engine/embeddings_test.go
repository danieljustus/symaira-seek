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
	if err := eg.doOllamaRequest([]byte(`{}`), &res); err != nil {
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
	if err := eg.doOllamaRequest([]byte(`{}`), &res); err != nil {
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
	if err := eg.doOllamaRequest([]byte(`{}`), &res); err == nil {
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
	err := eg.doOllamaRequest([]byte(`{}`), &res)
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
	if err := eg.doOllamaRequest([]byte(`{}`), &res); err == nil {
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
