package vectorquant

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"
)

// BenchmarkConfig holds parameters for a quantization benchmark run.
type BenchmarkConfig struct {
	Dim         int       // vector dimension (default 768)
	NumVectors  int       // number of database vectors
	NumQueries  int       // number of query vectors
	K           int       // top-k for recall measurement
	Seed        int64     // random seed for reproducibility
	RotSeed     int       // seed for random rotation
	BitWidths   []BitWidth // bit widths to benchmark
	UseRerank   bool      // whether to include exact-rerank mode
}

// DefaultBenchmarkConfig returns a sensible default for 768-dim embeddings.
func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		Dim:         768,
		NumVectors:  1000,
		NumQueries:  50,
		K:           10,
		Seed:        42,
		RotSeed:     1337,
		BitWidths:   []BitWidth{BitWidth2, BitWidth3, BitWidth4},
		UseRerank:   true,
	}
}

// BenchmarkResult holds the results for one bit-width configuration.
type BenchmarkResult struct {
	BitWidth        BitWidth
	BytesPerVector  int
	CompressionRatio float64
	EncodeTime      time.Duration
	QueryTime       time.Duration     // avg query time for approximate search
	RerankQueryTime time.Duration     // avg query time with exact rerank (if enabled)
	TopKRecall      float64           // recall@k vs exact float32 ranking
	RerankRecall    float64           // recall@k after exact rerank of shortlist
	MeanSquaredError float64          // MSE of reconstructed vs original
}

// RunBenchmark executes a full benchmark comparing exact float32 baseline
// against quantized representations at the configured bit widths.
func RunBenchmark(cfg BenchmarkConfig) []BenchmarkResult {
	if cfg.Dim <= 0 {
		cfg.Dim = 768
	}
	if cfg.NumVectors <= 0 {
		cfg.NumVectors = 1000
	}
	if cfg.NumQueries <= 0 {
		cfg.NumQueries = 50
	}
	if cfg.K <= 0 {
		cfg.K = 10
	}
	if len(cfg.BitWidths) == 0 {
		cfg.BitWidths = []BitWidth{BitWidth2, BitWidth3, BitWidth4}
	}

	rng := rand.New(rand.NewSource(cfg.Seed))

	// Generate database vectors (normalized embeddings)
	dbVecs := make([][]float32, cfg.NumVectors)
	for i := range dbVecs {
		dbVecs[i] = randomNormalizedVector(rng, cfg.Dim)
	}

	// Generate query vectors
	queryVecs := make([][]float32, cfg.NumQueries)
	for i := range queryVecs {
		queryVecs[i] = randomNormalizedVector(rng, cfg.Dim)
	}

	// Compute exact float32 ground-truth rankings
	exactRanks := computeExactRanks(queryVecs, dbVecs, cfg.K)

	float32BytesPerVec := cfg.Dim * 4
	results := []BenchmarkResult{}

	for _, bw := range cfg.BitWidths {
		result := benchmarkBitWidth(cfg, dbVecs, queryVecs, exactRanks, bw)
		codec, _ := NewCodec(cfg.Dim, bw, cfg.RotSeed, 0)
		result.BytesPerVector = bw.BytesPerVector(codec.RotDim)
		result.CompressionRatio = float64(float32BytesPerVec) / float64(result.BytesPerVector)
		results = append(results, result)
	}

	return results
}

// RunBenchmarkWithFixture runs a benchmark on a pre-generated fixture of
// database and query vectors. This allows testing with realistic embeddings.
func RunBenchmarkWithFixture(dbVecs, queryVecs [][]float32, k int, bitWidths []BitWidth, rerank bool, rotSeed int) []BenchmarkResult {
	dim := len(dbVecs[0])
	if len(bitWidths) == 0 {
		bitWidths = []BitWidth{BitWidth2, BitWidth3, BitWidth4}
	}

	exactRanks := computeExactRanks(queryVecs, dbVecs, k)
	float32BytesPerVec := dim * 4

	results := []BenchmarkResult{}
	for _, bw := range bitWidths {
		cfg := BenchmarkConfig{
			Dim:       dim,
			K:         k,
			BitWidths: []BitWidth{bw},
			UseRerank: rerank,
			RotSeed:   rotSeed,
		}
		result := benchmarkBitWidth(cfg, dbVecs, queryVecs, exactRanks, bw)
		codec, _ := NewCodec(dim, bw, rotSeed, 0)
		result.BytesPerVector = bw.BytesPerVector(codec.RotDim)
		result.CompressionRatio = float64(float32BytesPerVec) / float64(result.BytesPerVector)
		results = append(results, result)
	}

	return results
}

