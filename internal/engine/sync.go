package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/parser"
)

var supportedExtensions = map[string]bool{
	".md":   true,
	".txt":  true,
	".go":   true,
	".py":   true,
	".js":   true,
	".ts":   true,
	".json": true,
	".yaml": true,
	".yml":  true,
	".sh":   true,
	".html": true,
	".css":  true,
}

// IndexDirectory crawls a directory, computes hashes, parses changed files,
// generates embeddings, saves to DB, and deletes orphan documents.
func IndexDirectory(dbClient *db.DB, embedder *EmbeddingsGenerator, dirPath string) error {
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Verify target path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("target path error: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("target path is not a directory: %s", absPath)
	}

	// 1. Scan directory for valid files
	foundPaths := make(map[string]bool)
	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			// Skip hidden and build/dependency folders
			if strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			if name == "node_modules" || name == "dist" || name == "vendor" || name == "build" || name == "target" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if supportedExtensions[ext] {
			foundPaths[path] = true
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed walking directory: %w", err)
	}

	// 2. Fetch existing documents from DB
	existingDocs, err := dbClient.ListDocuments()
	if err != nil {
		return fmt.Errorf("failed listing existing documents: %w", err)
	}

	// 3. Process new and changed files
	for path := range foundPaths {
		currentHash, err := parser.GetFileHash(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to compute hash for %s: %v\n", path, err)
			continue
		}

		existing, err := dbClient.GetDocument(path)
		if err != nil {
			return fmt.Errorf("failed to query document from DB: %w", err)
		}

		if existing != nil {
			if existing.Hash == currentHash {
				// No change, skip embedding/re-parsing
				continue
			}
			// Content changed: clean up old DB state first
			err = dbClient.DeleteDocument(path)
			if err != nil {
				return fmt.Errorf("failed to delete old document version: %w", err)
			}
		}

		// Parse the file
		content, err := parser.ParseFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse %s: %v\n", path, err)
			continue
		}

		// Split file into chunks
		// Standard: 1000 characters chunk size, 200 characters overlap
		textChunks := parser.SplitText(content, 1000, 200)

		embeddings := embedder.GenerateVectors(textChunks)

		var chunks []*db.Chunk
		for idx, tc := range textChunks {
			hashSum := sha256.Sum256([]byte(tc))
			chunkHash := hex.EncodeToString(hashSum[:])

			chunks = append(chunks, &db.Chunk{
				UUID:         uuid.New().String(),
				DocumentPath: path,
				ChunkIndex:   idx,
				Content:      tc,
				Embedding:    embeddings[idx],
				Hash:         chunkHash,
			})
		}

		// Save document metadata
		err = dbClient.SaveDocument(&db.Document{
			Path:      path,
			Hash:      currentHash,
			UpdatedAt: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to save document metadata: %w", err)
		}

		// Save document chunks
		err = dbClient.SaveChunks(chunks)
		if err != nil {
			return fmt.Errorf("failed to save chunks: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Indexed: %s (%d chunks)\n", path, len(chunks))
	}

	// 4. Orphan detection: delete DB documents that no longer exist on disk
	for _, doc := range existingDocs {
		if strings.HasPrefix(doc.Path, absPath) && !foundPaths[doc.Path] {
			err = dbClient.DeleteDocument(doc.Path)
			if err != nil {
				return fmt.Errorf("failed to delete orphaned document %s: %w", doc.Path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed deleted file from index: %s\n", doc.Path)
		}
	}

	return nil
}
