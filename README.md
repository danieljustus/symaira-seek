# Symaira-Seek

> Local-first, CGO-free document retrieval for AI agents with hybrid BM25+vector search.

[![CI](https://github.com/danieljustus/symaira-seek/actions/workflows/ci.yml/badge.svg)](https://github.com/danieljustus/symaira-seek/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Symaira-Seek is a local-first, CGO-free document retrieval tool designed for AI agents and developers. It provides hybrid search (BM25 keyword search combined with vector semantic search) and fuses results using Reciprocal Rank Fusion (RRF).

## Why Symaira-Seek?

- **100% CGO-free**: Pure Go SQLite driver (`modernc.org/sqlite`) — cross-compile anywhere without C dependencies
- **Hybrid search**: Combines BM25 keyword matching with vector semantic search for better relevance
- **Dual embedding modes**: Local Ollama integration for quality, deterministic fallback for offline usage
- **Multiple interfaces**: CLI, MCP server for AI agents, and HTTP REST daemon
- **Local-first**: Your data stays on your machine — no cloud dependencies required

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

### Homebrew (macOS/Linux)

Install via Homebrew tap:

```bash
brew install danieljustus/symaira-seek/symaira-seek
```

### Pre-built Binaries (Recommended)

Download the latest release for your platform from [GitHub Releases](https://github.com/danieljustus/symaira-seek/releases):

- **Linux**: `symaira-seek_Linux_x86_64.tar.gz` or `symaira-seek_Linux_arm64.tar.gz`
- **macOS**: `symaira-seek_Darwin_x86_64.tar.gz` or `symaira-seek_Darwin_arm64.tar.gz`
- **Windows**: `symaira-seek_Windows_x86_64.zip` or `symaira-seek_Windows_arm64.zip`

Extract and install:
```bash
# Linux/macOS
tar -xzf symaira-seek_*.tar.gz
chmod +x seek
sudo mv seek /usr/local/bin/

# Windows
# Extract the .zip and add to PATH
```

Verify the installation:
```bash
seek version
```

### Build from Source

Ensure you have [Go](https://go.dev/) installed.

```bash
go build -o seek cmd/seek/main.go
```

To inject a version string at build time, set `main.version` via `-ldflags`. The CI workflow derives the value from the current git tag (or a `0.0.0-dev+<short-sha>` fallback) and passes it automatically:
```bash
VERSION="0.2.0"
go build -ldflags "-s -w -X main.version=${VERSION}" -o seek cmd/seek/main.go
./seek version
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

Export the same stats as JSON for monitoring pipelines:
```bash
./seek status --json
```

### Configuration

`seek` stores its configuration in `~/.config/symaira-seek/config.json` (overridable with `--config`). The file contains the Ollama endpoint URL and the embedding model name.

View the active configuration (the path is printed to stderr):
```bash
./seek config
```

Set a value without editing the file by hand:
```bash
./seek config --set-key ollama_url --set-value http://localhost:11434/api/embeddings
./seek config --set-key model --set-value mxbai-embed-large
```

The file is rewritten with mode `0600` on every write. Supported keys are `ollama_url` and `model`.

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
