package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/parser"
)

// isWithinDir reports whether path is dir itself or located inside dir.
// It uses a trailing path separator to avoid false matches where one
// directory name is a string prefix of another (e.g. /docs vs /docs2).
func isWithinDir(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(os.PathSeparator))
}

func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") && name != "." && name != ".." {
		return true
	}
	switch name {
	case "node_modules", "dist", "vendor", "build", "target":
		return true
	}
	return false
}

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
func IndexDirectory(dbClient db.Store, embedder Embedder, dirPath string) error {
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return userFriendlyError(err, "failed to get absolute path",
			"Check that the path is valid and try again")
	}

	// Verify target path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		return userFriendlyError(err, "cannot access directory",
			"Check file permissions and ensure the directory exists")
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
			if shouldSkipDir(d.Name()) {
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

	processFilesInParallel(dbClient, embedder, foundPaths)

	// 4. Orphan detection: delete DB documents that no longer exist on disk
	for _, doc := range existingDocs {
		if isWithinDir(doc.Path, absPath) && !foundPaths[doc.Path] {
			err = dbClient.DeleteDocument(doc.Path)
			if err != nil {
				return fmt.Errorf("failed to delete orphaned document %s: %w", doc.Path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed deleted file from index: %s\n", doc.Path)
		}
	}

	return nil
}

// IndexFile indexes a single file by delegating to the shared prepareIndex/commitIndex pipeline.
func IndexFile(dbClient db.Store, embedder Embedder, path string) (string, error) {
	chunks, doc, skipped, err := prepareIndex(dbClient, embedder, path)
	if err != nil {
		return "", err
	}
	if skipped {
		currentHash, _ := parser.GetFileHash(path)
		return currentHash, nil
	}
	if err := commitIndex(dbClient, path, chunks, doc); err != nil {
		return "", err
	}
	return doc.Hash, nil
}

// WatchDirectory watches a directory for changes and re-indexes when files change.
// It uses fsnotify for efficient event-based watching instead of polling.
func WatchDirectory(ctx context.Context, dbClient db.Store, embedder Embedder, dirPath string) error {
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return userFriendlyError(err, "failed to get absolute path",
			"Check that the path is valid and try again")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return userFriendlyError(err, "failed to create file watcher",
			"Ensure you have the necessary permissions to watch the directory")
	}
	defer watcher.Close()

	// Walk the directory and add all subdirectories to the watcher
	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
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

	// Count files being watched from database
	existingDocs, err := dbClient.ListDocuments()
	if err != nil {
		return fmt.Errorf("failed to list documents: %w", err)
	}
	fileCount := 0
	for _, doc := range existingDocs {
		if isWithinDir(doc.Path, absPath) {
			fileCount++
		}
	}
	fmt.Fprintf(os.Stderr, "Watching %d files in %s\n", fileCount, absPath)

	// Periodic status ticker
	statusTicker := time.NewTicker(30 * time.Second)
	defer statusTicker.Stop()
	lastChangeTime := time.Now()

	// Debounce timer to batch rapid events. The debounce window collects
	// all changed paths so a single file edit only re-indexes that file
	// (issue #46) instead of forcing a full directory crawl.
	var debounceTimer *time.Timer
	const debounceDelay = 500 * time.Millisecond
	pendingChanges := make(map[string]struct{})
	var pendingMu sync.Mutex

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-statusTicker.C:
			fmt.Fprintf(os.Stderr, "Watching %d files, last change at %s\n",
				fileCount, lastChangeTime.Format("15:04:05"))

		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher event channel closed")
			}
			if event.Op&fsnotify.Write == fsnotify.Write ||
				event.Op&fsnotify.Create == fsnotify.Create ||
				event.Op&fsnotify.Remove == fsnotify.Remove ||
				event.Op&fsnotify.Rename == fsnotify.Rename {

				if event.Op&fsnotify.Create == fsnotify.Create {
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() && !shouldSkipDir(info.Name()) {
						watcher.Add(event.Name)
					}
				}

				pendingMu.Lock()
				pendingChanges[event.Name] = struct{}{}
				pendingMu.Unlock()

				lastChangeTime = time.Now()

				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDelay, func() {
					pendingMu.Lock()
					changed := make(map[string]struct{}, len(pendingChanges))
					for p := range pendingChanges {
						changed[p] = struct{}{}
					}
					pendingChanges = make(map[string]struct{})
					pendingMu.Unlock()

					if err := applyIncrementalChanges(dbClient, embedder, absPath, changed); err != nil {
						fmt.Fprintf(os.Stderr, "Incremental re-index error: %v\n", err)
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

// applyIncrementalChanges re-indexes each changed path and drops
// documents whose backing file no longer exists.
func applyIncrementalChanges(dbClient db.Store, embedder Embedder, absPath string, changed map[string]struct{}) error {
	indexed := 0
	removed := 0
	for path := range changed {
		if !isWithinDir(path, absPath) {
			continue
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if delErr := dbClient.DeleteDocument(path); delErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to delete %s: %v\n", path, delErr)
				} else {
					removed++
				}
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: stat %s: %v\n", path, err)
			continue
		}

		if info.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExtensions[ext] {
			continue
		}

		if _, err := IndexFile(dbClient, embedder, path); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
		indexed++
	}

	// A Rename event that moves a tracked file out of the watched
	// tree does not produce a second event for the original path, so
	// sweep documents under absPath and drop any whose backing file
	// has disappeared. This matches IndexDirectory's orphan behavior
	// without forcing a full directory walk.
	existingDocs, err := dbClient.ListDocuments()
	if err != nil {
		return fmt.Errorf("failed listing existing documents: %w", err)
	}
	for _, doc := range existingDocs {
		if !isWithinDir(doc.Path, absPath) {
			continue
		}
		if _, statErr := os.Stat(doc.Path); statErr == nil {
			continue
		}
		if delErr := dbClient.DeleteDocument(doc.Path); delErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to drop orphan %s: %v\n", doc.Path, delErr)
			continue
		}
		removed++
	}

	if indexed > 0 || removed > 0 {
		fmt.Fprintf(os.Stderr, "Watch: indexed=%d removed=%d\n", indexed, removed)
	}
	return nil
}

var indexParallelism = func() int {
	n := runtime.NumCPU()
	if n < 2 {
		return 2
	}
	if n > 8 {
		return 8
	}
	return n
}()

func processFilesInParallel(dbClient db.Store, embedder Embedder, paths map[string]bool) {
	if len(paths) == 0 {
		return
	}

	workers := indexParallelism
	if workers > len(paths) {
		workers = len(paths)
	}

	type result struct {
		path    string
		chunks  []*db.Chunk
		doc     *db.Document
		err     error
		skipped bool
	}

	jobs := make(chan string, len(paths))
	prepared := make(chan result, len(paths))

	for p := range paths {
		jobs <- p
	}
	close(jobs)

	var prepWG sync.WaitGroup
	for w := 0; w < workers; w++ {
		prepWG.Add(1)
		go func() {
			defer prepWG.Done()
			for path := range jobs {
				chunks, doc, skipped, err := prepareIndex(dbClient, embedder, path)
				prepared <- result{path: path, chunks: chunks, doc: doc, err: err, skipped: skipped}
			}
		}()
	}

	go func() {
		prepWG.Wait()
		close(prepared)
	}()

	for r := range prepared {
		if r.skipped {
			continue
		}
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", r.err)
			continue
		}
		if err := commitIndex(dbClient, r.path, r.chunks, r.doc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			continue
		}
	}
}

func prepareIndex(dbClient db.Store, embedder Embedder, path string) ([]*db.Chunk, *db.Document, bool, error) {
	currentHash, err := parser.GetFileHash(path)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to compute hash for %s: %w", path, err)
	}

	existing, err := dbClient.GetDocument(path)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to query document from DB: %w", err)
	}
	if existing != nil && existing.Hash == currentHash {
		return nil, nil, true, nil
	}

	content, err := parser.ParseFile(path)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	textChunks := parser.SplitText(content, 1000, 200)
	embeddings := embedder.GenerateVectors(textChunks)

	chunks := make([]*db.Chunk, 0, len(textChunks))
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

	return chunks, &db.Document{
		Path:      path,
		Hash:      currentHash,
		UpdatedAt: time.Now(),
	}, false, nil
}

func commitIndex(dbClient db.Store, path string, chunks []*db.Chunk, doc *db.Document) error {
	existing, err := dbClient.GetDocument(path)
	if err != nil {
		return fmt.Errorf("failed to query document from DB: %w", err)
	}
	if existing != nil {
		if existing.Hash == doc.Hash {
			return nil
		}
		if err := dbClient.DeleteDocument(path); err != nil {
			return fmt.Errorf("failed to delete old document version: %w", err)
		}
	}

	if err := dbClient.SaveDocument(doc); err != nil {
		return fmt.Errorf("failed to save document metadata: %w", err)
	}
	if err := dbClient.SaveChunks(chunks); err != nil {
		return fmt.Errorf("failed to save chunks: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Indexed: %s (%d chunks)\n", path, len(chunks))
	return nil
}
