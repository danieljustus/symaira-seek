package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danieljustus/symaira-corekit/mcpserver"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
)

type fakeStore struct {
	searchFunc   func(query string, limit int) ([]*db.SearchResult, error)
	getDocFunc   func(path string) (*db.Document, error)
	listDocsFunc func() ([]*db.Document, error)
}

func (f *fakeStore) Close() error                                             { return nil }
func (f *fakeStore) SaveDocument(doc *db.Document) error                      { return nil }
func (f *fakeStore) DeleteDocument(path string) error                         { return nil }
func (f *fakeStore) SaveChunks(chunks []*db.Chunk) error                      { return nil }
func (f *fakeStore) GetChunksForDocument(docPath string) ([]*db.Chunk, error) { return nil, nil }
func (f *fakeStore) GetStats() (*db.Stats, error)                             { return &db.Stats{}, nil }

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

type fakeEmbedder struct{}

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

func (f *fakeEmbedder) GenerateVectorNoRetry(text string) []float32 {
	return f.GenerateVector(text)
}

func newTestServer(store db.Store, embedder engine.Embedder) *mcpserver.Server {
	ServerVersion = "test-version"
	s := mcpserver.New("symseek", ServerVersion)
	registerSearchDocuments(s, store, embedder)
	registerReadDocument(s, store, embedder)
	registerListDocuments(s, store, embedder)
	registerGetContext(s, store, embedder)
	registerIndexDocument(s, store, embedder)
	registerIndexURL(s, store, embedder)
	return s
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func pipeRequest(t *testing.T, server *mcpserver.Server, req jsonRPCRequest) jsonRPCResponse {
	t.Helper()

	reqData, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	var input strings.Builder
	fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(reqData), reqData)

	var stdout bytes.Buffer
	err = server.ServeIO(context.Background(), strings.NewReader(input.String()), &stdout)
	if err != nil && err != io.EOF {
		t.Logf("ServeIO returned: %v", err)
	}

	br := bufio.NewReader(&stdout)
	var contentLength int
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read response header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if rest, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			fmt.Sscanf(strings.TrimSpace(rest), "%d", &contentLength)
		}
	}

	if contentLength <= 0 {
		t.Fatalf("invalid content length: %d", contentLength)
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(br, body); err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v\nbody: %s", err, body)
	}
	return resp
}

func TestServerInitialize(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "initialize",
	})

	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is not a map: %T", resp.Result)
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want %q", result["protocolVersion"], "2024-11-05")
	}
	si := result["serverInfo"].(map[string]interface{})
	if si["name"] != "symseek" {
		t.Errorf("serverInfo.name = %q, want %q", si["name"], "symseek")
	}
	if si["version"] != "test-version" {
		t.Errorf("serverInfo.version = %q, want %q", si["version"], "test-version")
	}
}

func TestServerToolsList(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/list",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	result := resp.Result.(map[string]interface{})
	tools := result["tools"].([]interface{})

	names := make(map[string]bool)
	for _, tool := range tools {
		toolMap := tool.(map[string]interface{})
		names[toolMap["name"].(string)] = true
	}
	expected := []string{"search_documents", "read_document", "list_documents", "get_context", "index_document", "index_url"}
	for _, n := range expected {
		if !names[n] {
			t.Errorf("missing tool: %s", n)
		}
	}
}

func TestServerMethodNotFound(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "nonexistent",
	})

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	errMap := resp.Error.(map[string]interface{})
	if int(errMap["code"].(float64)) != -32601 {
		t.Errorf("error code = %v, want -32601", errMap["code"])
	}
}

func TestServerUnknownTool(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name":      "unknown_tool",
		"arguments": map[string]interface{}{},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	errMap := resp.Error.(map[string]interface{})
	if int(errMap["code"].(float64)) != -32601 {
		t.Errorf("error code = %v, want -32601", errMap["code"])
	}
}

