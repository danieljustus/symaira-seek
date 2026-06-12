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
