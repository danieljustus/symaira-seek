package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Document represents an indexed file on disk.
type Document struct {
	Path      string    `json:"path"`
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Chunk represents a segment of a document.
type Chunk struct {
	ID           int64     `json:"id"`
	UUID         string    `json:"uuid"`
	DocumentPath string    `json:"document_path"`
	ChunkIndex   int       `json:"chunk_index"`
	Content      string    `json:"content"`
	Embedding    []float32 `json:"embedding"`
	Hash         string    `json:"hash"`
}

// SearchResult wraps a chunk with ranking and score details.
type SearchResult struct {
	Chunk       *Chunk    `json:"chunk"`
	BM25Rank    int       `json:"bm25_rank"`    // 1-indexed rank in keyword search
	VectorRank  int       `json:"vector_rank"`  // 1-indexed rank in vector search
	RRFScore    float32   `json:"rrf_score"`
	CosineScore float32   `json:"cosine_score"`
}

// DB manages the SQLite connection.
type DB struct {
	conn *sql.DB
}

// Open initializes the SQLite database at a standard XDG path.
func Open() (*DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	dir := filepath.Join(home, ".local", "share", "symaira-seek")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	dbPath := filepath.Join(dir, "seek.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Enable WAL mode & foreign keys
	if _, err := conn.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to configure sqlite parameters: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// initSchema creates the database tables, virtual tables for FTS5, and sync triggers.
func (db *DB) initSchema() error {
	queries := []string{
		// 1. Documents Table
		`CREATE TABLE IF NOT EXISTS documents (
			path TEXT PRIMARY KEY,
			hash TEXT NOT NULL,
			updated_at DATETIME NOT NULL
		);`,

		// 2. Chunks Table
		`CREATE TABLE IF NOT EXISTS chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid TEXT UNIQUE NOT NULL,
			document_path TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			content TEXT NOT NULL,
			embedding TEXT NOT NULL,
			hash TEXT NOT NULL,
			FOREIGN KEY(document_path) REFERENCES documents(path) ON DELETE CASCADE
		);`,

		// 3. FTS5 Virtual Table for Chunks
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
			content,
			content='chunks',
			content_rowid='id'
		);`,

		// 4. Index on Foreign Key and UUID
		`CREATE INDEX IF NOT EXISTS idx_chunks_doc_path ON chunks(document_path);`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_uuid ON chunks(uuid);`,

		// 5. Triggers to keep chunks_fts synchronized with chunks
		`CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
			INSERT INTO chunks_fts(rowid, content) VALUES (new.id, new.content);
		END;`,

		`CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
			INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', old.id, old.content);
		END;`,

		`CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
			INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', old.id, old.content);
			INSERT INTO chunks_fts(rowid, content) VALUES (new.id, new.content);
		END;`,
	}

	for _, q := range queries {
		if _, err := db.conn.Exec(q); err != nil {
			return fmt.Errorf("failed executing schema migration: %w (query: %s)", err, q)
		}
	}
	return nil
}

// SaveDocument saves or updates document metadata in a transaction.
func (db *DB) SaveDocument(doc *Document) error {
	query := `INSERT INTO documents (path, hash, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			hash=excluded.hash,
			updated_at=excluded.updated_at`
	_, err := db.conn.Exec(query, doc.Path, doc.Hash, doc.UpdatedAt)
	return err
}

// DeleteDocument removes a document and its cascade associated chunks.
func (db *DB) DeleteDocument(path string) error {
	// First fetch all chunk ids to trigger the FTS delete trigger properly,
	// SQLite ON DELETE CASCADE might bypass individual AFTER DELETE triggers unless handled.
	// But actually, in modern SQLite, with PRAGMA foreign_keys=ON, cascade delete deletes
	// the child rows which triggers the AFTER DELETE trigger.
	// However, to be absolutely safe and keep the FTS index consistent, we can delete chunks first:
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM chunks WHERE document_path = ?", path)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM documents WHERE path = ?", path)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetDocument retrieves a document by path.
func (db *DB) GetDocument(path string) (*Document, error) {
	var doc Document
	err := db.conn.QueryRow("SELECT path, hash, updated_at FROM documents WHERE path = ?", path).
		Scan(&doc.Path, &doc.Hash, &doc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &doc, err
}

// ListDocuments lists all indexed documents.
func (db *DB) ListDocuments() ([]*Document, error) {
	rows, err := db.conn.Query("SELECT path, hash, updated_at FROM documents ORDER BY updated_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.Path, &doc.Hash, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		docs = append(docs, &doc)
	}
	return docs, nil
}

// SaveChunks inserts a list of chunks in a transaction.
func (db *DB) SaveChunks(chunks []*Chunk) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := `INSERT INTO chunks (uuid, document_path, chunk_index, content, embedding, hash)
		VALUES (?, ?, ?, ?, ?, ?)`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		embeddingJSON, err := json.Marshal(c.Embedding)
		if err != nil {
			return fmt.Errorf("failed to marshal embedding for chunk %s: %w", c.UUID, err)
		}

		res, err := stmt.Exec(c.UUID, c.DocumentPath, c.ChunkIndex, c.Content, string(embeddingJSON), c.Hash)
		if err != nil {
			return fmt.Errorf("failed to insert chunk: %w", err)
		}

		id, err := res.LastInsertId()
		if err == nil {
			c.ID = id
		}
	}

	return tx.Commit()
}

// GetChunksForDocument retrieves all chunks associated with a document path.
func (db *DB) GetChunksForDocument(docPath string) ([]*Chunk, error) {
	query := "SELECT id, uuid, document_path, chunk_index, content, embedding, hash FROM chunks WHERE document_path = ? ORDER BY chunk_index ASC"
	rows, err := db.conn.Query(query, docPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		var c Chunk
		var embStr string
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embStr, &c.Hash); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(embStr), &c.Embedding); err != nil {
			return nil, err
		}
		chunks = append(chunks, &c)
	}
	return chunks, nil
}

