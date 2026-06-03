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
	"time"
)

// EmbeddingsGenerator manages Ollama queries and the pure-Go hashing fallback.
type EmbeddingsGenerator struct {
	OllamaURL string
	Model     string
}

// NewEmbeddingsGenerator sets up the standard engine configuration.
func NewEmbeddingsGenerator() *EmbeddingsGenerator {
	return &EmbeddingsGenerator{
		OllamaURL: "http://localhost:11434/api/embeddings",
		Model:     "nomic-embed-text",
	}
}

// GenerateVector produces a 768-dimensional normalized embedding vector.
// It queries Ollama first, falling back to local deterministic hashing if offline or Ollama is unavailable.
func (eg *EmbeddingsGenerator) GenerateVector(text string) []float32 {
	dims := 768

	// Try Ollama first
	vec, err := eg.queryOllama(text)
	if err == nil && len(vec) == dims {
		return vec
	}

	// Local pure-Go fallback vector
	return GenerateLocalHashVector(text, dims)
}

func (eg *EmbeddingsGenerator) queryOllama(text string) ([]float32, error) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}

	reqBody, err := json.Marshal(map[string]string{
		"model":  eg.Model,
		"prompt": text,
	})
	if err != nil {
		return nil, err
	}

	resp, err := client.Post(eg.OllamaURL, "application/json", bytes.NewBuffer(reqBody))
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
