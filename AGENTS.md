# Agent Instructions

This repository is the public MIT-licensed Symaira Seek self-hosted foundation.

## Repository Role

- Keep this repository buildable, testable, and runnable without any private commercial code.
- Self-hosted Symaira Seek remains free and open source under the MIT License.
- Do not add Cloud Pro, hosted-service, tenant-management, billing, subscription, customer-support, or commercial deployment code here.

## Architecture & Code Style Guidelines

- **CGO-Free Go**: All database drivers (SQLite) and vector operations (Cosine Similarity) must remain 100% CGO-free for ultimate cross-platform compilation.
- **Database Safety**: Keep SQLite in WAL (Write-Ahead Logging) mode inside standard XDG directories to support simultaneous reads/writes.
- **Zero Stdio Pollution**: The MCP server transport runs over stdio. Under no circumstances must any package print to `os.Stdout` unless it is a structured JSON-RPC 2.0 message. All logs, warnings, and trace states must be safely routed to `os.Stderr` to prevent client handshake drop errors.
