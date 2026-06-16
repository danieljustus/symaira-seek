package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
}

type SearchResult struct {
	Chunk       *Chunk  `json:"chunk"`
	BM25Rank    int     `json:"bm25_rank"`
	VectorRank  int     `json:"vector_rank"`
	RRFScore    float32 `json:"rrf_score"`
	CosineScore float32 `json:"cosine_score"`
}

type DB struct {
	conn *sql.DB
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
	SearchVectorFiltered(queryVec []float32, candidateIDs []int64, limit int) ([]*SearchResult, error)
}

var _ Store = (*DB)(nil)

func Open() (*DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	dir := filepath.Join(home, ".local", "share", "symaira-seek")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	dbPath := filepath.Join(dir, "symseek.db")
	conn, err := sqlitekit.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	if err := RunMigrations(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
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
