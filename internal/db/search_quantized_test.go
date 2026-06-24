package db

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/vectorquant"
)

func newTestCodec(dim int, bitWidth int, seed int) (*vectorquant.Codec, error) {
	return vectorquant.NewCodec(dim, vectorquant.BitWidth(bitWidth), seed, 0)
}

func backfillSidecarsForTest(t *testing.T, d *DB, chunks []*Chunk, bitWidth int, seed int) {
	t.Helper()
	codec, err := newTestCodec(768, bitWidth, seed)
	if err != nil {
		t.Fatalf("newTestCodec: %v", err)
	}
	for _, c := range chunks {
		emb := c.Embedding
		if emb == nil {
			emb = make([]float32, 768)
			emb[0] = 1.0
		}
		var norm float32
		for _, v := range emb {
			norm += v * v
		}
		norm = float32(math.Sqrt(float64(norm)))

		blob, meta, err := codec.EncodeSidecar(emb, norm)
		if err != nil {
			t.Fatalf("EncodeSidecar chunk %d: %v", c.ID, err)
		}
		qcMeta := &QuantSidecarMeta{
			CodecVersion:   meta.CodecVersion,
			Dimension:      meta.Dimension,
			BitWidth:       meta.BitWidth,
			QuantizerMode:  meta.QuantizerMode,
			ProjectionSeed: meta.ProjectionSeed,
			Norm:           meta.Norm,
		}
		if err := d.SaveQuantizedSidecar(c.ID, blob, qcMeta); err != nil {
			t.Fatalf("SaveQuantizedSidecar chunk %d: %v", c.ID, err)
		}
	}
}

