package engine

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
)

// TestHashKeyEntropy is a regression test for issue #39. The cache key
// must contain enough entropy that distinct texts are statistically
// guaranteed to produce distinct keys, even at the 10K-entry cache size
// (and well beyond).
func TestHashKeyEntropy(t *testing.T) {
	if got := hashKey("anything"); len(got) != 32 {
		t.Errorf("expected 32 hex chars (128 bits) in cache key, got %d (%q)", len(got), got)
	}

	h := hashKey("alpha")
	if h != hashKey("alpha") {
		t.Error("hashKey must be deterministic")
	}

	if hashKey("alpha") == hashKey("beta") {
		t.Error("hashKey must distinguish different inputs")
	}
}

func TestLocalHashVector(t *testing.T) {
	vec1 := GenerateLocalHashVector("Hello Symaira Seek", 768)
	vec2 := GenerateLocalHashVector("Hello Symaira Seek", 768)
	vec3 := GenerateLocalHashVector("Something else entirely", 768)

	if len(vec1) != 768 {
		t.Errorf("expected vector size 768, got %d", len(vec1))
	}

	// Verify determinism
	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Errorf("expected deterministic vector generation")
			break
		}
	}

	// Verify L2 normalization
	var sumSquares float64
	for _, val := range vec1 {
		sumSquares += float64(val * val)
	}
	if math.Abs(sumSquares-1.0) > 1e-5 {
		t.Errorf("expected normalized L2 norm ~1.0, got %f", sumSquares)
	}

	// Cosine similarity with self should be ~1.0
	simSelf := db.CosineSimilarity(vec1, vec2)
	if math.Abs(float64(simSelf-1.0)) > 1e-5 {
		t.Errorf("expected cosine similarity with self to be 1.0, got %f", simSelf)
	}

	// Cosine similarity with different string should be lower
	simDiff := db.CosineSimilarity(vec1, vec3)
	if simDiff >= 0.99 {
		t.Errorf("expected different texts to have lower similarity, got %f", simDiff)
	}
}

// TestNewEmbeddingsGeneratorWithConfig is a regression test for issue #29.
// The factory must return a fully wired EmbeddingsGenerator (HTTP client,
// LRU cache, mutex) that produces vectors without a nil-pointer panic.
// Bare struct construction at CLI/MCP/HTTP call sites was the original
// runtime denial of service and is exercised by no test in this repo.
func TestNewEmbeddingsGeneratorWithConfig(t *testing.T) {
	eg := NewEmbeddingsGeneratorWithConfig("http://localhost:11434/api/embeddings", "nomic-embed-text")
	eg.sleepFn = func(time.Duration) {}
	if eg == nil {
		t.Fatal("expected non-nil EmbeddingsGenerator")
	}
	if eg.OllamaURL == "" {
		t.Error("expected OllamaURL to be set from config")
	}
	if eg.Model == "" {
		t.Error("expected Model to be set from config")
	}

	vec := eg.GenerateVector("regression test for #29")
	if len(vec) != 768 {
		t.Errorf("expected 768-dim vector, got %d", len(vec))
	}

	vec2 := eg.GenerateVector("regression test for #29")
	if len(vec2) != 768 {
		t.Errorf("expected cached vector of 768-dim, got %d", len(vec2))
	}

	batch := eg.GenerateVectors([]string{"alpha", "beta", "gamma"})
	if len(batch) != 3 {
		t.Errorf("expected 3 vectors from batch, got %d", len(batch))
	}
	for i, v := range batch {
		if len(v) != 768 {
			t.Errorf("batch index %d: expected 768-dim vector, got %d", i, len(v))
		}
	}
}

func newTestEmbeddingsGenerator() *EmbeddingsGenerator {
	eg := NewEmbeddingsGenerator()
	eg.sleepFn = func(time.Duration) {}
	return eg
}

// fakeEmbedder is a deterministic in-memory Embedder used to prove that
// SearchHybrid and the indexer accept the interface, not the concrete
// struct, and that the tests can substitute behavior without Ollama.
type fakeEmbedder struct {
	dim int
}

