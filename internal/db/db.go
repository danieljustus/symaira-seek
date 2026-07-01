package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danieljustus/symaira-corekit/sqlitekit"
	_ "modernc.org/sqlite"
)

type Document struct {
	Path      string    `json:"path"`
	Hash      string    `json:"hash"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Chunk struct {
	ID           int64     `json:"id"`
	UUID         string    `json:"uuid"`
	DocumentPath string    `json:"document_path"`
	ChunkIndex   int       `json:"chunk_index"`
	Content      string    `json:"content"`
	Embedding    []float32 `json:"embedding"`
	Hash         string    `json:"hash"`
	Norm         float32   `json:"norm"`
	Dim          int       `json:"dim"`
	Model        string    `json:"embedding_model"`
}

type SearchResult struct {
	Chunk       *Chunk  `json:"chunk"`
	BM25Rank    int     `json:"bm25_rank"`
	VectorRank  int     `json:"vector_rank"`
	RRFScore    float32 `json:"rrf_score"`
	CosineScore float32 `json:"cosine_score"`
}

type DB struct {
	conn        *sql.DB
	vectorIndex *VectorIndex
	generation  int64
	quantConfig *QuantConfig
}

// QuantConfig holds opt-in parameters for TurboQuant quantized vector search.
type QuantConfig struct {
	Enabled      bool
	BitWidth     int
	Shortlist    int
	ExactRerank  bool
	Seed         int
}

// SetQuantConfig enables or reconfigures quantized search on this DB handle.
// A nil or disabled config falls back to the standard search path.
func (db *DB) SetQuantConfig(cfg *QuantConfig) {
	if cfg != nil && cfg.BitWidth == 0 {
		cfg.BitWidth = 4
	}
	if cfg != nil && cfg.Shortlist <= 0 {
		cfg.Shortlist = 200
	}
	db.quantConfig = cfg
}

// QuantConfig returns the current quantization configuration, or nil if disabled.
func (db *DB) GetQuantConfig() *QuantConfig {
	return db.quantConfig
}

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
	DetectMixedEmbeddingSpaces() (map[string]int, error)
	SetFolderContext(path, text string) error
	GetFolderContexts() ([]FolderContext, error)
	GetMatchingContext(path string) (*FolderContext, error)
}

var _ Store = (*DB)(nil)

func Open() (*DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	dir := filepath.Join(home, ".local", "share", "symaira-seek")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	dbPath := filepath.Join(dir, "symseek.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600); err == nil {
			f.Close()
		}
	}
	conn, err := sqlitekit.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if err := RunMigrations(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	db := &DB{conn: conn}
	db.generation = db.loadGeneration()
	db.vectorIndex = db.loadVectorIndex()
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

// loadGeneration reads the current index generation from index_meta.
func (db *DB) loadGeneration() int64 {
	var gen int64
	_ = db.conn.QueryRow("SELECT value FROM index_meta WHERE key = 'generation'").Scan(&gen)
	return gen
}

// bumpGeneration atomically increments the stored generation and updates the
// in-memory copy.  Any other process reading the same database will observe
// the new value on its next vector query.
func (db *DB) bumpGeneration() {
	_, err := db.conn.Exec("UPDATE index_meta SET value = value + 1 WHERE key = 'generation'")
	if err == nil {
		db.generation = db.loadGeneration()
	}
}

// checkGeneration invalidates the in-memory IVF index when another process
// has written to the database.  It is called before serving a vector query.
func (db *DB) checkGeneration() {
	current := db.loadGeneration()
	if current != db.generation {
		db.generation = current
		db.vectorIndex = nil
	}
}

// rebuildVectorIndex reconstructs the in-memory IVF index from the current
// chunks table and persists the result.
func (db *DB) rebuildVectorIndex() {
	rows, err := db.conn.Query("SELECT id, embedding FROM chunks")
	if err != nil {
		db.vectorIndex = nil
		return
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		var c Chunk
		var embBytes []byte
		if err := rows.Scan(&c.ID, &embBytes); err != nil {
			db.vectorIndex = nil
			return
		}
		c.Embedding = BytesToFloat32Slice(embBytes)
		chunks = append(chunks, &c)
	}
	if err := rows.Err(); err != nil {
		db.vectorIndex = nil
		return
	}

	if db.vectorIndex == nil {
		db.vectorIndex = NewVectorIndex()
	}
	db.vectorIndex.Rebuild(chunks)
	db.saveVectorIndex()
}

// saveVectorIndex serializes the current index into index_storage keyed by
// the current generation.
func (db *DB) saveVectorIndex() {
	if db.vectorIndex == nil || !db.vectorIndex.IsReady() {
		_, _ = db.conn.Exec("DELETE FROM index_storage WHERE key = 'ivf'")
		return
	}
	data, err := db.vectorIndex.Serialize(db.generation)
	if err != nil {
		return
	}
	_, _ = db.conn.Exec("INSERT INTO index_storage (key, data) VALUES ('ivf', ?) ON CONFLICT(key) DO UPDATE SET data = excluded.data", data)
}

// loadVectorIndex attempts to restore a persisted IVF index.  It returns nil
// when no snapshot exists or the snapshot is stale (generation/chunk-count
// mismatch).
func (db *DB) loadVectorIndex() *VectorIndex {
	var data []byte
	err := db.conn.QueryRow("SELECT data FROM index_storage WHERE key = 'ivf'").Scan(&data)
	if err != nil {
		return nil
	}

	idx, storedGen, err := DeserializeIndex(data)
	if err != nil {
		_, _ = db.conn.Exec("DELETE FROM index_storage WHERE key = 'ivf'")
		return nil
	}

	if storedGen != db.generation {
		_, _ = db.conn.Exec("DELETE FROM index_storage WHERE key = 'ivf'")
		return nil
	}

	var chunkCount int
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&chunkCount); err != nil {
		return nil
	}
	if idx.TotalChunks() != chunkCount {
		_, _ = db.conn.Exec("DELETE FROM index_storage WHERE key = 'ivf'")
		return nil
	}

	return idx
}

func (db *DB) SaveDocument(doc *Document) error {
	query := `INSERT INTO documents (path, hash, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			hash=excluded.hash,
			updated_at=excluded.updated_at`
	_, err := db.conn.Exec(query, doc.Path, doc.Hash, doc.UpdatedAt)
	return err
}

func (db *DB) DeleteDocument(path string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// If an IVF index is warm, remove the affected chunk IDs from it before
	// deleting the rows.  This keeps the index current without forcing a full
	// rebuild on the next query.
	var chunkIDs []int64
	if db.vectorIndex != nil && db.vectorIndex.IsReady() {
		rows, err := tx.Query("SELECT id FROM chunks WHERE document_path = ?", path)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			chunkIDs = append(chunkIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}

	_, err = tx.Exec("DELETE FROM chunks WHERE document_path = ?", path)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM documents WHERE path = ?", path)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if db.vectorIndex != nil && db.vectorIndex.IsReady() {
		for _, id := range chunkIDs {
			db.vectorIndex.RemoveChunk(id)
		}
		if db.vectorIndex.NeedsRebuild() {
			db.rebuildVectorIndex()
		}
	}
	db.bumpGeneration()
	return nil
}

func (db *DB) GetDocument(path string) (*Document, error) {
	var doc Document
	err := db.conn.QueryRow("SELECT path, hash, updated_at FROM documents WHERE path = ?", path).
		Scan(&doc.Path, &doc.Hash, &doc.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &doc, err
}

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

func (db *DB) SaveChunks(chunks []*Chunk) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := `INSERT INTO chunks (uuid, document_path, chunk_index, content, embedding, hash, norm, binary_signature, embedding_dim, embedding_model)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		c.Norm = l2Norm(c.Embedding)
		embBytes := Float32SliceToBytes(c.Embedding)
		sigBytes := SignBinarySignature(c.Embedding)
		res, err := stmt.Exec(c.UUID, c.DocumentPath, c.ChunkIndex, c.Content, embBytes, c.Hash, c.Norm, sigBytes, c.Dim, c.Model)
		if err != nil {
			return fmt.Errorf("failed to insert chunk: %w", err)
		}

		id, err := res.LastInsertId()
		if err == nil {
			c.ID = id
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Keep the IVF index warm by adding the new chunks incrementally.  If the
	// index has never been built, leave it nil so the next search constructs it
	// lazily from the full chunks table.
	if db.vectorIndex != nil && db.vectorIndex.IsReady() {
		db.vectorIndex.AddChunks(chunks)
		if db.vectorIndex.NeedsRebuild() {
			db.rebuildVectorIndex()
		}
	}
	db.bumpGeneration()
	return nil
}

func (db *DB) GetChunksForDocument(docPath string) ([]*Chunk, error) {
	query := "SELECT id, uuid, document_path, chunk_index, content, embedding, hash, norm, embedding_dim, embedding_model FROM chunks WHERE document_path = ? ORDER BY chunk_index ASC"
	rows, err := db.conn.Query(query, docPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		var c Chunk
		var embBytes []byte
		if err := rows.Scan(&c.ID, &c.UUID, &c.DocumentPath, &c.ChunkIndex, &c.Content, &embBytes, &c.Hash, &c.Norm, &c.Dim, &c.Model); err != nil {
			return nil, err
		}
		c.Embedding = BytesToFloat32Slice(embBytes)
		chunks = append(chunks, &c)
	}
	return chunks, nil
}

// FolderContext stores a path prefix and its descriptive context text.
type FolderContext struct {
	PathPrefix  string `json:"path_prefix"`
	ContextText string `json:"context_text"`
}

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

// DetectMixedEmbeddingSpaces returns the distinct (dim, model) combinations
// present in the chunks table and their row counts.  NULL values in
// embedding_dim or embedding_model are treated as a distinct group keyed by
// "unknown/<model-or-unknown>" so that legacy rows never cause a scan error.
func (db *DB) DetectMixedEmbeddingSpaces() (map[string]int, error) {
	rows, err := db.conn.Query(
		"SELECT embedding_dim, embedding_model, COUNT(*) FROM chunks GROUP BY embedding_dim, embedding_model",
	)
	if err != nil {
		return nil, fmt.Errorf("detect mixed embedding spaces: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var dim sql.NullInt64
		var model sql.NullString
		var count int
		if err := rows.Scan(&dim, &model, &count); err != nil {
			return nil, fmt.Errorf("detect mixed embedding spaces: %w", err)
		}
		dimStr := "unknown"
		if dim.Valid {
			dimStr = fmt.Sprintf("%d", dim.Int64)
		}
		modelStr := "unknown"
		if model.Valid && model.String != "" {
			modelStr = model.String
		}
		key := fmt.Sprintf("%s/%s", dimStr, modelStr)
		result[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("detect mixed embedding spaces: %w", err)
	}
	return result, nil
}

func (db *DB) SetFolderContext(path, text string) error {
	_, err := db.conn.Exec(
		`INSERT INTO folder_contexts (path_prefix, context_text) VALUES (?, ?)
		 ON CONFLICT(path_prefix) DO UPDATE SET context_text = excluded.context_text`,
		path, text,
	)
	return err
}

func (db *DB) GetFolderContexts() ([]FolderContext, error) {
	rows, err := db.conn.Query("SELECT path_prefix, context_text FROM folder_contexts ORDER BY path_prefix")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contexts []FolderContext
	for rows.Next() {
		var fc FolderContext
		if err := rows.Scan(&fc.PathPrefix, &fc.ContextText); err != nil {
			return nil, err
		}
		contexts = append(contexts, fc)
	}
	return contexts, rows.Err()
}

// GetMatchingContext returns the context whose path_prefix is the longest
// prefix of path. Returns nil when no prefix matches.
func (db *DB) GetMatchingContext(path string) (*FolderContext, error) {
	rows, err := db.conn.Query("SELECT path_prefix, context_text FROM folder_contexts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var best *FolderContext
	bestLen := 0
	for rows.Next() {
		var fc FolderContext
		if err := rows.Scan(&fc.PathPrefix, &fc.ContextText); err != nil {
			return nil, err
		}
		if strings.HasPrefix(path, fc.PathPrefix) && len(fc.PathPrefix) > bestLen {
			best = &fc
			bestLen = len(fc.PathPrefix)
		}
	}
	return best, rows.Err()
}
