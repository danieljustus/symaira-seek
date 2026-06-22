package db

import (
	"strings"
)

func escapeFTS5Query(query string) string {
	replacer := strings.NewReplacer(
		"\"", " ",
		"*", " ",
		"(", " ",
		")", " ",
		"+", " ",
		"-", " ",
		".", " ",
		"~", " ",
	)
	cleaned := replacer.Replace(query)
	tokens := strings.Fields(cleaned)
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " AND ")
}

func (db *DB) SearchBM25(queryStr string, limit int) ([]*SearchResult, error) {
	sqlQuery := `
		SELECT c.id, c.uuid, c.document_path, c.chunk_index, c.content, c.embedding, c.hash
		FROM chunks c
		JOIN chunks_fts f ON c.id = f.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts) ASC
		LIMIT ?`

	escapedQuery := escapeFTS5Query(queryStr)
	rows, err := db.conn.Query(sqlQuery, escapedQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*SearchResult
	rank := 1
	for rows.Next() {
		var c Chunk
		var embBytes []byte
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embBytes, &c.Hash); err != nil {
			return nil, err
		}
		c.Embedding = BytesToFloat32Slice(embBytes)

		results = append(results, &SearchResult{
			Chunk:    &c,
			BM25Rank: rank,
		})
		rank++
	}
	return results, nil
}

// searchVectorScanSelect omits the content column on purpose: vector scoring
// needs only the embedding and its precomputed norm. Streaming every chunk's
// text on every query is the dominant cost on large indexes, so content is
// fetched afterwards for just the surviving top-k rows (see hydrateContent).
const searchVectorScanSelect = "SELECT id, uuid, document_path, chunk_index, embedding, hash, norm FROM chunks"

func (db *DB) SearchVector(queryVec []float32, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		return nil, nil
	}

	queryNorm := l2Norm(queryVec)

	rows, err := db.conn.Query(searchVectorScanSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]*SearchResult, 0, limit)
	for rows.Next() {
		var c Chunk
		var embBytes []byte
		var norm float32
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &embBytes, &c.Hash, &norm); err != nil {
			return nil, err
		}
		c.Norm = norm

		var score float32
		if queryNorm > 0 && norm > 0 {
			score = CosineSimilarityWithStoredNorm(queryVec, embBytes, queryNorm)
		} else {
			c.Embedding = BytesToFloat32Slice(embBytes)
			score = CosineSimilarity(queryVec, c.Embedding)
		}

		if len(results) < limit {
			results = appendSortedByScoreDesc(results, &SearchResult{
				Chunk:       &c,
				CosineScore: score,
			})
		} else if score > results[limit-1].CosineScore {
			results = appendSortedByScoreDesc(results[:limit-1], &SearchResult{
				Chunk:       &c,
				CosineScore: score,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, r := range results {
		r.VectorRank = i + 1
	}

	// Fetch content only for the top-k survivors of the scan.
	if err := db.hydrateContent(results); err != nil {
		return nil, err
	}
	return results, nil
}

// hydrateContent fills in Chunk.Content for the given results using a single
// IN-list query keyed on the surviving chunk ids.
func (db *DB) hydrateContent(results []*SearchResult) error {
	if len(results) == 0 {
		return nil
	}

	byID := make(map[int64]*Chunk, len(results))
	args := make([]interface{}, len(results))
	for i, r := range results {
		byID[r.Chunk.ID] = r.Chunk
		args[i] = r.Chunk.ID
	}

	query := "SELECT id, content FROM chunks WHERE id IN (" + strings.Repeat("?,", len(results)-1) + "?)"
	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			return err
		}
		if c, ok := byID[id]; ok {
			c.Content = content
		}
	}
	return rows.Err()
}
