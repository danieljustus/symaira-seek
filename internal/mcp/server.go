package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danieljustus/symaira-corekit/mcpserver"

	"github.com/danieljustus/symaira-seek/internal/db"
	"github.com/danieljustus/symaira-seek/internal/engine"
	"github.com/danieljustus/symaira-seek/internal/pathutil"
)

var ServerVersion = "dev"

func StartServer(cfg engine.OllamaConfig) error {
	dbClient, err := db.Open()
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer dbClient.Close()

	embedder := engine.NewEmbeddingsGeneratorWithOllamaConfig(cfg)
	server := mcpserver.New("symseek", ServerVersion)

	registerSearchDocuments(server, dbClient, embedder)
	registerReadDocument(server, dbClient, embedder)
	registerListDocuments(server, dbClient, embedder)
	registerGetContext(server, dbClient, embedder)
	registerIndexDocument(server, dbClient, embedder)
	registerIndexURL(server, dbClient, embedder)

	return server.ServeStdio(context.Background())
}

func registerSearchDocuments(server *mcpserver.Server, dbClient db.Store, embedder engine.Embedder) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "search_documents",
		Description: "Search the local document index for relevant content using hybrid keyword (BM25) and vector search. Use when the user asks about specific topics, files, or information that might be indexed.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Natural language search query"},"limit":{"type":"integer","description":"Maximum number of search results to return (default 5)"}},"required":["query"]}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
			if params.Query == "" {
				return nil, fmt.Errorf("missing or invalid query argument")
			}
			if params.Limit <= 0 {
				params.Limit = 5
			}

			results, err := engine.SearchHybrid(dbClient, embedder, params.Query, params.Limit)
			if err != nil {
				return nil, fmt.Errorf("search failed: %w", err)
			}

			var textBuilder strings.Builder
			for idx, r := range results {
				textBuilder.WriteString(fmt.Sprintf("[%d] File: %s (Chunk %d, RRF Score: %.4f)\n", idx+1, r.Chunk.DocumentPath, r.Chunk.ChunkIndex, r.RRFScore))
				textBuilder.WriteString(r.Chunk.Content)
				textBuilder.WriteString("\n\n")
			}
			return textBuilder.String(), nil
		},
	})
}

func registerReadDocument(server *mcpserver.Server, dbClient db.Store, _ engine.Embedder) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "read_document",
		Description: "Retrieve the full text content of an indexed document. Use when the user needs to inspect the detailed content of a specific file.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Absolute path to the document file"}},"required":["path"]}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
			if params.Path == "" {
				return nil, fmt.Errorf("missing or invalid path argument")
			}

			absPath, err := pathutil.RestrictToHome(params.Path)
			if err != nil {
				return nil, fmt.Errorf("path error: %w", err)
			}

			resolvedPath, err := filepath.EvalSymlinks(absPath)
			if err != nil {
				return nil, fmt.Errorf("path does not exist: %w", err)
			}

			doc, err := dbClient.GetDocument(resolvedPath)
			if err != nil {
				return nil, fmt.Errorf("database error: %w", err)
			}
			if doc == nil {
				return nil, fmt.Errorf("document is not indexed and cannot be read")
			}

			const maxReadBytes = 10 << 20
			f, err := os.Open(resolvedPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			defer f.Close()

			limitedReader := io.LimitReader(f, maxReadBytes+1)
			data, err := io.ReadAll(limitedReader)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			content := string(data)
			if int64(len(data)) > maxReadBytes {
				content = content[:maxReadBytes]
				content += "\n\n[Truncated: file exceeds 10 MB read limit]"
			}
			return content, nil
		},
	})
}

func registerListDocuments(server *mcpserver.Server, dbClient db.Store, _ engine.Embedder) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "list_documents",
		Description: "Browse the list of indexed documents. Use to explore what folders/files are currently in the index.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"folder":{"type":"string","description":"Optional folder path prefix to filter the document list"}}}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Folder string `json:"folder"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}

			docs, err := dbClient.ListDocuments()
			if err != nil {
				return nil, fmt.Errorf("failed to list documents: %w", err)
			}

			var textBuilder strings.Builder
			count := 0
			for _, d := range docs {
				if params.Folder == "" || strings.HasPrefix(d.Path, params.Folder) {
					textBuilder.WriteString(fmt.Sprintf("- %s (Last Indexed: %s)\n", d.Path, d.UpdatedAt.Format(time.RFC3339)))
					count++
				}
			}

			if count == 0 {
				textBuilder.WriteString("No documents matching filter found in index.")
			}
			return textBuilder.String(), nil
		},
	})
}

