## What's changed

### Build & Release
- #227 Sign and notarize macOS binaries with Developer ID — macOS users will no longer see Gatekeeper "malware" warnings when running `symseek`.
- #230 Migrate Homebrew cask to formula with service and test — adds a `launchd` service and a `brew test` block.

### Fixes
- #228 Fix `symseek` release version injection — release builds now report the correct version via `symseek version` and the MCP server info.
- #231 Backfill legacy NULL `embedding_dim` and tolerate mixed spaces — closes #229.

### Closed Issues
- #229 Legacy chunks with NULL `embedding_dim` need backfill.

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v2.2.2...v2.3.0