func benchmarkBitWidth(cfg BenchmarkConfig, dbVecs, queryVecs [][]float32, exactRanks [][]int, bw BitWidth) BenchmarkResult {
	dim := cfg.Dim
	k := cfg.K
	numQueries := len(queryVecs)

	codec, err := NewCodec(dim, bw, cfg.RotSeed, 0)
	if err != nil {
		return BenchmarkResult{BitWidth: bw}
	}

	// Encode all database vectors
	start := time.Now()
	codes := make([]*PackedCode, len(dbVecs))
	for i, vec := range dbVecs {
		codes[i], _ = codec.Encode(vec)
	}
	encodeTime := time.Since(start)

	// Query benchmark
	start = time.Now()
	approxRanks := make([][]int, numQueries)
	for qi, q := range queryVecs {
		scores := make([]float64, len(codes))
		for ci, code := range codes {
			scores[ci] = float64(codec.Score(q, code))
		}
		approxRanks[qi] = topKIndices(scores, k)
	}
	queryTime := time.Since(start) / time.Duration(numQueries)

	// Compute recall
	totalRecall := 0.0
	for qi := range queryVecs {
		totalRecall += recallAtK(exactRanks[qi], approxRanks[qi])
	}
	recall := totalRecall / float64(numQueries)

	// MSE
	totalMSE := 0.0
	for _, vec := range dbVecs {
		code, _ := codec.Encode(vec)
		decoded, _ := codec.Decode(code)
		for i := range vec {
			diff := float64(vec[i]) - float64(decoded[i])
			totalMSE += diff * diff
		}
	}
	mse := totalMSE / float64(len(dbVecs)*dim)

	result := BenchmarkResult{
		BitWidth:       bw,
		EncodeTime:     encodeTime,
		QueryTime:      queryTime,
		TopKRecall:     recall,
		MeanSquaredError: mse,
	}

	// Exact-rerank: fetch full float32 for a larger quantized shortlist, then rerank
	if cfg.UseRerank {
		rerankShortlist := k * 4 // 4x oversampling
		if rerankShortlist > len(dbVecs) {
			rerankShortlist = len(dbVecs)
		}

		start = time.Now()
		rerankRecalls := make([]float64, numQueries)
		for qi, q := range queryVecs {
			// Get a larger shortlist from quantized search
			scores := make([]float64, len(codes))
			for ci, code := range codes {
				scores[ci] = float64(codec.Score(q, code))
			}
			shortlist := topKIndices(scores, rerankShortlist)

			// Rerank with exact cosine similarity on full float32
			reranked := make([]float64, len(shortlist))
			for si, idx := range shortlist {
				reranked[si] = float64(cosineSimilarityFloat32(q, dbVecs[idx]))
			}
			// Sort by reranked score
			sortOrder := make([]int, len(shortlist))
			for i := range sortOrder {
				sortOrder[i] = i
			}
			sort.SliceStable(sortOrder, func(a, b int) bool {
				return reranked[sortOrder[a]] > reranked[sortOrder[b]]
			})
			rerankedTopK := make([]int, k)
			for i := 0; i < k && i < len(shortlist); i++ {
				rerankedTopK[i] = shortlist[sortOrder[i]]
			}
			rerankRecalls[qi] = recallAtK(exactRanks[qi], rerankedTopK)
		}
		rerankTime := time.Since(start) / time.Duration(numQueries)

		totalRerank := 0.0
		for _, r := range rerankRecalls {
			totalRerank += r
		}
		result.RerankQueryTime = rerankTime
		result.RerankRecall = totalRerank / float64(numQueries)
	}

	return result
}