func insertTestChunks(t *testing.T, d *DB, n int) []*Chunk {
	t.Helper()
	docPath := "/test/docs.md"
	if err := d.SaveDocument(&Document{Path: docPath, Hash: "test", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}
	chunks := make([]*Chunk, n)
	for i := 0; i < n; i++ {
		emb := make([]float32, 768)
		for j := range emb {
			emb[j] = float32(math.Sin(float64(i)*0.1 + float64(j)*0.05))
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
			UUID:         "test-" + strconv.Itoa(i),
			DocumentPath: docPath,
			ChunkIndex:   i,
			Content:      "searchable content " + strconv.Itoa(i),
			Hash:         "test-hash-" + strconv.Itoa(i),
		}
		chunks[i].Embedding = emb
	}
	if err := d.SaveChunks(chunks); err != nil {
		t.Fatalf("SaveChunks: %v", err)
	}
	return chunks
}

func makeQueryVec() []float32 {
	q := make([]float32, 768)
	for i := range q {
		q[i] = float32(math.Sin(float64(25)*0.1 + float64(i)*0.05))
	}
	var sum float64
	for _, v := range q {
		sum += float64(v * v)
	}
	n := float32(math.Sqrt(sum))
	if n > 0 {
		for i := range q {
			q[i] /= n
		}
	}
	return q
}

func TestQuantizedSearch_DisabledByDefault(t *testing.T) {
	d := openTestDB(t)
	chunks := insertTestChunks(t, d, 10)
	backfillSidecarsForTest(t, d, chunks, 4, 42)

	if d.GetQuantConfig() != nil {
		t.Error("expected nil quant config by default")
	}

	queryVec := makeQueryVec()
	results, err := d.SearchVectorQuantized(queryVec, 5)
	if err != nil {
		t.Fatalf("SearchVectorQuantized: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from fallback search")
	}
}

func TestQuantizedSearch_EnabledReturnsResults(t *testing.T) {
	d := openTestDB(t)
	chunks := insertTestChunks(t, d, 50)
	backfillSidecarsForTest(t, d, chunks, 4, 42)

	d.SetQuantConfig(&QuantConfig{
		Enabled:     true,
		BitWidth:    4,
		Shortlist:   200,
		ExactRerank: true,
		Seed:        42,
	})

	queryVec := makeQueryVec()
	results, err := d.SearchVectorQuantized(queryVec, 5)
	if err != nil {
		t.Fatalf("SearchVectorQuantized: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from quantized search")
	}
	for i, r := range results {
		if r.VectorRank != i+1 {
			t.Errorf("result %d: VectorRank = %d, want %d", i, r.VectorRank, i+1)
		}
	}
}

func TestQuantizedSearch_FallbackWhenNoSidecars(t *testing.T) {
	d := openTestDB(t)
	insertTestChunks(t, d, 50)

	d.SetQuantConfig(&QuantConfig{
		Enabled:     true,
		BitWidth:    4,
		Shortlist:   200,
		ExactRerank: true,
		Seed:        42,
	})

	queryVec := makeQueryVec()
	results, err := d.SearchVectorQuantized(queryVec, 5)
	if err != nil {
		t.Fatalf("SearchVectorQuantized: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected fallback results when no sidecars exist")
	}
}

func TestQuantizedSearch_FallbackWhenDisabled(t *testing.T) {
	d := openTestDB(t)
	chunks := insertTestChunks(t, d, 50)
	backfillSidecarsForTest(t, d, chunks, 4, 42)

	d.SetQuantConfig(&QuantConfig{
		Enabled:     false,
		BitWidth:    4,
		Shortlist:   200,
		ExactRerank: true,
		Seed:        42,
	})

	queryVec := makeQueryVec()
	results, err := d.SearchVectorQuantized(queryVec, 5)
	if err != nil {
		t.Fatalf("SearchVectorQuantized: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected fallback results when disabled")
	}
}

func TestQuantizedSearch_ExactRerankFalse(t *testing.T) {
	d := openTestDB(t)
	chunks := insertTestChunks(t, d, 50)
	backfillSidecarsForTest(t, d, chunks, 4, 42)

	d.SetQuantConfig(&QuantConfig{
		Enabled:     true,
		BitWidth:    4,
		Shortlist:   200,
		ExactRerank: false,
		Seed:        42,
	})

	queryVec := makeQueryVec()
	results, err := d.SearchVectorQuantized(queryVec, 5)
	if err != nil {
		t.Fatalf("SearchVectorQuantized: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from quantized search without rerank")
	}
}

func TestQuantizedSearch_MixedSidecarAndNoSidecar(t *testing.T) {
	d := openTestDB(t)
	chunks := insertTestChunks(t, d, 50)

	for i := 0; i < 25; i++ {
		emb := chunks[i].Embedding
		var norm float32
		for _, v := range emb {
			norm += v * v
		}
		norm = float32(math.Sqrt(float64(norm)))

		codec, err := newTestCodec(768, 4, 42)
		if err != nil {
			t.Fatalf("newTestCodec: %v", err)
		}
		blob, meta, err := codec.EncodeSidecar(emb, norm)
		if err != nil {
			t.Fatalf("EncodeSidecar: %v", err)
		}
		qcMeta := &QuantSidecarMeta{
			CodecVersion:   meta.CodecVersion,
			Dimension:      meta.Dimension,
			BitWidth:       meta.BitWidth,
			QuantizerMode:  meta.QuantizerMode,
			ProjectionSeed: meta.ProjectionSeed,
			Norm:           meta.Norm,
		}
		if err := d.SaveQuantizedSidecar(chunks[i].ID, blob, qcMeta); err != nil {
			t.Fatalf("SaveQuantizedSidecar: %v", err)
		}
	}

	d.SetQuantConfig(&QuantConfig{
		Enabled:     true,
		BitWidth:    4,
		Shortlist:   200,
		ExactRerank: true,
		Seed:        42,
	})

	queryVec := makeQueryVec()
	results, err := d.SearchVectorQuantized(queryVec, 5)
	if err != nil {
		t.Fatalf("SearchVectorQuantized: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from mixed sidecar/no-sidecar dataset")
	}
}

func TestQuantizedSearch_VectorStoreInterface(t *testing.T) {
	d := openTestDB(t)
	chunks := insertTestChunks(t, d, 50)
	backfillSidecarsForTest(t, d, chunks, 4, 42)

	d.SetQuantConfig(&QuantConfig{
		Enabled:     true,
		BitWidth:    4,
		Shortlist:   200,
		ExactRerank: true,
		Seed:        42,
	})

	queryVec := makeQueryVec()
	results, err := d.Search(nil, queryVec, 5)
	if err != nil {
		t.Fatalf("VectorStore.Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from VectorStore.Search with quantization")
	}
}

func TestSetQuantConfig_Defaults(t *testing.T) {
	d := openTestDB(t)

	d.SetQuantConfig(&QuantConfig{Enabled: true})
	cfg := d.GetQuantConfig()
	if cfg.BitWidth != 4 {
		t.Errorf("expected default BitWidth=4, got %d", cfg.BitWidth)
	}
	if cfg.Shortlist != 200 {
		t.Errorf("expected default Shortlist=200, got %d", cfg.Shortlist)
	}
}

func TestSetQuantConfig_NilDisables(t *testing.T) {
	d := openTestDB(t)
	d.SetQuantConfig(&QuantConfig{Enabled: true})
	d.SetQuantConfig(nil)
	if d.GetQuantConfig() != nil {
		t.Error("expected nil after setting nil config")
	}
}
