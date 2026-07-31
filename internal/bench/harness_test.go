package bench

import "testing"

// TestDeriveChunkID_Deterministic verifies that identical inputs produce the
// same chunk ID across calls, keeping bench runs comparable (issue #302).
func TestDeriveChunkID_Deterministic(t *testing.T) {
	a := deriveChunkID("doc-a", "hash-a", 0)
	b := deriveChunkID("doc-a", "hash-a", 0)
	if a != b {
		t.Errorf("expected deterministic chunk ID, got %q vs %q", a, b)
	}
}

// TestDeriveChunkID_DistinctInputs verifies that different documents, hashes,
// and offsets never collide.
func TestDeriveChunkID_DistinctInputs(t *testing.T) {
	seen := map[string]string{}
	inputs := [][3]any{
		{"doc-a", "hash-a", 0},
		{"doc-b", "hash-a", 0},
		{"doc-a", "hash-b", 0},
		{"doc-a", "hash-a", 1},
		{"doc-a", "hash-a", 10},
	}
	for _, in := range inputs {
		path := in[0].(string)
		hash := in[1].(string)
		start := in[2].(int)
		id := deriveChunkID(path, hash, start)
		if prev, ok := seen[id]; ok {
			t.Errorf("chunk ID collision: %q for both %q and %q", id, prev, in)
		}
		seen[id] = path
	}
	if len(seen) != len(inputs) {
		t.Errorf("expected %d unique IDs, got %d", len(inputs), len(seen))
	}
}

// TestDeriveChunkID_IsUUIDv5 verifies the output has UUID shape, matching the
// chunks.uuid column format used by the indexer.
func TestDeriveChunkID_IsUUIDv5(t *testing.T) {
	id := deriveChunkID("doc-a", "hash-a", 0)
	if len(id) != 36 {
		t.Fatalf("expected 36-char UUID, got %q (%d chars)", id, len(id))
	}
	// UUIDv5: version nibble is 5 in the third group.
	if id[14] != '5' {
		t.Errorf("expected UUIDv5 (version 5), got %q", id)
	}
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Errorf("expected dashed UUID format, got %q", id)
	}
}
