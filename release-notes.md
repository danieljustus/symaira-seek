## What's changed

This patch release ships the v2.3.0 feature set and fixes the macOS DMG build in the release workflow.

### v2.3.0 highlights
- Native macOS GUI (`Symseek.app`) and DMG installer
- `Symseek` daemon embedded inside the macOS app via `SymairaDaemonKit`
- Grounded extraction sidecars with `symseek extract search` / `list` / `import`
- Search API stable chunk IDs, structured results, and path-scope filter (`--path`)
- `symseek version --json` emits machine-readable version and schema metadata

### Fixes
- Corrected the GoReleaser dist path used by the macOS DMG packaging step so the release workflow can build and upload `Symseek.dmg`.

### Closed Issues
- #239 Native macOS GUI
- #240 macOS app bundle and DMG
- #251 Grounded extraction sidecars

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v2.3.0...v2.3.1
