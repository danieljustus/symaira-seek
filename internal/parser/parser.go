package parser

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ledongthuc/pdf"
)

// MaxIndexFileSize is the maximum file size (in bytes) that the indexer will
// read into memory. Files and individual ZIP entries exceeding this limit are
// skipped or rejected to prevent memory exhaustion.
const MaxIndexFileSize = 10 << 20

var (
	fileCache   = make(map[string]fileCacheEntry)
	fileCacheMu sync.RWMutex
)

// GetFileHash computes the SHA-256 hash of a file.
// Uses file metadata (mod time + size) to skip hash computation for unchanged files.
func GetFileHash(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	modTime := info.ModTime().UnixNano()
	size := info.Size()

	fileCacheMu.RLock()
	cached, exists := fileCache[path]
	fileCacheMu.RUnlock()

	if exists && cached.ModTime == modTime && cached.Size == size {
		fileCacheMu.Lock()
		cached = fileCache[path]
		fileCacheMu.Unlock()
		if cached.ModTime == modTime && cached.Size == size {
			return cached.Hash, nil
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	hash := hex.EncodeToString(h.Sum(nil))

	fileCacheMu.Lock()
	fileCache[path] = fileCacheEntry{ModTime: modTime, Size: size, Hash: hash}
	fileCacheMu.Unlock()

	return hash, nil
}

// fileCacheEntry stores file metadata for quick change detection.
type fileCacheEntry struct {
	ModTime int64
	Size    int64
	Hash    string
}

// ParseFile reads a file and returns its text content.
// It dispatches to format-specific extractors for PDF, DOCX, XLSX, and PPTX files;
// all other files are read as raw text (UTF-8).
func ParseFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return parsePDF(path)
	case ".docx":
		return parseDOCX(path)
	case ".xlsx":
		return parseXLSX(path)
	case ".pptx":
		return parsePPTX(path)
	default:
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, MaxIndexFileSize+1))
		if err != nil {
			return "", fmt.Errorf("failed to read file: %w", err)
		}
		if int64(len(data)) > MaxIndexFileSize {
			return "", fmt.Errorf("file %s exceeds %d byte limit (%d bytes)", path, MaxIndexFileSize, len(data))
		}
		return string(data), nil
	}
}

// parsePDF extracts text from a PDF file using a pure-Go PDF reader.
// Returns an error for encrypted or image-only PDFs (no OCR in scope).
func parsePDF(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var text strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		content, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		text.WriteString(content)
		text.WriteString("\n")
	}

	result := strings.TrimSpace(text.String())
	if result == "" {
		return "", fmt.Errorf("PDF contains no extractable text (may be image-only)")
	}
	return result, nil
}

// parseDOCX extracts text from a DOCX file (ZIP archive containing word/document.xml).
func parseDOCX(path string) (string, error) {
	return parseOfficeXML(path, "word/document.xml")
}

// parseXLSX extracts text from an XLSX file (ZIP archive with shared strings + sheet data).
func parseXLSX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("failed to open XLSX: %w", err)
	}
	defer r.Close()

	// Try to read shared strings first
	sharedStrings, err := readXLSXSharedStrings(r.File)
	if err != nil {
		sharedStrings = nil
	}

	var text strings.Builder
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			content, err := extractXLSXSheetText(f, sharedStrings)
			if err == nil && strings.TrimSpace(content) != "" {
				text.WriteString(content)
				text.WriteString("\n")
			}
		}
	}

	result := strings.TrimSpace(text.String())
	if result == "" {
		return "", fmt.Errorf("XLSX contains no extractable text")
	}
	return result, nil
}

// parsePPTX extracts text from a PPTX file (ZIP archive with slide XML files).
func parsePPTX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("failed to open PPTX: %w", err)
	}
	defer r.Close()

	var text strings.Builder
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			content, err := extractPPTXSlideText(f)
			if err == nil && strings.TrimSpace(content) != "" {
				text.WriteString(content)
				text.WriteString("\n")
			}
		}
	}

	result := strings.TrimSpace(text.String())
	if result == "" {
		return "", fmt.Errorf("PPTX contains no extractable text")
	}
	return result, nil
}

// Span is a chunk of text paired with its byte offset range within the
// original source text. Start/End are exact for chunks that fit without
// overlap reconstruction; for chunks stitched from an overlap tail plus a
// separator that was re-inserted to keep the join readable, End may overshoot
// the true source range by up to len(separator) bytes. This is precise enough
// to find the best-matching chunk for an extraction span, not a guarantee
// that text[Start:End] always reproduces Text byte-for-byte.
type Span struct {
	Text  string
	Start int
	End   int
}