// GenerateSyntheticDataset creates a deterministic synthetic dataset of
// normalized vectors for benchmarking.
func GenerateSyntheticDataset(dim, numVectors, numQueries int, seed int64) (dbVecs, queryVecs [][]float32) {
	rng := rand.New(rand.NewSource(seed))

	dbVecs = make([][]float32, numVectors)
	for i := range dbVecs {
		dbVecs[i] = randomNormalizedVector(rng, dim)
	}

	queryVecs = make([][]float32, numQueries)
	for i := range queryVecs {
		queryVecs[i] = randomNormalizedVector(rng, dim)
	}
	return
}

// GenerateRealisticFixture creates a fixture that mimics 768-dim embeddings
// with a mixture of clusters (representing document topics). This produces
// more realistic recall numbers than pure random vectors.
//
// To use with real embeddings, generate them externally and pass via
// RunBenchmarkWithFixture.
func GenerateRealisticFixture(dim, numVectors, numQueries int, seed int64) (dbVecs, queryVecs [][]float32) {
	rng := rand.New(rand.NewSource(seed))

	// Create cluster centroids (e.g., 10 topics)
	numClusters := 10
	centroids := make([][]float32, numClusters)
	for i := range centroids {
		centroids[i] = randomNormalizedVector(rng, dim)
	}

	// Assign vectors to clusters with noise
	dbVecs = make([][]float32, numVectors)
	for i := range dbVecs {
		cluster := rng.Intn(numClusters)
		vec := make([]float32, dim)
		for d := range vec {
			vec[d] = centroids[cluster][d] + float32(rng.NormFloat64())*0.3
		}
		normalizeInPlace(vec)
		dbVecs[i] = vec
	}

	// Queries come from the same distribution
	queryVecs = make([][]float32, numQueries)
	for i := range queryVecs {
		cluster := rng.Intn(numClusters)
		vec := make([]float32, dim)
		for d := range vec {
			vec[d] = centroids[cluster][d] + float32(rng.NormFloat64())*0.3
		}
		normalizeInPlace(vec)
		queryVecs[i] = vec
	}
	return
}

// FormatBenchmarkReport produces a human-readable report of benchmark results.
func FormatBenchmarkReport(cfg BenchmarkConfig, results []BenchmarkResult) string {
	float32Bytes := cfg.Dim * 4
	out := ""
	out += "=== TurboQuant Benchmark Report ===\n"
	out += fmt.Sprintf("Dimension: %d  |  Vectors: %d  |  Queries: %d  |  Top-K: %d\n\n", cfg.Dim, cfg.NumVectors, cfg.NumQueries, cfg.K)
	out += fmt.Sprintf("%-12s %8s %8s %12s %12s %10s %12s\n",
		"Mode", "Bytes/V", "Ratio", "Encode", "Query", "Recall@K", "MSE")
	out += fmt.Sprintf("%-12s %8s %8s %12s %12s %10s %12s\n",
		"--------", "--------", "--------", "------------", "------------", "----------", "------------")

	// Float32 baseline
	out += fmt.Sprintf("%-12s %8d %8s %12s %12s %10s %12s\n",
		"float32", float32Bytes, "1.0x", "baseline", "baseline", "1.0000", "0.0000")

	for _, r := range results {
		out += fmt.Sprintf("%-12s %8d %7.1fx %12s %12s %10.4f %12.6f\n",
			r.BitWidth.String(), r.BytesPerVector, r.CompressionRatio,
			r.EncodeTime.String(), r.QueryTime.String(),
			r.TopKRecall, r.MeanSquaredError)
	}

	if cfg.UseRerank && len(results) > 0 && results[0].RerankQueryTime > 0 {
		out += "\n--- Exact Rerank (fetch float32 for quantized shortlist) ---\n"
		out += fmt.Sprintf("%-12s %12s %10s\n", "Mode", "Rerank Q", "Rerank@K")
		out += fmt.Sprintf("%-12s %12s %10s\n", "--------", "------------", "----------")
		for _, r := range results {
			out += fmt.Sprintf("%-12s %12s %10.4f\n",
				r.BitWidth.String(), r.RerankQueryTime.String(), r.RerankRecall)
		}
	}

	// Recommendation
	out += "\n--- Recommendation ---\n"
	best := bestBitWidth(results)
	if best != nil {
		out += fmt.Sprintf("Best trade-off: %s (%.1fx compression, %.1f%% recall@%d)\n",
			best.BitWidth.String(), best.CompressionRatio, best.TopKRecall*100, cfg.K)
		if best.TopKRecall >= 0.95 {
			out += fmt.Sprintf("KEEP: %s achieves >95%% recall with %.1fx storage reduction.\n", best.BitWidth.String(), best.CompressionRatio)
		} else if best.TopKRecall >= 0.85 {
			out += fmt.Sprintf("CONDITIONAL KEEP: %s recall is %.1f%%; recommend exact-rerank stage.\n", best.BitWidth.String(), best.TopKRecall*100)
		} else {
			out += fmt.Sprintf("KILL: Best recall is only %.1f%%; quantization cost too high for search quality.\n", best.TopKRecall*100)
		}
	} else {
		out += "Insufficient data for recommendation.\n"
	}

	return out
}

