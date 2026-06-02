package engine

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
)

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

func TestHybridSearch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-engine-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

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

	embedder := NewEmbeddingsGenerator()

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