// SplitTextWithSpans behaves like SplitText but also returns each chunk's
// byte offset range within the original text, so callers can persist source
// character spans alongside chunk content.
func SplitTextWithSpans(text string, chunkSize, chunkOverlap int) []Span {
	if chunkSize <= 0 {
		return []Span{{Text: text, Start: 0, End: len(text)}}
	}
	if chunkOverlap >= chunkSize {
		chunkOverlap = chunkSize / 2
	}

	separators := []string{"\n\n", "\n", " ", ""}
	return splitRecursiveSpans(text, 0, separators, chunkSize, chunkOverlap)
}

func splitRecursiveSpans(text string, base int, separators []string, chunkSize, chunkOverlap int) []Span {
	if len(text) <= chunkSize {
		return []Span{{Text: text, Start: base, End: base + len(text)}}
	}

	var separator string
	var nextSeps []string
	found := false
	for i, sep := range separators {
		if strings.Contains(text, sep) {
			separator = sep
			nextSeps = separators[i+1:]
			found = true
			break
		}
	}

	if !found {
		var spans []Span
		for i := 0; i < len(text); i += chunkSize - chunkOverlap {
			end := i + chunkSize
			if end > len(text) {
				end = len(text)
			}
			spans = append(spans, Span{Text: text[i:end], Start: base + i, End: base + end})
			if end == len(text) {
				break
			}
		}
		return spans
	}

	splits := strings.Split(text, separator)
	var finalSpans []Span
	var currentChunk strings.Builder
	chunkStart := 0
	pos := 0

	for i, part := range splits {
		partStart := pos
		pos += len(part)
		if i < len(splits)-1 {
			pos += len(separator)
		}

		if len(part) > chunkSize {
			if currentChunk.Len() > 0 {
				chunkStr := currentChunk.String()
				finalSpans = append(finalSpans, Span{Text: chunkStr, Start: base + chunkStart, End: base + chunkStart + len(chunkStr)})
				currentChunk.Reset()
			}
			subSpans := splitRecursiveSpans(part, base+partStart, nextSeps, chunkSize, chunkOverlap)
			finalSpans = append(finalSpans, subSpans...)
			continue
		}

		sepLen := len(separator)
		if currentChunk.Len() > 0 {
			if currentChunk.Len()+sepLen+len(part) <= chunkSize {
				currentChunk.WriteString(separator)
				currentChunk.WriteString(part)
			} else {
				chunkStr := currentChunk.String()
				finalSpans = append(finalSpans, Span{Text: chunkStr, Start: base + chunkStart, End: base + chunkStart + len(chunkStr)})

				overlapStart := len(chunkStr) - chunkOverlap
				if overlapStart < 0 {
					overlapStart = 0
				}
				tail := chunkStr[overlapStart:]
				// An empty tail means nothing carries over from the previous
				// chunk, so the new chunk starts exactly at this part's real
				// position rather than at chunkStart+overlapStart (which
				// would land on the separator instead of the part).
				var newChunkStart int
				if len(tail) > 0 {
					newChunkStart = chunkStart + overlapStart
				} else {
					newChunkStart = partStart
				}
				currentChunk.Reset()
				currentChunk.WriteString(tail)
				if currentChunk.Len() > 0 && !strings.HasSuffix(tail, separator) {
					currentChunk.WriteString(separator)
				}
				currentChunk.WriteString(part)
				chunkStart = newChunkStart
			}
		} else {
			currentChunk.WriteString(part)
			chunkStart = partStart
		}
	}

	if currentChunk.Len() > 0 {
		chunkStr := currentChunk.String()
		finalSpans = append(finalSpans, Span{Text: chunkStr, Start: base + chunkStart, End: base + chunkStart + len(chunkStr)})
	}

	return finalSpans
}

// SplitText recursively splits a string into chunks of max chunkSize, overlapping by chunkOverlap.
func SplitText(text string, chunkSize, chunkOverlap int) []string {
	if chunkSize <= 0 {
		return []string{text}
	}
	if chunkOverlap >= chunkSize {
		chunkOverlap = chunkSize / 2
	}

	separators := []string{"\n\n", "\n", " ", ""}
	return splitRecursive(text, separators, chunkSize, chunkOverlap)
}

