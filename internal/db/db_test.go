package db

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// seedBenchmarkDB populates d with nChunks chunks whose embeddings
// are 768-dim normalized random-ish vectors. The seeding cost is
// excluded from the timed benchmark via b.ResetTimer below.
func seedBenchmarkDB(b *testing.B, d *DB, nChunks int) {
	b.Helper()
	docPath := "/bench/doc.md"
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "bench", UpdatedAt: time.Now()}); err != nil {
		b.Fatalf("SaveDocument: %v", err)
	}
	chunks := make([]*Chunk, nChunks)
	for i := 0; i < nChunks; i++ {
		emb := make([]float32, 768)
		for j := range emb {
			emb[j] = float32(i+j) / float32(nChunks+j+1)
		}
		var sumSquares float64
		for _, v := range emb {
			sumSquares += float64(v * v)
		}
		norm := float32(math.Sqrt(sumSquares))
		if norm > 0 {
			for j := range emb {
				emb[j] /= norm
			}
		}
		chunks[i] = &Chunk{
			UUID:         "bench-" + strconv.Itoa(i),
			DocumentPath: docPath,
			ChunkIndex:   i,
			Content:      "bench content",
			Embedding:    emb,
			Hash:         "bench-hash",
		}
	}
	if err := d.SaveChunks(chunks); err != nil {
		b.Fatalf("SaveChunks: %v", err)
	}
}

// BenchmarkSearchVectorSinglePass vs BenchmarkSearchVectorTwoPass
// measures the impact of issue #49's single-pass rewrite. The
// single-pass variant issues one query for the full chunk payload
// (embedding + content + metadata) and keeps a top-K window in
// memory; the two-pass variant issues a paginated scoring query
// followed by a top-K detail query.
//
// Run with:
//   go test -bench=BenchmarkSearchVector -benchtime=2s ./internal/db
func BenchmarkSearchVectorSinglePass(b *testing.B) {
	d := setupDB(b)
	const nChunks = 1000
	seedBenchmarkDB(b, d, nChunks)
	query := make([]float32, 768)
	for i := range query {
		query[i] = 0.5
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := d.SearchVector(query, 10)
		if err != nil {
			b.Fatalf("SearchVector: %v", err)
		}
		if len(results) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}

func BenchmarkSearchVectorTwoPass(b *testing.B) {
	d := setupDB(b)
	const nChunks = 1000
	seedBenchmarkDB(b, d, nChunks)
	query := make([]float32, 768)
	for i := range query {
		query[i] = 0.5
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := d.searchVectorTwoPass(query, nil, 10)
		if err != nil {
			b.Fatalf("searchVectorTwoPass: %v", err)
		}
		if len(results) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}

type scoredEntry struct {
	id    int64
	score float32
}

func insertSorted(list []*scoredEntry, item *scoredEntry, maxLen int) []*scoredEntry {
	pos := sort.Search(len(list), func(i int) bool {
		return list[i].score < item.score
	})

	if len(list) < maxLen {
		list = append(list, nil)
		copy(list[pos+1:], list[pos:len(list)-1])
		list[pos] = item
	} else if pos < maxLen {
		copy(list[pos+1:], list[pos:maxLen-1])
		list[pos] = item
	}
	return list
}

func (db *DB) searchVectorTwoPass(queryVec []float32, candidateIDs []int64, limit int) ([]*SearchResult, error) {
	const vectorBatchSize = 500
	var topK []*scoredEntry

	candidateSet := make(map[int64]struct{}, len(candidateIDs))
	for _, id := range candidateIDs {
		candidateSet[id] = struct{}{}
	}
	hasFilter := len(candidateSet) > 0

	offset := 0
	for {
		rows, err := db.conn.Query(
			"SELECT id, embedding, norm FROM chunks ORDER BY id LIMIT ? OFFSET ?",
			vectorBatchSize, offset,
		)
		if err != nil {
			return nil, err
		}

		batchCount := 0
		for rows.Next() {
			var id int64
			var embBytes []byte
			var norm float32
			if err := rows.Scan(&id, &embBytes, &norm); err != nil {
				rows.Close()
				return nil, err
			}

			if hasFilter {
				if _, ok := candidateSet[id]; !ok {
					continue
				}
			}

			vec := BytesToFloat32Slice(embBytes)
			var score float32
			if norm > 0 {
				score = CosineSimilarityWithNorm(queryVec, vec, norm)
			} else {
				score = CosineSimilarity(queryVec, vec)
			}
			batchCount++

			topK = insertSorted(topK, &scoredEntry{id: id, score: score}, limit)
		}
		rows.Close()

		if batchCount < vectorBatchSize {
			break
		}
		offset += vectorBatchSize
	}

	if len(topK) == 0 {
		return nil, nil
	}

	scoreMap := make(map[int64]float32)
	idOrderMap := make(map[int64]int)
	topIDs := make([]interface{}, len(topK))
	for i, s := range topK {
		topIDs[i] = s.id
		scoreMap[s.id] = s.score
		idOrderMap[s.id] = i
	}

	placeholders := make([]string, len(topK))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	query := fmt.Sprintf(
		"SELECT id, uuid, document_path, chunk_index, content, embedding, hash FROM chunks WHERE id IN (%s)",
		strings.Join(placeholders, ","),
	)

	detailRows, err := db.conn.Query(query, topIDs...)
	if err != nil {
		return nil, err
	}
	defer detailRows.Close()

	unsortedResults := make([]*SearchResult, 0, len(topK))
	for detailRows.Next() {
		var c Chunk
		var embBytes []byte
		if err := detailRows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embBytes, &c.Hash); err != nil {
			return nil, err
		}
		c.Embedding = BytesToFloat32Slice(embBytes)

		unsortedResults = append(unsortedResults, &SearchResult{
			Chunk:       &c,
			VectorRank:  0,
			CosineScore: scoreMap[c.ID],
		})
	}

	sort.Slice(unsortedResults, func(i, j int) bool {
		return idOrderMap[unsortedResults[i].Chunk.ID] < idOrderMap[unsortedResults[j].Chunk.ID]
	})

	for i, res := range unsortedResults {
		res.VectorRank = i + 1
	}

	return unsortedResults, nil
}
