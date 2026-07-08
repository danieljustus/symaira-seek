## What's changed

### Native macOS GUI
- #239 Build native macOS app (`Symseek.app`) and DMG installer
- #240 Embed `symseek` daemon inside the macOS app via `SymairaDaemonKit`
- The macOS app is available as a drag-and-drop DMG from GitHub Releases

### Grounded Extraction Sidecars
- #251 Index grounded extraction sidecars and expose extraction search
- `symseek extract search` searches extraction values and evidence text
- `symseek extract list` lists imported extractions, optionally filtered by class
- `symseek extract import` imports a sidecar JSONL for an already-indexed document
- Markdown documents with a frontmatter `sha256` auto-discover sidecars in `.symaira/extractions/`

### Search API
- Stable chunk IDs, structured results, and path-scope filter (`--path`)
- Search results now include character offsets, RRF score, BM25 rank, and vector rank
- `symseek search --json` returns the new structured format

### Tooling & Integration
- `symseek version --json` emits machine-readable version and schema metadata
- `SymseekModule.expectedSchemaVersion` is now `1` for the macOS module integration

### Closed Issues
- #239 Native macOS GUI
- #240 macOS app bundle and DMG
- #251 Grounded extraction sidecars

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v2.2.2...v2.3.0
