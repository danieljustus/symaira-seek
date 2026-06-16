package db

import (
	"database/sql"
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

func (db *DB) SearchVector(queryVec []float32, limit int) ([]*SearchResult, error) {
	return db.searchVectorSinglePass(queryVec, nil, limit)
}

func (db *DB) SearchVectorFiltered(queryVec []float32, candidateIDs []int64, limit int) ([]*SearchResult, error) {
	return db.searchVectorSinglePass(queryVec, candidateIDs, limit)
}

func (db *DB) searchVectorSinglePass(queryVec []float32, candidateIDs []int64, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		return nil, nil
	}

	queryNorm := l2Norm(queryVec)

	var (
		rows *sql.Rows
		err  error
	)
	if len(candidateIDs) == 0 {
		rows, err = db.conn.Query(searchVectorSinglePassSelect)
	} else {
		rows, err = db.conn.Query(buildFilteredSelect(len(candidateIDs)), int64SliceArgs(candidateIDs)...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]*SearchResult, 0, limit)
	for rows.Next() {
		var c Chunk
		var embBytes []byte
		var norm float32
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embBytes, &c.Hash, &norm); err != nil {
			return nil, err
		}
		c.Embedding = BytesToFloat32Slice(embBytes)
		c.Norm = norm

		var score float32
		if queryNorm > 0 && norm > 0 {
			score = CosineSimilarityWithBothNorms(queryVec, c.Embedding, queryNorm, norm)
		} else if norm > 0 {
			score = CosineSimilarityWithNorm(queryVec, c.Embedding, norm)
		} else {
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
	return results, nil
}

const searchVectorSinglePassSelect = "SELECT id, uuid, document_path, chunk_index, content, embedding, hash, norm FROM chunks"

func buildFilteredSelect(n int) string {
	if n <= 0 {
		return searchVectorSinglePassSelect
	}
	return "SELECT id, uuid, document_path, chunk_index, content, embedding, hash, norm FROM chunks WHERE id IN (" + strings.Repeat("?,", n-1) + "?)"
}

func int64SliceArgs(ids []int64) []interface{} {
	out := make([]interface{}, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
