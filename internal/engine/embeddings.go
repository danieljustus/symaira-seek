package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// EmbeddingsGenerator manages Ollama queries and the pure-Go hashing fallback.
type EmbeddingsGenerator struct {
	OllamaURL  string
	Model      string
	httpClient *http.Client
	cache      map[string][]float32
	cacheMu    sync.RWMutex
}

// NewEmbeddingsGenerator sets up the standard engine configuration.
func NewEmbeddingsGenerator() *EmbeddingsGenerator {
	return &EmbeddingsGenerator{
		OllamaURL:  "http://localhost:11434/api/embeddings",
		Model:      "nomic-embed-text",
		httpClient: &http.Client{Timeout: 5000 * time.Millisecond},
		cache:      make(map[string][]float32),
	}
}

// GenerateVector produces a 768-dimensional normalized embedding vector.
// Uses an in-memory cache to avoid recomputing embeddings for repeated text.
// Queries Ollama first, falling back to local deterministic hashing if offline.
func (eg *EmbeddingsGenerator) GenerateVector(text string) []float32 {
	// Check cache first
	eg.cacheMu.RLock()
	if vec, ok := eg.cache[text]; ok {
		eg.cacheMu.RUnlock()
		return vec
	}
	eg.cacheMu.RUnlock()

	dims := 768

	// Try Ollama first
	vec, err := eg.queryOllama(text)
	if err == nil && len(vec) == dims {
		eg.cacheMu.Lock()
		eg.cache[text] = vec
		eg.cacheMu.Unlock()
		return vec
	}

	// Local pure-Go fallback vector
	fallback := GenerateLocalHashVector(text, dims)
	eg.cacheMu.Lock()
	eg.cache[text] = fallback
	eg.cacheMu.Unlock()
	return fallback
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

	eg.cacheMu.RLock()
	for i, t := range texts {
		if vec, ok := eg.cache[t]; ok {
			results[i] = vec
		} else {
			uncachedList = append(uncachedList, uncached{idx: i, text: t})
		}
	}
	eg.cacheMu.RUnlock()

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
		eg.cacheMu.Lock()
		for i, u := range uncachedList {
			vec := batchVectors[i]
			if len(vec) == dims {
				results[u.idx] = vec
				eg.cache[u.text] = vec
			} else {
				results[u.idx] = GenerateLocalHashVector(u.text, dims)
				eg.cache[u.text] = results[u.idx]
			}
		}
		eg.cacheMu.Unlock()
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

	resp, err := eg.httpClient.Post(eg.OllamaURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned HTTP %d", resp.StatusCode)
	}

	var res struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
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

	resp, err := eg.httpClient.Post(eg.OllamaURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama batch returned HTTP %d", resp.StatusCode)
	}

	var res struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return res.Embeddings, nil
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
