package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
)

type countingStore struct {
	db.Store
	getDocCalls int
}

func (c *countingStore) GetDocument(path string) (*db.Document, error) {
	c.getDocCalls++
	return c.Store.GetDocument(path)
}

// Regression test for issue #159: one GetDocument per IndexFile call.
func TestIndexFileSingleGetDocumentCall(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-159-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	cs := &countingStore{Store: dbClient}
	embedder := &fakeEmbedder{dim: 768}

	file := filepath.Join(tempDir, "doc.md")
	if err := os.WriteFile(file, []byte("original content"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// First index: new file — expect 1 GetDocument (returns nil)
	cs.getDocCalls = 0
	if _, err := IndexFile(cs, embedder, file); err != nil {
		t.Fatalf("first IndexFile: %v", err)
	}
	if cs.getDocCalls != 1 {
		t.Errorf("first index: expected 1 GetDocument call, got %d", cs.getDocCalls)
	}

	// Second index: unchanged file — expect 1 GetDocument (skip)
	cs.getDocCalls = 0
	if _, err := IndexFile(cs, embedder, file); err != nil {
		t.Fatalf("second IndexFile: %v", err)
	}
	if cs.getDocCalls != 1 {
		t.Errorf("unchanged index: expected 1 GetDocument call, got %d", cs.getDocCalls)
	}

	// Third index: changed file — expect 1 GetDocument (prepareIndex), 0 in commitIndex
	cs.getDocCalls = 0
	if err := os.WriteFile(file, []byte("updated content"), 0644); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := IndexFile(cs, embedder, file); err != nil {
		t.Fatalf("third IndexFile: %v", err)
	}
	if cs.getDocCalls != 1 {
		t.Errorf("changed index: expected 1 GetDocument call, got %d", cs.getDocCalls)
	}
}

// Regression test for issue #159: one GetDocument per indexContent call.
func TestIndexContentSingleGetDocumentCall(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-159-content-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	cs := &countingStore{Store: dbClient}
	embedder := &fakeEmbedder{dim: 768}

	// First index: new content — expect 1 GetDocument
	cs.getDocCalls = 0
	if err := indexContent(cs, embedder, "test://issue159", "first version"); err != nil {
		t.Fatalf("first indexContent: %v", err)
	}
	if cs.getDocCalls != 1 {
		t.Errorf("first content index: expected 1 GetDocument call, got %d", cs.getDocCalls)
	}

	// Second index: unchanged content — expect 1 GetDocument (skip)
	cs.getDocCalls = 0
	if err := indexContent(cs, embedder, "test://issue159", "first version"); err != nil {
		t.Fatalf("second indexContent: %v", err)
	}
	if cs.getDocCalls != 1 {
		t.Errorf("unchanged content index: expected 1 GetDocument call, got %d", cs.getDocCalls)
	}

	// Third index: changed content — expect 1 GetDocument, 0 in commitIndex
	cs.getDocCalls = 0
	if err := indexContent(cs, embedder, "test://issue159", "second version"); err != nil {
		t.Fatalf("third indexContent: %v", err)
	}
	if cs.getDocCalls != 1 {
		t.Errorf("changed content index: expected 1 GetDocument call, got %d", cs.getDocCalls)
	}
}

// TestParallelIndexSkipsUnchangedFiles is a regression test for issue #70.
// The parallel indexing path must check the file hash before generating
// embeddings, so unchanged files are not re-embedded on every index run.
func TestParallelIndexSkipsUnchangedFiles(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parallel-hash-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	embedder := &countingEmbedder{dim: 768}

	docsDir := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	file1 := filepath.Join(docsDir, "a.md")
	file2 := filepath.Join(docsDir, "b.md")
	if err := os.WriteFile(file1, []byte("content one"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(file2, []byte("content two"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	paths := map[string]bool{file1: true, file2: true}
	processFilesInParallel(dbClient, embedder, paths)

	if embedder.calls != 2 {
		t.Fatalf("first run: expected 2 embedding calls, got %d", embedder.calls)
	}

	embedder.calls = 0
	processFilesInParallel(dbClient, embedder, paths)

	if embedder.calls != 0 {
		t.Errorf("second run: expected 0 embedding calls for unchanged files, got %d", embedder.calls)
	}
}

// countingEmbedder wraps fakeEmbedder and counts GenerateVectors calls.
type countingEmbedder struct {
	dim   int
	calls int
}

func (c *countingEmbedder) GenerateVector(text string) []float32 {
	c.calls++
	return (&fakeEmbedder{dim: c.dim}).GenerateVector(text)
}

func (c *countingEmbedder) GenerateVectors(texts []string) [][]float32 {
	c.calls++
	return (&fakeEmbedder{dim: c.dim}).GenerateVectors(texts)
}

func (c *countingEmbedder) GenerateVectorNoRetry(text string) []float32 {
	c.calls++
	return (&fakeEmbedder{dim: c.dim}).GenerateVectorNoRetry(text)
}

func (c *countingEmbedder) Dim() int {
	return c.dim
}

func (c *countingEmbedder) ModelName() string {
	return "counting-model"
}

// TestIndexDirectorySiblingPrefix is a regression test for issue #66.
// Re-indexing a directory must never delete documents that live in a
// sibling directory whose name shares a string prefix (e.g. /docs vs /docs2).
func TestIndexDirectorySiblingPrefix(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-sibling-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	embedder := &fakeEmbedder{dim: 768}

	docsDir := filepath.Join(tempDir, "docs")
	docs2Dir := filepath.Join(tempDir, "docs2")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.MkdirAll(docs2Dir, 0755); err != nil {
		t.Fatalf("mkdir docs2: %v", err)
	}

	file1 := filepath.Join(docsDir, "a.md")
	file2 := filepath.Join(docs2Dir, "b.md")
	if err := os.WriteFile(file1, []byte("content in docs"), 0644); err != nil {
		t.Fatalf("write a.md: %v", err)
	}
	if err := os.WriteFile(file2, []byte("content in docs2"), 0644); err != nil {
		t.Fatalf("write b.md: %v", err)
	}

	if err := IndexDirectory(dbClient, embedder, docsDir); err != nil {
		t.Fatalf("index docs: %v", err)
	}
	if err := IndexDirectory(dbClient, embedder, docs2Dir); err != nil {
		t.Fatalf("index docs2: %v", err)
	}

	doc2, err := dbClient.GetDocument(file2)
	if err != nil || doc2 == nil {
		t.Fatalf("docs2/b.md not indexed after initial pass")
	}

	if err := IndexDirectory(dbClient, embedder, docsDir); err != nil {
		t.Fatalf("re-index docs: %v", err)
	}

	doc2After, err := dbClient.GetDocument(file2)
	if err != nil {
		t.Fatalf("GetDocument after re-index: %v", err)
	}
	if doc2After == nil {
		t.Error("docs2/b.md was deleted from index after re-indexing sibling docs/ — orphan cleanup used plain HasPrefix")
	}
}

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

	embedder := newTestEmbeddingsGenerator()

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

	embedder := newTestEmbeddingsGenerator()

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

// TestProcessFilesInParallelMatchesSequential is a regression test
// for issue #50. The bounded worker pool must produce the same
// database state as a sequential sweep: every input path is
// indexed exactly once and no goroutine panics or leaks the
// worker-count budget.
func TestProcessFilesInParallelMatchesSequential(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-par-test")
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

	embedder := newTestEmbeddingsGenerator()

	const nFiles = 20
	docsDir := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		t.Fatalf("failed to create docs dir: %v", err)
	}
	paths := make(map[string]bool, nFiles)
	for i := 0; i < nFiles; i++ {
		p := filepath.Join(docsDir, fmt.Sprintf("file_%02d.md", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("content of file %d", i)), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", p, err)
		}
		paths[p] = true
	}

	processFilesInParallel(dbClient, embedder, paths)

	stats, err := dbClient.GetStats()
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.DocumentCount != nFiles {
		t.Errorf("expected %d documents indexed, got %d", nFiles, stats.DocumentCount)
	}
	for p := range paths {
		doc, err := dbClient.GetDocument(p)
		if err != nil {
			t.Errorf("GetDocument(%s): %v", p, err)
			continue
		}
		if doc == nil {
			t.Errorf("expected %s to be indexed, found nil", p)
		}
	}
}

// BenchmarkIndexDirectorySequential vs BenchmarkIndexDirectoryParallel
// measures the impact of issue #50's bounded worker pool.
//
// Run with:
//
//	go test -bench=BenchmarkIndexDirectory -benchtime=1x ./internal/engine
func BenchmarkIndexDirectorySequential(b *testing.B) {
	benchmarkIndexDirectory(b, false)
}

func BenchmarkIndexDirectoryParallel(b *testing.B) {
	benchmarkIndexDirectory(b, true)
}

func benchmarkIndexDirectory(b *testing.B, parallel bool) {
	b.Helper()
	tempDir, err := os.MkdirTemp("", "seek-par-bench")
	if err != nil {
		b.Fatalf("tempdir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	dbClient, err := db.Open()
	if err != nil {
		b.Fatalf("db.Open: %v", err)
	}
	defer dbClient.Close()

	embedder := newTestEmbeddingsGenerator()

	const nFiles = 20
	docsDir := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		b.Fatalf("mkdir: %v", err)
	}
	paths := make(map[string]bool, nFiles)
	for i := 0; i < nFiles; i++ {
		p := filepath.Join(docsDir, fmt.Sprintf("file_%02d.md", i))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("content of file %d", i)), 0644); err != nil {
			b.Fatalf("write: %v", err)
		}
		paths[p] = true
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if parallel {
			processFilesInParallel(dbClient, embedder, paths)
		} else {
			for p := range paths {
				if _, err := IndexFile(dbClient, embedder, p); err != nil {
					b.Fatalf("IndexFile(%s): %v", p, err)
				}
			}
		}
	}
}

func TestIndexDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-sync-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("HOME", tempDir)

	dbClient, err := db.Open()
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer dbClient.Close()

	embedder := newTestEmbeddingsGenerator()

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
