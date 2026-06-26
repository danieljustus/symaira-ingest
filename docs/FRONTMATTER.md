# Frontmatter YAML Schema (v0.1)

Generated Markdown notes written to the vault by `symingest` include a YAML frontmatter block that defines metadata about the document. This schema is structured to be compatible with Obsidian, `symseek`, and the broader Symaira ecosystem.

## Frontmatter Fields

| Field | Go Type | YAML Representation | Description |
|---|---|---|---|
| `source_path` | `string` | `source_path: <path>` | The absolute path to the original ingested file on the local filesystem. |
| `ingested_at` | `time.Time` | `ingested_at: <ISO-8601>` | UTC timestamp when the document was successfully ingested. |
| `sha256` | `string` | `sha256: <hash>` | SHA-256 hash of the original file content, used for duplicate detection. |
| `mime` | `string` | `mime: <mime-type>` | Normalized MIME type detected for the source file (e.g. `application/pdf`, `image/png`, `text/plain`). |
| `tags` | `[]string` | `tags: [...]` | Obsidian-compatible tag list. Initialized to `[]` by default. |
| `category` | `string` | `category: ""` | Document classification category. Initialized to `""` by default. |
| `ocr_engine` | `string` | `ocr_engine: <engine>` | Name of the OCR/extraction engine used (e.g. `tesseract`, `pdftoppm+tesseract`). Omitted if no OCR was performed. |
| `archive_path` | `string` | `archive_path: ""` | Path to the safely archived copy of the original source file. Initialized to `""` by default. |

## Example Frontmatter Blocks

### Ingested PDF with OCR
```yaml
---
source_path: /Users/daniel/Scans/invoice.pdf
ingested_at: 2026-06-26T15:00:00Z
sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
mime: application/pdf
tags: []
category: ""
ocr_engine: pdftoppm+tesseract
archive_path: ""
---
```

### Ingested Plain Text (No OCR)
```yaml
---
source_path: /Users/daniel/Notes/todo.txt
ingested_at: 2026-06-26T15:00:00Z
sha256: 09c2a6136fa512ba5c24e754d92427ae41e4649b934ca495991b7852b85514b8a1
mime: text/plain
tags: []
category: ""
archive_path: ""
---
```
