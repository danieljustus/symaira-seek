package db

import "context"

// VectorStore is the pluggable backend interface for vector operations.
// The default implementation is the SQLite/IVF path provided by *DB.
// Future backends (HNSW, Qdrant, etc.) can implement this interface
// without changing the engine layer.
type VectorStore interface {
	// Upsert inserts or replaces chunks (including their embeddings).
	Upsert(ctx context.Context, chunks []*Chunk) error
	// Delete removes all chunks belonging to the given document path.
	Delete(ctx context.Context, docPath string) error
	// Search returns the most similar chunks for the query vector.
	Search(ctx context.Context, queryVec []float32, limit int) ([]*SearchResult, error)
}

// Compile-time check that *DB satisfies VectorStore.
var _ VectorStore = (*DB)(nil)

// Upsert delegates to SaveChunks, satisfying VectorStore.
func (db *DB) Upsert(ctx context.Context, chunks []*Chunk) error {
	return db.SaveChunks(chunks)
}

// Delete delegates to DeleteDocument, satisfying VectorStore.
func (db *DB) Delete(ctx context.Context, docPath string) error {
	return db.DeleteDocument(docPath)
}

// Search delegates to SearchVector, satisfying VectorStore.
func (db *DB) Search(ctx context.Context, queryVec []float32, limit int) ([]*SearchResult, error) {
	return db.SearchVector(queryVec, limit)
}
