# symaira-ingest

Document ingestion + OCR core for the Symaira ecosystem.

## Overview

`symingest` is a standalone Go CLI + MCP server that turns scanned PDFs, images, and office files into searchable Markdown notes with YAML frontmatter. Think of it as a Paperless-ngx "consume" pipeline that emits plain Markdown instead of a proprietary archive.

- **Language:** Go 1.26.4 (CGO-free, `modernc.org/sqlite`)
- **Built on:** `symaira-corekit`
- **External tools (shelled out):** `tesseract`, `pdftoppm`
- **License:** Apache-2.0
- **Status:** Planning — see local design notes in `docs/`

## Design Rules

- `symingest` writes Markdown + frontmatter into the vault and **stops there**
- Indexing and embeddings are `symseek`'s job
- Must remain fully standalone — no dependency on other Symaira binaries at startup
- Uses `symaira-corekit/mcpserver` for MCP transport

## Entry Point

```
cmd/symingest/main.go
```

Standard Go layout: `cmd/<name>/main.go` → `internal/` packages.

## Internal Packages

| Package | Purpose |
|---------|---------|
| `config` | CLI flag parsing, config file loading |
| `ingest` | Core pipeline orchestration, dedup detection |
| `extract` | Text extraction from PDFs/images (tesseract, pdftoppm) |
| `ocr` | OCR engine abstraction |
| `frontmatter` | YAML frontmatter generation |
| `writer` | Markdown note writer (vault path resolution) |
| `store` | SQLite-backed dedup store (`modernc.org/sqlite`) |
| `mcp` | MCP server registration (`ingest_file` tool) |
| `version` | Version string embedding |

## MCP Server

Exposes one tool via stdio transport:

- **`ingest_file`** — Ingest a single file, returns metadata (vault_path, MIME, engine, text_length)

**Zero stdio pollution:** All logs to `os.Stderr`. Only JSON-RPC 2.0 on `os.Stdout`.

## Developer Commands

```bash
cd symaira-ingest
go build ./cmd/symingest        # build binary
go test ./...                    # run tests
go run ./cmd/symingest --help    # CLI help
```

No Makefile. Build and test are plain `go` commands.

## Testing

- 8 test files across `cmd/` and `internal/`
- Standard `go test ./...` — no race detector, no coverage in CI
- No fuzz tests, no smoke tests, no integration tests yet

## CI

- Single `ci.yml` workflow
- Build + test on ubuntu-latest
- **Note:** Currently pinned to Go 1.23 (stale — should be 1.26.4)

## XDG Directory Convention

- Config: `~/.config/symingest/`
- Cache: `~/.cache/symingest/`
- Data: `~/.local/share/symingest/`
- Env prefix: `SYMINGEST_*`

## Anti-Patterns

- **NEVER** print to `os.Stdout` except structured JSON-RPC 2.0 messages
- **NEVER** echo secrets in chat or logs
- **NEVER** commit `replace ../symaira-corekit` in `go.mod`
- **DO NOT** add Cloud Pro, billing, or tenant-management code — this is a public repo

## macOS Client (`client/`)

- SwiftUI app (XcodeGen: `cd client && xcodegen generate`, scheme
  `Symingest`; local builds need `DEVELOPER_DIR` pointing at Xcode).
- Depends on the shared **symaira-appkit** package, pinned exact (`0.1.1`)
  in `client/project.yml`: SymairaTheme (via `ThemeBridge.swift`; the
  ingest-specific AmbientBackground/24px-BlueprintGrid stay local),
  SymairaCLIRunner (subprocess execution with SYMINGEST_* env injection)
  and SymairaToolKit (binary discovery incl. tesseract/pdftoppm/sips —
  replaces the former `/usr/bin/which` subprocess).
- `EngineManager` (watch-mode daemon supervisor) stays app-local — third
  requirements donor for a future SymairaDaemonKit (appkit v0.2).
- Do not reintroduce app-local Theme/Process-runner code; extend
  symaira-appkit instead. Migration context: see
  `../docs/symaira-appkit-migration.md`.
