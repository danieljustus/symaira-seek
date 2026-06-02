package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetFileHashAndParse(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "sample.txt")
	content := "Hello Symaira Seek!\nWelcome to Phase 2."
	err = os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hash, err := GetFileHash(filePath)
	if err != nil {
		t.Fatalf("GetFileHash failed: %v", err)
	}
	if len(hash) != 64 {
		t.Errorf("expected 64 character SHA-256 hash, got %d chars (%s)", len(hash), hash)
	}

	parsed, err := ParseFile(filePath)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if parsed != content {
		t.Errorf("parsed content mismatch: got %q, want %q", parsed, content)
	}
}

func TestSplitText(t *testing.T) {
	text := "This is a simple text. It has multiple sentences. We want to test recursive splitting."
	// Let's split with a small chunk size of 20 characters and 5 character overlap
	chunks := SplitText(text, 25, 5)

	if len(chunks) == 0 {
		t.Fatalf("expected chunks, got none")
	}

	// Verify all chunks are below or equal to 25 chars
	for i, chunk := range chunks {
		if len(chunk) > 25 {
			t.Errorf("chunk %d exceeds max length (size: %d, content: %q)", i, len(chunk), chunk)
		}
	}

	// Reconstruct text (approximately, allowing for overlaps and whitespace differences)
	reconstructed := strings.Join(chunks, " ")
	if !strings.Contains(reconstructed, "recursive") {
		t.Errorf("reconstructed text does not contain key words, got chunks: %v", chunks)
	}
}