func TestServerInvalidParams(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name": 123}`),
	})

	if resp.Error == nil {
		t.Fatal("expected error for invalid params")
	}
	errMap := resp.Error.(map[string]interface{})
	if int(errMap["code"].(float64)) != -32602 {
		t.Errorf("error code = %v, want -32602", errMap["code"])
	}
}

func TestServerNotificationsInitialized(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	var stdout bytes.Buffer
	reqData, _ := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(reqData), reqData)

	err := server.ServeIO(context.Background(), strings.NewReader(input), &stdout)
	if err != nil && err != io.EOF {
		t.Logf("ServeIO returned: %v", err)
	}

	if stdout.Len() != 0 {
		t.Errorf("expected no output for notification, got: %s", stdout.String())
	}
}

func TestServerSearchDocuments(t *testing.T) {
	store := &fakeStore{
		searchFunc: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{}, nil
		},
	}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name": "search_documents",
		"arguments": map[string]interface{}{
			"query": "test query",
			"limit": float64(5),
		},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestServerSearchDocumentsMissingQuery(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name":      "search_documents",
		"arguments": map[string]interface{}{},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp.Error)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	if result["isError"] != true {
		t.Fatal("expected isError=true for missing query")
	}
}

func TestServerListDocuments(t *testing.T) {
	store := &fakeStore{
		listDocsFunc: func() ([]*db.Document, error) {
			return []*db.Document{
				{Path: "/home/user/doc1.md"},
				{Path: "/home/user/doc2.md"},
			}, nil
		},
	}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name":      "list_documents",
		"arguments": map[string]interface{}{},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestServerListDocumentsWithFilter(t *testing.T) {
	store := &fakeStore{
		listDocsFunc: func() ([]*db.Document, error) {
			return []*db.Document{
				{Path: "/home/user/projects/doc1.md"},
				{Path: "/home/user/other/doc2.md"},
			}, nil
		},
	}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name": "list_documents",
		"arguments": map[string]interface{}{
			"folder": "/home/user/projects",
		},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestServerGetContext(t *testing.T) {
	store := &fakeStore{
		searchFunc: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{}, nil
		},
	}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	t.Run("with max_chars", func(t *testing.T) {
		params, _ := json.Marshal(map[string]interface{}{
			"name": "get_context",
			"arguments": map[string]interface{}{
				"topic":     "test topic",
				"max_chars": float64(100),
			},
		})
		resp := pipeRequest(t, server, jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      float64(1),
			Method:  "tools/call",
			Params:  params,
		})
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
	})

	t.Run("with deprecated max_tokens", func(t *testing.T) {
		params, _ := json.Marshal(map[string]interface{}{
			"name": "get_context",
			"arguments": map[string]interface{}{
				"topic":      "test topic",
				"max_tokens": float64(100),
			},
		})
		resp := pipeRequest(t, server, jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      float64(1),
			Method:  "tools/call",
			Params:  params,
		})
		if resp.Error != nil {
			t.Fatalf("unexpected error: %v", resp.Error)
		}
	})

	t.Run("missing topic", func(t *testing.T) {
		params, _ := json.Marshal(map[string]interface{}{
			"name":      "get_context",
			"arguments": map[string]interface{}{},
		})
		resp := pipeRequest(t, server, jsonRPCRequest{
			JSONRPC: "2.0",
			ID:      float64(1),
			Method:  "tools/call",
			Params:  params,
		})
		if resp.Error != nil {
			t.Fatalf("unexpected JSON-RPC error: %v", resp.Error)
		}
		result, ok := resp.Result.(map[string]interface{})
		if !ok {
			t.Fatalf("expected result map, got %T", resp.Result)
		}
		if result["isError"] != true {
			t.Fatal("expected isError=true for missing topic")
		}
	})
}

