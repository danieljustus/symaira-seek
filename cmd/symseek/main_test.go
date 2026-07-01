package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danieljustus/symaira-seek/internal/config"
	"github.com/danieljustus/symaira-seek/internal/db"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// resetGlobals restores every package-level flag variable to its zero/default
// value so that tests do not leak state through shared globals.
func resetGlobals() {
	cfgFile = ""
	cfg = config.Config{}
	limitFlag = 5
	jsonFlag = false
	tuiFlag = false
	plainFlag = false
	watchFlag = false
	portFlag = 0
	urlFlag = ""
	stdinFlag = false
	sourceFlag = ""
	verboseFlag = false
	quietFlag = false
}

// setupTestEnv sets HOME to a fresh temporary directory and resets all global
// flag variables.  Every sub-command that touches the database or config will
// operate inside this isolated directory.
func setupTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	resetGlobals()
	cfg = *config.DefaultConfig()
}

// freePort asks the OS for a free TCP port on 127.0.0.1 and returns it.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// waitForServer polls until the server accepts TCP connections on addr or the
// deadline expires.  It retries more aggressively than a fixed-sleep loop to
// tolerate the slower goroutine scheduling under the race detector.
func waitForServer(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready within %v", addr, timeout)
}

// captureStdout redirects os.Stdout for the duration of fn and returns the
// captured bytes.  It is NOT safe for concurrent use.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = origStdout
	var buf bytes.Buffer
	io.Copy(&buf, r) //nolint:errcheck
	return buf.String()
}

// ---------------------------------------------------------------------------
// Existing tests (preserved)
// ---------------------------------------------------------------------------

func TestRootCmd_VersionIsSet(t *testing.T) {
	cmd := newRootCmd()
	if cmd.Version == "" {
		t.Error("expected rootCmd.Version to be set")
	}
	if cmd.Version != version {
		t.Errorf("expected rootCmd.Version == %q, got %q", version, cmd.Version)
	}
}

func TestRootCmd_VersionFlag(t *testing.T) {
	cmd := newRootCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--version"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error on --version, got %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, version) {
		t.Errorf("expected output to contain version %q, got %q", version, out)
	}
}

func TestRootCmd_VersionSubcommandStillWorks(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"version"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected no error on 'version' subcommand, got %v", err)
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

// ---------------------------------------------------------------------------
// initConfig tests  (lines 320-332)
// ---------------------------------------------------------------------------

// TestInitConfig_DefaultPath covers lines 321-322: when cfgFile is empty,
// initConfig sets it to config.GlobalPath() and loads defaults (no file exists).
func TestInitConfig_DefaultPath(t *testing.T) {
	setupTestEnv(t)
	cfgFile = ""
	initConfig()

	if cfgFile == "" {
		t.Fatal("cfgFile should have been set to GlobalPath()")
	}
	if cfg.OllamaURL != "http://localhost:11434/api/embeddings" {
		t.Errorf("expected default OllamaURL, got %q", cfg.OllamaURL)
	}
	if cfg.Model != "nomic-embed-text" {
		t.Errorf("expected default model, got %q", cfg.Model)
	}
}

// TestInitConfig_ValidFile covers lines 325, 331: loading a valid TOML file.
func TestInitConfig_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetGlobals()

	cfgDir := filepath.Join(tmpDir, ".config", "symseek")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.toml")
	contents := []byte("ollama_url = \"http://custom:11434/api/embeddings\"\nmodel = \"custom-model\"\ntimeout_seconds = 60\n")
	if err := os.WriteFile(cfgPath, contents, 0600); err != nil {
		t.Fatal(err)
	}

	cfgFile = cfgPath
	initConfig()

	if cfg.OllamaURL != "http://custom:11434/api/embeddings" {
		t.Errorf("expected custom OllamaURL, got %q", cfg.OllamaURL)
	}
	if cfg.Model != "custom-model" {
		t.Errorf("expected custom model, got %q", cfg.Model)
	}
	if cfg.TimeoutSeconds != 60 {
		t.Errorf("expected timeout 60, got %d", cfg.TimeoutSeconds)
	}
}

