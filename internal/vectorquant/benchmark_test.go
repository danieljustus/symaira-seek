package vectorquant

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// TestBenchmarkReport runs the full benchmark and prints a report.
// This is a test (not a benchmark) so it prints the full report output.
func TestBenchmarkReport(t *testing.T) {
	cfg := DefaultBenchmarkConfig()
	cfg.Dim = 128    // smaller for fast test
	cfg.NumVectors = 200
	cfg.NumQueries = 10
	cfg.BitWidths = []BitWidth{BitWidth2, BitWidth3, BitWidth4}

	results := RunBenchmark(cfg)
	report := FormatBenchmarkReport(cfg, results)
	fmt.Fprint(os.Stderr, report)

	// Verify we got results for all bit widths
	if len(results) != len(cfg.BitWidths) {
		t.Errorf("Expected %d results, got %d", len(cfg.BitWidths), len(results))
	}
}

// TestBenchmark768Dim runs the benchmark at the target 768 dimension.
// Skipped by default; run with -run TestBenchmark768Dim.
func TestBenchmark768Dim(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping 768-dim benchmark in short mode")
	}

	cfg := DefaultBenchmarkConfig()
	cfg.NumVectors = 500
	cfg.NumQueries = 20

	results := RunBenchmark(cfg)
	report := FormatBenchmarkReport(cfg, results)
	fmt.Fprint(os.Stderr, report)

	for _, r := range results {
		t.Logf("768-dim %s: recall@%d=%.4f, compression=%.1fx, bytes=%d",
			r.BitWidth, cfg.K, r.TopKRecall, r.CompressionRatio, r.BytesPerVector)
	}
}

// TestBenchmarkRealistic runs the benchmark with cluster-structured vectors.
func TestBenchmarkRealistic(t *testing.T) {
	dim := 128
	numVecs := 200
	numQueries := 10
	k := 10

	dbVecs, queryVecs := GenerateRealisticFixture(dim, numVecs, numQueries, 42)

	bitWidths := []BitWidth{BitWidth2, BitWidth3, BitWidth4}
	results := RunBenchmarkWithFixture(dbVecs, queryVecs, k, bitWidths, true, 1337)

	cfg := BenchmarkConfig{
		Dim:       dim,
		K:         k,
		BitWidths: bitWidths,
		UseRerank: true,
	}
	report := FormatBenchmarkReport(cfg, results)
	fmt.Fprint(os.Stderr, report)
}

// TestBenchmarkChannelSplit runs the benchmark with channel-split modes.
func TestBenchmarkChannelSplit(t *testing.T) {
	dim := 128
	numVecs := 200
	numQueries := 10
	k := 10

	dbVecs, queryVecs := GenerateSyntheticDataset(dim, numVecs, numQueries, 42)

	bitWidths := []BitWidth{BitWidthHalf2and3, BitWidthHalf3and4}
	results := RunBenchmarkWithFixture(dbVecs, queryVecs, k, bitWidths, true, 1337)

	for _, r := range results {
		t.Logf("Channel-split %s: recall@%d=%.4f, bytes=%d",
			r.BitWidth, k, r.TopKRecall, r.BytesPerVector)
	}
}

// TestGoBenchmarks provides standard Go benchmarks for micro-benchmarks.
func BenchmarkEncode2Bit(b *testing.B) {
	benchmarkEncode(b, BitWidth2, 768)
}

func BenchmarkEncode3Bit(b *testing.B) {
	benchmarkEncode(b, BitWidth3, 768)
}

func BenchmarkEncode4Bit(b *testing.B) {
	benchmarkEncode(b, BitWidth4, 768)
}

func BenchmarkScore2Bit(b *testing.B) {
	benchmarkScore(b, BitWidth2, 768)
}

func BenchmarkScore3Bit(b *testing.B) {
	benchmarkScore(b, BitWidth3, 768)
}

func BenchmarkScore4Bit(b *testing.B) {
	benchmarkScore(b, BitWidth4, 768)
}

func BenchmarkDecode2Bit(b *testing.B) {
	benchmarkDecode(b, BitWidth2, 768)
}

func BenchmarkDecode3Bit(b *testing.B) {
	benchmarkDecode(b, BitWidth3, 768)
}

func BenchmarkDecode4Bit(b *testing.B) {
	benchmarkDecode(b, BitWidth4, 768)
}

func BenchmarkSidecarEncode2Bit(b *testing.B) {
	benchmarkSidecarEncode(b, BitWidth2, 768)
}

func BenchmarkSidecarEncode4Bit(b *testing.B) {
	benchmarkSidecarEncode(b, BitWidth4, 768)
}

func benchmarkEncode(b *testing.B, bw BitWidth, dim int) {
	codec, _ := NewCodec(dim, bw, 42, 0)
	rng := rand.New(rand.NewSource(1))
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Encode(vec)
	}
}

func benchmarkScore(b *testing.B, bw BitWidth, dim int) {
	codec, _ := NewCodec(dim, bw, 42, 0)
	rng := rand.New(rand.NewSource(1))

	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	code, _ := codec.Encode(vec)

	query := make([]float32, dim)
	for i := range query {
		query[i] = float32(rng.NormFloat64())
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = codec.Score(query, code)
	}
}

func benchmarkDecode(b *testing.B, bw BitWidth, dim int) {
	codec, _ := NewCodec(dim, bw, 42, 0)
	rng := rand.New(rand.NewSource(1))
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	code, _ := codec.Encode(vec)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = codec.Decode(code)
	}
}

func benchmarkSidecarEncode(b *testing.B, bw BitWidth, dim int) {
	codec, _ := NewCodec(dim, bw, 42, 0)
	rng := rand.New(rand.NewSource(1))
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = codec.EncodeSidecar(vec, 1.0)
	}
}
