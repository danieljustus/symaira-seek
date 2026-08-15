## What's changed

This patch release hardens the local HTTP server lifecycle, refreshes the
macOS installer branding, and updates the Go toolchain and runtime
dependencies.

### Server stability
- The bound HTTP listener is now awaited before serving begins, so lifecycle
  tests and callers observe the real assigned address (#358)
- Error paths of the HTTP server are covered by tests: occupied-port failure,
  serve-error propagation, and graceful shutdown releasing the bound port (#363)

### macOS app
- The DMG now ships unified Symaira branding with a drag-to-Applications
  window (#352, fixes #351)

### Security & dependencies
- Go toolchain 1.26.5 → 1.26.6, fixing six standard-library vulnerabilities
  reported by govulncheck (net/url, crypto/tls, net/http, encoding/xml,
  encoding/asn1) (#360)
- symaira-corekit 0.8.0 → 0.9.1 (#359)
- modernc.org/sqlite 1.55.0 → 1.56.0 (#359)
- Pinned actions/upload-artifact (v7.0.1) and staticcheck (2026.1) for
  reproducible builds (#361)

### Docs
- Apache-2.0 license stated consistently across AGENTS.md and CONTRIBUTING.md
  (#353)
- Human-readable search output example added to the README (#357)

### Closed Issues
- #351 DMG installs with missing branding and no drag-to-Applications window
- #362 Add error-path tests for HTTP server listener startup

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v2.6.0...v2.6.1
