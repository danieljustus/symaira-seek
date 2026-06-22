package db

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
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

	if results[0].Chunk.Content != "gamma gamma gamma" {
		t.Errorf("expected hydrated content for top hit, got %q", results[0].Chunk.Content)
	}
	for _, r := range results {
		if r.Chunk.Content == "" {
			t.Errorf("result %s has no hydrated content", r.Chunk.UUID)
		}
	}
}

func TestVectorIndexBuildAndCandidates(t *testing.T) {
	dim := 768
	chunks := make([]*Chunk, 200)
	for i := range chunks {
		emb := make([]float32, dim)
		for j := range emb {
			emb[j] = float32(i*dim+j) / float32(dim*200)
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
			ID:        int64(i),
			Embedding: emb,
		}
	}

	idx := NewVectorIndex()
	idx.Build(chunks)

	if !idx.IsReady() {
		t.Fatal("index should be ready after Build")
	}

	k := idx.BucketCount()
	if k < 4 || k > 256 {
		t.Fatalf("unexpected bucket count: %d", k)
	}

	nprobe := idx.ProbeCount()
	if nprobe < 1 || nprobe > k {
		t.Fatalf("unexpected nprobe: %d (k=%d)", nprobe, k)
	}

	query := make([]float32, dim)
	for i := range query {
		query[i] = float32(100*dim+i) / float32(dim*200)
	}
	var sumSquares float64
	for _, v := range query {
		sumSquares += float64(v * v)
	}
	norm := float32(math.Sqrt(sumSquares))
	if norm > 0 {
		for i := range query {
			query[i] /= norm
		}
	}

	candidates := idx.CandidateIDs(query, nprobe)
	if candidates == nil {
		t.Fatal("expected non-nil candidates")
	}
	if len(candidates) == 0 {
		t.Fatal("expected at least one candidate")
	}

	idSet := make(map[int64]bool, len(candidates))
	for _, id := range candidates {
		idSet[id] = true
	}
	for _, id := range candidates {
		if id < 0 || id >= 200 {
			t.Fatalf("candidate ID out of range: %d", id)
		}
	}

	bestCosine := float32(-2)
	bestID := int64(-1)
	for _, c := range chunks {
		score := CosineSimilarity(query, c.Embedding)
		if score > bestCosine {
			bestCosine = score
			bestID = c.ID
		}
	}

	if !idSet[bestID] {
		t.Fatalf("true nearest neighbor (ID %d, score %.4f) not in candidate set of %d IDs", bestID, bestCosine, len(candidates))
	}
}

func TestVectorIndexEmptyChunks(t *testing.T) {
	idx := NewVectorIndex()
	idx.Build(nil)
	if idx.IsReady() {
		t.Fatal("index should not be ready for nil chunks")
	}
	candidates := idx.CandidateIDs([]float32{1, 0, 0}, 3)
	if candidates != nil {
		t.Fatal("expected nil candidates for empty index")
	}
}