func registerGetContext(server *mcpserver.Server, dbClient db.Store, embedder engine.Embedder) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "get_context",
		Description: "Compile a consolidated context block from multiple matching documents on a given topic. This combines search and read tools.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string","description":"The topic or concept to extract context for"},"max_chars":{"type":"integer","description":"Maximum character count (Unicode code points) of the combined context (default 4000). Deprecated: max_tokens accepted for backward compatibility."}},"required":["topic"]}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Topic     string `json:"topic"`
				MaxChars  int    `json:"max_chars"`
				MaxTokens int    `json:"max_tokens"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
			if params.Topic == "" {
				return nil, fmt.Errorf("missing or invalid topic argument")
			}

			maxChars := params.MaxChars
			if maxChars == 0 && params.MaxTokens > 0 {
				fmt.Fprintf(os.Stderr, "WARNING: max_tokens is deprecated, use max_chars instead\n")
				maxChars = params.MaxTokens
			}
			if maxChars == 0 {
				maxChars = 4000
			}

			results, err := engine.SearchHybrid(dbClient, embedder, params.Topic, 10)
			if err != nil {
				return nil, fmt.Errorf("search failed: %w", err)
			}

			var textBuilder strings.Builder
			runeCount := 0
			header := fmt.Sprintf("=== CONTEXT FOR TOPIC: %s ===\n", params.Topic)
			textBuilder.WriteString(header)
			runeCount += utf8.RuneCountInString(header)
			for _, r := range results {
				chunkRunes := utf8.RuneCountInString(r.Chunk.Content)
				if runeCount+chunkRunes > maxChars {
					break
				}
				src := fmt.Sprintf("Source: %s (Chunk %d)\n", r.Chunk.DocumentPath, r.Chunk.ChunkIndex)
				sep := "\n---\n"
				textBuilder.WriteString(src)
				textBuilder.WriteString(r.Chunk.Content)
				textBuilder.WriteString(sep)
				runeCount += utf8.RuneCountInString(src) + chunkRunes + utf8.RuneCountInString(sep)
			}
			return textBuilder.String(), nil
		},
	})
}

func registerIndexDocument(server *mcpserver.Server, dbClient db.Store, embedder engine.Embedder) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "index_document",
		Description: "Index a specific local file or folder immediately. Use when the user wants to add a new directory or file to the search database.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Absolute path to the file or directory to index"}},"required":["path"]}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
			if params.Path == "" {
				return nil, fmt.Errorf("missing or invalid path argument")
			}

			absPath, err := pathutil.RestrictToHome(params.Path)
			if err != nil {
				return nil, fmt.Errorf("path error: %w", err)
			}

			info, err := os.Stat(absPath)
			if err != nil {
				return nil, fmt.Errorf("path error: %w", err)
			}

			if info.IsDir() {
				if err := engine.IndexDirectory(dbClient, embedder, absPath); err != nil {
					return nil, fmt.Errorf("indexing failed: %w", err)
				}
				return fmt.Sprintf("Successfully indexed directory: %s", absPath), nil
			}

			hash, err := IndexSingleFile(dbClient, embedder, absPath)
			if err != nil {
				return nil, fmt.Errorf("failed to index file: %w", err)
			}
			return fmt.Sprintf("Successfully indexed file: %s (Hash: %s)", absPath, hash), nil
		},
	})
}

func IndexSingleFile(dbClient db.Store, embedder engine.Embedder, path string) (string, error) {
	return engine.IndexFile(dbClient, embedder, path)
}

func registerIndexURL(server *mcpserver.Server, dbClient db.Store, embedder engine.Embedder) {
	server.RegisterTool(&mcpserver.Tool{
		Name:        "index_url",
		Description: "Index content from a URL. Fetches content using symfetch (if available) or HTTP GET, then indexes it for search.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"URL to fetch and index"}},"required":["url"]}`),
		Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
			var params struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid params: %w", err)
			}
			if params.URL == "" {
				return nil, fmt.Errorf("missing or invalid url argument")
			}

			if err := engine.IndexURL(dbClient, embedder, params.URL); err != nil {
				return nil, fmt.Errorf("failed to index URL: %w", err)
			}
			return fmt.Sprintf("Successfully indexed URL: %s", params.URL), nil
		},
	})
}
