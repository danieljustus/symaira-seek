package engine

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	maxEmbeddingCacheSize = 10000

	defaultOllamaTimeout    = 120 * time.Second
	defaultOllamaRetries    = 2
	defaultOllamaBackoff    = 500 * time.Millisecond
	defaultOllamaMaxBackoff = 8 * time.Second
)

// EmbeddingsGenerator manages Ollama queries and the pure-Go hashing fallback.
type EmbeddingsGenerator struct {
	OllamaURL   string
	Model       string
	Timeout     time.Duration
	RetryCount  int
	RetryBackoff time.Duration
	httpClient  *http.Client
	cache       map[string]*list.Element
	cacheOrder  *list.List
	cacheMu     sync.Mutex
}

type cacheEntry struct {
	key   string
	value []float32
}

func newEmbeddingsGenerator() *EmbeddingsGenerator {
	return &EmbeddingsGenerator{
		OllamaURL:    "http://localhost:11434/api/embeddings",
		Model:        "nomic-embed-text",
		Timeout:      defaultOllamaTimeout,
		RetryCount:   defaultOllamaRetries,
		RetryBackoff: defaultOllamaBackoff,
		httpClient:   &http.Client{Timeout: defaultOllamaTimeout},
		cache:        make(map[string]*list.Element),
		cacheOrder:   list.New(),
	}
}

// NewEmbeddingsGenerator sets up the standard engine configuration.
func NewEmbeddingsGenerator() *EmbeddingsGenerator {
	return newEmbeddingsGenerator()
}

// Embedder is the public surface of an embedding generator. Callers depend
// on the contract, not on the concrete *EmbeddingsGenerator struct, so the
// HTTP client / cache / mutex plumbing stays encapsulated.
type Embedder interface {
	GenerateVector(text string) []float32
	GenerateVectors(texts []string) [][]float32
}

// Compile-time check that *EmbeddingsGenerator satisfies Embedder.
var _ Embedder = (*EmbeddingsGenerator)(nil)

// OllamaConfig bundles the user-tunable knobs for the embeddings
// generator. Zero values fall back to package defaults.
type OllamaConfig struct {
	URL          string
	Model        string
	Timeout      time.Duration
	RetryCount   int
	RetryBackoff time.Duration
}

func (c OllamaConfig) applyDefaults() OllamaConfig {
	if c.URL == "" {
		c.URL = "http://localhost:11434/api/embeddings"
	}
	if c.Model == "" {
		c.Model = "nomic-embed-text"
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultOllamaTimeout
	}
	if c.RetryCount <= 0 {
		c.RetryCount = defaultOllamaRetries
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = defaultOllamaBackoff
	}
	return c
}

// NewEmbeddingsGeneratorWithConfig builds an EmbeddingsGenerator pre-configured
// with the given Ollama endpoint and model name. It performs the same internal
// initialization as NewEmbeddingsGenerator (HTTP client, LRU cache, mutex) so
// callers can construct one without depending on the unexported fields.
func NewEmbeddingsGeneratorWithConfig(ollamaURL, model string) *EmbeddingsGenerator {
	eg := newEmbeddingsGenerator()
	eg.OllamaURL = ollamaURL
	eg.Model = model
	return eg
}

// NewEmbeddingsGeneratorWithOllamaConfig builds an EmbeddingsGenerator
// from a fully-populated OllamaConfig. Zero values fall back to package
// defaults; negative retry counts are clamped to zero.
func NewEmbeddingsGeneratorWithOllamaConfig(cfg OllamaConfig) *EmbeddingsGenerator {
	cfg = cfg.applyDefaults()
	eg := newEmbeddingsGenerator()
	eg.OllamaURL = cfg.URL
	eg.Model = cfg.Model
	eg.Timeout = cfg.Timeout
	eg.RetryCount = cfg.RetryCount
	eg.RetryBackoff = cfg.RetryBackoff
	eg.httpClient = &http.Client{Timeout: cfg.Timeout}
	return eg
}

