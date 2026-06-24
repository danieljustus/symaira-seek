package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/danieljustus/symaira-seek/internal/vectorquant"
)

type quantCandidate struct {
	id       int64
	uuid     string
	docPath  string
	chunkIdx int
	hash     string
	norm     float32
	dim      int
	model    string
	score    float32
}

func (db *DB) SearchVectorQuantized(queryVec []float32, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		return nil, nil
	}
	cfg := db.quantConfig
	if cfg == nil || !cfg.Enabled {
		return db.SearchVector(queryVec, limit)
	}

	queryNorm := l2Norm(queryVec)
	db.checkGeneration()

	results, err := db.searchVectorQuantizedInner(queryVec, queryNorm, cfg, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: quantized search failed, falling back to standard search: %v\n", err)
		return db.SearchVector(queryVec, limit)
	}
	return results, nil
}

func (db *DB) searchVectorQuantizedInner(queryVec []float32, queryNorm float32, cfg *QuantConfig, limit int) ([]*SearchResult, error) {
	codec, err := vectorquant.NewCodec(len(queryVec), vectorquant.BitWidth(cfg.BitWidth), cfg.Seed, 0)
	if err != nil {
		return nil, fmt.Errorf("create codec: %w", err)
	}

	rows, err := db.conn.Query(
		`SELECT id, uuid, document_path, chunk_index, hash, norm, embedding_dim, embedding_model,
		        embedding_quant, embedding_quant_meta
		 FROM chunks WHERE embedding_quant IS NOT NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("quantized scan query: %w", err)
	}
	defer rows.Close()

	var candidates []quantCandidate
	for rows.Next() {
		var c quantCandidate
		var quantBytes []byte
		var metaRaw sql.NullString
		if err := rows.Scan(
			&c.id, &c.uuid, &c.docPath, &c.chunkIdx, &c.hash, &c.norm, &c.dim, &c.model,
			&quantBytes, &metaRaw,
		); err != nil {
			continue
		}

		if len(quantBytes) == 0 {
			continue
		}

		var sideMeta vectorquant.SidecarMeta
		if metaRaw.Valid && metaRaw.String != "" {
			if err := json.Unmarshal([]byte(metaRaw.String), &sideMeta); err != nil {
				continue
			}
		}

		if sideMeta.CodecVersion != vectorquant.CodecVersion {
			continue
		}
		if sideMeta.Dimension != len(queryVec) {
			continue
		}

		code, err := vectorquant.UnpackSidecarBlob(quantBytes)
		if err != nil {
			continue
		}

		score := codec.Score(queryVec, code)
		c.score = score
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("quantized scan rows: %w", err)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no quantized sidecars found")
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	shortlistSize := cfg.Shortlist
	if shortlistSize > len(candidates) {
		shortlistSize = len(candidates)
	}
	candidates = candidates[:shortlistSize]

	if !cfg.ExactRerank {
		return db.buildQuantResults(candidates, limit)
	}

	shortlistIDs := make([]int64, len(candidates))
	for i, c := range candidates {
		shortlistIDs[i] = c.id
	}

	return db.exactRerankShortlist(queryVec, queryNorm, shortlistIDs, limit)
}

func (db *DB) buildQuantResults(candidates []quantCandidate, limit int) ([]*SearchResult, error) {
	results := make([]*SearchResult, 0, limit)
	for i := range candidates {
		if i >= limit {
			break
		}
		c := &candidates[i]
		results = append(results, &SearchResult{
			Chunk: &Chunk{
				ID:           c.id,
				UUID:         c.uuid,
				DocumentPath: c.docPath,
				ChunkIndex:   c.chunkIdx,
				Hash:         c.hash,
				Norm:         c.norm,
				Dim:          c.dim,
				Model:        c.model,
			},
			CosineScore: c.score,
			VectorRank:  i + 1,
		})
	}
	if err := db.hydrateContent(results); err != nil {
		return nil, err
	}
	return results, nil
}

func (db *DB) exactRerankShortlist(queryVec []float32, queryNorm float32, shortlistIDs []int64, limit int) ([]*SearchResult, error) {
	placeholders := make([]string, len(shortlistIDs))
	args := make([]interface{}, len(shortlistIDs))
	for i, id := range shortlistIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		"SELECT id, uuid, document_path, chunk_index, embedding, hash, norm, embedding_dim, embedding_model FROM chunks WHERE id IN (%s)",
		strings.Join(placeholders, ","),
	)

	fetchRows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("rerank fetch query: %w", err)
	}
	defer fetchRows.Close()

	type fetchedRow struct {
		chunk    Chunk
		embBytes []byte
		norm     float32
	}

	rowByID := make(map[int64]*fetchedRow)
	for fetchRows.Next() {
		var r fetchedRow
		if err := fetchRows.Scan(
			&r.chunk.ID, &r.chunk.UUID, &r.chunk.DocumentPath, &r.chunk.ChunkIndex,
			&r.embBytes, &r.chunk.Hash, &r.norm, &r.chunk.Dim, &r.chunk.Model,
		); err != nil {
			continue
		}
		r.chunk.Norm = r.norm
		rowByID[r.chunk.ID] = &r
	}
	if err := fetchRows.Err(); err != nil {
		return nil, fmt.Errorf("rerank fetch rows: %w", err)
	}

	results := make([]*SearchResult, 0, limit)
	for _, id := range shortlistIDs {
		r, ok := rowByID[id]
		if !ok {
			continue
		}

		var score float32
		if queryNorm > 0 && r.norm > 0 {
			score = CosineSimilarityWithStoredNorm(queryVec, r.embBytes, queryNorm, r.norm)
		} else {
			r.chunk.Embedding = BytesToFloat32Slice(r.embBytes)
			score = CosineSimilarity(queryVec, r.chunk.Embedding)
		}

		if len(results) < limit {
			results = appendSortedByScoreDesc(results, &SearchResult{
				Chunk:       &r.chunk,
				CosineScore: score,
			})
		} else if score > results[limit-1].CosineScore {
			results = appendSortedByScoreDesc(results[:limit-1], &SearchResult{
				Chunk:       &r.chunk,
				CosineScore: score,
			})
		}
	}

	for i, r := range results {
		r.VectorRank = i + 1
	}

	if err := db.hydrateContent(results); err != nil {
		return nil, err
	}
	return results, nil
}
