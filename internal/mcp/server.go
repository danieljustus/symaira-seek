package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   interface{} `json:"error,omitempty"`
}

// StartServer starts the MCP server over stdio.
func StartServer(cfgOllamaURL, cfgModel string) error {
	reader := bufio.NewReader(os.Stdin)

	// Suppress any printing to Stdout in packages.
	// Only print structured JSON-RPC messages to Stdout.
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("error reading stdin: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			sendError(nil, -32700, "Parse error: "+err.Error())
			continue
		}

		handleRequest(&req, cfgOllamaURL, cfgModel)
	}
}

func handleRequest(req *JSONRPCRequest, ollamaURL, model string) {
	switch req.Method {
	case "initialize":
		sendResponse(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]string{
				"name":    "symaira-seek",
				"version": "0.1.0",
			},
		})

	case "notifications/initialized":
		// No-op client notification

	case "tools/list":
		tools := []map[string]interface{}{
			{
				"name":        "search_documents",
				"description": "Search the local document index for relevant content using hybrid keyword (BM25) and vector search. Use when the user asks about specific topics, files, or information that might be indexed.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{
							"type":        "string",
							"description": "Natural language search query",
						},
						"limit": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum number of search results to return (default 5)",
						},
					},
					"required": []string{"query"},
				},
			},
			{
				"name":        "read_document",
				"description": "Retrieve the full text content of an indexed document. Use when the user needs to inspect the detailed content of a specific file.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Absolute path to the document file",
						},
					},
					"required": []string{"path"},
				},
			},
			{
				"name":        "list_documents",
				"description": "Browse the list of indexed documents. Use to explore what folders/files are currently in the index.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"folder": map[string]interface{}{
							"type":        "string",
							"description": "Optional folder path prefix to filter the document list",
						},
					},
				},
			},
			{
				"name":        "get_context",
				"description": "Compile a consolidated context block from multiple matching documents on a given topic. This combines search and read tools.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"topic": map[string]interface{}{
							"type":        "string",
							"description": "The topic or concept to extract context for",
						},
						"max_tokens": map[string]interface{}{
							"type":        "integer",
							"description": "Maximum character length of the combined context (default 4000)",
						},
					},
					"required": []string{"topic"},
				},
			},
			{
				"name":        "index_document",
				"description": "Index a specific local file or folder immediately. Use when the user wants to add a new directory or file to the search database.",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Absolute path to the file or directory to index",
						},
					},
					"required": []string{"path"},
				},
			},
		}

		sendResponse(req.ID, map[string]interface{}{
			"tools": tools,
		})

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}

		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(req.ID, -32602, "Invalid params: "+err.Error())
			return
		}

		handleToolCall(req.ID, params.Name, params.Arguments, ollamaURL, model)

	default:
		sendError(req.ID, -32601, "Method not found: "+req.Method)
	}
}

