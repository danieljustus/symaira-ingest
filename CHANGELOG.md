# Changelog

All notable changes to Symaira Ingest are documented here.

## [0.12.0] - 2026-08-14

### Added

- Updated the macOS DMG with unified Symaira branding and a drag-to-Applications window.

### Changed

- Updated the Symaira Corekit and `modernc.org/sqlite` runtime dependencies.
- Updated the CodeQL GitHub Actions dependency group.

### Fixed

- Corrected OLE compound-file sector sizing so directory entries and UTF-16 stream names can be read according to the MS-CFB specification.
- Added regression coverage for container-detection fallback paths.

### Security

- Migration reports are now written with owner-only permissions (`0600`) because they can contain document titles, correspondents, and vault/archive paths.

### Closed issues

- #284 — Unified Symaira branding and drag-to-Applications DMG window.
- #289 — Cover container-detection fallback helpers and correct OLE sector sizing.

[0.11.0]: https://github.com/danieljustus/symaira-ingest/releases/tag/v0.11.0
