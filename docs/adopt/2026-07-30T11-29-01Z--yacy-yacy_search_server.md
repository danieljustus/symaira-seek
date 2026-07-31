<!-- review: timestamp=2026-07-30T11-29-01Z  repo=danieljustus/symaira-seek  head=0d95ee9e8f6878a2caddbc702d3326c4cf73e34d -->
<!-- adopt: source=yacy/yacy_search_server  source_ref=1f181065cebaabff33f961ecdf81fb2a57748053  source_url=https://github.com/yacy/yacy_search_server  depth=clone  license=GPL-2.0-or-later+LGPL-2.1-or-later -->

# Adoption Report — `danieljustus/symaira-seek` ← `yacy/yacy_search_server` — 2026-07-30

## Sources

| Field | Value |
|---|---|
| SOURCE | `yacy/yacy_search_server` ([GitHub](https://github.com/yacy/yacy_search_server)) |
| Ref analyzed | `1f181065cebaabff33f961ecdf81fb2a57748053` (`master`) |
| Language / License | Java / GPL-2.0-or-later for most source; LGPL-2.1-or-later for `source/net/yacy/cora` |
| Health | 3.9k stars, 14,898 commits, last push 2026-07-14, not archived, active recent search/RAG changes; latest release not verified because the local GitHub CLI authentication is invalid |
| Scope | All facets; full shallow clone with 200 recent commits fetched for rationale checks |
| TARGET | `danieljustus/symaira-seek` @ `0d95ee9e8f6878a2caddbc702d3326c4cf73e34d` |

## Verdict

YaCy is worth learning from only in the narrow area of search-result presentation and retrieval-to-context handoff. Its long-running crawler, peer network, Solr federation, and web appliance do not fit Seek's standalone, local-first, CGO-free scope. The highest-value transferable pattern is a bounded, query-focused snippet with separate raw/detail retrieval and sanitization; one finding survives the adoption gates.

## What we already do as well or better

- Hybrid local retrieval with BM25, vector search, and RRF → `internal/engine/retrieval.go:21-31` and `internal/engine/retrieval.go:89-183` already provide a simpler, explicit two-leg pipeline.
- Deferred vector-content loading → `internal/db/search.go:82-85`, `internal/db/search.go:190-244`, and `internal/db/search.go:320-352` already avoid reading full text until the top-k vector candidates survive scoring.
- Stable structured search results with citation offsets → `internal/db/db.go:65-93` and `internal/mcp/server.go:52-91` already define a shared consumer-facing JSON shape; `CharStart` and `CharEnd` are persisted and returned.
- Multiple local interfaces with a shared engine → `README.md`, `internal/mcp/server.go:24-49`, and `internal/server/server.go:182-206` already cover CLI/MCP/HTTP integration without YaCy's appliance-scale web UI and peer runtime.
- Search-to-detail routing is already documented → `docs/research/README.md:96-113` explicitly distinguishes targeted `search` from detailed `read`; the missing piece is making the search payload honor that contract.

## Findings

- [ ] **[UX/DX] Return bounded, query-focused snippets from search results**
  - **Status quo:** `internal/db/db.go:65-93` calls the consumer field `Snippet` but fills it with the complete `Chunk.Content`; `internal/mcp/server.go:52-91` returns that value by default, while `internal/server/server.go:240-264` serializes the internal `SearchResult` directly, including full chunk content and the embedding-bearing `Chunk`. This weakens the documented search→read split and context budget in `docs/research/README.md:106-113` and can make agent search payloads carry an entire chunk. Upstream `yacy/yacy_search_server` implements a separate bounded snippet path in `source/net/yacy/search/snippet/TextSnippet.java:219-236`, `source/net/yacy/search/snippet/TextSnippet.java:430-476`, and `source/net/yacy/search/snippet/TextSnippet.java:484-552`, with query-term extraction, a small cache, raw/display forms, and sanitization; `test/java/net/yacy/search/snippet/TextSnippetTest.java:57-219` covers matching, encoding, and JavaScript-fragment removal. Recent commits `5f4575a`, `f9933a1`, and `b8daaac` explicitly address RAG snippets, Markdown, and leaked JavaScript.
  - **Proposed solution:** Pattern adoption only—add a pure-Go snippet builder at the retrieval/output boundary that selects a bounded window around query terms, normalizes Markdown/HTML/script residue, preserves the existing `char_start`/`char_end` citation anchors, and exposes raw/full content only through `read_document` or an explicit opt-in. Reuse the existing `StructuredSearchResult` for HTTP as well as MCP/CLI, with a versioned or opt-in contract if changing `/search` would be breaking. Do not copy GPL/LGPL code; reimplement the behavior and keep the target's MIT licensing boundary intact.
  - **Effort/Impact:** Medium effort / high impact. No new dependency and reversible at the response layer. It should reduce MCP/HTTP context payloads and improve citation-focused agent routing; validate the default window with a fixture corpus using payload-size, snippet-recall, Unicode, Markdown, and script-residue tests before changing the public default.

## Considered and rejected

- **YaCy's P2P peer search, remote federation, and Solr sharding** — gate 1 (Transferable): `README.md` and `source/net/yacy/cora/federate/` assume a distributed search appliance, while `AGENTS.md` and `internal/engine/retrieval.go:21-31` define Seek as a standalone local-first tool with no required remote service.
- **YaCy's query modifiers and facet/navigator system** — gate 4 (Worth it): `source/net/yacy/search/query/QueryModifier.java:205-275`, `source/net/yacy/search/query/QueryModifier.java:302-330`, and `source/net/yacy/search/query/QueryParams.java:319-340` would require a new grammar, metadata schema, and filtering UI; Seek currently stores only path/time/chunk metadata in `internal/db/db.go:15-33` and has no recorded demand for web-style host/filetype facets.
- **YaCy's adaptive `SearchEventCache`** — gate 4 (Worth it): `source/net/yacy/search/query/SearchEventCache.java:44-95` and `source/net/yacy/search/query/SearchEventCache.java:120-199` cache long-lived asynchronous local/remote search events; Seek's `internal/engine/retrieval.go:31-126` is synchronous and `internal/db/db.go:192-216` already invalidates its in-memory vector index across writers, so a result cache would add stale-result rules without a recorded repeated-query bottleneck.
- **YaCy's page-offset result queue sizing** — gate 1 (Transferable): the upstream fix in `source/net/yacy/search/query/SearchEvent.java:357` and commit `7c0a00a` protect paginated remote result pages, while Seek exposes a bounded `limit` rather than offset pagination in `cmd/symseek/main.go:125-129` and `internal/mcp/server.go:56-72`.
- **YaCy's full web appliance, crawler scheduler, and install/runtime packaging** — gate 1 (Transferable): `README.md`, `build.xml`, `htroot/`, and `source/net/yacy/crawler/` solve a different deployment shape from Seek's CLI/MCP/local HTTP daemon; importing that surface would violate standalone simplicity.
- **YaCy's RAG/web-search convergence as a separate retrieval path** — gate 2 (New): the target already has one hybrid retrieval core shared by interfaces in `internal/engine/retrieval.go:21-31` and documents that architecture in `docs/research/README.md:36-52`; the recent upstream alignment in `source/net/yacy/ai/RAGAugmentor.java` adds no new target capability.
- **YaCy's query-response accumulation for facets/highlighting** — gate 2 (New): the target already has a stable structured result envelope in `internal/db/db.go:65-93`; `source/net/yacy/cora/federate/solr/instance/ResponseAccumulator.java:31-121` is primarily a Solr response merger for remote shards, not a missing local result contract.
- **YaCy's snippet cache as a standalone dependency or copied implementation** — gate 4 (Worth it): `source/net/yacy/search/snippet/TextSnippet.java:109-133` is GPL-licensed implementation code and its cache is coupled to YaCy's document-loading lifecycle; the transferable value is the snippet behavior, not another cache/security surface or code copy.

## Open questions

- Should the HTTP `/search` endpoint change to the existing structured envelope by default, or should a new versioned endpoint preserve raw compatibility?
- What default snippet window preserves enough evidence for Seek's current chunking strategy, and should callers be able to request a larger snippet without receiving embeddings?
- A target-side fixture corpus and payload-size measurement are needed to confirm that bounded snippets reduce agent context without lowering citation usefulness.

The best first step is to prototype the snippet builder and compare payload size plus query-term coverage against the current full-chunk `Snippet` field.
