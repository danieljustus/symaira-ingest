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
- **Paperless-ngx import** — pull documents from an existing Paperless-ngx instance, preserving tags, correspondent and document type as frontmatter

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

```text
$ symingest ingest -vault ~/vault -db ~/.local/share/symingest/jobs.db invoice.txt
ingested: invoice.txt
engine: text
text length: 71
```

The resulting note in your vault carries YAML frontmatter plus the extracted text:

```text
---
source_path: invoice.txt
ingested_at: 2026-06-30T22:24:49.542837Z
sha256: 39f4280386fd5df04e0e06d7d7fa1c5a2aaaa54b643e92ae9c859c0c6f1117d6
mime: text/plain
tags: []
category: ""
ocr_engine: text
archive_path: ~/.local/share/symingest/archive/39f4280386fd5df04e0e06d7d7fa1c5a2aaaa54b643e92ae9c859c0c6f1117d6.txt
---

INVOICE #4471
Acme Hardware Supply
Date: 2026-03-12
Total Due: $284.50

---
[Archived Original](file:///.../archive/39f4280386fd5df04e0e06d7d7fa1c5a2aaaa54b643e92ae9c859c0c6f1117d6.txt)
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

**Import from a Paperless-ngx instance:**

```bash
symingest import paperless --base-url https://paperless.example.com --token <api-token> --vault ~/vault
```

```text
Flags:
  --base-url string   Paperless-ngx instance URL (or PAPERLESS_URL env)
  --token string      API token (or PAPERLESS_TOKEN env)
  --since string      Only import documents whose Paperless created date is on
                      or after this date (YYYY-MM-DD)
  --limit int         Import at most N documents (newest first); 0 means no limit
  --ids string        Import only these Paperless document IDs (comma-separated,
                      e.g. 123,456); takes precedence over --since and --limit
  --vault string      Target vault directory
  --archive string    Target archive directory
  --db string         SQLite database path
  --dry-run           List what would be imported without writing
  --report string     Write a JSON migration report to this path (works with
                      --dry-run and real imports)
  --verify            Verify a completed import against the Paperless source
                      (compares notes, archived originals, and metadata), then exit
  --status            List per-document import status from a previous run, then exit
  --json              With --status or --verify, output the result as JSON
```

`--report <path>` writes a stable JSON migration report for a dry-run or a real import: overall counts plus a per-document array (`id`, `status`, optional `reason`, and generated `vault_path`/`archive_path`), collected warnings, and — for a dry-run — the unsupported file types and unresolved metadata IDs from the audit. Like every other output it contains no document content, so it is safe to hand to a review step or a later UI.

After an import, `--verify` re-reads the Paperless source and the generated vault notes and reports any document that is missing, duplicated, missing its archived original, or whose metadata (tags, correspondent, document type, storage path, created date) drifted from the source. It prints a human summary, or a stable JSON report with `--json`, and exits non-zero when any discrepancy is found — suitable as an automated migration gate before Paperless is retired. Only IDs, field names, and paths appear in the output; document content never does.

`--since` filters on the document's Paperless *created* date (the date shown on the document), not the date it was added to Paperless. Use `--limit` or `--ids` to run a small, inspectable pilot before a full migration; both bounds apply to `--dry-run` and real imports alike, and a bounded run echoes the selected document IDs. Imports are resumable: a document already recorded as imported is skipped on a re-run, and a document that previously failed is retried automatically. Also available as the `import_paperless` MCP tool, which accepts the same options (`base_url`, `token`, `since`, `dry_run`, plus optional `vault_path`/`archive_path`/`db_path` overrides).

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
