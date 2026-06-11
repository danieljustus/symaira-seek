package db

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	Norm         float32   `json:"norm"` // Precomputed L2 norm of the embedding vector
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

// Store is the public surface of the persistence layer used by the MCP and
// HTTP servers. It exposes the read / search / write operations the
// servers actually need without leaking the concrete *DB struct or its
// raw *sql.DB field, which lets tests substitute an in-memory fake.
type Store interface {
	Close() error
	SaveDocument(doc *Document) error
	DeleteDocument(path string) error
	GetDocument(path string) (*Document, error)
	ListDocuments() ([]*Document, error)
	SaveChunks(chunks []*Chunk) error
	GetChunksForDocument(docPath string) ([]*Chunk, error)
	GetStats() (*Stats, error)
	SearchBM25(queryStr string, limit int) ([]*SearchResult, error)
	SearchVector(queryVec []float32, limit int) ([]*SearchResult, error)
	SearchVectorFiltered(queryVec []float32, candidateIDs []int64, limit int) ([]*SearchResult, error)
}

// Compile-time check that *DB satisfies Store.
var _ Store = (*DB)(nil)

// Float32SliceToBytes converts a slice of float32 to a byte slice using standard bitwise math.
func Float32SliceToBytes(slice []float32) []byte {
	buf := make([]byte, len(slice)*4)
	for i, f := range slice {
		bits := math.Float32bits(f)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

// BytesToFloat32Slice converts a byte slice back to a float32 slice.
func BytesToFloat32Slice(buf []byte) []float32 {
	if len(buf)%4 != 0 {
		return nil
	}
	slice := make([]float32, len(buf)/4)
	for i := range slice {
		bits := uint32(buf[i*4]) |
			uint32(buf[i*4+1])<<8 |
			uint32(buf[i*4+2])<<16 |
			uint32(buf[i*4+3])<<24
		slice[i] = math.Float32frombits(bits)
	}
	return slice
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

	if _, err := conn.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}
	if _, err := conn.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}
	// Allow concurrent writers (issue #50) to wait for the WAL
	// write lock instead of failing immediately with SQLITE_BUSY.
	if _, err := conn.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
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

		// 2. Chunks Table (embedding is stored as BLOB for performance)
		`CREATE TABLE IF NOT EXISTS chunks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			uuid TEXT UNIQUE NOT NULL,
			document_path TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			content TEXT NOT NULL,
			embedding BLOB NOT NULL,
			hash TEXT NOT NULL,
			norm REAL DEFAULT 0,
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

	// Migration: add norm column for databases created before this field was introduced.
	// An error is expected if the column already exists; other errors are logged.
	if _, err := db.conn.Exec(`ALTER TABLE chunks ADD COLUMN norm REAL DEFAULT 0;`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			fmt.Fprintf(os.Stderr, "initSchema: ALTER TABLE chunks ADD COLUMN norm failed: %v\n", err)
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

	query := `INSERT INTO chunks (uuid, document_path, chunk_index, content, embedding, hash, norm)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		c.Norm = l2Norm(c.Embedding)
		embBytes := Float32SliceToBytes(c.Embedding)
		res, err := stmt.Exec(c.UUID, c.DocumentPath, c.ChunkIndex, c.Content, embBytes, c.Hash, c.Norm)
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
	query := "SELECT id, uuid, document_path, chunk_index, content, embedding, hash, norm FROM chunks WHERE document_path = ? ORDER BY chunk_index ASC"
	rows, err := db.conn.Query(query, docPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		var c Chunk
		var embBytes []byte
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embBytes, &c.Hash, &c.Norm); err != nil {
			return nil, err
		}
		c.Embedding = BytesToFloat32Slice(embBytes)
		chunks = append(chunks, &c)
	}
	return chunks, nil
}

// Stats returns database statistics.
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

// escapeFTS5Query escapes special characters in an FTS5 query string to
// prevent syntax errors. Special characters (", *, (, ), +, -, ., ~) are
// replaced with spaces. The resulting tokens are joined with "AND" so that
// multi-word queries and symbol-bearing terms (e.g. "C++", ".NET", "node.js")
// produce correct BM25 recall instead of being treated as exact phrases.
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

