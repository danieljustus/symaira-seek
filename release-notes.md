## What's changed

### Features
- #179 Vector index lifecycle: incremental updates, external-write detection, and persistence — closes #171, #172, #173
- #180 Binary-quantized vector search, dimension tracking, and VectorStore — closes #136, #151, #174
- #181 TurboQuant-style vector quantization prototype and benchmark — closes #175
- #182 Quantized embedding sidecar storage and backfill plumbing — closes #176, #177, #178
- #195 Add fromLine/maxLines to read_document MCP tool — closes #190, #191, #192
- #196 Optional LLM re-ranking via Ollama for search results — closes #193, #194

### Fixes
- #189 Benchmark768Dim and BitWidth.Levels fixes — closes #185, #186

### Security
- #206 Enable secret scanning and push protection — closes #197, #198

### Docs
- #208 Add architecture diagram to README — closes #199
- #216 Update read_document MCP tool documentation — closes #213, #214, #215

### Tests
- #187 Unit tests for BackfillQuantSidecars — closes #183
- #188 Meaningful assertions to CLI serve/version tests — closes #184
- #209 Config load, reload, and save error path coverage — closes #200
- #210 Folder-context and document-list database API tests — closes #201
- #211 Directory watch and re-rank HTTP path tests — closes #202
- #212 Parser PPTX and text-split branch tests — closes #203

### Dependencies
- #204 Bump actions/checkout from 6 to 7
- #205 Bump modernc.org/sqlite from 1.52.0 to 1.53.0

### Closed Issues
- #136 Binary-quantized vector search
- #151 Embedding dimension mismatch silent degradation
- #171 Vector index incremental updates
- #172 External-write detection
- #173 Vector index persistence
- #174 VectorStore seam
- #175 TurboQuant-style quantization
- #176 Quantized sidecar storage
- #177 Backfill plumbing
- #178 Sidecar format
- #183 BackfillQuantSidecars tests
- #184 CLI serve/version test assertions
- #185 Benchmark768Dim fix
- #186 BitWidth.Levels fix
- #190 read_document fromLine
- #191 read_document maxLines
- #192 read_document line-range docs
- #193 LLM re-ranking
- #194 Ollama re-ranking config
- #197 Secret scanning
- #198 Push protection
- #199 Architecture diagram
- #200 Config error paths
- #201 Folder-context and document-list tests
- #202 Directory watch and re-rank tests
- #203 Parser PPTX and text-split tests
- #213 read_document fromLine docs
- #214 read_document maxLines docs
- #215 LLM re-ranking docs

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v2.1.1...v2.2.0
