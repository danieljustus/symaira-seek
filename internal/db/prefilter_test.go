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

func setupDB(t *testing.T) *DB {
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

func TestSearchVectorFilteredRespectsCandidateSet(t *testing.T) {
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

	// Use a query vector identical to chunk 3 (uuid=u3, baseIdx=30). Without a
	// filter the closest chunk is u3 itself. With a candidate set that
	// excludes u3, the closest match must come from the allowed candidates.
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

	unfiltered, err := d.SearchVector(queryVec, 3)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	if len(unfiltered) == 0 || unfiltered[0].Chunk.UUID != "u3" {
		t.Fatalf("unfiltered top hit should be u3, got %+v", unfiltered)
	}

	filtered, err := d.SearchVectorFiltered(queryVec, []int64{chunks[0].ID, chunks[1].ID, chunks[3].ID, chunks[4].ID}, 3)
	if err != nil {
		t.Fatalf("SearchVectorFiltered: %v", err)
	}
	for _, r := range filtered {
		if r.Chunk.UUID == "u3" {
			t.Errorf("filtered search returned excluded chunk u3")
		}
	}
	if len(filtered) == 0 {
		t.Fatalf("expected at least one filtered result, got none")
	}
}

func TestSearchVectorFilteredEmptyCandidatesFallsBackToFullScan(t *testing.T) {
	d := setupDB(t)
	docPath := filepath.Join(t.TempDir(), "doc.md")
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "h", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}
	chunks := []*Chunk{
		makeChunk("e1", docPath, 0, "first", 100),
		makeChunk("e2", docPath, 1, "second", 200),
	}
	if err := d.SaveChunks(chunks); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	results, err := d.SearchVectorFiltered(make([]float32, 768), nil, 5)
	if err != nil {
		t.Fatalf("SearchVectorFiltered: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results from empty-candidate fallback, got %d", len(results))
	}
}
