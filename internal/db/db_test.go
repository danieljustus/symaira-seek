package db

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a    []float32
		b    []float32
		want float32
	}{
		{
			name: "Identical vectors",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0, 0},
			want: 1.0,
		},
		{
			name: "Orthogonal vectors",
			a:    []float32{1, 0, 0},
			b:    []float32{0, 1, 0},
			want: 0.0,
		},
		{
			name: "Opposite vectors",
			a:    []float32{1, 2, 3},
			b:    []float32{-1, -2, -3},
			want: -1.0,
		},
		{
			name: "Length mismatch",
			a:    []float32{1, 2},
			b:    []float32{1, 2, 3},
			want: 0.0,
		},
		{
			name: "Empty vectors",
			a:    []float32{},
			b:    []float32{},
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if math.Abs(float64(got-tt.want)) > 1e-6 {
				t.Errorf("CosineSimilarity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDatabaseOperations(t *testing.T) {
	// Set home dir override for test
	tempDir, err := os.MkdirTemp("", "seek-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	db, err := Open()
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	defer db.Close()

	// Test SaveDocument
	docPath := filepath.Join(tempDir, "test.md")
	doc := &Document{
		Path:      docPath,
		Hash:      "abcd123",
		UpdatedAt: time.Now(),
	}

	err = db.SaveDocument(doc)
	if err != nil {
		t.Fatalf("failed to save document: %v", err)
	}

	// Test GetDocument
	fetched, err := db.GetDocument(docPath)
	if err != nil {
		t.Fatalf("failed to get document: %v", err)
	}
	if fetched == nil || fetched.Hash != doc.Hash {
		t.Errorf("fetched document mismatch: got %+v, want %+v", fetched, doc)
	}

	// Test SaveChunks
	chunks := []*Chunk{
		{
			UUID:         "c1",
			DocumentPath: docPath,
			ChunkIndex:   0,
			Content:      "The quick brown fox jumps over the lazy dog.",
			Embedding:    []float32{1.0, 0.0, 0.0},
			Hash:         "chunkhash1",
		},
		{
			UUID:         "c2",
			DocumentPath: docPath,
			ChunkIndex:   1,
			Content:      "A fast auburn canine leaps across an inactive hound.",
			Embedding:    []float32{0.0, 1.0, 0.0},
			Hash:         "chunkhash2",
		},
	}

	err = db.SaveChunks(chunks)
	if err != nil {
		t.Fatalf("failed to save chunks: %v", err)
	}

	// Test GetChunksForDocument
	fetchedChunks, err := db.GetChunksForDocument(docPath)
	if err != nil {
		t.Fatalf("failed to get chunks: %v", err)
	}
	if len(fetchedChunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(fetchedChunks))
	}

	// Test SearchBM25
	bm25Res, err := db.SearchBM25("fox", 10)
	if err != nil {
		t.Fatalf("failed BM25 search: %v", err)
	}
	if len(bm25Res) == 0 {
		t.Errorf("expected BM25 search results for 'fox'")
	} else if bm25Res[0].Chunk.UUID != "c1" {
		t.Errorf("expected chunk c1 to be returned, got %s", bm25Res[0].Chunk.UUID)
	}

	// Test SearchVector
	vecRes, err := db.SearchVector([]float32{0.0, 0.9, 0.1}, 10)
	if err != nil {
		t.Fatalf("failed vector search: %v", err)
	}
	if len(vecRes) != 2 {
		t.Fatalf("expected 2 vector search results, got %d", len(vecRes))
	}
	if vecRes[0].Chunk.UUID != "c2" {
		t.Errorf("expected nearest vector match to be chunk c2, got %s", vecRes[0].Chunk.UUID)
	}

	// Test GetStats
	stats, err := db.GetStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.DocumentCount != 1 || stats.ChunkCount != 2 {
		t.Errorf("unexpected stats: %+v", stats)
	}

	// Test DeleteDocument
	err = db.DeleteDocument(docPath)
	if err != nil {
		t.Fatalf("failed to delete document: %v", err)
	}

	deletedDoc, err := db.GetDocument(docPath)
	if err != nil {
		t.Fatalf("error fetching deleted document: %v", err)
	}
	if deletedDoc != nil {
		t.Errorf("document should have been deleted")
	}

	stats2, _ := db.GetStats()
	if stats2.DocumentCount != 0 || stats2.ChunkCount != 0 {
		t.Errorf("expected 0 docs and 0 chunks in stats after deletion, got %+v", stats2)
	}
}
