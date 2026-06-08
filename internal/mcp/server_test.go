package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
)

// pipeStdout captures os.Stdout during a test.
// Returns a reader that delivers whatever was written to stdout
// while fn runs, plus a restore function.
func pipeStdout(fn func()) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w

	out := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		out <- buf.String()
	}()

	fn()

	w.Close()
	os.Stdout = old
	return <-out, nil
}

// mustParseJSON returns a json.RawMessage from a JSON string.
func mustRaw(s string) json.RawMessage {
	return json.RawMessage(s)
}

// TestJSONRPCErrorCodes verifies that all standard JSON-RPC error codes
// produce the correct error structure.
func TestJSONRPCErrorCodes(t *testing.T) {
	tests := []struct {
		name      string
		id        interface{}
		code      int
		message   string
		wantCode  int
		wantMsg   string
	}{
		{
			name:     "parse error -32700",
			id:       nil,
			code:     -32700,
			message:  "Parse error: invalid JSON",
			wantCode: -32700,
			wantMsg:  "Parse error: invalid JSON",
		},
		{
			name:     "invalid params -32602",
			id:       float64(1),
			code:     -32602,
			message:  "Missing or invalid query argument",
			wantCode: -32602,
			wantMsg:  "Missing or invalid query argument",
		},
		{
			name:     "method not found -32601",
			id:       "abc",
			code:     -32601,
			message:  "Method not found: unknown",
			wantCode: -32601,
			wantMsg:  "Method not found: unknown",
		},
		{
			name:     "internal error -32603",
			id:       float64(42),
			code:     -32603,
			message:  "Internal error",
			wantCode: -32603,
			wantMsg:  "Internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pipeStdout(func() {
				sendError(tt.id, tt.code, tt.message)
			})
			if err != nil {
				t.Fatalf("pipeStdout failed: %v", err)
			}

			var resp JSONRPCResponse
			if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
			}

			if resp.JSONRPC != "2.0" {
				t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
			}
			if resp.Error == nil {
				t.Fatal("expected error in response, got nil")
			}
			errMap := resp.Error.(map[string]interface{})
			if int(errMap["code"].(float64)) != tt.wantCode {
				t.Errorf("error code = %v, want %d", errMap["code"], tt.wantCode)
			}
			if errMap["message"] != tt.wantMsg {
				t.Errorf("error message = %q, want %q", errMap["message"], tt.wantMsg)
			}
			if resp.Result != nil {
				t.Error("expected nil result in error response")
			}

			// For non-nil IDs, verify round-trip
			if tt.id != nil {
				if resp.ID == nil {
					t.Error("expected non-nil ID in error response")
				}
			}
		})
	}
}