func (f *fakeEmbedder) GenerateVector(text string) []float32 {
	vec := make([]float32, f.dim)
	for i, b := range []byte(text) {
		vec[i%f.dim] += float32(b) / 255.0
	}
	var sumSquares float64
	for _, v := range vec {
		sumSquares += float64(v * v)
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

func (f *fakeEmbedder) GenerateVectors(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.GenerateVector(t)
	}
	return out
}

// TestSearchHybridAcceptsEmbedderInterface guards the contract from #35:
// the indexer must depend on the Embedder interface, not the concrete
// *EmbeddingsGenerator, so callers can substitute behavior in tests.
func TestSearchHybridAcceptsEmbedderInterface(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-engine-iface-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	docPath := filepath.Join(tempDir, "test.md")
	dbClient.SaveDocument(&db.Document{
		Path:      docPath,
		Hash:      "ifacehash",
		UpdatedAt: time.Now(),
	})

	var ie Embedder = &fakeEmbedder{dim: 768}

	dbClient.SaveChunks([]*db.Chunk{
		{
			UUID:         "iface-uuid-1",
			DocumentPath: docPath,
			ChunkIndex:   0,
			Content:      "interface driven search",
			Embedding:    ie.GenerateVector("interface driven search"),
			Hash:         "h1",
		},
	})

	res, err := SearchHybrid(dbClient, ie, "interface", 5)
	if err != nil {
		t.Fatalf("SearchHybrid with interface embedder failed: %v", err)
	}
	if len(res) == 0 {
		t.Fatalf("expected at least one result")
	}
	if res[0].Chunk.UUID != "iface-uuid-1" {
		t.Errorf("expected iface-uuid-1, got %s", res[0].Chunk.UUID)
	}
}

// TestSearchHybridSemanticOnlyMatch is a regression test for issue #65.
// A chunk with high cosine similarity but zero keyword overlap must appear
// in SearchHybrid results even when BM25 has hits on other chunks.
func TestSearchHybridSemanticOnlyMatch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-hybrid-semantic-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	docPath := filepath.Join(tempDir, "test.md")
	dbClient.SaveDocument(&db.Document{
		Path:      docPath,
		Hash:      "semhash",
		UpdatedAt: time.Now(),
	})

	embedder := &fakeEmbedder{dim: 768}

	// Chunk A: contains the query keyword "falcon"
	textA := "The swift falcon soars above the mountains"
	// Chunk B: no keyword overlap with "falcon" but semantically similar
	// (both are about birds/flight) — we'll make its embedding close to A's
	textB := "A bird flies high over peaks"

	embeddingA := embedder.GenerateVector(textA)
	embeddingB := embedder.GenerateVector(textB)

	// Make B's embedding artificially similar to A's so cosine sim is high
	// even though there's zero keyword overlap with "falcon"
	for i := range embeddingB {
		embeddingB[i] = embeddingA[i]*0.9 + embeddingB[i]*0.1
	}
	// Re-normalize
	var sumSquares float64
	for _, v := range embeddingB {
		sumSquares += float64(v * v)
	}
	norm := float32(math.Sqrt(sumSquares))
	for i := range embeddingB {
		embeddingB[i] /= norm
	}

	dbClient.SaveChunks([]*db.Chunk{
		{
			UUID:         "uuid-keyword",
			DocumentPath: docPath,
			ChunkIndex:   0,
			Content:      textA,
			Embedding:    embeddingA,
			Hash:         "ha",
		},
		{
			UUID:         "uuid-semantic",
			DocumentPath: docPath,
			ChunkIndex:   1,
			Content:      textB,
			Embedding:    embeddingB,
			Hash:         "hb",
		},
	})

	// Search for "falcon" — BM25 will find chunk A but not chunk B.
	// The fix ensures chunk B still appears via full vector scan.
	res, err := SearchHybrid(dbClient, embedder, "falcon", 10)
	if err != nil {
		t.Fatalf("SearchHybrid failed: %v", err)
	}

	if len(res) < 2 {
		t.Fatalf("expected at least 2 results (keyword + semantic-only match), got %d", len(res))
	}

	found := make(map[string]bool)
	for _, r := range res {
		found[r.Chunk.UUID] = true
	}
	if !found["uuid-keyword"] {
		t.Error("expected uuid-keyword (BM25 match) in results")
	}
	if !found["uuid-semantic"] {
		t.Error("expected uuid-semantic (semantic-only match) in results — vector search was likely still filtered by BM25 candidates")
	}
}

func TestHybridSearch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-engine-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	docPath := filepath.Join(tempDir, "test.md")
	dbClient.SaveDocument(&db.Document{
		Path:      docPath,
		Hash:      "hash123",
		UpdatedAt: time.Now(),
	})

	embedder := newTestEmbeddingsGenerator()

	// Embed some sample text
	text1 := "The swift azure falcon soars above the sleeping canine"
	text2 := "Database management system optimization strategies"

	dbClient.SaveChunks([]*db.Chunk{
		{
			UUID:         "uuid1",
			DocumentPath: docPath,
			ChunkIndex:   0,
			Content:      text1,
			Embedding:    embedder.GenerateVector(text1),
			Hash:         "chash1",
		},
		{
			UUID:         "uuid2",
			DocumentPath: docPath,
			ChunkIndex:   1,
			Content:      text2,
			Embedding:    embedder.GenerateVector(text2),
			Hash:         "chash2",
		},
	})

	// Search for something related to text1
	res, err := SearchHybrid(dbClient, embedder, "falcon soars", 2)
	if err != nil {
		t.Fatalf("SearchHybrid failed: %v", err)
	}

	if len(res) == 0 {
		t.Fatalf("expected results, got none")
	}

	if res[0].Chunk.UUID != "uuid1" {
		t.Errorf("expected primary result to be uuid1 (falcon text), got %s", res[0].Chunk.UUID)
	}

	// Verify rank fields are set
	if res[0].BM25Rank == 0 && res[0].VectorRank == 0 {
		t.Errorf("expected BM25Rank or VectorRank to be non-zero")
	}
}
