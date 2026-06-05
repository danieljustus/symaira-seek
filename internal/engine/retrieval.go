package engine

import (
	"sort"
	"sync"

	"github.com/danieljustus/symaira-seek/internal/db"
)

// SearchHybrid combines BM25 keyword search and semantic vector search using Reciprocal Rank Fusion (RRF).
// The embedder is consumed through the Embedder interface so callers can
// pass a mock or alternate implementation in tests.
func SearchHybrid(dbClient *db.DB, embedder Embedder, query string, limit int) ([]*db.SearchResult, error) {
	if query == "" {
		return nil, nil
	}

	// 1. Generate query vector
	queryVec := embedder.GenerateVector(query)

	// We fetch a bit more than limit to ensure good fusion overlap
	fetchLimit := limit * 3
	if fetchLimit < 50 {
		fetchLimit = 50
	}

	var bm25Results []*db.SearchResult
	var vectorResults []*db.SearchResult
	var vecErr error

	var wg sync.WaitGroup
	wg.Add(2)

	// 2. Perform BM25 Search concurrently
	go func() {
		defer wg.Done()
		var err error
		bm25Results, err = dbClient.SearchBM25(query, fetchLimit)
		if err != nil {
			bm25Results = nil
		}
	}()

	// 3. Perform Vector Search concurrently
	go func() {
		defer wg.Done()
		vectorResults, vecErr = dbClient.SearchVector(queryVec, fetchLimit)
	}()

	wg.Wait()

	if vecErr != nil {
		return nil, vecErr
	}

	// 4. Reciprocal Rank Fusion
	k := float32(60.0) // Standard RRF parameter
	merged := make(map[string]*db.SearchResult)

	// Process BM25 ranks
	for i, res := range bm25Results {
		uuid := res.Chunk.UUID
		res.BM25Rank = i + 1
		merged[uuid] = res
	}

	// Process Vector ranks
	for i, res := range vectorResults {
		uuid := res.Chunk.UUID
		rank := i + 1
		if existing, ok := merged[uuid]; ok {
			existing.VectorRank = rank
			existing.CosineScore = res.CosineScore
		} else {
			res.VectorRank = rank
			merged[uuid] = res
		}
	}

	// Calculate final RRF scores
	var combined []*db.SearchResult
	for _, res := range merged {
		var score float32
		if res.BM25Rank > 0 {
			score += 1.0 / (k + float32(res.BM25Rank))
		}
		if res.VectorRank > 0 {
			score += 1.0 / (k + float32(res.VectorRank))
		}
		res.RRFScore = score
		combined = append(combined, res)
	}

	// Sort descending by RRFScore
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].RRFScore > combined[j].RRFScore
	})

	// Truncate to limit
	if len(combined) > limit {
		combined = combined[:limit]
	}

	return combined, nil
}
