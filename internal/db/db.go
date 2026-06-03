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
		embBytes := Float32SliceToBytes(c.Embedding)
		res, err := stmt.Exec(c.UUID, c.DocumentPath, c.ChunkIndex, c.Content, embBytes, c.Hash)
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
		var embBytes []byte
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embBytes, &c.Hash); err != nil {
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
// prevent syntax errors. Special characters (\", *, (, ), +, -) are
// replaced with spaces. The entire query is then wrapped in double quotes
// to treat it as a phrase, which avoids column-filter syntax issues.
func escapeFTS5Query(query string) string {
	replacer := strings.NewReplacer(
		"\"", " ",
		"*", " ",
		"(", " ",
		")", " ",
		"+", " ",
		"-", " ",
	)
	cleaned := replacer.Replace(query)
	return "\"" + cleaned + "\""
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
// This function uses a full-table scan with batched pagination to avoid
// loading all embeddings into memory at once. An approximate nearest-neighbor
// (ANN) index is not used here to keep the database layer CGO-free and
// dependency-light. For small to medium index sizes this linear scan is fast
// enough; users with very large indexes can pre-filter results via BM25
// before running vector search.
func (db *DB) SearchVector(queryVec []float32, limit int) ([]*SearchResult, error) {
	const vectorBatchSize = 500
	var topK []*scoredEntry

	offset := 0
	for {
		rows, err := db.conn.Query(
			"SELECT id, embedding FROM chunks ORDER BY id LIMIT ? OFFSET ?",
			vectorBatchSize, offset,
		)
		if err != nil {
			return nil, err
		}

		batchCount := 0
		for rows.Next() {
			var id int64
			var embBytes []byte
			if err := rows.Scan(&id, &embBytes); err != nil {
				rows.Close()
				return nil, err
			}

			vec := BytesToFloat32Slice(embBytes)
			score := CosineSimilarity(queryVec, vec)
			batchCount++

			// Insert into topK maintaining sorted order
			topK = insertSorted(topK, &scoredEntry{id: id, score: score}, limit)
		}
		rows.Close()

		if batchCount < vectorBatchSize {
			break // Last page
		}
		offset += vectorBatchSize
	}

	if len(topK) == 0 {
		return nil, nil
	}

	// Collect the top K IDs and mapping maps
	scoreMap := make(map[int64]float32)
	idOrderMap := make(map[int64]int)
	topIDs := make([]interface{}, len(topK))
	for i, s := range topK {
		topIDs[i] = s.id
		scoreMap[s.id] = s.score
		idOrderMap[s.id] = i
	}

	// Query details for ONLY the top K chunk IDs
	placeholders := make([]string, len(topK))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	query := fmt.Sprintf(
		"SELECT id, uuid, document_path, chunk_index, content, embedding, hash FROM chunks WHERE id IN (%s)",
		strings.Join(placeholders, ","),
	)

	detailRows, err := db.conn.Query(query, topIDs...)
	if err != nil {
		return nil, err
	}
	defer detailRows.Close()

	unsortedResults := make([]*SearchResult, 0, len(topK))
	for detailRows.Next() {
		var c Chunk
		var embBytes []byte
		if err := detailRows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embBytes, &c.Hash); err != nil {
			return nil, err
		}
		c.Embedding = BytesToFloat32Slice(embBytes)

		unsortedResults = append(unsortedResults, &SearchResult{
			Chunk:       &c,
			VectorRank:  0,
			CosineScore: scoreMap[c.ID],
		})
	}

	// Re-sort results back into correct similarity score order
	sort.Slice(unsortedResults, func(i, j int) bool {
		return idOrderMap[unsortedResults[i].Chunk.ID] < idOrderMap[unsortedResults[j].Chunk.ID]
	})

	// Set rank values
	for i, res := range unsortedResults {
		res.VectorRank = i + 1
	}

	return unsortedResults, nil
}

// scoredEntry holds an intermediate cosine similarity result.
type scoredEntry struct {
	id    int64
	score float32
}

// insertSorted maintains a descending-score top-K list of at most maxLen entries.
func insertSorted(list []*scoredEntry, item *scoredEntry, maxLen int) []*scoredEntry {
	pos := sort.Search(len(list), func(i int) bool {
		return list[i].score < item.score
	})

	if len(list) < maxLen {
		// Expand
		list = append(list, nil)
		copy(list[pos+1:], list[pos:len(list)-1])
		list[pos] = item
	} else if pos < maxLen {
		// Shift and drop last
		copy(list[pos+1:], list[pos:maxLen-1])
		list[pos] = item
	}
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