// SearchBM25 performs a keyword search on the FTS5 virtual table.
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

// SearchVector performs Cosine Similarity search over chunks.
//
// For unfiltered full-corpus scans we issue a single query that
// selects the embedding, norm, and the full chunk payload together
// (issue #49). This replaces the previous two-query approach
// (paginated scan for scoring followed by a second pass to fetch
// top-K details) which paid the cost of N round-trips even though
// only one was strictly needed for the score computation. The
// per-row payload is larger, but for the small-to-medium index
// sizes this tool targets (the CGO-free SQLite + linear scan trade-
// off is documented in ARCHITECTURE_PLAN.md) the single round-trip
// is a clear net win.
func (db *DB) SearchVector(queryVec []float32, limit int) ([]*SearchResult, error) {
	return db.searchVectorSinglePass(queryVec, nil, limit)
}

// SearchVectorFiltered is like SearchVector but only scans the chunks whose
// IDs are present in candidateIDs. It is the BM25-pre-filtered path used by
// SearchHybrid to keep the vector scan linear in the number of keyword
// candidates rather than the total chunk count. An empty candidateIDs slice
// triggers a full scan, matching the unfiltered behavior.
func (db *DB) SearchVectorFiltered(queryVec []float32, candidateIDs []int64, limit int) ([]*SearchResult, error) {
	return db.searchVectorSinglePass(queryVec, candidateIDs, limit)
}

// searchVectorSinglePass scores every chunk in one query and only
// retains the top-K full chunk payloads in memory. When candidateIDs
// is non-empty the WHERE clause restricts the scan to those chunk
// IDs, which keeps the BM25-pre-filtered path cheap without
// requiring a second round-trip for top-K details. A two-pass
// "score-then-fetch" variant is preserved in the benchmark file for
// direct head-to-head comparison; see BenchmarkSearchVector.
func (db *DB) searchVectorSinglePass(queryVec []float32, candidateIDs []int64, limit int) ([]*SearchResult, error) {
	if limit <= 0 {
		return nil, nil
	}

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
		if norm > 0 {
			score = CosineSimilarityWithNorm(queryVec, c.Embedding, norm)
		} else {
			score = CosineSimilarity(queryVec, c.Embedding)
		}

		// Maintain a sorted top-K window (highest score first);
		// everything below the window is discarded immediately so
		// we never hold a full result set for very large indexes.
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

// searchVectorSinglePassSelect is the unfiltered scoring query: it
// pulls every column the SearchResult needs in a single round-trip
// so the previous "score then fetch top-K details" two-query path
// is no longer required (issue #49).
const searchVectorSinglePassSelect = "SELECT id, uuid, document_path, chunk_index, content, embedding, hash, norm FROM chunks"

// buildFilteredSelect returns a SELECT statement whose IN-list has
// exactly n placeholders. The number of bound IDs at call time
// drives both the SQL and the argument list, so the driver never
// sees a placeholder/argument mismatch.
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

// appendSortedByScoreDesc inserts res into a descending CosineScore
// ordered list and returns the new slice. Caller is responsible for
// trimming to the desired window size before calling.
func appendSortedByScoreDesc(list []*SearchResult, res *SearchResult) []*SearchResult {
	pos := sort.Search(len(list), func(i int) bool {
		return list[i].CosineScore < res.CosineScore
	})
	list = append(list, nil)
	copy(list[pos+1:], list[pos:len(list)-1])
	list[pos] = res
	return list
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

// CosineSimilarityWithNorm computes cosine similarity when the L2 norm of vector b
// is already known, avoiding redundant norm calculations during search.
func CosineSimilarityWithNorm(a, b []float32, normB float32) float32 {
	if len(a) != len(b) || len(a) == 0 || normB == 0 {
		return 0
	}
	var dotProduct, normA float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
	}
	if normA == 0 {
		return 0
	}
	return float32(dotProduct / (math.Sqrt(normA) * float64(normB)))
}

// l2Norm computes the Euclidean (L2) norm of a float32 vector.
func l2Norm(v []float32) float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x * x)
	}
	return float32(math.Sqrt(sum))
}
