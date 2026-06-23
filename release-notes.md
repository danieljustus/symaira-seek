## What's changed

### Security
- #154 Fix SSRF: pin validated IP in HTTP fallback and harden symfetch path — closes #146
- #154 Fix DB: create data directory and DB with restrictive permissions — closes #147
- #163 Cap file and archive sizes during indexing to prevent memory exhaustion / zip-bomb DoS — closes #155

### Fixes
- #154 Fix --config flag honored on write but silently ignored on read — closes #150
- #154 Warn on embedding dimension mismatch instead of silent hash fallback — closes #151
- #163 Warn at startup when HTTP daemon runs without SEEK_API_TOKEN — closes #156

### Performance
- #154 Avoid per-chunk float32 slice allocation in vector search — closes #152
- #163 Avoid Ollama retry latency during search by using no-retry embedding path — closes #162
- #163 Add recall-preserving IVF prefilter for vector search — closes #161

### Features
- #154 Add --plain flag to search to skip the TUI — closes #148
- #158 Add top-level --version flag — closes #158

### Docs
- #154 Fix --config path help and document SEEK_API_TOKEN / SEEK_ALLOW_PRIVATE_URLS — closes #149
- #144 Distilled architecture research notes

### Tests
- #169 Add tests for HTTP REST daemon endpoints and startup — closes #164, #165, #166, #167, #168

### Dependencies
- #157 Make embeddings retry backoff injectable to cut engine test time — closes #157

### Closed Issues
- #146 SSRF: validate-then-fetch bypassable via DNS rebinding
- #147 Index database directory created world-readable
- #148 search: add --plain/--no-tui opt-out
- #149 Docs: fix --config path help text
- #150 --config flag ignored on read
- #151 Embedding dimension mismatch silent degradation
- #152 Vector search allocates fresh float32 slice per chunk
- #155 Cap file and archive sizes during indexing
- #156 Warn at startup without SEEK_API_TOKEN
- #157 Make embeddings retry backoff injectable
- #158 Add top-level --version flag
- #161 Add recall-preserving IVF prefilter
- #162 Avoid Ollama retry latency during search
- #164–#168 Test coverage for HTTP, CLI, MCP, URL fetching, PDF/XLSX

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v2.1.0...v2.1.1