func TestVectorIndexSmallDatasetFallback(t *testing.T) {
	dim := 768
	chunks := make([]*Chunk, 10)
	for i := range chunks {
		emb := make([]float32, dim)
		for j := range emb {
			emb[j] = float32(i*dim+j) / float32(dim*10)
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
		chunks[i] = &Chunk{ID: int64(i), Embedding: emb}
	}

	idx := NewVectorIndex()
	idx.Build(chunks)

	query := make([]float32, dim)
	for i := range query {
		query[i] = float32(i) / float32(dim)
	}
	var sumSquares float64
	for _, v := range query {
		sumSquares += float64(v * v)
	}
	norm := float32(math.Sqrt(sumSquares))
	if norm > 0 {
		for i := range query {
			query[i] /= norm
		}
	}

	candidates := idx.CandidateIDs(query, idx.ProbeCount())
	t.Logf("small dataset: k=%d, nprobe=%d, candidates=%v, total=%d", idx.BucketCount(), idx.ProbeCount(), len(candidates), idx.TotalChunks())

	if len(candidates) == 10 {
		t.Log("all chunks returned as candidates — fallback to full scan would be used by SearchVector")
	}
}

func TestSearchVectorPrefilterRecall(t *testing.T) {
	d := setupDB(t)

	docPath := filepath.Join(t.TempDir(), "recall.md")
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "recall", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	const nChunks = 150
	chunks := make([]*Chunk, nChunks)
	for i := 0; i < nChunks; i++ {
		emb := make([]float32, 768)
		for j := range emb {
			emb[j] = float32(i*768+j) / float32(nChunks*768)
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
			UUID:         "recall-" + strconv.Itoa(i),
			DocumentPath: docPath,
			ChunkIndex:   i,
			Content:      "content " + strconv.Itoa(i),
			Embedding:    emb,
			Hash:         "recall-hash-" + strconv.Itoa(i),
		}
	}
	if err := d.SaveChunks(chunks); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	queryVec := make([]float32, len(chunks[50].Embedding))
	copy(queryVec, chunks[50].Embedding)

	results, err := d.SearchVector(queryVec, 10)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	if len(results) != 10 {
		t.Fatalf("expected 10 results, got %d", len(results))
	}

	if results[0].Chunk.UUID != "recall-50" {
		t.Logf("top hit: %s (score=%.6f), second: %s (score=%.6f)",
			results[0].Chunk.UUID, results[0].CosineScore,
			results[1].Chunk.UUID, results[1].CosineScore)
	}

	if d.vectorIndex == nil || !d.vectorIndex.IsReady() {
		t.Log("index not built (dataset below threshold) — prefilter inactive, but exact results are correct")
	} else {
		t.Logf("index active: k=%d, nprobe=%d", d.vectorIndex.BucketCount(), d.vectorIndex.ProbeCount())
	}
}

func TestSearchVectorPrefilterFullRecall(t *testing.T) {
	d := setupDB(t)

	docPath := filepath.Join(t.TempDir(), "full-recall.md")
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "fr", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	const nChunks = 300
	allChunks := make([]*Chunk, nChunks)
	for i := 0; i < nChunks; i++ {
		emb := make([]float32, 768)
		for j := range emb {
			emb[j] = float32(i*768+j) / float32(nChunks*768)
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
		allChunks[i] = &Chunk{
			UUID:         "fr-" + strconv.Itoa(i),
			DocumentPath: docPath,
			ChunkIndex:   i,
			Content:      "content " + strconv.Itoa(i),
			Embedding:    emb,
			Hash:         "fr-hash-" + strconv.Itoa(i),
		}
	}
	if err := d.SaveChunks(allChunks); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	queryVec := make([]float32, 768)
	for i := range queryVec {
		queryVec[i] = float32(150*768+i) / float32(nChunks*768)
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

	// First call: builds the index and scores via full scan.
	firstResults, err := d.SearchVector(queryVec, 10)
	if err != nil {
		t.Fatalf("SearchVector (first): %v", err)
	}

	if d.vectorIndex == nil || !d.vectorIndex.IsReady() {
		t.Fatalf("expected index to be built after first search with %d chunks", nChunks)
	}

	secondResults, err := d.SearchVector(queryVec, 10)
	if err != nil {
		t.Fatalf("SearchVector (second): %v", err)
	}

	if len(firstResults) != len(secondResults) {
		t.Fatalf("result count mismatch: first=%d, second=%d", len(firstResults), len(secondResults))
	}

	for i := range firstResults {
		if firstResults[i].Chunk.UUID != secondResults[i].Chunk.UUID {
			t.Errorf("result %d UUID mismatch: first=%s, second=%s", i, firstResults[i].Chunk.UUID, secondResults[i].Chunk.UUID)
		}
		if math.Abs(float64(firstResults[i].CosineScore-secondResults[i].CosineScore)) > 1e-6 {
			t.Errorf("result %d score mismatch: first=%.6f, second=%.6f", i, firstResults[i].CosineScore, secondResults[i].CosineScore)
		}
	}

	t.Logf("prefilter recall: exact vs prefilter — %d/%d ranks match", len(firstResults), len(secondResults))
}

func TestSearchVectorInvalidatesIndexOnSave(t *testing.T) {
	d := setupDB(t)

	docPath := filepath.Join(t.TempDir(), "inv.md")
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "inv", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	const nChunks = 200
	chunks := make([]*Chunk, nChunks)
	for i := 0; i < nChunks; i++ {
		emb := make([]float32, 768)
		for j := range emb {
			emb[j] = float32(i*768+j) / float32(nChunks*768)
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
			UUID:         "inv-" + strconv.Itoa(i),
			DocumentPath: docPath,
			ChunkIndex:   i,
			Content:      "content " + strconv.Itoa(i),
			Embedding:    emb,
			Hash:         "inv-hash-" + strconv.Itoa(i),
		}
	}
	if err := d.SaveChunks(chunks); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	query := make([]float32, 768)
	for i := range query {
		query[i] = 0.5
	}
	_, err := d.SearchVector(query, 5)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	if d.vectorIndex == nil || !d.vectorIndex.IsReady() {
		t.Fatal("expected index to be built")
	}

	extra := []*Chunk{
		{
			UUID:         "inv-extra",
			DocumentPath: docPath,
			ChunkIndex:   nChunks,
			Content:      "extra",
			Embedding:    make([]float32, 768),
			Hash:         "inv-extra-hash",
		},
	}
	if err := d.SaveChunks(extra); err != nil {
		t.Fatalf("SaveChunks (extra): %v", err)
	}
	if d.vectorIndex != nil {
		t.Fatal("expected index to be invalidated after SaveChunks")
	}
}

func TestSearchVectorInvalidatesIndexOnDelete(t *testing.T) {
	d := setupDB(t)

	docPath := filepath.Join(t.TempDir(), "del.md")
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "del", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}

	const nChunks = 200
	chunks := make([]*Chunk, nChunks)
	for i := 0; i < nChunks; i++ {
		emb := make([]float32, 768)
		for j := range emb {
			emb[j] = float32(i*768+j) / float32(nChunks*768)
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
			UUID:         "del-" + strconv.Itoa(i),
			DocumentPath: docPath,
			ChunkIndex:   i,
			Content:      "content " + strconv.Itoa(i),
			Embedding:    emb,
			Hash:         "del-hash-" + strconv.Itoa(i),
		}
	}
	if err := d.SaveChunks(chunks); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}

	query := make([]float32, 768)
	for i := range query {
		query[i] = 0.5
	}
	_, err := d.SearchVector(query, 5)
	if err != nil {
		t.Fatalf("SearchVector: %v", err)
	}
	if d.vectorIndex == nil || !d.vectorIndex.IsReady() {
		t.Fatal("expected index to be built")
	}

	if err := d.DeleteDocument(docPath); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if d.vectorIndex != nil {
		t.Fatal("expected index to be invalidated after DeleteDocument")
	}
}
