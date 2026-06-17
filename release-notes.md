## What's changed

### Breaking Changes
- #104 Remove deprecated `max_tokens` parameter from `get_context` MCP tool — closes #102

### Features
- #117 Add request timeouts to MCP tool handlers to prevent indefinite blocking — closes #113
  - Search handlers (search_documents, get_context): 60s timeout
  - Index handlers (index_document, index_url): 300s timeout

### Performance
- #117 Precompute query vector norm in SearchVector — closes #111
- #112 Split db.go into focused files (search.go, vecmath.go) — closes #112

### Improvements
- #114 Add configurable log verbosity — closes #114
- #115 Decouple config.OllamaConfig from engine.OllamaConfig — closes #115
- #116 Add HTTP server indexCooldown to config — closes #116

### Tests
- #104 Add tests for cmd/symseek and internal/server — closes #101
- #108 Add integration tests for MCP server tools — closes #105
- #110 Add unit tests for internal/errors package — closes #109

### Docs
- #104 Translate ARCHITECTURE_PLAN.md from German to English — closes #103

### Closed Issues
- #101 Improve test coverage for cmd/symseek and internal/server
- #102 Remove deprecated max_tokens parameter from get_context
- #103 Translate ARCHITECTURE_PLAN.md from German to English
- #105 Add integration tests for MCP server and HTTP server
- #106 Add structured error types for all interfaces
- #107 Add API documentation for MCP tools and HTTP endpoints
- #109 Achieve 100% test coverage for internal/errors
- #111 Precompute query vector norm in SearchVector
- #112 Split db.go into focused files
- #113 Add request timeout to MCP tool handlers
- #114 Add configurable log verbosity
- #115 Decouple config.OllamaConfig from engine.OllamaConfig
- #116 Add HTTP server indexCooldown to config

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v1.2.0...v2.0.0