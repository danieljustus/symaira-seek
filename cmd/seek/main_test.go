package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-seek/internal/db"
)

func TestLoadOrInitConfig_MissingFileWritesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := loadOrInitConfig(path)

	if cfg != defaultConfig() {
		t.Errorf("expected defaults, got %+v", cfg)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected defaults to be written: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 permissions, got %v", info.Mode().Perm())
	}
}

func TestLoadOrInitConfig_ValidFileParsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	written := Config{
		OllamaURL: "http://example.test/api/embeddings",
		Model:     "test-model",
	}
	data, err := json.MarshalIndent(written, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := loadOrInitConfig(path)

	if cfg != written {
		t.Errorf("expected %+v, got %+v", written, cfg)
	}
}

func TestLoadOrInitConfig_MalformedFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	stderr := captureStderr(t, func() {
		cfg := loadOrInitConfig(path)
		if cfg != defaultConfig() {
			t.Errorf("expected defaults on malformed config, got %+v", cfg)
		}
	})

	if !strings.Contains(stderr, "malformed") {
		t.Errorf("expected stderr to mention malformed, got %q", stderr)
	}
	if !strings.Contains(stderr, path) {
		t.Errorf("expected stderr to mention path %q, got %q", path, stderr)
	}
}

func TestLoadOrInitConfig_UnreadableFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	stderr := captureStderr(t, func() {
		cfg := loadOrInitConfig(path)
		if cfg != defaultConfig() {
			t.Errorf("expected defaults when read fails, got %+v", cfg)
		}
	})

	if !strings.Contains(stderr, "could not read") {
		t.Errorf("expected stderr to mention read failure, got %q", stderr)
	}
}

func TestLoadOrInitConfig_UnwritablePathFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(parent, []byte("blocker"), 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	path := filepath.Join(parent, "config.json")

	stderr := captureStderr(t, func() {
		cfg := loadOrInitConfig(path)
		if cfg != defaultConfig() {
			t.Errorf("expected defaults when I/O fails, got %+v", cfg)
		}
	})

	if !strings.Contains(stderr, "could not read") && !strings.Contains(stderr, "could not write") {
		t.Errorf("expected stderr to report read or write failure, got %q", stderr)
	}
}

func TestDefaultConfig_StableValues(t *testing.T) {
	got := defaultConfig()
	if got.OllamaURL == "" {
		t.Error("default OllamaURL must be non-empty")
	}
	if got.Model == "" {
		t.Error("default Model must be non-empty")
	}
	if got != defaultConfig() {
		t.Error("defaultConfig must be deterministic")
	}
}

func TestWriteSearchHuman_EmptyResults(t *testing.T) {
	var buf bytes.Buffer
	writeSearchHuman(&buf, nil)
	if !strings.Contains(buf.String(), "No matching documents found.") {
		t.Errorf("expected empty-results message, got %q", buf.String())
	}
}

func TestWriteSearchHuman_OneResultRendersToWriter(t *testing.T) {
	var buf bytes.Buffer
	results := []*db.SearchResult{
		{
			Chunk: &db.Chunk{
				DocumentPath: "/docs/a.md",
				ChunkIndex:   0,
				Content:      "first line\nsecond line",
			},
			RRFScore:    0.0123,
			CosineScore: 0.9876,
			BM25Rank:    1,
			VectorRank:  2,
		},
	}
	writeSearchHuman(&buf, results)

	out := buf.String()
	if !strings.Contains(out, "/docs/a.md") {
		t.Errorf("expected path in output, got %q", out)
	}
	if !strings.Contains(out, "RRF=0.0123") {
		t.Errorf("expected RRF score in output, got %q", out)
	}
	if !strings.Contains(out, "first line") || !strings.Contains(out, "second line") {
		t.Errorf("expected chunk content lines in output, got %q", out)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	fn()

	w.Close()
	os.Stderr = old
	return <-done
}
