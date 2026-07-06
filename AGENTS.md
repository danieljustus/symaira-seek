# Agent Instructions

This repository is the public MIT-licensed Symaira Seek self-hosted foundation.

## Ecosystem Guidance

- Before changing cross-tool integrations, shared conventions, or product
  boundaries, read `../docs/00-MASTERPLAN.md` and `../ECOSYSTEM.md`.
- Keep the standalone-first contract: this repo must build, test, and run
  without any other Symaira tool installed.

## Repository Role

- Keep this repository buildable, testable, and runnable without any private commercial code.
- Self-hosted Symaira Seek remains free and open source under the MIT License.
- Do not add Cloud Pro, hosted-service, tenant-management, billing, subscription, customer-support, or commercial deployment code here.

## Architecture & Code Style Guidelines

- **CGO-Free Go**: All database drivers (SQLite) and vector operations (Cosine Similarity) must remain 100% CGO-free for ultimate cross-platform compilation.
- **Database Safety**: Keep SQLite in WAL (Write-Ahead Logging) mode inside standard XDG directories to support simultaneous reads/writes.
- **Zero Stdio Pollution**: The MCP server transport runs over stdio. Under no circumstances must any package print to `os.Stdout` unless it is a structured JSON-RPC 2.0 message. All logs, warnings, and trace states must be safely routed to `os.Stderr` to prevent client handshake drop errors.

## macOS Client (`client/`)

- Pure SPM executable (`cd client && swift build` → `symseek-gui`; local
  builds need `DEVELOPER_DIR` pointing at Xcode).
- Depends on the shared **symaira-appkit** package, pinned exact (`0.1.0`)
  in `client/Package.swift`: SymairaTheme (shared `Color.symaira*` tokens;
  seek-specific border/glow tokens and `SymairaCardModifier` stay local in
  `ThemeBridge.swift`), SymairaToolKit (binary discovery in
  `EngineManager`).
- `EngineManager` is a long-running daemon supervisor and stays app-local
  by design — it defines the requirements for a future SymairaDaemonKit
  (appkit v0.2); do not replace it with CLIRunner one-shot calls.
- Migration context: see `../docs/symaira-appkit-migration.md` (Welle 2).
