package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-seek/internal/db"
)

func TestIndexURL_WithSymfetch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-url-symfetch-test")
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

	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	fakeSymfetch := filepath.Join(binDir, "symfetch")
	script := `#!/bin/sh
echo "# Test Document from symfetch"
echo "This content was fetched by the fake symfetch."
`
	if err := os.WriteFile(fakeSymfetch, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake symfetch: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+originalPath)

	if err := IndexURL(dbClient, embedder, "https://example.com/test"); err != nil {
		t.Fatalf("IndexURL failed: %v", err)
	}

	doc, err := dbClient.GetDocument("https://example.com/test")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document to be indexed")
	}

	chunks, err := dbClient.GetChunksForDocument("https://example.com/test")
	if err != nil {
		t.Fatalf("GetChunksForDocument failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	if !strings.Contains(chunks[0].Content, "fake symfetch") {
		t.Errorf("expected chunk to contain symfetch content, got: %s", chunks[0].Content)
	}
}

func TestIndexURL_HTTPFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-url-http-test")
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "# Test Document from HTTP")
		fmt.Fprintln(w, "This content was fetched via HTTP GET fallback.")
	}))
	defer server.Close()

	t.Setenv("PATH", "/nonexistent")

	if err := IndexURL(dbClient, embedder, server.URL+"/test"); err != nil {
		t.Fatalf("IndexURL failed: %v", err)
	}

	doc, err := dbClient.GetDocument(server.URL + "/test")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document to be indexed")
	}

	chunks, err := dbClient.GetChunksForDocument(server.URL + "/test")
	if err != nil {
		t.Fatalf("GetChunksForDocument failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	if !strings.Contains(chunks[0].Content, "HTTP GET fallback") {
		t.Errorf("expected chunk to contain HTTP fallback content, got: %s", chunks[0].Content)
	}
}