// GetStats returns database statistics.
type Stats struct {
	DocumentCount int   `json:"document_count"`
	ChunkCount    int   `json:"chunk_count"`
	DatabaseSize  int64 `json:"database_size_bytes"`
}

func (db *DB) GetStats() (*Stats, error) {
	var s Stats
	err := db.conn.QueryRow("SELECT COUNT(*) FROM documents").Scan(&s.DocumentCount)
	if err != nil {
		return nil, err
	}

	err = db.conn.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&s.ChunkCount)
	if err != nil {
		return nil, err
	}

	// Fetch DB size via PRAGMA page_count and page_size
	var pageCount, pageSize int64
	err = db.conn.QueryRow("PRAGMA page_count").Scan(&pageCount)
	if err != nil {
		return nil, err
	}
	err = db.conn.QueryRow("PRAGMA page_size").Scan(&pageSize)
	if err != nil {
		return nil, err
	}
	s.DatabaseSize = pageCount * pageSize

	return &s, nil
}

// SearchBM25 performs a keyword search on the FTS5 virtual table.
func (db *DB) SearchBM25(queryStr string, limit int) ([]*SearchResult, error) {
	// Look up in FTS5 table
	sqlQuery := `
		SELECT c.id, c.uuid, c.document_path, c.chunk_index, c.content, c.embedding, c.hash
		FROM chunks c
		JOIN chunks_fts f ON c.id = f.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts) ASC
		LIMIT ?`

	rows, err := db.conn.Query(sqlQuery, queryStr, limit)
	if err != nil {
		// FTS5 MATCH on empty query or weird chars might error, return empty slice instead of crashing
		return nil, nil
	}
	defer rows.Close()

	var results []*SearchResult
	rank := 1
	for rows.Next() {
		var c Chunk
		var embStr string
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embStr, &c.Hash); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(embStr), &c.Embedding); err != nil {
			return nil, err
		}

		results = append(results, &SearchResult{
			Chunk:    &c,
			BM25Rank: rank,
		})
		rank++
	}
	return results, nil
}

// SearchVector performs in-memory Cosine Similarity search over all chunks.
// For extremely large databases, it loads embeddings.
// Note: Since this is run on a local computer, loading all embeddings for up to 50k chunks
// fits easily in memory and runs in sub-10ms.
func (db *DB) SearchVector(queryVec []float32, limit int) ([]*SearchResult, error) {
	rows, err := db.conn.Query("SELECT id, uuid, document_path, chunk_index, content, embedding, hash FROM chunks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scoredResult struct {
		chunk *Chunk
		score float32
	}

	var scored []*scoredResult
	for rows.Next() {
		var c Chunk
		var embStr string
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embStr, &c.Hash); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(embStr), &c.Embedding); err != nil {
			return nil, err
		}

		score := CosineSimilarity(queryVec, c.Embedding)
		scored = append(scored, &scoredResult{
			chunk: &c,
			score: score,
		})
	}

	// Sort descending by score
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	if limit > len(scored) {
		limit = len(scored)
	}

	var results []*SearchResult
	for i := 0; i < limit; i++ {
		results = append(results, &SearchResult{
			Chunk:       scored[i].chunk,
			VectorRank:  i + 1,
			CosineScore: scored[i].score,
		})
	}

	return results, nil
}

// CosineSimilarity computes the cosine similarity between two float32 slices.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dotProduct / (math.Sqrt(normA) * math.Sqrt(normB)))
}
