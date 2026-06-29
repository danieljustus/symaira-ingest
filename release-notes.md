## What's changed

### Features
- #75 Add Paperless-ngx import command and MCP tool
- #74 Add IngestOptions for preset metadata override
- #70 Add LIMIT to ListJobs query for bounded result sets

### Fixes
- #68 Prevent goroutine and watcher leak in MCP start_watch tool
- #67 Fix race conditions, permissions, and watcher TTL in ingest pipeline
- #59 JSON-encode MCP tool handler results for valid TextContent payloads

### Refactoring
- #69 Extract shared config resolution helper and standardize --help

### CI/Build
- #57 Add GoReleaser + macOS signing/release workflow
- #56 Add CodeQL code scanning workflow for Go and Actions

**Full Changelog**: https://github.com/danieljustus/symaira-ingest/compare/v0.1.0...v0.2.0
