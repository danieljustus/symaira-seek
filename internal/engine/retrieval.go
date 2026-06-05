package engine

import (
	"sort"

	"github.com/danieljustus/symaira-seek/internal/db"
)

// SearchHybrid combines BM25 keyword search and semantic vector search using Reciprocal Rank Fusion (RRF).
// Both the persistence layer and the embedder are consumed through their
// respective interfaces so callers can pass mocks or alternate
// implementations in tests.
func SearchHybrid(dbClient db.Store, embedder Embedder, query string, limit int) ([]*db.SearchResult, error) {
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

	// 2. Run BM25 first to obtain a candidate set for the vector pre-filter
	// (issue #38). If BM25 returns nothing useful we fall back to a full
	// vector scan via SearchVector with an empty candidate list.
	bm25Results, err := dbClient.SearchBM25(query, fetchLimit)
	if err != nil {
		bm25Results = nil
	}

	candidateIDs := make([]int64, 0, len(bm25Results))
	for _, r := range bm25Results {
		if r != nil && r.Chunk != nil {
			candidateIDs = append(candidateIDs, r.Chunk.ID)
		}
	}

	var vectorResults []*db.SearchResult
	if len(candidateIDs) > 0 {
		vectorResults, err = dbClient.SearchVectorFiltered(queryVec, candidateIDs, fetchLimit)
		if err != nil {
			return nil, err
		}
	} else {
		vectorResults, err = dbClient.SearchVector(queryVec, fetchLimit)
		if err != nil {
			return nil, err
		}
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
