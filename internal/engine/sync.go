package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
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
func IndexDirectory(dbClient *db.DB, embedder Embedder, dirPath string) error {
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
		if _, err := IndexFile(dbClient, embedder, path); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
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

// IndexFile indexes a single file: parses, chunks, embeds, and saves to the database.
// It handles hash-based change detection and skips unchanged files.
func IndexFile(dbClient *db.DB, embedder Embedder, path string) (string, error) {
	currentHash, err := parser.GetFileHash(path)
	if err != nil {
		return "", fmt.Errorf("failed to compute hash for %s: %w", path, err)
	}

	existing, err := dbClient.GetDocument(path)
	if err != nil {
		return "", fmt.Errorf("failed to query document from DB: %w", err)
	}

	if existing != nil {
		if existing.Hash == currentHash {
			return currentHash, nil
		}
		if err := dbClient.DeleteDocument(path); err != nil {
			return "", fmt.Errorf("failed to delete old document version: %w", err)
		}
	}

	content, err := parser.ParseFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s: %w", path, err)
	}

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

	if err := dbClient.SaveDocument(&db.Document{
		Path:      path,
		Hash:      currentHash,
		UpdatedAt: time.Now(),
	}); err != nil {
		return "", fmt.Errorf("failed to save document metadata: %w", err)
	}

	if err := dbClient.SaveChunks(chunks); err != nil {
		return "", fmt.Errorf("failed to save chunks: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Indexed: %s (%d chunks)\n", path, len(chunks))
	return currentHash, nil
}

// WatchDirectory watches a directory for changes and re-indexes when files change.
// It uses fsnotify for efficient event-based watching instead of polling.
func WatchDirectory(ctx context.Context, dbClient *db.DB, embedder Embedder, dirPath string) error {
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	// Walk the directory and add all subdirectories to the watcher
	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			if name == "node_modules" || name == "dist" || name == "vendor" || name == "build" || name == "target" {
				return filepath.SkipDir
			}
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to setup watchers: %w", err)
	}

	// Perform initial sync
	fmt.Fprintf(os.Stderr, "Performing initial sync for: %s\n", absPath)
	if err := IndexDirectory(dbClient, embedder, absPath); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// Debounce timer to batch rapid events
	var debounceTimer *time.Timer
	const debounceDelay = 500 * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher event channel closed")
			}
			if event.Op&fsnotify.Write == fsnotify.Write ||
				event.Op&fsnotify.Create == fsnotify.Create ||
				event.Op&fsnotify.Remove == fsnotify.Remove ||
				event.Op&fsnotify.Rename == fsnotify.Rename {

				// If a new directory is created, add it to the watcher
				if event.Op&fsnotify.Create == fsnotify.Create {
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() {
						watcher.Add(event.Name)
					}
				}

				// Debounce: reset timer on each event
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDelay, func() {
					fmt.Fprintf(os.Stderr, "Change detected, re-indexing: %s\n", absPath)
					if err := IndexDirectory(dbClient, embedder, absPath); err != nil {
						fmt.Fprintf(os.Stderr, "Re-index error: %v\n", err)
					}
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher error channel closed")
			}
			fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)
		}
	}
}
