## What's Changed

### Features
- #84 Security hardening, stdout fix, config validation, code cleanup, cache fix, parallel search
- #75 Fix hybrid search, orphan cleanup, batch embedding, and 6 more issues
- #64 FTS5 query sanitisation, MCP path validation, panic recovery, and more (+11 more)
- #51 Session 2026-06-05: security/UX hardening batch (#41-#45)
- #40 Nil-pointer panic in EmbeddingsGenerator across all interfaces (+10...)
- #28 fix(mcp): validate read_document paths and restrict to indexed files
- #19 HTTP server startup message writes to stdout (+11 more)
- #6 Fix queryOllama nil error leak (+4 more)

### Fixes
- #75 Fix hybrid search, orphan cleanup, batch embedding, and 6 more issues
- #40 Nil-pointer panic in EmbeddingsGenerator across all interfaces (+10...)
- #28 fix(mcp): validate read_document paths and restrict to indexed files
- #19 HTTP server startup message writes to stdout (+11 more)
- #6 Fix queryOllama nil error leak (+4 more)

### Security
- #84 Security hardening, stdout fix, config validation, code cleanup, cache fix, parallel search
- #64 FTS5 query sanitisation, MCP path validation, panic recovery, and more (+11 more)
- #51 Session 2026-06-05: security/UX hardening batch (#41-#45)

### Dependencies
- #86 Build(deps): bump modernc.org/sqlite in the go-dependencies group
- #85 Build(deps): bump the github-actions group with 2 updates

### Closed Issues
- #1 queryOllama leaks nil error on non-200 HTTP status
- #2 Batch embeddings during indexing to avoid per-chunk HTTP overhead
- #3 SearchVector loads all embeddings into memory causing scalability bottleneck
- #4 Watch mode lacks graceful shutdown and signal handling
- #5 REST server opens and closes database connection per request

**Full Changelog**: https://github.com/danieljustus/symaira-seek/releases/tag/v1.0.0
