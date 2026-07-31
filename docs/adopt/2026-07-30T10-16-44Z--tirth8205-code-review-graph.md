<!-- review: timestamp=2026-07-30T10:16:44Z  repo=danieljustus/symaira-seek  head=0d95ee9 -->
<!-- adopt: source=tirth8205/code-review-graph  source_ref=90d760aa23fac0353637d2e8f2a431aa08f14366  source_url=https://github.com/tirth8205/code-review-graph  depth=clone  license=MIT -->

# Adoption Report — symaira-seek ← tirth8205/code-review-graph — 2026-07-30

## Sources

| Field | Value |
|---|---|
| SOURCE | `tirth8205/code-review-graph` (https://github.com/tirth8205/code-review-graph) |
| Ref analyzed | `90d760aa` (main) |
| Language / License | Python 3.10+ (94%), TypeScript (VS Code ext) / MIT |
| Health | 27.7k stars, last push 2026-07-27, active PyPI releases, CI + weekly eval, 65 test fixtures |
| Scope | all facets, full clone |
| TARGET | `danieljustus/symaira-seek` @ `0d95ee9` (Go 1.26.5, MCP + CLI + SwiftUI client) |

## Verdict

Same shape as symaira-seek: a local-first SQLite index served to AI clients over MCP, with hybrid retrieval and a CLI. The one thing that clearly transfers is measurement. symaira-seek has the more sophisticated retrieval stack — RRF fusion, binary quantization, an LLM reranker with a score-blending function — and **no way to tell whether any of it helps**: there is no ground-truth corpus, no MRR/recall metric, and nothing in CI that would notice a quality regression. CRG has a weaker retrieval stack and a far better feedback loop around it. That asymmetry is the report. A second, smaller finding covers a CI guard against the Go↔Swift schema-constant drift that our two-language handshake currently leaves unchecked. Security-wise CRG has nothing to teach us — we already implement its loopback guard, better.

## What we already do as well or better

- DNS-rebinding protection on the local HTTP endpoint (`http_origin_guard.py`) → `internal/server/server.go:100` `hostValidation` + `:112` `originValidation` already validate both headers on every request, with tests. Equivalent coverage.
- Rate limiting on the HTTP surface → `internal/server/server.go` `rateLimiter` with eviction; CRG has none.
- Table-driven config key registry → landed in `c8ff53f` ("replace the SetValue switch with a table-driven key registry"), matching CRG's `config_keys.py`.
- Single handler for streaming and non-streaming search → `1eff16b` already merged `/search` and `/search/stream`; CRG keeps these separate.
- SHA-pinned GitHub Actions and pinned `govulncheck` → `6a81e49`; CRG still uses floating `actions/checkout@v7` and `actions/setup-python@v7`.
- Hybrid retrieval with a reranking stage → `internal/engine/retrieval.go:25` `SearchHybrid` + `internal/engine/rerank.go`; strictly richer than CRG's `hybrid_search`.
- Fast PR gate vs. full weekly suite → already the shape of `.github/workflows/ci.yml`.

## Findings

- [ ] **[Architecture] Add a pinned-corpus retrieval evaluation harness and run it weekly, report-only**
  - **Status quo:** `symaira-seek/internal/engine/retrieval.go:25` (`SearchHybrid`), `internal/engine/rerank.go:94` (`blendScore` — an RRF/reranker blend with hand-tuned weights) and `internal/db/search_quantized.go` (binary quantization, a lossy step) are all covered only by unit tests that assert mechanics, never quality: `rerank_test.go` checks that parsing and blending do not crash, not that ranking improves. There is no ground-truth query set, no MRR or recall@k anywhere in the repo, and no CI job that would catch a regression. Concretely: a change to `blendScore`'s weights, or to the quantization codec in `internal/db/quant.go`, can silently make results worse and the test suite stays green. Upstream `tirth8205/code-review-graph` closes this loop: `code_review_graph/eval/scorer.py:44` implements MRR and precision/recall/F1, `code_review_graph/eval/benchmarks/search_quality.py:12` runs a query set against the real search path with a documented fallback, `code_review_graph/eval/configs/*.yaml` pin each corpus repo to an exact commit SHA (with `runner.py:48` refusing a config whose pin has drifted from its latest test commit), and `.github/workflows/eval.yml` runs the two smallest corpora weekly, uploads CSVs for 90 days and renders a table into the job summary. Its workflow header documents the restraint: report-only, `|| true`, no gate on main until baseline history exists.
  - **Proposed solution:** Pattern adoption (SOURCE is MIT; reimplement in Go rather than copy). Add `internal/bench/` with (a) a small pinned fixture corpus of documents plus a query→expected-document ground-truth set as testdata, (b) `MRR`, `RecallAtK` and `NDCGAtK` scorers, (c) a runner that executes the real `SearchHybrid` path across configurations (vector-only / hybrid / hybrid+rerank) and emits CSV. Wire it to a `symseek bench` subcommand, then add `.github/workflows/eval.yml` on a weekly cron plus `workflow_dispatch`, report-only. Our sibling repo `symaira-memory` already has this exact structure in `internal/bench/` (`corpus.go`, `metrics.go`, `harness.go`) — port that layout rather than CRG's Python one; only the CI-integration and pinned-snapshot ideas come from upstream.
  - **Effort/Impact:** Medium effort / high impact. Building an honest ground-truth set is the real cost — the scorers and runner are a day's work, and `symaira-memory/internal/bench` supplies the Go template. Fully reversible; no production code changes. Confidence high: the mechanism is visible, tested and documented upstream, and the gap on our side is independently obvious.

- [ ] **[UX/DX] Fail CI when the Go and Swift schema-version constants diverge**
  - **Status quo:** The Go↔Swift handshake version is declared twice as an unlinked literal: `cmd/symseek/main.go` passes `1` to `versionkit.New("symseek", version, 1)`, and `client/Sources/SymseekFeature/SymseekModule.swift` declares `public static let expectedSchemaVersion = 1`. Nothing checks that they agree. Bumping the Go side for a machine-output contract change and forgetting the Swift side produces a client that silently accepts a payload shape it was not written for — a failure that surfaces at runtime in the GUI, not in CI. `symaira-scope` has the identical pair and the identical gap. Upstream `tirth8205/code-review-graph` guards exactly this with a dedicated `schema-sync` job (`.github/workflows/ci.yml:52`) that extracts the version constant from each language's source and fails the build with `::error::Schema version mismatch!` when they differ — added because their Python core and TypeScript VS Code extension read the same SQLite schema.
  - **Proposed solution:** Pattern adoption; the mechanism is a shell job, not code. Add a `schema-sync` job to `.github/workflows/ci.yml` that greps the integer out of `versionkit.New("symseek", version, N)` in `cmd/symseek/main.go` and out of `expectedSchemaVersion = N` in `SymseekModule.swift`, and exits non-zero on mismatch. Keep it grep-based and dependency-free as upstream does. If the extraction proves brittle, the sturdier variant is a generated Swift constant, but that is a larger change and not required to close the gap.
  - **Effort/Impact:** Low effort / medium impact. Under an hour, no new dependency, trivially reversible, and it converts a runtime GUI failure mode into a CI failure. The same job is worth adding to `symaira-scope` unchanged.

- [ ] **[UX/DX] Return index provenance with MCP tool results so the agent can see a stale index**
  - **Status quo:** `internal/mcp/server.go:52` (`search_documents`), `:213` (`list_documents`) and `:248` (`get_context`) return content with no indication of when the index was last built or whether it covers the current state of the indexed sources. An agent receiving results from an index that has not been synced since a document changed cannot tell, and neither can the user reading the transcript — the results simply look authoritative. `internal/engine/sync.go` and the fsnotify watcher reduce the window but do not close it (watch is not always running, and remote/URL-indexed documents have no watcher at all). Upstream `tirth8205/code-review-graph` addresses this in `code_review_graph/tools/_common.py:60` (`graph_provenance`), attaching build timestamp, age in seconds, indexed branch/SHA and a `head_matches_build` boolean to context-tool responses. The docstring records the motivation from their issue #458: an earlier version reported a blanket `is_stale=False` that overstated freshness, so the replacement deliberately compares only what it can actually prove.
  - **Proposed solution:** Pattern adoption. Add an optional provenance block (`indexed_at`, `age_seconds`, `document_count`) to the responses of the read tools in `internal/mcp/server.go`, sourced from existing index metadata. Take upstream's discipline with it: report only what is verifiable, never a synthesized "fresh/stale" verdict, and never let a missing or unreadable metadata row fail the enclosing tool call. Upstream wires this into a single tool (`tools/context.py:88`); starting with `get_context` alone is the proportionate first move.
  - **Effort/Impact:** Low effort / medium impact. Additive and reversible. Confidence medium — the motivation is documented upstream, but the pattern is thin there (one call site), and our watcher already covers the common case, so the win is on the URL-indexed and watch-off paths rather than across the board.

## Considered and rejected

- **`http_origin_guard.py` — Host/Origin validation against DNS rebinding** — gate 2 (New): already implemented in `internal/server/server.go:100-120`, on every request, with tests. Ours also covers the full loopback range.
- **Standardised error-response envelope** (`tools/_common.py:22` `_error_response`) — gate 2 (New): our MCP handlers already return structured errors; `internal/server/server.go` uses explicit status codes throughout.
- **Table-driven config key registry** (`code_review_graph/config_keys.py`) — gate 2 (New): landed as `c8ff53f`. Deliberate prior decision, same conclusion.
- **Bandit / ruff / mypy CI jobs** — gate 1 (Transferable): Python tooling; our `golangci-lint` + `govulncheck` pipeline is the equivalent and already stricter (SHA-pinned, version-pinned).
- **Tree-sitter structural parsing of source code** (`code_review_graph/parser.py`, 15.6k lines) — gate 1 (Transferable): CRG indexes code structure to build a call graph; symaira-seek indexes documents for semantic retrieval. Different problem; the machinery has no place to attach.
- **Multi-client MCP installer + symmetric uninstall** (`skills.py`, `uninstall.py`) — gate 4 (Worth it): `symaira-scope` already owns MCP client discovery and `mcp add/rm` for this ecosystem. A second writer to the same user config files is cost without benefit.
- **VS Code extension in-repo** (`code-review-graph-vscode/`) — gate 4 (Worth it): we ship a native SwiftUI client; a second GUI surface has no stated demand.
- **Five translated READMEs, Discord, trendshift badge** — gate 4 (Worth it): 27.7k-star community infrastructure. Scale-fit failure for a solo repo.
- **Pagination/bounding of list results** — gate 2 (New): already tracked as our own open issue #293; CRG adds no mechanism we do not already know we want.

## Open questions

- CRG's `search_quality` benchmark matches an expected result by substring/suffix comparison (`benchmarks/search_quality.py:38-52`), which is generous and inflates MRR when qualified names share suffixes. Whether that trade is acceptable depends on how their ground-truth set was authored, which the repo does not record. If we adopt the harness we should decide our own matching strictness deliberately rather than inheriting theirs — exact document-ID matching is the safer default and costs nothing.
- Whether an honest ground-truth query set for symaira-seek should be synthetic (authored fixtures, fully deterministic, cheap) or drawn from real indexed corpora (realistic, but licensing and reproducibility become issues) cannot be settled from CRG's example — they use pinned public repos, which is a corpus type we do not have an equivalent of. A dozen hand-authored queries over the existing testdata would settle whether the metric moves at all before investing in more.

**First step:** stand up `internal/bench/` for symaira-seek by porting the layout of `symaira-memory/internal/bench/` and scoring the current `SearchHybrid` against a dozen hand-authored ground-truth queries — that one number tells you whether the reranker is earning its latency.
