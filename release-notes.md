## What's changed

This patch release ships the v2.5.0 feature set and focuses on the document
parser (new formats, block boundaries, archive safety), release-workflow fixes,
and dependency bumps.

### v2.5.0 highlights
- Root-level Swift package so consumers can pin by tag (#336)

### Parser improvements
- HTML and ODF (`.odt`/`.ods`/`.odp`) and CSV documents are now indexed (#341)
- Office extraction preserves block boundaries: paragraph breaks, line breaks,
  tabs, and row separators (#339)
- HTML markup is stripped before indexing; `script`/`style`/`head` subtrees are
  dropped and block-level boundaries are emitted (#340)
- Whole-archive decompression budget (100 MiB, max 10 000 entries) protects
  against decompression bombs (#342)
- Unindexable document formats now emit a skip message naming the file (#341)

### Release workflow
- DMG Info.plist now receives the real tag version (was literal `__VERSION__`)
  and a `PkgInfo` file; a verification step fails the release when the bundle
  metadata does not match the tag (#335)

### Fixes & housekeeping
- Root SwiftPM build artifacts ignored (#344)
- Adoption reports kept out of version control (#343)

### Dependencies
- symaira-corekit 0.7.0 → 0.8.0 (#338)
- actions/upload-artifact 4 → 7 (#337)

### Notes
- Existing corpora must be reindexed for the improved extraction to take
  effect.

### Closed Issues
- #339 Preserve block boundaries when extracting Office text
- #340 Strip markup before indexing HTML
- #341 Make unindexable document formats visible, then add ODF and CSV
- #342 Add a whole-archive decompression budget

**Full Changelog**: https://github.com/danieljustus/symaira-seek/compare/v2.5.0...v2.5.1