func TestServerReadDocument_SymlinkSwapRejected(t *testing.T) {
	tmpDir := t.TempDir()
	origFile := filepath.Join(tmpDir, "original.txt")
	sensitiveFile := filepath.Join(tmpDir, "secret.txt")

	if err := os.WriteFile(origFile, []byte("indexed content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sensitiveFile, []byte("secret data"), 0644); err != nil {
		t.Fatal(err)
	}

	resolvedOrigFile, err := filepath.EvalSymlinks(origFile)
	if err != nil {
		t.Fatal(err)
	}

	store := &fakeStore{
		getDocFunc: func(path string) (*db.Document, error) {
			if path == resolvedOrigFile {
				return &db.Document{Path: resolvedOrigFile}, nil
			}
			return nil, nil
		},
	}

	os.Remove(origFile)
	if err := os.Symlink(sensitiveFile, origFile); err != nil {
		t.Fatal(err)
	}

	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name": "read_document",
		"arguments": map[string]interface{}{
			"path": origFile,
		},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp.Error)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	if result["isError"] != true {
		t.Fatal("expected isError=true for symlink-swap path")
	}
}

func TestServerReadDocument_LargeFileTruncated(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := filepath.Join(home, ".symseek-test-"+strings.ToLower(t.Name()))
	if err := os.MkdirAll(tmpDir, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	bigFile := filepath.Join(tmpDir, "big.txt")

	bigContent := strings.Repeat("A", 11<<20)
	if err := os.WriteFile(bigFile, []byte(bigContent), 0644); err != nil {
		t.Fatal(err)
	}

	resolvedBigFile, err := filepath.EvalSymlinks(bigFile)
	if err != nil {
		t.Fatal(err)
	}

	store := &fakeStore{
		getDocFunc: func(path string) (*db.Document, error) {
			if path == resolvedBigFile {
				return &db.Document{Path: resolvedBigFile}, nil
			}
			return nil, nil
		},
	}

	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name": "read_document",
		"arguments": map[string]interface{}{
			"path": bigFile,
		},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result := resp.Result.(map[string]interface{})
	content := result["content"].([]interface{})
	text := content[0].(map[string]interface{})["text"].(string)

	if !strings.HasSuffix(text, "[Truncated: file exceeds 10 MB read limit]") {
		t.Error("expected truncation notice at end of content")
	}
	if len(text) > 11<<20 {
		t.Errorf("content too large: %d bytes", len(text))
	}
}

func TestIndexSingleFile(t *testing.T) {
	store := &fakeStore{}
	embed := &fakeEmbedder{}

	_, err := IndexSingleFile(store, embed, "/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestServerSearchDocumentsWithResults(t *testing.T) {
	store := &fakeStore{
		searchFunc: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{
				{
					Chunk: &db.Chunk{
						DocumentPath: "/test/doc.md",
						ChunkIndex:   0,
						Content:      "Test content",
					},
					RRFScore: 0.5,
				},
			}, nil
		},
	}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name": "search_documents",
		"arguments": map[string]interface{}{
			"query": "test",
			"limit": float64(5),
		},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestServerListDocumentsEmpty(t *testing.T) {
	store := &fakeStore{
		listDocsFunc: func() ([]*db.Document, error) {
			return []*db.Document{}, nil
		},
	}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name":      "list_documents",
		"arguments": map[string]interface{}{},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}

func TestServerGetContextWithResults(t *testing.T) {
	store := &fakeStore{
		searchFunc: func(query string, limit int) ([]*db.SearchResult, error) {
			return []*db.SearchResult{
				{
					Chunk: &db.Chunk{
						DocumentPath: "/test/doc.md",
						ChunkIndex:   0,
						Content:      "Test content",
					},
					RRFScore: 0.5,
				},
			}, nil
		},
	}
	embed := &fakeEmbedder{}
	server := newTestServer(store, embed)

	params, _ := json.Marshal(map[string]interface{}{
		"name": "get_context",
		"arguments": map[string]interface{}{
			"topic":     "test topic",
			"max_chars": float64(100),
		},
	})
	resp := pipeRequest(t, server, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  params,
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}
