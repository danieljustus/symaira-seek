# Contributing to Symaira-Seek

Thank you for your interest in contributing to Symaira-Seek! This document provides guidelines and information for contributors.

## Getting Started

### Prerequisites

- Go 1.26 or later
- Git

### Setup

1. Fork the repository
2. Clone your fork:
   ```bash
   git clone https://github.com/<your-username>/symaira-seek.git
   cd symaira-seek
   ```
3. Create a branch for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

### Building

```bash
go build -o seek ./cmd/seek
```

### Running Tests

```bash
go test ./...
```

### Code Quality

Before submitting a PR, ensure your code passes all checks:

```bash
go vet ./...
```

If you have `staticcheck` installed:
```bash
staticcheck ./...
```

## Making Changes

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions focused and small
- Add comments for non-obvious logic
- Use meaningful variable and function names

### Commit Messages

- Use clear, descriptive commit messages
- Start with a verb in imperative mood (e.g., "Add feature", "Fix bug")
- Keep the subject line under 72 characters
- Reference issue numbers when applicable (e.g., "Fix #42")

### Pull Requests

1. Ensure all tests pass
2. Update documentation if your change affects user-facing behavior
3. Add tests for new functionality
4. Keep PRs focused on a single change
5. Fill out the PR template completely

## Reporting Issues

- Use the provided issue templates when available
- Include steps to reproduce for bugs
- Provide environment details (OS, Go version)
- For feature requests, explain the use case

## Security

For security vulnerabilities, please see [SECURITY.md](.github/SECURITY.md).

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
