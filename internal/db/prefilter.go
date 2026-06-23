package db

import (
	"math"
	"sort"
	"sync"
)

// VectorIndex is an in-memory IVF (Inverted File) index for approximate
// nearest neighbor search.  It partitions chunk embeddings into K buckets
// by centroid proximity so that a query only needs to score the chunks in
// the nprobe nearest buckets instead of the full corpus.
//
// The index is NOT persisted — it is built lazily from the chunks table
// on first use and invalidated whenever the underlying data changes.
type VectorIndex struct {
	dim       int
	centroids [][]float32
	inverted  [][]int64 // inverted[bucket] = chunk IDs
	k         int
	nprobe    int
	totalN    int
	ready     bool
	mu        sync.RWMutex
}

// NewVectorIndex creates a new, empty VectorIndex.
func NewVectorIndex() *VectorIndex {
	return &VectorIndex{}
}

// Build constructs the IVF index from the given normalized chunks.
// It uses a simple k-means-like assignment over cosine similarity.
// Safe to call multiple times; each call replaces the previous index.
func (vi *VectorIndex) Build(chunks []*Chunk) {
	vi.mu.Lock()
	defer vi.mu.Unlock()

	if len(chunks) == 0 {
		vi.ready = false
		return
	}

	vi.dim = len(chunks[0].Embedding)
	vi.totalN = len(chunks)

	// Determine K: sqrt(N), clamped to [4, 256].
	k := int(math.Sqrt(float64(vi.totalN)))
	if k < 4 {
		k = 4
	}
	if k > 256 {
		k = 256
	}
	if k > vi.totalN {
		k = vi.totalN
	}

	// Initialize centroids by evenly sampling chunks.
	step := vi.totalN / k
	if step < 1 {
		step = 1
	}
	vi.centroids = make([][]float32, 0, k)
	for i := 0; i < k && i*step < vi.totalN; i++ {
		c := make([]float32, vi.dim)
		copy(c, chunks[i*step].Embedding)
		vi.centroids = append(vi.centroids, c)
	}
	vi.k = len(vi.centroids)

	// Determine nprobe.
	vi.nprobe = int(math.Sqrt(float64(vi.k))) + 1
	if vi.nprobe < 3 {
		vi.nprobe = 3
	}
	if vi.nprobe > vi.k {
		vi.nprobe = vi.k
	}

	// Run 3 iterations of k-means assignment.
	vi.inverted = make([][]int64, vi.k)
	for iter := 0; iter < 3; iter++ {
		// Clear buckets.
		for b := range vi.inverted {
			vi.inverted[b] = vi.inverted[b][:0]
		}

		// Assign each chunk to its nearest centroid.
		for _, chunk := range chunks {
			bestBucket := 0
			bestScore := float32(-2)
			for bi, cent := range vi.centroids {
				score := CosineSimilarity(chunk.Embedding, cent)
				if score > bestScore {
					bestScore = score
					bestBucket = bi
				}
			}
			vi.inverted[bestBucket] = append(vi.inverted[bestBucket], chunk.ID)
		}

		// Update centroids to the mean of their assigned chunks.
		for bi := 0; bi < vi.k; bi++ {
			ids := vi.inverted[bi]
			if len(ids) == 0 {
				continue
			}
			// Build an ID set for O(1) lookup.
			idSet := make(map[int64]struct{}, len(ids))
			for _, id := range ids {
				idSet[id] = struct{}{}
			}
			mean := make([]float32, vi.dim)
			count := 0
			for _, chunk := range chunks {
				if _, ok := idSet[chunk.ID]; ok {
					for d := 0; d < vi.dim; d++ {
						mean[d] += chunk.Embedding[d]
					}
					count++
				}
			}
			if count > 0 {
				inv := float32(count)
				for d := 0; d < vi.dim; d++ {
					mean[d] /= inv
				}
				vi.centroids[bi] = mean
			}
		}
	}

	vi.ready = true
}

// CandidateIDs returns chunk IDs from the nprobe nearest centroid buckets.
// If the index is not ready, has no buckets, or the candidate set covers
// more than half the total chunks (making the filter counterproductive),
// it returns nil to signal that the caller should fall back to a full scan.
func (vi *VectorIndex) CandidateIDs(queryVec []float32, nprobe int) []int64 {
	vi.mu.RLock()
	defer vi.mu.RUnlock()

	if !vi.ready || vi.k == 0 {
		return nil
	}

	if nprobe > vi.k {
		nprobe = vi.k
	}

	// Score the query against every centroid.
	type centScore struct {
		idx   int
		score float32
	}
	ranked := make([]centScore, vi.k)
	for i, cent := range vi.centroids {
		ranked[i] = centScore{idx: i, score: CosineSimilarity(queryVec, cent)}
	}

	// Partial sort to pick the top-nprobe centroids.
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	// Count candidates in the top-nprobe buckets.
	candidateCount := 0
	for i := 0; i < nprobe; i++ {
		candidateCount += len(vi.inverted[ranked[i].idx])
	}

	// If this covers >50 % of the corpus, just do a full scan.
	if vi.totalN > 0 && candidateCount > vi.totalN/2 {
		return nil
	}

	// Deduplicate (a chunk can appear in multiple buckets from k-means
	// iterations, though in practice each chunk maps to exactly one
	// bucket after the final iteration).
	seen := make(map[int64]struct{}, candidateCount)
	candidates := make([]int64, 0, candidateCount)
	for i := 0; i < nprobe; i++ {
		for _, id := range vi.inverted[ranked[i].idx] {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				candidates = append(candidates, id)
			}
		}
	}

	return candidates
}

// ProbeCount returns the default number of centroids to probe for a query.
func (vi *VectorIndex) ProbeCount() int {
	vi.mu.RLock()
	defer vi.mu.RUnlock()
	return vi.nprobe
}

// IsReady reports whether the index has been built and is usable.
func (vi *VectorIndex) IsReady() bool {
	vi.mu.RLock()
	defer vi.mu.RUnlock()
	return vi.ready
}

// BucketCount returns the number of buckets (K) in the index.
func (vi *VectorIndex) BucketCount() int {
	vi.mu.RLock()
	defer vi.mu.RUnlock()
	return vi.k
}

// TotalChunks returns the number of chunks that were indexed.
func (vi *VectorIndex) TotalChunks() int {
	vi.mu.RLock()
	defer vi.mu.RUnlock()
	return vi.totalN
}

// indexBuildThreshold is the minimum number of chunks required before the
// IVF prefilter is activated.  Below this threshold the full scan is fast
// enough and the WHERE-IN overhead would dominate.
const indexBuildThreshold = 128
