package parser

import (
	"archive/zip"
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

func TestParseFileRejectsOversizedText(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-oversize")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	bigPath := filepath.Join(tempDir, "big.txt")
	bigData := make([]byte, MaxIndexFileSize+1)
	for i := range bigData {
		bigData[i] = 'A'
	}
	if err := os.WriteFile(bigPath, bigData, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = ParseFile(bigPath)
	if err == nil {
		t.Fatal("expected error for oversized text file, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should mention size limit, got: %v", err)
	}
}

func TestParseDOCXZipBomb(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-docx-bomb")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	docxPath := filepath.Join(tempDir, "bomb.docx")
	createZipBomb(t, docxPath, "word/document.xml", MaxIndexFileSize+1)

	content, err := ParseFile(docxPath)
	if err == nil && int64(len(content)) > MaxIndexFileSize {
		t.Fatalf("DOCX zip-bomb returned %d bytes, exceeding limit", len(content))
	}
}

func TestParseXLSXZipBomb(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-xlsx-bomb")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	xlsxPath := filepath.Join(tempDir, "bomb.xlsx")
	createZipBomb(t, xlsxPath, "xl/sharedStrings.xml", MaxIndexFileSize+1)

	_, err = ParseFile(xlsxPath)
	if err == nil {
		t.Fatal("expected error or truncated result for zip-bomb XLSX, got nil")
	}
}

func TestParsePPTXZipBomb(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-pptx-bomb")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pptxPath := filepath.Join(tempDir, "bomb.pptx")
	createZipBomb(t, pptxPath, "ppt/slides/slide1.xml", MaxIndexFileSize+1)

	_, err = ParseFile(pptxPath)
	if err == nil {
		t.Fatal("expected error or truncated result for zip-bomb PPTX, got nil")
	}
}

// createZipBomb writes a ZIP file containing a single entry whose
// decompressed content exceeds MaxIndexFileSize bytes.
func createZipBomb(t *testing.T, zipPath, entryName string, decompressedSize int) {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	entry, err := w.Create(entryName)
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	chunk := make([]byte, 64*1024)
	for i := range chunk {
		chunk[i] = 'X'
	}
	written := 0
	for written < decompressedSize {
		n := len(chunk)
		if written+n > decompressedSize {
			n = decompressedSize - written
		}
		if _, err := entry.Write(chunk[:n]); err != nil {
			t.Fatalf("write entry: %v", err)
		}
		written += n
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}