// TestInitConfig_InvalidFile covers lines 326-329: when the TOML file is
// corrupt, initConfig prints a warning and falls back to defaults.
func TestInitConfig_InvalidFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetGlobals()

	cfgDir := filepath.Join(tmpDir, ".config", "symseek")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("{{{{invalid toml"), 0600); err != nil {
		t.Fatal(err)
	}

	cfgFile = cfgPath
	initConfig()

	// Should fall back to defaults
	if cfg.OllamaURL != "http://localhost:11434/api/embeddings" {
		t.Errorf("expected default OllamaURL after invalid config, got %q", cfg.OllamaURL)
	}
}

// ---------------------------------------------------------------------------
// PersistentPreRun tests  (lines 63-71)
// ---------------------------------------------------------------------------

// TestRootCmd_Verbose covers lines 65-66: verboseFlag sets log level to Debug.
func TestRootCmd_Verbose(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--verbose", "version"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRootCmd_Quiet covers lines 67-68: quietFlag sets log level to Error.
func TestRootCmd_Quiet(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--quiet", "version"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Search command tests  (lines 80-119)
// ---------------------------------------------------------------------------

// TestSearchCmd_NoArgs covers line 83: cobra.ExactArgs(1) rejects no-arg calls.
func TestSearchCmd_NoArgs(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"search"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for search with no args")
	}
}

// TestSearchCmd_PlainOutput covers lines 84-112: search with --plain flag on
// an empty database writes "No matching documents found."
func TestSearchCmd_PlainOutput(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"search", "test query", "--plain"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "No matching documents found.") {
		t.Errorf("expected 'No matching documents found.' in output, got %q", out)
	}
}

// TestSearchCmd_JSONOutput covers lines 100-103: --json flag encodes results
// as JSON to stdout.
func TestSearchCmd_JSONOutput(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"search", "test query", "--json"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	// Empty DB may produce [] or null — both are valid JSON.
	out = strings.TrimSpace(out)
	if out != "[]" && out != "null" {
		t.Errorf("expected JSON array or null in output, got %q", out)
	}
}

// TestSearchCmd_PlainWithLimit covers lines 115-118: --limit flag is parsed.
func TestSearchCmd_PlainWithLimit(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"search", "query", "--plain", "--limit", "10"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "No matching documents found.") {
		t.Errorf("expected no-results message, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Index command tests  (lines 122-169)
// ---------------------------------------------------------------------------

// TestIndexCmd_NoArgsNoFlags covers lines 149-150: no folder path, --url, or
// --stdin returns an error.
func TestIndexCmd_NoArgsNoFlags(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"index"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for index with no args/flags")
	}
	if !strings.Contains(err.Error(), "folder path, --url, or --stdin required") {
		t.Errorf("expected 'folder path' error, got: %v", err)
	}
}

// TestIndexCmd_FolderPath covers lines 126-133, 149-162: index a folder path.
func TestIndexCmd_FolderPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetGlobals()
	cfg = *config.DefaultConfig()

	testDir := filepath.Join(tmpDir, "test-docs")
	if err := os.MkdirAll(testDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "test.md"), []byte("# Test\nHello world"), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"index", testDir})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIndexCmd_URLFlag covers lines 135-138: --url flag triggers URL indexing.
func TestIndexCmd_URLFlag(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	// Use a port that immediately refuses connections so the fetch fails fast.
	cmd.SetArgs([]string{"index", "--url", "http://127.0.0.1:1"})
	err := cmd.Execute()
	// URL fetch is expected to fail — we just need the code path exercised.
	if err == nil {
		t.Log("index --url succeeded (unexpected but acceptable)")
	}
}

