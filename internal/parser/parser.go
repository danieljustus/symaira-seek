package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

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
func ParseFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return string(data), nil
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