func splitRecursive(text string, separators []string, chunkSize, chunkOverlap int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}

	// Find the first separator that splits the text
	var separator string
	var nextSeps []string
	found := false
	for i, sep := range separators {
		if strings.Contains(text, sep) {
			separator = sep
			nextSeps = separators[i+1:]
			found = true
			break
		}
	}

	// If no separator found, just cut it by chunkSize
	if !found {
		var chunks []string
		for i := 0; i < len(text); i += chunkSize - chunkOverlap {
			end := i + chunkSize
			if end > len(text) {
				end = len(text)
			}
			chunks = append(chunks, text[i:end])
			if end == len(text) {
				break
			}
		}
		return chunks
	}

	// Split the text by the selected separator
	splits := strings.Split(text, separator)
	var finalChunks []string
	var currentChunk strings.Builder

	for _, part := range splits {
		// If part itself is larger than chunkSize, split it recursively
		if len(part) > chunkSize {
			// First flush current chunk if it has anything
			if currentChunk.Len() > 0 {
				finalChunks = append(finalChunks, currentChunk.String())
				currentChunk.Reset()
			}
			subChunks := splitRecursive(part, nextSeps, chunkSize, chunkOverlap)
			finalChunks = append(finalChunks, subChunks...)
			continue
		}

		// Check if we can add this part to the current chunk
		sepLen := len(separator)
		if currentChunk.Len() > 0 {
			if currentChunk.Len()+sepLen+len(part) <= chunkSize {
				currentChunk.WriteString(separator)
				currentChunk.WriteString(part)
			} else {
				// Flush current and start a new one with overlap
				chunkStr := currentChunk.String()
				finalChunks = append(finalChunks, chunkStr)

				// Determine overlap: take the end of the previous chunk
				overlapStart := len(chunkStr) - chunkOverlap
				if overlapStart < 0 {
					overlapStart = 0
				}
				// Start next chunk with the overlap portion
				currentChunk.Reset()
				currentChunk.WriteString(chunkStr[overlapStart:])
				if currentChunk.Len() > 0 && !strings.HasSuffix(chunkStr[overlapStart:], separator) {
					currentChunk.WriteString(separator)
				}
				currentChunk.WriteString(part)
			}
		} else {
			currentChunk.WriteString(part)
		}
	}

	if currentChunk.Len() > 0 {
		finalChunks = append(finalChunks, currentChunk.String())
	}

	return finalChunks
}

// parseOfficeXML reads an Office Open XML file (DOCX/PPTX) from a ZIP archive.
func parseOfficeXML(path, xmlEntry string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("failed to open Office XML: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == xmlEntry {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("failed to read %s: %w", xmlEntry, err)
			}
			defer rc.Close()
			return extractXMLText(io.LimitReader(rc, MaxIndexFileSize))
		}
	}
	return "", fmt.Errorf("entry %s not found in archive", xmlEntry)
}

// extractXMLText parses an XML document and extracts all text content from <w:t> elements (DOCX)
// or <a:t> elements (PPTX), returning the concatenated text.
func extractXMLText(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var text strings.Builder
	inText := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				text.Write(t)
			}
		}
	}
	return text.String(), nil
}

// readXLSXSharedStrings reads the shared strings table from an XLSX archive.
func readXLSXSharedStrings(files []*zip.File) ([]string, error) {
	for _, f := range files {
		if f.Name == "xl/sharedStrings.xml" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return parseSharedStrings(io.LimitReader(rc, MaxIndexFileSize))
		}
	}
	return nil, fmt.Errorf("shared strings not found")
}

// parseSharedStrings parses the XLSX shared strings XML file.
func parseSharedStrings(r io.Reader) ([]string, error) {
	decoder := xml.NewDecoder(r)
	var strings_ []string
	var current strings.Builder
	inT := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inT = true
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			}
			if t.Name.Local == "si" {
				strings_ = append(strings_, current.String())
				current.Reset()
			}
		case xml.CharData:
			if inT {
				current.Write(t)
			}
		}
	}
	return strings_, nil
}

// extractXLSXSheetText extracts text from a single XLSX worksheet XML file.
func extractXLSXSheetText(f *zip.File, sharedStrings []string) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(io.LimitReader(rc, MaxIndexFileSize))
	var text strings.Builder
	inV := false
	var cellType string
	var cellValue strings.Builder

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "c" {
				cellType = ""
				for _, attr := range t.Attr {
					if attr.Name.Local == "t" {
						cellType = attr.Value
					}
				}
			}
			if t.Name.Local == "v" {
				inV = true
				cellValue.Reset()
			}
		case xml.EndElement:
			if t.Name.Local == "v" {
				inV = false
			}
			if t.Name.Local == "c" {
				val := cellValue.String()
				if cellType == "s" && sharedStrings != nil {
					idx := 0
					fmt.Sscanf(val, "%d", &idx)
					if idx < len(sharedStrings) {
						val = sharedStrings[idx]
					}
				}
				if val != "" {
					text.WriteString(val)
					text.WriteString("\t")
				}
			}
		case xml.CharData:
			if inV {
				cellValue.Write(t)
			}
		}
	}
	return text.String(), nil
}

// extractPPTXSlideText extracts text from a single PPTX slide XML file.
func extractPPTXSlideText(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	decoder := xml.NewDecoder(io.LimitReader(rc, MaxIndexFileSize))
	var text strings.Builder
	inT := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inT = true
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inT = false
			}
			if t.Name.Local == "a:p" || t.Name.Local == "p" {
				text.WriteString("\n")
			}
		case xml.CharData:
			if inT {
				text.Write(t)
			}
		}
	}
	return text.String(), nil
}