// GenerateVector produces a 768-dimensional normalized embedding vector.
// Uses an LRU in-memory cache to avoid recomputing embeddings for repeated text.
// Queries Ollama first, falling back to local deterministic hashing if offline.
func (eg *EmbeddingsGenerator) GenerateVector(text string) []float32 {
	key := hashKey(text)

	eg.cacheMu.Lock()
	if elem, ok := eg.cache[key]; ok {
		eg.cacheOrder.MoveToFront(elem)
		eg.cacheMu.Unlock()
		return elem.Value.(*cacheEntry).value
	}
	eg.cacheMu.Unlock()

	dims := 768

	// Try Ollama first
	vec, err := eg.queryOllama(text)
	if err == nil && len(vec) == dims {
		eg.cachePut(key, vec)
		return vec
	}

	// Local pure-Go fallback vector
	fallback := GenerateLocalHashVector(text, dims)
	eg.cachePut(key, fallback)
	return fallback
}

func (eg *EmbeddingsGenerator) cachePut(key string, value []float32) {
	eg.cacheMu.Lock()
	defer eg.cacheMu.Unlock()

	if _, exists := eg.cache[key]; exists {
		return
	}

	if eg.cacheOrder.Len() >= maxEmbeddingCacheSize {
		oldest := eg.cacheOrder.Back()
		if oldest != nil {
			entry := oldest.Value.(*cacheEntry)
			delete(eg.cache, entry.key)
			eg.cacheOrder.Remove(oldest)
		}
	}

	elem := eg.cacheOrder.PushFront(&cacheEntry{key: key, value: value})
	eg.cache[key] = elem
}

// GenerateVectors produces embeddings for a batch of texts.
// Sends them to Ollama in a single HTTP request when possible, falling back
// to individual queries and local hashing per text.
// Returns a slice with one embedding per input text, in the same order.
func (eg *EmbeddingsGenerator) GenerateVectors(texts []string) [][]float32 {
	if len(texts) == 0 {
		return nil
	}

	dims := 768
	results := make([][]float32, len(texts))

	// Collect uncached texts and their indexes
	type uncached struct {
		idx  int
		text string
	}
	var uncachedList []uncached

	eg.cacheMu.Lock()
	for i, t := range texts {
		key := hashKey(t)
		if elem, ok := eg.cache[key]; ok {
			eg.cacheOrder.MoveToFront(elem)
			results[i] = elem.Value.(*cacheEntry).value
		} else {
			uncachedList = append(uncachedList, uncached{idx: i, text: t})
		}
	}
	eg.cacheMu.Unlock()

	if len(uncachedList) == 0 {
		return results
	}

	// Build uncached text slice for batch query
	uncachedTexts := make([]string, len(uncachedList))
	for i, u := range uncachedList {
		uncachedTexts[i] = u.text
	}

	// Try batch Ollama query
	batchVectors, err := eg.queryOllamaBatch(uncachedTexts)
	if err == nil && len(batchVectors) == len(uncachedList) {
		for i, u := range uncachedList {
			vec := batchVectors[i]
			key := hashKey(u.text)
			if len(vec) == dims {
				results[u.idx] = vec
				eg.cachePut(key, vec)
			} else {
				results[u.idx] = GenerateLocalHashVector(u.text, dims)
				eg.cachePut(key, results[u.idx])
			}
		}
		return results
	}

	// Fall back to individual queries with caching
	for _, u := range uncachedList {
		results[u.idx] = eg.GenerateVector(u.text)
	}
	return results
}

// queryOllama sends a single embedding request to Ollama.
func (eg *EmbeddingsGenerator) queryOllama(text string) ([]float32, error) {
	reqBody, err := json.Marshal(map[string]string{
		"model":  eg.Model,
		"prompt": text,
	})
	if err != nil {
		return nil, err
	}

	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := eg.doOllamaRequest(reqBody, &res); err != nil {
		return nil, err
	}
	return res.Embedding, nil
}