func handleToolCall(reqID interface{}, name string, args map[string]interface{}, ollamaURL, model string) {
	dbClient, err := db.Open()
	if err != nil {
		sendError(reqID, -32603, "Database error: "+err.Error())
		return
	}
	defer dbClient.Close()

	embedder := engine.NewEmbeddingsGeneratorWithConfig(ollamaURL, model)

	switch name {
	case "search_documents":
		query, ok := args["query"].(string)
		if !ok || query == "" {
			sendError(reqID, -32602, "Missing or invalid query argument")
			return
		}

		limit := 5
		if lVal, ok := args["limit"].(float64); ok {
			limit = int(lVal)
		}

		results, err := engine.SearchHybrid(dbClient, embedder, query, limit)
		if err != nil {
			sendError(reqID, -32603, "Search failed: "+err.Error())
			return
		}

		var textBuilder strings.Builder
		for idx, r := range results {
			textBuilder.WriteString(fmt.Sprintf("[%d] File: %s (Chunk %d, RRF Score: %.4f)\n", idx+1, r.Chunk.DocumentPath, r.Chunk.ChunkIndex, r.RRFScore))
			textBuilder.WriteString(r.Chunk.Content)
			textBuilder.WriteString("\n\n")
		}

		sendToolResponse(reqID, textBuilder.String())

	case "read_document":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			sendError(reqID, -32602, "Missing or invalid path argument")
			return
		}

		// Resolve and normalize the path. filepath.Clean collapses any ".."
		// segments, so a later strings.Contains check on the absolute path is
		// dead code — the indexed-document whitelist below is the real
		// authorization boundary.
		cleanPath := filepath.Clean(path)
		absPath, err := filepath.Abs(cleanPath)
		if err != nil {
			sendError(reqID, -32603, "Invalid path: "+err.Error())
			return
		}

		// Authorization: only allow reading files that are actually in the
		// local index. This is the primary defense and the only one that
		// reliably blocks reads of arbitrary filesystem paths.
		doc, err := dbClient.GetDocument(absPath)
		if err != nil {
			sendError(reqID, -32603, "Database error: "+err.Error())
			return
		}
		if doc == nil {
			sendError(reqID, -32603, "Document is not indexed and cannot be read")
			return
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			sendError(reqID, -32603, "Failed to read file: "+err.Error())
			return
		}

		sendToolResponse(reqID, string(data))

	case "list_documents":
		folderPrefix, _ := args["folder"].(string)
		docs, err := dbClient.ListDocuments()
		if err != nil {
			sendError(reqID, -32603, "Failed to list documents: "+err.Error())
			return
		}

		var textBuilder strings.Builder
		count := 0
		for _, d := range docs {
			if folderPrefix == "" || strings.HasPrefix(d.Path, folderPrefix) {
				textBuilder.WriteString(fmt.Sprintf("- %s (Last Indexed: %s)\n", d.Path, d.UpdatedAt.Format(time.RFC3339)))
				count++
			}
		}

		if count == 0 {
			textBuilder.WriteString("No documents matching filter found in index.")
		}

		sendToolResponse(reqID, textBuilder.String())

	case "get_context":
		topic, ok := args["topic"].(string)
		if !ok || topic == "" {
			sendError(reqID, -32602, "Missing or invalid topic argument")
			return
		}

		maxChars := 4000
		if maxVal, ok := args["max_tokens"].(float64); ok {
			maxChars = int(maxVal)
		}

		results, err := engine.SearchHybrid(dbClient, embedder, topic, 10)
		if err != nil {
			sendError(reqID, -32603, "Search failed: "+err.Error())
			return
		}

		var textBuilder strings.Builder
		textBuilder.WriteString(fmt.Sprintf("=== CONTEXT FOR TOPIC: %s ===\n", topic))
		for _, r := range results {
			if textBuilder.Len()+len(r.Chunk.Content) > maxChars {
				break
			}
			textBuilder.WriteString(fmt.Sprintf("Source: %s (Chunk %d)\n", r.Chunk.DocumentPath, r.Chunk.ChunkIndex))
			textBuilder.WriteString(r.Chunk.Content)
			textBuilder.WriteString("\n---\n")
		}

		sendToolResponse(reqID, textBuilder.String())

	case "index_document":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			sendError(reqID, -32602, "Missing or invalid path argument")
			return
		}

		info, err := os.Stat(path)
		if err != nil {
			sendError(reqID, -32603, "Path error: "+err.Error())
			return
		}

		if info.IsDir() {
			err = engine.IndexDirectory(dbClient, embedder, path)
			if err != nil {
				sendError(reqID, -32603, "Indexing failed: "+err.Error())
				return
			}
			sendToolResponse(reqID, fmt.Sprintf("Successfully indexed directory: %s", path))
		} else {
			absPath, _ := filepath.Abs(path)
			hash, err := IndexSingleFile(dbClient, embedder, absPath)
			if err != nil {
				sendError(reqID, -32603, "Failed to index file: "+err.Error())
				return
			}
			sendToolResponse(reqID, fmt.Sprintf("Successfully indexed file: %s (Hash: %s)", absPath, hash))
		}

	default:
		sendError(reqID, -32601, "Unknown tool: "+name)
	}
}

// IndexSingleFile indexes a single file instead of a directory.
func IndexSingleFile(dbClient *db.DB, embedder *engine.EmbeddingsGenerator, path string) (string, error) {
	return engine.IndexFile(dbClient, embedder, path)
}

func sendResponse(id interface{}, result interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	os.Stdout.Write(append(data, '\n'))
}

func sendToolResponse(id interface{}, text string) {
	result := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		},
		"isError": false,
	}
	sendResponse(id, result)
}

func sendError(id interface{}, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(resp)
	os.Stdout.Write(append(data, '\n'))
}
