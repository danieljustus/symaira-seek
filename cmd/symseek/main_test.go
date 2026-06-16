package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-seek/internal/db"
)

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

func TestWriteSearchHuman_MultipleResults(t *testing.T) {
	var buf bytes.Buffer
	results := []*db.SearchResult{
		{
			Chunk: &db.Chunk{
				DocumentPath: "/docs/a.md",
				ChunkIndex:   0,
				Content:      "first document",
			},
			RRFScore:    0.1,
			CosineScore: 0.9,
			BM25Rank:    1,
			VectorRank:  1,
		},
		{
			Chunk: &db.Chunk{
				DocumentPath: "/docs/b.md",
				ChunkIndex:   0,
				Content:      "second document",
			},
			RRFScore:    0.05,
			CosineScore: 0.8,
			BM25Rank:    2,
			VectorRank:  2,
		},
	}
	writeSearchHuman(&buf, results)

	out := buf.String()
	if !strings.Contains(out, "/docs/a.md") {
		t.Errorf("expected first path in output, got %q", out)
	}
	if !strings.Contains(out, "/docs/b.md") {
		t.Errorf("expected second path in output, got %q", out)
	}
	if !strings.Contains(out, "first document") {
		t.Errorf("expected first document content in output, got %q", out)
	}
	if !strings.Contains(out, "second document") {
		t.Errorf("expected second document content in output, got %q", out)
	}
}

func TestWriteSearchHuman_ScoresFormatting(t *testing.T) {
	var buf bytes.Buffer
	results := []*db.SearchResult{
		{
			Chunk: &db.Chunk{
				DocumentPath: "/test.md",
				ChunkIndex:   0,
				Content:      "test content",
			},
			RRFScore:    0.123456,
			CosineScore: 0.987654,
			BM25Rank:    3,
			VectorRank:  5,
		},
	}
	writeSearchHuman(&buf, results)

	out := buf.String()
	if !strings.Contains(out, "RRF=0.1235") {
		t.Errorf("expected RRF score in output, got %q", out)
	}
	if !strings.Contains(out, "Cosine=0.9877") {
		t.Errorf("expected Cosine score in output, got %q", out)
	}
	if !strings.Contains(out, "BM25=3") {
		t.Errorf("expected BM25 rank in output, got %q", out)
	}
	if !strings.Contains(out, "Vector=5") {
		t.Errorf("expected Vector rank in output, got %q", out)
	}
}
