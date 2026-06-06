package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
)

// TestApplyIncrementalChangesTouchesOnlyChangedFiles is a regression
// test for issue #46.
func TestApplyIncrementalChangesTouchesOnlyChangedFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-inc-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	embedder := NewEmbeddingsGenerator()

	docsDir := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("failed to create docs dir: %v", err)
	}
	untouched := filepath.Join(docsDir, "untouched.md")
	if err := os.WriteFile(untouched, []byte("untouched original content"), 0644); err != nil {
		t.Fatalf("failed to write untouched.md: %v", err)
	}
	changed := filepath.Join(docsDir, "changed.md")
	if err := os.WriteFile(changed, []byte("changed original content"), 0644); err != nil {
		t.Fatalf("failed to write changed.md: %v", err)
	}

	if err := IndexDirectory(dbClient, embedder, docsDir); err != nil {
		t.Fatalf("initial IndexDirectory failed: %v", err)
	}

	untouchedDoc, err := dbClient.GetDocument(untouched)
	if err != nil || untouchedDoc == nil {
		t.Fatalf("expected untouched.md to be indexed before edit, err=%v doc=%v", err, untouchedDoc)
	}
	originalUntouchedHash := untouchedDoc.Hash

	if err := os.WriteFile(changed, []byte("changed updated content"), 0644); err != nil {
		t.Fatalf("failed to update changed.md: %v", err)
	}

	changes := map[string]struct{}{changed: {}}
	if err := applyIncrementalChanges(dbClient, embedder, docsDir, changes); err != nil {
		t.Fatalf("applyIncrementalChanges failed: %v", err)
	}

	untouchedAfter, err := dbClient.GetDocument(untouched)
	if err != nil || untouchedAfter == nil {
		t.Fatalf("untouched.md missing after incremental change: err=%v doc=%v", err, untouchedAfter)
	}
	if untouchedAfter.Hash != originalUntouchedHash {
		t.Errorf("incremental change touched the unrelated file: hash %q -> %q",
			originalUntouchedHash, untouchedAfter.Hash)
	}

	changedAfter, err := dbClient.GetDocument(changed)
	if err != nil || changedAfter == nil {
		t.Fatalf("changed.md missing after incremental change: err=%v doc=%v", err, changedAfter)
	}
	if changedAfter.Hash == originalUntouchedHash {
		t.Errorf("expected changed.md to be re-indexed with a new hash")
	}
}

// TestApplyIncrementalChangesDropsMissingFiles is a regression test
// for issue #46.
func TestApplyIncrementalChangesDropsMissingFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-inc-orphan-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	embedder := NewEmbeddingsGenerator()

	docsDir := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("failed to create docs dir: %v", err)
	}
	doomed := filepath.Join(docsDir, "doomed.md")
	if err := os.WriteFile(doomed, []byte("doomed content"), 0644); err != nil {
		t.Fatalf("failed to write doomed.md: %v", err)
	}
	if err := IndexDirectory(dbClient, embedder, docsDir); err != nil {
		t.Fatalf("IndexDirectory failed: %v", err)
	}

	if doc, err := dbClient.GetDocument(doomed); err != nil || doc == nil {
		t.Fatalf("expected doomed.md to be indexed: err=%v doc=%v", err, doc)
	}

	if err := os.Remove(doomed); err != nil {
		t.Fatalf("failed to delete doomed.md: %v", err)
	}
	changes := map[string]struct{}{doomed: {}}
	if err := applyIncrementalChanges(dbClient, embedder, docsDir, changes); err != nil {
		t.Fatalf("applyIncrementalChanges failed: %v", err)
	}

	if doc, err := dbClient.GetDocument(doomed); err != nil {
		t.Fatalf("GetDocument after delete: %v", err)
	} else if doc != nil {
		t.Errorf("expected doomed.md to be removed from index, still present: %+v", doc)
	}
}

func TestIndexDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-sync-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	embedder := NewEmbeddingsGenerator()

	// 1. Create a dummy folder with markdown files
	docsDir := filepath.Join(tempDir, "docs")
	err = os.MkdirAll(docsDir, 0755)
	if err != nil {
		t.Fatalf("failed to create docs dir: %v", err)
	}

	file1 := filepath.Join(docsDir, "first.md")
	err = os.WriteFile(file1, []byte("# First Document\nThis is the content of the first file."), 0644)
	if err != nil {
		t.Fatalf("failed to write first.md: %v", err)
	}

	file2 := filepath.Join(docsDir, "second.txt")
	err = os.WriteFile(file2, []byte("Second document contains simple plain text data."), 0644)
	if err != nil {
		t.Fatalf("failed to write second.txt: %v", err)
	}

	// 2. Perform index
	err = IndexDirectory(dbClient, embedder, docsDir)
	if err != nil {
		t.Fatalf("IndexDirectory failed: %v", err)
	}

	stats, err := dbClient.GetStats()
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.DocumentCount != 2 {
		t.Errorf("expected 2 documents in index, got %d", stats.DocumentCount)
	}

	// 3. Test update (incremental change)
	time.Sleep(10 * time.Millisecond) // Ensure file modification times would update if checked
	err = os.WriteFile(file1, []byte("# First Document\nThis is updated content of the first file with extra information."), 0644)
	if err != nil {
		t.Fatalf("failed to update first.md: %v", err)
	}

	err = IndexDirectory(dbClient, embedder, docsDir)
	if err != nil {
		t.Fatalf("re-index failed: %v", err)
	}

	stats2, _ := dbClient.GetStats()
	if stats2.DocumentCount != 2 {
		t.Errorf("expected 2 documents in index after update, got %d", stats2.DocumentCount)
	}

	// Verify update content by searching
	res, err := SearchHybrid(dbClient, embedder, "extra information", 10)
	if err != nil {
		t.Fatalf("hybrid search failed: %v", err)
	}
	if len(res) == 0 || res[0].Chunk.DocumentPath != file1 {
		t.Errorf("expected updated content to be searchable, got: %v", res)
	}

	// 4. Test orphan detection (deleting file on disk)
	err = os.Remove(file2)
	if err != nil {
		t.Fatalf("failed to delete file2: %v", err)
	}

	err = IndexDirectory(dbClient, embedder, docsDir)
	if err != nil {
		t.Fatalf("index after deletion failed: %v", err)
	}

	stats3, _ := dbClient.GetStats()
	if stats3.DocumentCount != 1 {
		t.Errorf("expected 1 document after orphan cleanup, got %d", stats3.DocumentCount)
	}
}
