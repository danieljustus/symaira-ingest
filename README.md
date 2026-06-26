# symaira-ingest (`symingest`)

Document ingestion + OCR core for the [Symaira](https://github.com/danieljustus?tab=repositories&q=symaira) ecosystem.

Drop a scanned PDF or an image into a folder → get a searchable, classified **Markdown** note out. Think of it as a Paperless-ngx "consume" pipeline that emits plain Markdown + YAML frontmatter instead of a proprietary archive.

`symingest` is a standalone Go **CLI + MCP server**. It is the one genuinely new in-policy capability in the ecosystem: turning scans/PDFs/office files into text (the existing search core, [`symseek`](https://github.com/danieljustus/symaira-seek), deliberately has "no OCR in scope"). It runs on its own, and is composed at runtime by the [`symdesk`](https://github.com/danieljustus/symaira-desktop) shell.

- **Language:** Go (CGO-free, `modernc.org/sqlite`), built on `symaira-corekit`
- **External tools (shelled out):** `tesseract`, `pdftoppm`
- **Status:** planning — see local design notes in `docs/` (not published)
- **YAML Frontmatter:** see [docs/FRONTMATTER.md](file:///Users/daniel/Dev/Symaira%20Dev/symaira-ingest/docs/FRONTMATTER.md) for the v0.1 note metadata contract.
- **License:** Apache-2.0

> Design rule: `symingest` writes Markdown + frontmatter into the vault and **stops there**. Indexing and embeddings are `symseek`'s job.
