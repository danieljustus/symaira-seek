package db

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeChunk constructs a chunk with a fixed-length deterministic embedding.
func makeChunk(uuid, docPath string, idx int, content string, baseIdx int) *Chunk {
	emb := make([]float32, 768)
	for i := range emb {
		emb[i] = float32(baseIdx+i) / 1000.0
	}
	var sumSquares float64
	for _, v := range emb {
		sumSquares += float64(v * v)
	}
	norm := float32(math.Sqrt(sumSquares))
	if norm > 0 {
		for i := range emb {
			emb[i] /= norm
		}
	}
	return &Chunk{
		UUID:         uuid,
		DocumentPath: docPath,
		ChunkIndex:   idx,
		Content:      content,
		Embedding:    emb,
		Hash:         uuid + "-hash",
	}
}

func setupDB(t testing.TB) *DB {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "seek-db-prefilter-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Setenv("HOME", tempDir)

	d, err := Open()
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
		os.RemoveAll(tempDir)
	})
	return d
}

func TestSearchVectorRanksTopKAndHydratesContent(t *testing.T) {
	d := setupDB(t)

	docPath := filepath.Join(t.TempDir(), "doc.md")
	now := time.Now()
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "h", UpdatedAt: now}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	chunks := []*Chunk{
		makeChunk("u1", docPath, 0, "alpha alpha alpha", 10),
		makeChunk("u2", docPath, 1, "beta beta beta", 20),
		makeChunk("u3", docPath, 2, "gamma gamma gamma", 30),
		makeChunk("u4", docPath, 3, "delta delta delta", 40),
		makeChunk("u5", docPath, 4, "epsilon epsilon", 50),
	}
	if err := d.SaveChunks(chunks); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	// Query vector identical to chunk 3 (uuid=u3, baseIdx=30): the closest
	// chunk is u3 itself.
	queryVec := make([]float32, 768)
	for i := range queryVec {
		queryVec[i] = float32(30+i) / 1000.0
	}
	var sumSquares float64
	for _, v := range queryVec {
		sumSquares += float64(v * v)
	}
	norm := float32(math.Sqrt(sumSquares))
	if norm > 0 {
		for i := range queryVec {
			queryVec[i] /= norm
		}
	}

	results, err := d.SearchVector(queryVec, 3)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	if len(results) == 0 || results[0].Chunk.UUID != "u3" {
		t.Fatalf("top hit should be u3, got %+v", results)
	}
	if len(results) != 3 {
		t.Fatalf("expected top-3 results, got %d", len(results))
	}

	// The scoring scan omits the content column; content must still be
	// hydrated for the returned top-k rows.
	if results[0].Chunk.Content != "gamma gamma gamma" {
		t.Errorf("expected hydrated content for top hit, got %q", results[0].Chunk.Content)
	}
	for _, r := range results {
		if r.Chunk.Content == "" {
			t.Errorf("result %s has no hydrated content", r.Chunk.UUID)
		}
	}
}