// queryOllamaBatch sends a batch embedding request to Ollama.
// Ollama's /api/embeddings endpoint supports a list in the "input" field
// and returns "embeddings" as a list of vectors.
func (eg *EmbeddingsGenerator) queryOllamaBatch(texts []string) ([][]float32, error) {
	reqBody, err := json.Marshal(map[string]interface{}{
		"model": eg.Model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}

	var res struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := eg.doOllamaRequest(reqBody, &res); err != nil {
		return nil, err
	}
	return res.Embeddings, nil
}

func (eg *EmbeddingsGenerator) doOllamaRequest(reqBody []byte, result interface{}) error {
	backoff := eg.RetryBackoff
	if backoff <= 0 {
		backoff = defaultOllamaBackoff
	}
	maxBackoff := defaultOllamaMaxBackoff
	retries := eg.RetryCount
	if retries < 0 {
		retries = 0
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			fmt.Fprintf(os.Stderr, "engine: ollama retry %d/%d after %v (last error: %v)\n", attempt, retries, backoff, lastErr)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		req, err := http.NewRequest(http.MethodPost, eg.OllamaURL, bytes.NewReader(reqBody))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := eg.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("ollama returned HTTP %d", resp.StatusCode)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		defer resp.Body.Close()
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return fmt.Errorf("ollama: exhausted %d retries: %w", retries, lastErr)
}

// GenerateLocalHashVector utilizes the "Hashing Trick" to produce a normalized
// 768-dimensional float32 vector based on word hashes.
func GenerateLocalHashVector(text string, dimensions int) []float32 {
	vec := make([]float32, dimensions)

	// Clean and normalize text
	cleaned := strings.ToLower(text)
	for _, char := range []string{".", ",", "!", "?", ";", ":", "-", "_", "(", ")", "[", "]", "{", "}"} {
		cleaned = strings.ReplaceAll(cleaned, char, " ")
	}

	words := strings.Fields(cleaned)
	if len(words) == 0 {
		// Non-empty fallback vector to prevent division by zero
		vec[0] = 1.0
		return vec
	}

	// Calculate a secondary hash signature for the entire block to seed weights
	textHash := sha256.Sum256([]byte(text))

	for i, word := range words {
		if isStopWord(word) {
			continue
		}

		h := fnv.New32a()
		h.Write([]byte(word))
		hashVal := h.Sum32()

		idx := int(hashVal) % dimensions

		// Use a combination of word hash and context sequence weight
		weight := float32(1.0)
		if i < len(textHash) {
			weight += float32(textHash[i]) / 255.0
		}
		vec[idx] += weight
	}

	// Normalize vector (L2 norm) so cosine similarity behave correctly
	var sumSquares float64
	for _, val := range vec {
		sumSquares += float64(val * val)
	}

	if sumSquares > 0 {
		norm := float32(math.Sqrt(sumSquares))
		for i := range vec {
			vec[i] /= norm
		}
	} else {
		vec[0] = 1.0
	}

	return vec
}

func isStopWord(w string) bool {
	stops := map[string]bool{
		"and": true, "the": true, "a": true, "an": true, "of": true, "to": true, "in": true, "is": true, "it": true, "that": true,
		"und": true, "der": true, "die": true, "das": true, "ein": true, "eine": true, "ist": true, "es": true, "dass": true,
		"von": true, "zu": true, "mit": true, "auf": true, "für": true, "den": true, "dem": true, "des": true, "im": true, "am": true,
	}
	return stops[w]
}

// hashKey returns a hex-encoded SHA-256 hash of the input text, truncated
// to 32 hex chars (128 bits of entropy). Used as a cache key to avoid
// storing large raw text strings in memory while keeping collision risk
// negligible well past the 10K-entry cache ceiling.
func hashKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum[:16])
}
