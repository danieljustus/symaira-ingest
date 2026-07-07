# symaira-ingest (`symingest`)

[![CI](https://github.com/danieljustus/symaira-ingest/actions/workflows/ci.yml/badge.svg)](https://github.com/danieljustus/symaira-ingest/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Document ingestion + OCR core for the [Symaira](https://github.com/danieljustus?tab=repositories&q=symaira) ecosystem.

Drop a scanned PDF, image, or text-like export into a folder → get a searchable, classified **Markdown** note out. Think of it as a Paperless-ngx "consume" pipeline that emits plain Markdown + YAML frontmatter instead of a proprietary archive.

## What is this / Why use it?

- **Standalone CLI + MCP server** — no external services required, runs entirely on your machine
- **OCR for scanned documents** — extracts text from PDFs and images using Tesseract (`pdf`, `png`, `jpeg`, `tiff`, `webp`, plus `heic/heif` on macOS via `sips`)
- **Text and structured imports** — preserves plain text, Markdown, CSV, HTML, RTF, DOCX, XLSX, ODT and EML without forcing them through OCR
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
- `sips` on macOS for direct HEIC/HEIF OCR when Paperless has no archived PDF rendition

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

Generated vault notes are private by default: note files are written with `0600` permissions and newly created vault subdirectories with `0700`, matching the archive and database defaults.

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
symingest rules add "invoice" category Finance
```

Rule patterns are case-insensitive substrings matched against extracted document text. They are not filename globs, so a pattern like `*.pdf` will only match literal text in a document, not PDF filenames.

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
  --preserve-storage-paths
                      Place notes under vault subdirectories derived from each
                      document's Paperless storage path (default: flat layout)
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

`--report <path>` writes a stable JSON migration report for a dry-run or a real import: `schema_version`, `tool_version`, overall counts plus a per-document array (`id`, `status`, optional `reason`, and generated `vault_path`/`archive_path`/`sha256`), collected warnings, and — for a dry-run — the unsupported file types and unresolved metadata IDs from the audit. Like every other output it contains no document content, so it is safe to hand to a review step or a later UI. A real import exits non-zero if any document fails; re-run the same command or use `--retry-failed` until `failed: 0`.

After an import, `--verify` re-reads the Paperless source and the generated vault notes and reports any document that is missing, duplicated, missing its archived original, or whose metadata (tags, correspondent, document type, storage path, created date) drifted from the source. It prints a human summary, or a stable JSON report with `--json`, and exits non-zero when any discrepancy is found — suitable as an automated migration gate before Paperless is retired. Only IDs, field names, and paths appear in the output; document content never does.

**Gate Paperless cutover:**

```bash
symingest cutover-check \
  --dry-run-report dryrun-report.json \
  --import-report import-report.json \
  --verify-report verify-report.json \
  --vault ~/vault \
  --min-documents 6000 \
  --min-body-length 40
```

For maximum source fidelity, run verification in deep mode before the cutover check:

```bash
symingest import paperless --verify --deep --json > verify-report.json
```

`--deep` re-downloads each selected Paperless original and compares its SHA-256 with the archived original in the vault. It is slower by design and belongs in the final migration gate, not every quick local check.

`cutover-check` is intentionally strict: Paperless stays the source of truth unless the full dry-run, real import, verifier output, and vault validation are all clean and the document counts agree. Use `--json` for CI or app integration.

Validate machine-readable report files before using them as cutover evidence:

```bash
symingest report validate dryrun-report.json
symingest report --json validate verify-report.json
```

Create a body-safe review surface and apply explicit corrections with count gates:

```bash
symingest review-report --failed --warnings --unsupported --unresolved --html review.html import-report.json
symingest review-report --duplicate-content --json verify-report.json
symingest apply-corrections --vault ~/vault --dry-run --require-count 3 corrections.yaml
symingest apply-corrections --vault ~/vault --require-count 3 --max 3 --backup-dir undo corrections.yaml
symingest bulk-update --vault ~/vault --where tag:needs-review --add-tag reviewed --dry-run --require-count 12 --max 12
```

`corrections.yaml` supports the versioned shape:

```yaml
schema_version: 1
corrections:
  - paperless_id: 123
    add_tags: [reviewed]
    correspondent: Example GmbH
```

For OCR quality checks, add a body-length gate to vault validation:

```bash
symingest validate-vault --min-body-length 40 --json ~/vault
```

This fails notes with empty or near-empty Markdown bodies, catching scanned documents where OCR technically ran but produced no useful text.

`--since` filters on the document's Paperless *created* date (the date shown on the document), not the date it was added to Paperless. Use `--limit` or `--ids` to run a small, inspectable pilot before a full migration; both bounds apply to `--dry-run` and real imports alike, and a bounded run echoes the selected document IDs. Imports are resumable: a document already recorded as imported is skipped on a re-run, and a document that previously failed is retried automatically. With `--preserve-storage-paths`, each note is placed under a vault subdirectory derived from the document's Paperless storage path instead of the vault root; unsafe path segments are sanitized and collisions are resolved deterministically. Also available as the `import_paperless` MCP tool, which accepts the same options (`base_url`, `token`, `since`, `dry_run`, `limit`, `ids`, `preserve_storage_paths`, `report_path`, plus optional `vault_path`/`archive_path`/`db_path` overrides).

For a complete, gated migration path — dry-run, bounded pilot, full import, verification, and search validation, with an explicit rule for when Paperless can stop being the source of truth — follow the [Paperless replacement runbook](docs/paperless-replacement-runbook.md).

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
