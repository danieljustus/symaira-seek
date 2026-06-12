package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/parser"
)

const (
	// symfetchTimeout is the maximum time to wait for symfetch to complete.
	symfetchTimeout = 60 * time.Second
	// httpFallbackTimeout is the timeout for the HTTP fallback client.
	httpFallbackTimeout = 30 * time.Second
	// maxHTTPResponseSize limits the response body to 10 MB.
	maxHTTPResponseSize = 10 << 20
)

// IndexURL fetches content from a URL and indexes it.
// It first attempts to use symfetch if available, falling back to a simple
// HTTP GET with minimal HTML-to-text conversion if symfetch is not found.
func IndexURL(dbClient db.Store, embedder Embedder, url string) error {
	content, err := fetchURLContent(url)
	if err != nil {
		return fmt.Errorf("failed to fetch URL content: %w", err)
	}

	return indexContent(dbClient, embedder, url, content)
}

// IndexStdin reads content from a reader and indexes it with the given source.
func IndexStdin(dbClient db.Store, embedder Embedder, reader io.Reader, source string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read from stdin: %w", err)
	}

	content := string(data)
	if content == "" {
		return fmt.Errorf("no content provided via stdin")
	}

	return indexContent(dbClient, embedder, source, content)
}

// fetchURLContent attempts to use symfetch, falling back to HTTP GET.
func fetchURLContent(url string) (string, error) {
	// Try symfetch first
	if symfetchPath, err := exec.LookPath("symfetch"); err == nil {
		content, err := fetchWithSymfetch(symfetchPath, url)
		if err == nil {
			return content, nil
		}
		fmt.Fprintf(os.Stderr, "symfetch failed: %v, falling back to HTTP GET\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "symfetch not found in PATH, falling back to HTTP GET\n")
	}

	// Fallback to HTTP GET
	return fetchWithHTTP(url)
}

// fetchWithSymfetch runs symfetch get <url> --format md and returns stdout.
func fetchWithSymfetch(symfetchPath, url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), symfetchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, symfetchPath, "get", url, "--format", "md")
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("symfetch execution failed: %w", err)
	}

	return string(output), nil
}

// fetchWithHTTP performs a simple HTTP GET and converts HTML to text.
func fetchWithHTTP(url string) (string, error) {
	client := &http.Client{
		Timeout: httpFallbackTimeout,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP GET returned status %d", resp.StatusCode)
	}

	limitedReader := io.LimitReader(resp.Body, maxHTTPResponseSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read HTTP response: %w", err)
	}

	if int64(len(data)) > maxHTTPResponseSize {
		data = data[:maxHTTPResponseSize]
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") {
		return htmlToText(string(data)), nil
	}

	return string(data), nil
}

// htmlToText performs minimal HTML-to-text conversion.
func htmlToText(html string) string {
	html = regexp.MustCompile(`(?i)<script[^>]*>[\s\S]*?</script>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?i)<style[^>]*>[\s\S]*?</style>`).ReplaceAllString(html, "")

	html = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(html, "\n")
	html = regexp.MustCompile(`(?i)<(?:hr|p|div|h[1-6]|li|tr)[^>]*>`).ReplaceAllString(html, "\n")

	html = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, "")

	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")
	html = strings.ReplaceAll(html, "&nbsp;", " ")

	html = regexp.MustCompile(`\n{3,}`).ReplaceAllString(html, "\n\n")

	return strings.TrimSpace(html)
}

// indexContent indexes the given content with the source as document path.
func indexContent(dbClient db.Store, embedder Embedder, source, content string) error {
	// Compute hash of content
	hashSum := sha256.Sum256([]byte(content))
	currentHash := hex.EncodeToString(hashSum[:])

	// Check if document already exists with same hash
	existing, err := dbClient.GetDocument(source)
	if err != nil {
		return fmt.Errorf("failed to check existing document: %w", err)
	}
	if existing != nil && existing.Hash == currentHash {
		fmt.Fprintf(os.Stderr, "Document unchanged, skipping: %s\n", source)
		return nil
	}

	// Delete old version if exists
	if existing != nil {
		if err := dbClient.DeleteDocument(source); err != nil {
			return fmt.Errorf("failed to delete old document: %w", err)
		}
	}

	// Split content into chunks
	textChunks := parser.SplitText(content, 1000, 200)
	embeddings := embedder.GenerateVectors(textChunks)

	// Create chunks
	chunks := make([]*db.Chunk, 0, len(textChunks))
	for idx, tc := range textChunks {
		chunkHashSum := sha256.Sum256([]byte(tc))
		chunkHash := hex.EncodeToString(chunkHashSum[:])
		chunks = append(chunks, &db.Chunk{
			UUID:         uuid.New().String(),
			DocumentPath: source,
			ChunkIndex:   idx,
			Content:      tc,
			Embedding:    embeddings[idx],
			Hash:         chunkHash,
		})
	}

	// Save document
	doc := &db.Document{
		Path:      source,
		Hash:      currentHash,
		UpdatedAt: time.Now(),
	}

	if err := dbClient.SaveDocument(doc); err != nil {
		return fmt.Errorf("failed to save document: %w", err)
	}

	if err := dbClient.SaveChunks(chunks); err != nil {
		return fmt.Errorf("failed to save chunks: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Indexed: %s (%d chunks)\n", source, len(chunks))
	return nil
}
