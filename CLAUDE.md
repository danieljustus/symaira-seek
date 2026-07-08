# Symaira Seek Developer Guidelines

Guidelines and commands for developers and AI agents working on this codebase.

## Build and Test Commands

- **Build binary**: `go build -o symseek ./cmd/symseek`
- **Run all tests**: `go test ./...`
- **Run verbose tests**: `go test -v ./...`

## CLI Verification Cheatsheet

- **Check version**: `./symseek version`
- **Search documents**: `./symseek search "search query"`
- **Index a folder**: `./symseek index /path/to/folder`
- **Watch folder**: `./symseek index /path/to/folder --watch`
- **Check status**: `./symseek status`
- **Start MCP Server**: `./symseek serve`
- **Start HTTP Daemon**: `./symseek serve -p 8788`

## Code Style & Formatting

- **Go Code style**: Follow standard `gofmt` guidelines.
- **Indentation**:
  - Go source files (`.go`): Use **tabs** for indentation (tab size 4).
  - Web & Config files (`.yaml`, `.json`, `.css`, `.html`, `.sh`, `.md`): Use **2 spaces** for indentation.
- **Imports order**: Standard Go grouping (stdlib block, space, external modules block).
- **Zero-CGO**: Maintain CGO-free compilations. Avoid importing packages that require C-linkers.
- **Standard Library first**: Prefer Go standard library over external dependencies where possible.