func TestIndexURL_HTTPFallback_HTMLToText(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-url-html-test")
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

	htmlContent := `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
<h1>Hello World</h1>
<p>This is a test paragraph.</p>
<script>console.log('should be removed');</script>
<style>.hidden { display: none; }</style>
<p>Another paragraph with <strong>bold</strong> text.</p>
</body>
</html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, htmlContent)
	}))
	defer server.Close()

	t.Setenv("PATH", "/nonexistent")

	if err := IndexURL(dbClient, embedder, server.URL+"/html-test"); err != nil {
		t.Fatalf("IndexURL failed: %v", err)
	}

	chunks, err := dbClient.GetChunksForDocument(server.URL + "/html-test")
	if err != nil {
		t.Fatalf("GetChunksForDocument failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	content := chunks[0].Content
	if strings.Contains(content, "console.log") {
		t.Error("expected script content to be removed")
	}
	if strings.Contains(content, "display: none") {
		t.Error("expected style content to be removed")
	}
	if !strings.Contains(content, "Hello World") {
		t.Error("expected visible text to be preserved")
	}
	if !strings.Contains(content, "Another paragraph") {
		t.Error("expected visible text to be preserved")
	}
}

func TestIndexStdin(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-stdin-test")
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

	content := "This is a test document from stdin with unique content."
	reader := strings.NewReader(content)

	if err := IndexStdin(dbClient, embedder, reader, "test://stdin-doc"); err != nil {
		t.Fatalf("IndexStdin failed: %v", err)
	}

	doc, err := dbClient.GetDocument("test://stdin-doc")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document to be indexed")
	}

	chunks, err := dbClient.GetChunksForDocument("test://stdin-doc")
	if err != nil {
		t.Fatalf("GetChunksForDocument failed: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	if !strings.Contains(chunks[0].Content, "test document from stdin") {
		t.Errorf("expected chunk to contain stdin content, got: %s", chunks[0].Content)
	}
}

func TestIndexStdin_EmptyContent(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-stdin-empty-test")
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

	reader := strings.NewReader("")

	err = IndexStdin(dbClient, embedder, reader, "test://empty")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(err.Error(), "no content provided") {
		t.Errorf("expected 'no content provided' error, got: %v", err)
	}
}

func TestIndexStdin_DefaultSource(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-stdin-default-source-test")
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

	content := "Content with default source."
	reader := strings.NewReader(content)

	if err := IndexStdin(dbClient, embedder, reader, ""); err != nil {
		t.Fatalf("IndexStdin failed: %v", err)
	}

	doc, err := dbClient.GetDocument("")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}
	if doc == nil {
		t.Fatal("expected document to be indexed with empty source")
	}
}

func TestIndexContent_UnchangedSkip(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-content-skip-test")
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

	content := "Test content for skip check."

	if err := indexContent(dbClient, embedder, "test://skip", content); err != nil {
		t.Fatalf("first indexContent failed: %v", err)
	}

	stats1, _ := dbClient.GetStats()

	if err := indexContent(dbClient, embedder, "test://skip", content); err != nil {
		t.Fatalf("second indexContent failed: %v", err)
	}

	stats2, _ := dbClient.GetStats()
	if stats2.ChunkCount != stats1.ChunkCount {
		t.Errorf("expected chunk count to remain same after re-indexing unchanged content, got %d vs %d",
			stats2.ChunkCount, stats1.ChunkCount)
	}
}

func TestIndexContent_UpdatedContent(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-content-update-test")
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

	content1 := "Original content for update test."
	if err := indexContent(dbClient, embedder, "test://update", content1); err != nil {
		t.Fatalf("first indexContent failed: %v", err)
	}

	doc1, _ := dbClient.GetDocument("test://update")
	hash1 := doc1.Hash

	content2 := "Updated content for update test."
	if err := indexContent(dbClient, embedder, "test://update", content2); err != nil {
		t.Fatalf("second indexContent failed: %v", err)
	}

	doc2, _ := dbClient.GetDocument("test://update")
	if doc2.Hash == hash1 {
		t.Error("expected hash to change after updating content")
	}

	chunks, _ := dbClient.GetChunksForDocument("test://update")
	if len(chunks) == 0 {
		t.Fatal("expected chunks to be present")
	}
	if !strings.Contains(chunks[0].Content, "Updated content") {
		t.Errorf("expected updated content in chunks, got: %s", chunks[0].Content)
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes scripts",
			input:    "<p>Hello</p><script>alert('xss')</script><p>World</p>",
			expected: "Hello\nWorld",
		},
		{
			name:     "removes styles",
			input:    "<p>Hello</p><style>.hidden{display:none}</style><p>World</p>",
			expected: "Hello\nWorld",
		},
		{
			name:     "converts block elements",
			input:    "<div>Line1</div><br><p>Line2</p><h1>Header</h1>",
			expected: "Line1\n\nLine2\nHeader",
		},
		{
			name:     "decodes entities",
			input:    "<p>&amp; &lt; &gt; &quot; &#39; &nbsp;</p>",
			expected: "& < > \" '",
		},
		{
			name:     "collapses newlines",
			input:    "<p>A</p>\n\n\n<p>B</p>",
			expected: "A\n\nB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := htmlToText(tt.input)
			if result != tt.expected {
				t.Errorf("htmlToText(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFetchURLContent_SymfetchNotFound(t *testing.T) {
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	defer t.Setenv("PATH", originalPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "Fallback content")
	}))
	defer server.Close()

	content, err := fetchURLContent(server.URL)
	if err != nil {
		t.Fatalf("fetchURLContent failed: %v", err)
	}

	if !strings.Contains(content, "Fallback content") {
		t.Errorf("expected fallback content, got: %s", content)
	}
}

func TestFetchWithHTTP_StatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := fetchWithHTTP(server.URL)
	if err == nil {
		t.Fatal("expected error for 404 status")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got: %v", err)
	}
}

func TestFetchWithSymfetch_CommandFails(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-symfetch-fail-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	fakeSymfetch := filepath.Join(binDir, "symfetch")
	script := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(fakeSymfetch, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake symfetch: %v", err)
	}

	_, err = fetchWithSymfetch(fakeSymfetch, "https://example.com")
	if err == nil {
		t.Fatal("expected error when symfetch fails")
	}
}

func TestIndexURL_HTTPFallback_NotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-url-notfound-test")
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("PATH", "/nonexistent")

	err = IndexURL(dbClient, embedder, server.URL)
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestIndexContent_IndexAndSearch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-index-search-test")
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

	content := "This is a unique searchable document about quantum computing and machine learning."
	if err := indexContent(dbClient, embedder, "test://quantum", content); err != nil {
		t.Fatalf("indexContent failed: %v", err)
	}

	results, err := SearchHybrid(dbClient, embedder, "quantum computing", 5)
	if err != nil {
		t.Fatalf("SearchHybrid failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected search results")
	}

	found := false
	for _, r := range results {
		if r.Chunk.DocumentPath == "test://quantum" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected indexed document to be found in search results")
	}
}

func TestFetchWithHTTP_LargeResponse(t *testing.T) {
	largeContent := strings.Repeat("A", 11<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, largeContent)
	}))
	defer server.Close()

	content, err := fetchWithHTTP(server.URL)
	if err != nil {
		t.Fatalf("fetchWithHTTP failed: %v", err)
	}

	if len(content) > 10<<20 {
		t.Errorf("expected content to be truncated to 10MB, got %d bytes", len(content))
	}
}

func TestFetchWithHTTP_NonHTMLContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"key": "value"}`)
	}))
	defer server.Close()

	content, err := fetchWithHTTP(server.URL)
	if err != nil {
		t.Fatalf("fetchWithHTTP failed: %v", err)
	}

	if !strings.Contains(content, `"key": "value"`) {
		t.Errorf("expected JSON content to be preserved, got: %s", content)
	}
}

func TestIndexStdin_LargeContent(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-stdin-large-test")
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

	largeContent := strings.Repeat("This is line number X of the large document. ", 1000)
	reader := strings.NewReader(largeContent)

	if err := IndexStdin(dbClient, embedder, reader, "test://large"); err != nil {
		t.Fatalf("IndexStdin failed: %v", err)
	}

	chunks, err := dbClient.GetChunksForDocument("test://large")
	if err != nil {
		t.Fatalf("GetChunksForDocument failed: %v", err)
	}

	if len(chunks) <= 1 {
		t.Errorf("expected multiple chunks for large content, got %d", len(chunks))
	}
}

func TestIndexContent_MultipleSources(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-multi-source-test")
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

	sources := []struct {
		source  string
		content string
	}{
		{"test://doc1", "Content for document one."},
		{"test://doc2", "Content for document two."},
		{"test://doc3", "Content for document three."},
	}

	for _, s := range sources {
		if err := indexContent(dbClient, embedder, s.source, s.content); err != nil {
			t.Fatalf("indexContent(%s) failed: %v", s.source, err)
		}
	}

	stats, _ := dbClient.GetStats()
	if stats.DocumentCount != 3 {
		t.Errorf("expected 3 documents, got %d", stats.DocumentCount)
	}
}