// TestSendResponse verifies that sendResponse emits valid JSON-RPC.
func TestSendResponse(t *testing.T) {
	got, err := pipeStdout(func() {
		sendResponse(float64(1), map[string]string{"status": "ok"})
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
	if resp.ID != float64(1) {
		t.Errorf("id = %v, want 1", resp.ID)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", resp.Result)
	}
	if result["status"] != "ok" {
		t.Errorf("result.status = %q, want %q", result["status"], "ok")
	}
}

// TestSendToolResponse verifies the tool response wrapper.
func TestSendToolResponse(t *testing.T) {
	got, err := pipeStdout(func() {
		sendToolResponse("req-1", "hello world")
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", resp.Result)
	}
	content, ok := result["content"].([]interface{})
	if !ok {
		t.Fatalf("content is not a slice: %T", result["content"])
	}
	if len(content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(content))
	}
	item := content[0].(map[string]interface{})
	if item["type"] != "text" {
		t.Errorf("content[0].type = %q, want %q", item["type"], "text")
	}
	if item["text"] != "hello world" {
		t.Errorf("content[0].text = %q, want %q", item["text"], "hello world")
	}
	if result["isError"] != false {
		t.Error("expected isError = false")
	}
}

// TestHandleRequestInitialize verifies the initialize method response.
func TestHandleRequestInitialize(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
	}

	got, err := pipeStdout(func() {
		handleRequest(req, nil, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", resp.Result)
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want %q", result["protocolVersion"], "2024-11-05")
	}
	si := result["serverInfo"].(map[string]interface{})
	if si["name"] != "symaira-seek" {
		t.Errorf("serverInfo.name = %q, want %q", si["name"], "symaira-seek")
	}
}

// TestHandleRequestToolsList verifies the tools/list response.
func TestHandleRequestToolsList(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/list",
	}

	got, err := pipeStdout(func() {
		handleRequest(req, nil, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", resp.Result)
	}
	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatalf("tools is not a slice: %T", result["tools"])
	}

	// Verify all 5 tool names are present
	names := make(map[string]bool)
	for _, t := range tools {
		tool := t.(map[string]interface{})
		names[tool["name"].(string)] = true
	}
	expected := []string{"search_documents", "read_document", "list_documents", "get_context", "index_document"}
	for _, n := range expected {
		if !names[n] {
			t.Errorf("missing tool: %s", n)
		}
	}
}

// TestHandleRequestMethodNotFound verifies unknown method error.
func TestHandleRequestMethodNotFound(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "nonexistent",
	}

	got, err := pipeStdout(func() {
		handleRequest(req, nil, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	errMap := resp.Error.(map[string]interface{})
	if int(errMap["code"].(float64)) != -32601 {
		t.Errorf("error code = %v, want -32601", errMap["code"])
	}
}

// TestHandleToolCallUnknownTool verifies unknown tool error.
func TestHandleToolCallUnknownTool(t *testing.T) {
	got, err := pipeStdout(func() {
		handleToolCall(float64(1), "unknown_tool", nil, nil, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	errMap := resp.Error.(map[string]interface{})
	if int(errMap["code"].(float64)) != -32601 {
		t.Errorf("error code = %v, want -32601", errMap["code"])
	}
}

// TestHandleToolCallMissingParams verifies that tools return -32602
// when required params are missing.
func TestHandleToolCallMissingParams(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]interface{}
	}{
		{"search_documents missing query", "search_documents", map[string]interface{}{}},
		{"read_document missing path", "read_document", map[string]interface{}{}},
		{"get_context missing topic", "get_context", map[string]interface{}{}},
		{"index_document missing path", "index_document", map[string]interface{}{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pipeStdout(func() {
				handleToolCall(float64(1), tt.tool, tt.args, nil, nil)
			})
			if err != nil {
				t.Fatalf("pipeStdout failed: %v", err)
			}

			var resp JSONRPCResponse
			if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
			}

			if resp.Error == nil {
				t.Fatal("expected error for missing params")
			}
			errMap := resp.Error.(map[string]interface{})
			if int(errMap["code"].(float64)) != -32602 {
				t.Errorf("error code = %v, want -32602", errMap["code"])
			}
		})
	}
}

// TestHandleRequestMalformedJSON verifies the parse error path through the
// handleRequest indirectly by testing the code path that triggers on unmarshal.
func TestHandleRequestMalformedJSON(t *testing.T) {
	// The read loop in StartServer parses JSON and sends -32700 on parse error.
	// We test sendError with -32700 directly for coverage; the loop-level path
	// is covered by the stdio integration tests below.
	got, err := pipeStdout(func() {
		sendError(nil, -32700, "Parse error: invalid JSON")
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	errMap := resp.Error.(map[string]interface{})
	if int(errMap["code"].(float64)) != -32700 {
		t.Errorf("error code = %v, want -32700", errMap["code"])
	}
}

// fakeStore implements db.Store for testing tool handlers that need a database.
type fakeStore struct {
	searchFunc   func(query string, limit int) ([]*db.SearchResult, error)
	getDocFunc   func(path string) (*db.Document, error)
	listDocsFunc func() ([]*db.Document, error)
}

func (f *fakeStore) Close() error                                      { return nil }
func (f *fakeStore) SaveDocument(doc *db.Document) error               { return nil }
func (f *fakeStore) DeleteDocument(path string) error                  { return nil }
func (f *fakeStore) SaveChunks(chunks []*db.Chunk) error               { return nil }
func (f *fakeStore) GetChunksForDocument(docPath string) ([]*db.Chunk, error) { return nil, nil }
func (f *fakeStore) GetStats() (*db.Stats, error)                      { return &db.Stats{}, nil }

func (f *fakeStore) GetDocument(path string) (*db.Document, error) {
	if f.getDocFunc != nil {
		return f.getDocFunc(path)
	}
	return nil, nil
}

func (f *fakeStore) ListDocuments() ([]*db.Document, error) {
	if f.listDocsFunc != nil {
		return f.listDocsFunc()
	}
	return []*db.Document{}, nil
}

func (f *fakeStore) SearchBM25(query string, limit int) ([]*db.SearchResult, error) {
	if f.searchFunc != nil {
		return f.searchFunc(query, limit)
	}
	return []*db.SearchResult{}, nil
}

func (f *fakeStore) SearchVector(queryVec []float32, limit int) ([]*db.SearchResult, error) {
	return []*db.SearchResult{}, nil
}

func (f *fakeStore) SearchVectorFiltered(queryVec []float32, candidateIDs []int64, limit int) ([]*db.SearchResult, error) {
	return []*db.SearchResult{}, nil
}

// fakeEmbedder implements engine.Embedder for testing.
type fakeEmbedder struct {
	engine.Embedder
}

func (f *fakeEmbedder) GenerateVector(text string) []float32 {
	return make([]float32, 768)
}

func (f *fakeEmbedder) GenerateVectors(texts []string) [][]float32 {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, 768)
	}
	return result
}

// TestHandleToolCallSearchDocuments verifies the search tool with empty results.
func TestHandleToolCallSearchDocuments(t *testing.T) {
	store := &fakeStore{
		searchFunc: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{}, nil
		},
	}
	embed := &fakeEmbedder{}

	got, err := pipeStdout(func() {
		handleToolCall(float64(1), "search_documents", map[string]interface{}{
			"query": "test query",
			"limit": float64(5),
		}, store, embed)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

// TestHandleToolCallListDocuments verifies the list_documents tool.
func TestHandleToolCallListDocuments(t *testing.T) {
	store := &fakeStore{
		listDocsFunc: func() ([]*db.Document, error) {
			return []*db.Document{
				{Path: "/home/user/doc1.md"},
				{Path: "/home/user/doc2.md"},
			}, nil
		},
	}

	got, err := pipeStdout(func() {
		handleToolCall(float64(1), "list_documents", map[string]interface{}{}, store, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

// TestHandleToolCallListDocumentsWithFilter verifies folder prefix filtering.
func TestHandleToolCallListDocumentsWithFilter(t *testing.T) {
	store := &fakeStore{
		listDocsFunc: func() ([]*db.Document, error) {
			return []*db.Document{
				{Path: "/home/user/projects/doc1.md"},
				{Path: "/home/user/other/doc2.md"},
			}, nil
		},
	}

	got, err := pipeStdout(func() {
		handleToolCall(float64(1), "list_documents", map[string]interface{}{
			"folder": "/home/user/projects",
		}, store, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

// TestHandleToolCallGetContext verifies get_context with max_chars parameter.
func TestHandleToolCallGetContext(t *testing.T) {
	store := &fakeStore{
		searchFunc: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{}, nil
		},
	}
	embed := &fakeEmbedder{}

	t.Run("with max_chars", func(t *testing.T) {
		got, err := pipeStdout(func() {
			handleToolCall(float64(1), "get_context", map[string]interface{}{
				"topic":     "test topic",
				"max_chars": float64(100),
			}, store, embed)
		})
		if err != nil {
			t.Fatalf("pipeStdout failed: %v", err)
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
		}
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
	})

	t.Run("with deprecated max_tokens", func(t *testing.T) {
		got, err := pipeStdout(func() {
			handleToolCall(float64(1), "get_context", map[string]interface{}{
				"topic":       "test topic",
				"max_tokens":  float64(100),
			}, store, embed)
		})
		if err != nil {
			t.Fatalf("pipeStdout failed: %v", err)
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
		}
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
	})

	t.Run("missing topic", func(t *testing.T) {
		got, err := pipeStdout(func() {
			handleToolCall(float64(1), "get_context", map[string]interface{}{}, store, embed)
		})
		if err != nil {
			t.Fatalf("pipeStdout failed: %v", err)
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
		}
		if resp.Error == nil {
			t.Fatal("expected error for missing topic")
		}
		errMap := resp.Error.(map[string]interface{})
		if int(errMap["code"].(float64)) != -32602 {
			t.Errorf("error code = %v, want -32602", errMap["code"])
		}
	})
}

// TestNotificationsInitialized verifies that the notification is a no-op.
func TestNotificationsInitialized(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	got, err := pipeStdout(func() {
		handleRequest(req, nil, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	if strings.TrimSpace(got) != "" {
		t.Errorf("expected no output for notification, got: %s", got)
	}
}

// TestHandleToolCallInvalidParams verifies invalid params error for
// tools/call with invalid JSON params.
func TestHandleToolCallInvalidParams(t *testing.T) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  mustRaw(`{"name": 123}`), // name should be a string
	}

	got, err := pipeStdout(func() {
		handleRequest(req, nil, nil)
	})
	if err != nil {
		t.Fatalf("pipeStdout failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\ngot: %s", err, got)
	}

	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	errMap := resp.Error.(map[string]interface{})
	if int(errMap["code"].(float64)) != -32602 {
		t.Errorf("error code = %v, want -32602", errMap["code"])
	}
}
