# symaira-ingest (`symingest`)

[![CI](https://github.com/danieljustus/symaira-ingest/actions/workflows/ci.yml/badge.svg)](https://github.com/danieljustus/symaira-ingest/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Document ingestion + OCR core for the [Symaira](https://github.com/danieljustus?tab=repositories&q=symaira) ecosystem.

Drop a scanned PDF or an image into a folder → get a searchable, classified **Markdown** note out. Think of it as a Paperless-ngx "consume" pipeline that emits plain Markdown + YAML frontmatter instead of a proprietary archive.

## What is this / Why use it?

- **Standalone CLI + MCP server** — no external services required, runs entirely on your machine
- **OCR for scanned documents** — extracts text from PDFs and images using Tesseract
- **Markdown output** — produces clean, searchable Markdown with YAML frontmatter instead of proprietary formats
- **MCP integration** — works as an MCP tool for AI-powered document processing workflows
- **Classification rules** — automatically categorize documents based on content patterns

## Install

```bash
go install github.com/danieljustus/symaira-ingest/cmd/symingest@latest
```

**Prerequisites:**
- Go 1.26.4+
- `tesseract` (for OCR)
- `pdftoppm` (for PDF rendering)

## Usage

**Ingest a single file:**

```bash
symingest ingest /path/to/document.pdf
```

**Watch a directory for new files:**

```bash
symingest watch /path/to/inbox
```

**MCP server mode:**

```bash
symingest mcp
```

**Manage classification rules:**

```bash
symingest rules list
symingest rules add --pattern "*.pdf" --category "Documents"
```

**Check job queue:**

```bash
symingest jobs
symingest retry <job-id>
```

## Development

**Build:**

```bash
go build ./cmd/symingest
```

**Run tests:**

```bash
go test ./...
```

**Run linter:**

```bash
go vet ./...
```

## Architecture

`symingest` is part of the Symaira ecosystem:

- **symingest** (this repo) — Document ingestion + OCR
- **[symseek](https://github.com/danieljustus/symaira-seek)** — Search and retrieval
- **[symdesk](https://github.com/danieljustus/symaira-desktop)** — Desktop shell

> Design rule: `symingest` writes Markdown + frontmatter into the vault and **stops there**. Indexing and embeddings are `symseek`'s job.

## License

Apache-2.0
