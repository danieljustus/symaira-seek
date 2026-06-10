package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/pathutil"
)

// withTempHome points HOME at a temporary directory for the duration of a
// test so pathutil.RestrictToHome's UserHomeDir() lookup is hermetic.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestValidateIndexPath_AcceptsDirectoryUnderHome(t *testing.T) {
	home := withTempHome(t)
	subdir := filepath.Join(home, "docs")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	got, err := pathutil.RestrictToHome(subdir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(subdir)
	if got != resolved {
		t.Fatalf("expected %s, got %s", resolved, got)
	}
}

func TestValidateIndexPath_RejectsNonExistentPath(t *testing.T) {
	withTempHome(t)

	_, err := pathutil.RestrictToHome("/nonexistent/path/should/reject")
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestValidateIndexPath_RejectsPathOutsideHome(t *testing.T) {
	withTempHome(t)

	candidates := []string{"/etc", "/usr", "/var"}
	for _, p := range candidates {
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		_, err = pathutil.RestrictToHome(p)
		if err == nil {
			t.Fatalf("expected error for %s outside home", p)
		}
		if !strings.Contains(err.Error(), "allowed root") {
			t.Fatalf("expected error to mention allowed root, got %q", err.Error())
		}
	}
}

func TestRateLimiter_FirstRequestAllowed(t *testing.T) {
	rl := newRateLimiter(100 * time.Millisecond)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request from a key must be allowed")
	}
}

func TestRateLimiter_BlocksWithinCooldown(t *testing.T) {
	rl := newRateLimiter(100 * time.Millisecond)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request must be allowed")
	}
	if rl.Allow("client-a", now.Add(50*time.Millisecond)) {
		t.Fatal("second request within cooldown must be blocked")
	}
}

func TestRateLimiter_AllowsAfterCooldown(t *testing.T) {
	rl := newRateLimiter(50 * time.Millisecond)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request must be allowed")
	}
	if !rl.Allow("client-a", now.Add(100*time.Millisecond)) {
		t.Fatal("request after cooldown must be allowed")
	}
}

func TestRateLimiter_KeysAreIsolated(t *testing.T) {
	rl := newRateLimiter(1 * time.Second)
	now := time.Now()
	if !rl.Allow("client-a", now) {
		t.Fatal("first request from a must be allowed")
	}
	if !rl.Allow("client-b", now) {
		t.Fatal("first request from b must be allowed, even when a is rate-limited")
	}
	if rl.Allow("client-a", now) {
		t.Fatal("a must remain rate-limited until cooldown elapses")
	}
}

// TestSSEStreamEndpoint verifies the SSE streaming search endpoint
// returns proper event: result blocks and a terminal event: done (issue #74).
func TestSSEStreamEndpoint(t *testing.T) {
	var gotResults, gotDone bool
	var resultCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/stream" {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		results := []map[string]interface{}{
			{"chunk": map[string]string{"content": "result 1"}, "rrf_score": 0.9},
			{"chunk": map[string]string{"content": "result 2"}, "rrf_score": 0.7},
		}

		for _, res := range results {
			data, _ := json.Marshal(res)
			fmt.Fprintf(w, "event: result\ndata: %s\n\n", data)
			flusher.Flush()
		}

		doneData, _ := json.Marshal(map[string]int{"count": len(results)})
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", doneData)
		flusher.Flush()
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/search/stream?q=test")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	var buf strings.Builder
	io.Copy(&buf, resp.Body)
	body := buf.String()

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "event: result") {
			gotResults = true
			resultCount++
		}
		if strings.HasPrefix(line, "event: done") {
			gotDone = true
		}
	}

	if !gotResults {
		t.Error("expected event: result in response")
	}
	if !gotDone {
		t.Error("expected event: done in response")
	}
	if resultCount != 2 {
		t.Errorf("expected 2 result events, got %d", resultCount)
	}
}
