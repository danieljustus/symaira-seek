package parser

import (
	"archive/zip"
	"bytes"
	"fmt"
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

// createMinimalPDF writes a valid PDF with the given text rendered via a Type1 font.
func createMinimalPDF(t *testing.T, path, text string) {
	t.Helper()

	type segment struct {
		data []byte
	}
	var segs []segment

	segs = append(segs, segment{[]byte("%PDF-1.4\n")})
	segs = append(segs, segment{[]byte("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")})
	segs = append(segs, segment{[]byte("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")})
	segs = append(segs, segment{[]byte("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")})

	stream := fmt.Sprintf("BT\n/F1 12 Tf\n100 700 Td\n(%s) Tj\nET\n", text)
	contentObj := fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(stream), stream)
	segs = append(segs, segment{[]byte(contentObj)})

	segs = append(segs, segment{[]byte("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")})

	offsets := make([]int, 6)
	off := 0
	for i, s := range segs {
		offsets[i] = off
		off += len(s.data)
	}
	xrefOff := off

	var xref strings.Builder
	xref.WriteString("xref\n0 6\n")
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&xref, "%010d 00000 n \n", offsets[i])
	}

	var trailer strings.Builder
	trailer.WriteString(xref.String())
	fmt.Fprintf(&trailer, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOff)

	var buf []byte
	for _, s := range segs {
		buf = append(buf, s.data...)
	}
	buf = append(buf, []byte(trailer.String())...)

	if err := os.WriteFile(path, buf, 0644); err != nil {
		t.Fatalf("write PDF: %v", err)
	}
}

// createEmptyTextPDF writes a valid PDF with a blank page (no text operators).
func createEmptyTextPDF(t *testing.T, path string) {
	t.Helper()

	var segs [][]byte
	segs = append(segs, []byte("%PDF-1.4\n"))
	segs = append(segs, []byte("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"))
	segs = append(segs, []byte("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n"))
	segs = append(segs, []byte("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << >> >>\nendobj\n"))
	segs = append(segs, []byte("4 0 obj\n<< /Length 0 >>\nstream\nendstream\nendobj\n"))

	offsets := make([]int, len(segs))
	off := 0
	for i, s := range segs {
		offsets[i] = off
		off += len(s)
	}
	xrefOff := off

	var buf bytes.Buffer
	for _, s := range segs {
		buf.Write(s)
	}
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(segs))
	for i := 0; i < len(segs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(segs), xrefOff)

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write empty PDF: %v", err)
	}
}

func TestParseFilePDF(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-pdf")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pdfPath := filepath.Join(tempDir, "hello.pdf")
	createMinimalPDF(t, pdfPath, "Hello PDF")

	parsed, err := ParseFile(pdfPath)
	if err != nil {
		t.Fatalf("ParseFile(.pdf) failed: %v", err)
	}
	if !strings.Contains(parsed, "Hello PDF") {
		t.Errorf("expected parsed text to contain %q, got %q", "Hello PDF", parsed)
	}
}

func TestParseFilePDFNotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/path/to/file.pdf")
	if err == nil {
		t.Fatal("expected error for nonexistent PDF, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open PDF") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseFilePDFEmptyText(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-pdf-empty")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pdfPath := filepath.Join(tempDir, "empty.pdf")
	createEmptyTextPDF(t, pdfPath)

	_, err = ParseFile(pdfPath)
	if err == nil {
		t.Fatal("expected error for empty-text PDF, got nil")
	}
	if !strings.Contains(err.Error(), "no extractable text") {
		t.Errorf("expected 'no extractable text' error, got: %v", err)
	}
}

// --- XLSX tests ---

// createMinimalXLSX builds a minimal XLSX (ZIP+XML). sharedStrings are the
// string table; rows[r][c] is the cell value; cellTypes[r][c] is "s" (shared
// string index), "inlineStr", or "" (numeric).
func createMinimalXLSX(t *testing.T, path string, sharedStrings []string, rows [][]string, cellTypes [][]string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create xlsx: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)

	if len(sharedStrings) > 0 {
		ssWriter, err := w.Create("xl/sharedStrings.xml")
		if err != nil {
			t.Fatalf("create sharedStrings entry: %v", err)
		}
		var ss strings.Builder
		ss.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		ss.WriteString(fmt.Sprintf(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, len(sharedStrings), len(sharedStrings)))
		for _, s := range sharedStrings {
			ss.WriteString(fmt.Sprintf(`<si><t>%s</t></si>`, s))
		}
		ss.WriteString("</sst>")
		if _, err := ssWriter.Write([]byte(ss.String())); err != nil {
			t.Fatalf("write sharedStrings: %v", err)
		}
	}

	sheetWriter, err := w.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatalf("create sheet entry: %v", err)
	}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for rowIdx, row := range rows {
		sheet.WriteString(fmt.Sprintf(`<row r="%d">`, rowIdx+1))
		cols := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		for colIdx, val := range row {
			cellType := ""
			if cellTypes != nil && rowIdx < len(cellTypes) && colIdx < len(cellTypes[rowIdx]) {
				cellType = cellTypes[rowIdx][colIdx]
			}
			cellRef := fmt.Sprintf("%c%d", cols[colIdx%len(cols)], rowIdx+1)
			if cellType == "s" {
				sheet.WriteString(fmt.Sprintf(`<c r="%s" t="s"><v>%s</v></c>`, cellRef, val))
			} else if cellType == "inlineStr" {
				sheet.WriteString(fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, cellRef, val))
			} else {
				sheet.WriteString(fmt.Sprintf(`<c r="%s"><v>%s</v></c>`, cellRef, val))
			}
		}
		sheet.WriteString("</row>")
	}
	sheet.WriteString("</sheetData></worksheet>")
	if _, err := sheetWriter.Write([]byte(sheet.String())); err != nil {
		t.Fatalf("write sheet: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

func TestParseFileXLSXWithSharedStrings(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-xlsx")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	xlsxPath := filepath.Join(tempDir, "data.xlsx")
	sharedStrings := []string{"Hello", "World"}
	rows := [][]string{{"0", "1"}}
	cellTypes := [][]string{{"s", "s"}}

	createMinimalXLSX(t, xlsxPath, sharedStrings, rows, cellTypes)

	parsed, err := ParseFile(xlsxPath)
	if err != nil {
		t.Fatalf("ParseFile(.xlsx) failed: %v", err)
	}
	if !strings.Contains(parsed, "Hello") || !strings.Contains(parsed, "World") {
		t.Errorf("expected parsed text to contain 'Hello' and 'World', got %q", parsed)
	}
}

func TestParseFileXLSXWithMixedCellTypes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-xlsx-mixed")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	xlsxPath := filepath.Join(tempDir, "mixed.xlsx")
	sharedStrings := []string{"Alpha", "Beta"}
	rows := [][]string{{"0", "99", "1"}}
	cellTypes := [][]string{{"s", "", "s"}}

	createMinimalXLSX(t, xlsxPath, sharedStrings, rows, cellTypes)

	parsed, err := ParseFile(xlsxPath)
	if err != nil {
		t.Fatalf("ParseFile(.xlsx) mixed failed: %v", err)
	}
	if !strings.Contains(parsed, "Alpha") || !strings.Contains(parsed, "Beta") || !strings.Contains(parsed, "99") {
		t.Errorf("expected parsed text to contain 'Alpha', 'Beta', and '99', got %q", parsed)
	}
}

func TestParseFileXLSXWithNumbers(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-xlsx-num")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	xlsxPath := filepath.Join(tempDir, "numbers.xlsx")
	rows := [][]string{{"42", "3.14"}}
	cellTypes := [][]string{{"", ""}}

	createMinimalXLSX(t, xlsxPath, nil, rows, cellTypes)

	parsed, err := ParseFile(xlsxPath)
	if err != nil {
		t.Fatalf("ParseFile(.xlsx) numbers failed: %v", err)
	}
	if !strings.Contains(parsed, "42") || !strings.Contains(parsed, "3.14") {
		t.Errorf("expected parsed text to contain '42' and '3.14', got %q", parsed)
	}
}

func TestParseFileXLSXEmptyText(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "seek-parser-xlsx-empty")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	xlsxPath := filepath.Join(tempDir, "empty.xlsx")
	createMinimalXLSX(t, xlsxPath, nil, nil, nil)

	_, err = ParseFile(xlsxPath)
	if err == nil {
		t.Fatal("expected error for empty XLSX, got nil")
	}
	if !strings.Contains(err.Error(), "no extractable text") {
		t.Errorf("expected 'no extractable text' error, got: %v", err)
	}
}

func TestParseFileXLSXNotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/path/to/file.xlsx")
	if err == nil {
		t.Fatal("expected error for nonexistent XLSX, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open XLSX") {
		t.Errorf("unexpected error message: %v", err)
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