// TestIndexCmd_StdinFlag covers lines 140-147: --stdin flag reads from stdin.
func TestIndexCmd_StdinFlag(t *testing.T) {
	setupTestEnv(t)

	// Provide content through a pipe so IndexStdin has data to read.
	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	if _, err := w.Write([]byte("# Test document\nThis is test content for indexing.")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer func() { os.Stdin = origStdin }()

	cmd := newRootCmd()
	cmd.SetArgs([]string{"index", "--stdin"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIndexCmd_StdinFlagWithSource covers lines 141-142: --source flag sets
// the source label when used with --stdin.
func TestIndexCmd_StdinFlagWithSource(t *testing.T) {
	setupTestEnv(t)

	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	if _, err := w.Write([]byte("# Source doc\nSome content here.")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer func() { os.Stdin = origStdin }()

	cmd := newRootCmd()
	cmd.SetArgs([]string{"index", "--stdin", "--source", "my-source"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Delete command tests  (lines 172-201)
// ---------------------------------------------------------------------------

// TestDeleteCmd_NoArgs covers line 175: cobra.ExactArgs(1) rejects no-arg calls.
func TestDeleteCmd_NoArgs(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"delete"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for delete with no args")
	}
}

// TestDeleteCmd_DocNotFound covers lines 176-191: deleting a document that
// does not exist prints a message and returns nil.
func TestDeleteCmd_DocNotFound(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"delete", "/nonexistent/path.md"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDeleteCmd_DocFound covers lines 176-198: indexing then deleting a document.
func TestDeleteCmd_DocFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetGlobals()
	cfg = *config.DefaultConfig()

	// Create a test file and index it.
	testDir := filepath.Join(tmpDir, "docs")
	if err := os.MkdirAll(testDir, 0700); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(testDir, "hello.md")
	if err := os.WriteFile(testFile, []byte("# Hello\nWorld"), 0600); err != nil {
		t.Fatal(err)
	}

	// Index the directory first.
	indexCmd := newRootCmd()
	indexCmd.SetArgs([]string{"index", testDir})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index failed: %v", err)
	}

	// Now delete the indexed document.
	deleteCmd := newRootCmd()
	deleteCmd.SetArgs([]string{"delete", testFile})
	err := deleteCmd.Execute()
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Status command tests  (lines 204-232)
// ---------------------------------------------------------------------------

// TestStatusCmd covers lines 207-228: status on an empty database prints
// zero counts.
func TestStatusCmd(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"status"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "Indexed Documents") {
		t.Errorf("expected 'Indexed Documents' in output, got %q", out)
	}
}

// TestStatusCmd_JSON covers lines 219-223: --json flag outputs JSON stats.
func TestStatusCmd_JSON(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"status", "--json"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "document_count") {
		t.Errorf("expected 'document_count' in JSON output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Config command tests  (lines 235-260)
// ---------------------------------------------------------------------------

// TestConfigCmd_View covers lines 249-254: viewing the config prints JSON.
func TestConfigCmd_View(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cfgFile = filepath.Join(t.TempDir(), ".config", "symseek", "config.toml")
	cmd.SetArgs([]string{"config"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "ollama_url") {
		t.Errorf("expected 'ollama_url' in config output, got %q", out)
	}
}

// TestConfigCmd_JSON covers lines 249, 252-254: --json flag suppresses the
// file path line and outputs JSON only.
func TestConfigCmd_JSON(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cfgFile = filepath.Join(t.TempDir(), ".config", "symseek", "config.toml")
	cmd.SetArgs([]string{"config", "--json"})
	out := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "ollama_url") {
		t.Errorf("expected 'ollama_url' in JSON config output, got %q", out)
	}
}

// TestConfigCmd_SetKey covers lines 242-248: --set-key and --set-value write
// a config value to disk.
func TestConfigCmd_SetKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetGlobals()
	cfg = *config.DefaultConfig()

	cmd := newRootCmd()
	cfgFile = filepath.Join(tmpDir, ".config", "symseek", "config.toml")
	initConfig()

	cmd.SetArgs([]string{"config", "--set-key", "ollama_url", "--set-value", "http://custom:11434"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.OllamaURL != "http://custom:11434" {
		t.Errorf("expected OllamaURL to be 'http://custom:11434', got %q", cfg.OllamaURL)
	}
}

// TestConfigCmd_SetKeyModel covers the "model" key branch in SetValue.
func TestConfigCmd_SetKeyModel(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetGlobals()
	cfg = *config.DefaultConfig()

	cmd := newRootCmd()
	cfgFile = filepath.Join(tmpDir, ".config", "symseek", "config.toml")
	initConfig()

	cmd.SetArgs([]string{"config", "--set-key", "model", "--set-value", "my-embed-model"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != "my-embed-model" {
		t.Errorf("expected model 'my-embed-model', got %q", cfg.Model)
	}
}

// TestConfigCmd_SetKeyTimeout covers the "timeout_seconds" key branch.
func TestConfigCmd_SetKeyTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	resetGlobals()
	cfg = *config.DefaultConfig()

	cmd := newRootCmd()
	cfgFile = filepath.Join(tmpDir, ".config", "symseek", "config.toml")
	initConfig()

	cmd.SetArgs([]string{"config", "--set-key", "timeout_seconds", "--set-value", "30"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TimeoutSeconds != 30 {
		t.Errorf("expected timeout_seconds=30, got %d", cfg.TimeoutSeconds)
	}
}

// TestConfigCmd_SetKeyInvalidValue covers the error branch for invalid integer
// values in SetValue.
func TestConfigCmd_SetKeyInvalidValue(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cfgFile = filepath.Join(t.TempDir(), ".config", "symseek", "config.toml")

	cmd.SetArgs([]string{"config", "--set-key", "timeout_seconds", "--set-value", "not-a-number"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid integer value")
	}
}

// TestConfigCmd_SetKeyUnknownKey covers the default (unknown key) branch in
// SetValue.
func TestConfigCmd_SetKeyUnknownKey(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cfgFile = filepath.Join(t.TempDir(), ".config", "symseek", "config.toml")

	cmd.SetArgs([]string{"config", "--set-key", "unknown_key", "--set-value", "val"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown config key")
	}
}

// ---------------------------------------------------------------------------
// Migrate command tests  (lines 263-272)
// ---------------------------------------------------------------------------

// TestMigrateCmd covers lines 266-270: migrate runs without error.
func TestMigrateCmd(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"migrate"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Version command tests  (lines 275-299)
// ---------------------------------------------------------------------------

// TestVersionCmd_Check covers lines 281-295: --check flag triggers an update
// check.  The network call may fail, but the version string is always printed
// before the check runs.
func TestVersionCmd_Check(t *testing.T) {
	setupTestEnv(t)
	cmd := newRootCmd()
	cmd.SetArgs([]string{"version", "--check"})
	out := captureStdout(t, func() {
		_ = cmd.Execute()
	})
	if !strings.Contains(out, "symseek version") {
		t.Errorf("expected output to contain 'symseek version', got %q", out)
	}
	if !strings.Contains(out, version) {
		t.Errorf("expected output to contain version %q, got %q", version, out)
	}
}

// ---------------------------------------------------------------------------
// Serve command tests  (lines 302-315)
// ---------------------------------------------------------------------------

func TestServeCmd_HTTP(t *testing.T) {
	setupTestEnv(t)
	port := freePort(t)

	done := make(chan error, 1)
	go func() {
		cmd := newRootCmd()
		cmd.SetArgs([]string{"serve", "--port", portStr(port)})
		done <- cmd.Execute()
	}()

	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
}

// TestServeCmd_MCP covers lines 310-311: no --port starts the MCP server.
// Redirecting stdin to EOF makes ServeStdio return immediately.
func TestServeCmd_MCP(t *testing.T) {
	setupTestEnv(t)

	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.Close() // immediate EOF
	defer func() { os.Stdin = origStdin }()

	cmd := newRootCmd()
	cmd.SetArgs([]string{"serve"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// startHTTPServer tests  (lines 334-340)
// ---------------------------------------------------------------------------

func TestStartHTTPServer_DefaultCooldown(t *testing.T) {
	setupTestEnv(t)
	cfg = *config.DefaultConfig()
	cfg.IndexCooldownSeconds = 0

	port := freePort(t)
	done := make(chan error, 1)
	go func() {
		done <- startHTTPServer(port)
	}()

	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
}

func TestStartHTTPServer_CustomCooldown(t *testing.T) {
	setupTestEnv(t)
	cfg = *config.DefaultConfig()
	cfg.IndexCooldownSeconds = 10

	port := freePort(t)
	done := make(chan error, 1)
	go func() {
		done <- startHTTPServer(port)
	}()

	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
}

// ---------------------------------------------------------------------------
// startMCPServer tests  (lines 342-345)
// ---------------------------------------------------------------------------

// TestStartMCPServer covers lines 343-344: sets ServerVersion and starts the
// MCP server.  With stdin at EOF, ServeStdio returns nil immediately.
func TestStartMCPServer(t *testing.T) {
	setupTestEnv(t)
	cfg = *config.DefaultConfig()

	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.Close()
	defer func() { os.Stdin = origStdin }()

	// Redirect stdout to /dev/null so MCP JSON-RPC output does not pollute
	// test output.
	origStdout := os.Stdout
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devNull
	defer func() {
		os.Stdout = origStdout
		devNull.Close()
	}()

	if err := startMCPServer(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers (internal)
// ---------------------------------------------------------------------------

func portStr(port int) string {
	return fmt.Sprintf("%d", port)
}