// String returns a human-readable label for the bit width.
func (b BitWidth) String() string {
	switch b {
	case BitWidth2:
		return "2-bit"
	case BitWidth3:
		return "3-bit"
	case BitWidth4:
		return "4-bit"
	case BitWidthHalf2and3:
		return "2.5-bit"
	case BitWidthHalf3and4:
		return "3.5-bit"
	default:
		return fmt.Sprintf("unknown(%d)", int(b))
	}
}

// --- internal helpers ---

func randomNormalizedVector(rng *rand.Rand, dim int) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	normalizeInPlace(vec)
	return vec
}

func normalizeInPlace(vec []float32) {
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm < 1e-10 {
		return
	}
	for i := range vec {
		vec[i] = float32(float64(vec[i]) / norm)
	}
}

func cosineSimilarityFloat32(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

// computeExactRanks returns the ground-truth top-k indices for each query.
func computeExactRanks(queries, dbVecs [][]float32, k int) [][]int {
	ranks := make([][]int, len(queries))
	for qi, q := range queries {
		scores := make([]float64, len(dbVecs))
		for di, d := range dbVecs {
			scores[di] = float64(cosineSimilarityFloat32(q, d))
		}
		ranks[qi] = topKIndices(scores, k)
	}
	return ranks
}

// topKIndices returns the indices of the k largest values in scores.
func topKIndices(scores []float64, k int) []int {
	n := len(scores)
	if k > n {
		k = n
	}
	type idxScore struct {
		idx   int
		score float64
	}
	arr := make([]idxScore, n)
	for i, s := range scores {
		arr[i] = idxScore{i, s}
	}
	sort.SliceStable(arr, func(i, j int) bool {
		return arr[i].score > arr[j].score
	})
	result := make([]int, k)
	for i := 0; i < k; i++ {
		result[i] = arr[i].idx
	}
	return result
}

// recallAtK computes recall@k: fraction of exact top-k that appears in approx top-k.
func recallAtK(exact, approx []int) float64 {
	k := len(exact)
	if len(approx) < k {
		k = len(approx)
	}
	exactSet := make(map[int]bool, len(exact))
	for _, idx := range exact {
		exactSet[idx] = true
	}
	hits := 0
	for i := 0; i < k; i++ {
		if exactSet[approx[i]] {
			hits++
		}
	}
	return float64(hits) / float64(k)
}

func bestBitWidth(results []BenchmarkResult) *BenchmarkResult {
	if len(results) == 0 {
		return nil
	}
	// Pick the one with highest recall that achieves >= 0.9, else highest recall
	best := &results[0]
	for i := range results {
		if results[i].TopKRecall > best.TopKRecall {
			best = &results[i]
		}
	}
	return best
}
