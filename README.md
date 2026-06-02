# Symaira-Seek

Symaira-Seek is a local-first, CGO-free document retrieval tool designed for AI agents and developers. It provides hybrid search (BM25 keyword search combined with vector semantic search) and fuses results using Reciprocal Rank Fusion (RRF).

It exposes multiple interfaces:
1. **Command Line Interface (CLI)**: A Unix-friendly command utility.
2. **Model Context Protocol (MCP)**: Native stdio-based tool integration for AI agents (Claude, Cursor, ChatGPT, etc.).
3. **HTTP REST Daemon**: A lightweight localhost API with search and index endpoints.

## Tech Stack & Architecture

- **Language**: Pure Go (1.26+)
- **Database**: SQLite (via pure-Go `modernc.org/sqlite` to maintain **100% CGO-free compilation**)
- **Keyword Search**: SQLite FTS5 with BM25 ranking
- **Vector Search**: Cosine similarity calculations on normalized 768-dimensional float32 arrays
- **Result Fusion**: Reciprocal Rank Fusion (RRF) with parameter $k=60$
- **Embeddings**: Dual-mode generation:
  - **Local Ollama Integration**: Uses the `nomic-embed-text` model.
  - **Deterministic Local Fallback**: Fallback word-hash vector generator to allow 100% offline usage.

---

## Installation & Setup

Ensure you have [Go](https://go.dev/) installed.

### Build Binary
```bash
go build -o seek cmd/seek/main.go
```

### Run Tests
```bash
go test -v ./...
```

---

## CLI Usage

### Index a Directory
Crawl and index all markdown, text, code, JSON, and yaml files inside a folder:
```bash
./seek index /path/to/my-documents
```

#### Watch Daemon
Keep the tool running in the background to automatically synchronize changes (creation, modification, and deletion of files) every 5 seconds:
```bash
./seek index /path/to/my-documents --watch
```

### Search Documents
Perform a hybrid semantic and keyword search:
```bash
./seek search "renewable energy optimization" --limit 5
```

Export structured search results directly to JSON:
```bash
./seek search "renewable energy optimization" --json
```

### Get Database Stats
```bash
./seek status
```

---

## MCP Server Integration

To use Symaira-Seek as an MCP tool for AI clients (like Claude Desktop or Cursor), register it in your client's configuration file:

```json
{
  "mcpServers": {
    "symaira-seek": {
      "command": "/absolute/path/to/symaira-seek/seek",
      "args": ["serve"]
    }
  }
}
```

### Exposed Tools
1. `search_documents(query, limit)`: Hybrid search over all indexed files.
2. `read_document(path)`: Retrieves the complete content of an indexed file.
3. `list_documents(folder)`: Explorative folder and index structure scanning.
4. `get_context(topic, max_tokens)`: Aggregates relevant context blocks from multiple documents.
5. `index_document(path)`: Manually indexes a local file or directory.

---

## HTTP REST Daemon

Start the REST API on port `8788`:
```bash
./seek serve --port 8788
```

### Endpoints
- **GET** `/health`: Check status (`{"status": "ok"}`).
- **GET** `/status`: Returns document counts, chunk counts, and database file size.
- **GET** `/search?q=query&limit=5`: Query the hybrid search engine.
- **POST** `/index` with body `{"path": "/absolute/path"}`: Synchronously crawl and index a folder.

---

## License

MIT License. Part of the Symaira tool suite.
