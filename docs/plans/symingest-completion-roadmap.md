# Symingest Completion Roadmap

> Local working roadmap for finishing Symaira Ingest before any full Paperless document import.
>
> **Scope:** finish software, fix bugs/blockers, harden release/GUI/watcher/review flows.  
> **Explicitly out of scope for now:** importing the full Paperless archive.

## Operating Mode

Work phase-by-phase. After each phase:

1. run the relevant verification commands,
2. summarize changed files and remaining blockers,
3. wait for Daniel's Telegram `weiter` before entering the next phase if the next phase has risky side effects.

Do not tag releases, push branches, install persistent LaunchAgents, overwrite productive config, or run the full Paperless import without explicit approval.

## Current Baseline

- Local branch normalized to `main` tracking `origin/main`.
- Installed Homebrew CLI: `symingest 0.6.0`.
- Open GitHub issues/PRs at baseline: none.
- Known good pilot: 7-document Paperless mixed-MIME pilot with deep verify and cutover-check.
- Known blocker: v0.6.0 release workflow failed in the GUI/DMG packaging job; CLI/Homebrew release is usable.
- Productive config is missing on this Mac unless supplied via env vars.

## Phase 0 — Local Work Basis

**Goal:** remove stale local project facts and create a durable roadmap.

- [x] Switch local work back to `main` because the feature branch tree matched `origin/main` and the remote branch was deleted.
- [x] Confirm repository has no tracked working-tree changes before Phase 0 edits.
- [x] Update stale `AGENTS.md` facts:
  - project is no longer planning-only,
  - test count is stale,
  - CI Go version note is stale,
  - appkit/daemonkit client notes are stale.
- [x] Make `docs/plans/` trackable despite the broad `docs/*` ignore rule.
- [x] Add this roadmap.
- [x] Run Go tests/vet/build after docs/config edits.

**Exit gate:** clean branch with only intentional Phase 0 docs/gitignore changes; `go test ./...`, `go vet ./...`, and build pass.

## Phase 1 — Release, CI, Homebrew, DMG

**Goal:** make distribution boring and reproducible.

- [x] Split release workflow into clearly named jobs:
  - CLI/GoReleaser/Homebrew formula,
  - macOS GUI build,
  - signing/notarization,
  - DMG upload.
- [x] Reproduce and fix the failed v0.6.0 GUI/DMG release job locally:
  - release log showed Swift 6 `Sendable` errors for `IngestJob`, `SwiftRule`, and `DependencyReport`,
  - local Xcode 27 build now succeeds.
- [x] Ensure XcodeGen build is deterministic enough for GitHub macOS runner:
  - CI now generates the project and runs full `xcodebuild`, not just Go tests.
- [x] Fix Swift 6 concurrency/build errors visible in the failed workflow:
  - `Sendable` types fixed,
  - current `EngineManager` has no `nonisolated deinit`/`nonisolated(unsafe)` issue.
- [x] Add CI check for full Swift client build.
- [x] Keep CLI release independent enough that GUI failure does not hide/corrupt CLI/Homebrew release state.
- [x] Synchronize app visible version with CLI version via `MARKETING_VERSION`/Info.plist expansion.
- [ ] Add Homebrew install smoke test on a real release artifact:
  - `brew install danieljustus/tap/symingest`,
  - `symingest version --json`,
  - `symingest doctor` with temp config.
- [ ] Decide whether to add a Homebrew Cask for `Symingest.app`.
- [ ] Verify Apple Developer ID signing/notarization/stapling for GUI DMG in GitHub release environment.

**Exit gate:** local dry-run equivalent passes: Go tests/vet/build, YAML parse, XcodeGen, full Xcode build, codesign verification, embedded CLI smoke, and basic DMG creation. Real tag/release remains gated behind explicit approval.

## Phase 2 — Core Ingest Feature Completeness

**Goal:** every advertised accepted format really works, or the UI/docs stop claiming it.

- [ ] Fix feature truthfulness: Dashboard currently mentions DOCX; core currently marks DOCX/XLSX/HTML/RTF/ODT/EML as unsupported optional formats.
- [ ] Implement or explicitly hide/defer DOCX extraction.
- [ ] Implement or explicitly hide/defer RTF extraction.
- [ ] Implement or explicitly hide/defer HTML extraction.
- [ ] Implement or explicitly hide/defer ODT extraction.
- [ ] Implement or explicitly hide/defer XLSX extraction.
- [ ] Implement or explicitly hide/defer EML extraction.
- [ ] Add optional converter discovery to doctor:
  - `textutil`,
  - `pandoc`,
  - `soffice`/LibreOffice.
- [ ] Add fixtures and golden tests for every supported format.
- [ ] Add OCR-quality metadata or report fields:
  - pages processed,
  - extracted character count,
  - low-body warning.
- [ ] Add text-first PDF extraction before OCR fallback if not already supported.

**Exit gate:** advertised formats match implementation; unsupported formats fail with useful diagnostics; tests cover all supported formats.

## Phase 3 — Paperless Migration Safety Without Full Import

**Goal:** harden the migration machinery without running the full archive.

- [ ] Finalize Paperless duplicate semantics:
  - one vault note per Paperless document ID,
  - archive dedupe by SHA is allowed,
  - metadata must never collapse across Paperless IDs.
- [ ] Add duplicate-content tests with different Paperless metadata.
- [ ] Re-check `paperless_import_state` conflict keys for multi-vault/multi-archive targets.
- [ ] Make `--json` behavior consistent:
  - either JSON everywhere it is accepted,
  - or reject/clarify where JSON is not supported.
- [ ] Put human progress on stderr for machine-readable modes.
- [ ] Version all reports with `schema_version` and `tool_version`.
- [ ] Add `symingest report validate` for migration/verify/cutover reports.
- [ ] Extend `cutover-check`:
  - require minimum tool version,
  - optionally require `deep_verify: true`,
  - consume future symseek validation report.

**Exit gate:** fake Paperless tests prove duplicate semantics, report schema validation, and JSON contracts.

## Phase 4 — Review and Correction Workflow

**Goal:** make migration review and metadata repair safe and inspectable.

- [ ] Improve `review-report` filters:
  - failed,
  - warnings,
  - missing metadata,
  - low body length,
  - duplicate content,
  - unsupported formats,
  - unresolved references.
- [ ] Improve HTML review output:
  - filters,
  - grouping,
  - note/archive links,
  - no document body leakage by default.
- [ ] Version `corrections.yaml` schema.
- [ ] Support correction dry-run with exact affected IDs.
- [ ] Add `--max` and `--require-count` safety flags for bulk updates.
- [ ] Add backup/undo log before bulk frontmatter edits.
- [ ] Add tests for idempotent corrections.

**Exit gate:** review report can produce actionable corrections and apply them safely with dry-run and undo evidence.

## Phase 5 — macOS App / GUI Productization

**Goal:** make the app a real operator console, not just a pretty CLI wrapper.

- [ ] Full local Xcode build.
- [ ] Full GitHub Actions Xcode build.
- [ ] App version synchronized with CLI version.
- [ ] Doctor view shows real config/tool state.
- [ ] Setup assistant writes safe config without secrets.
- [ ] Paperless token uses SecureField and Keychain/SymVault path, never CLI args/UserDefaults/logs.
- [ ] Import UI supports:
  - plan,
  - dry-run,
  - bounded pilots,
  - retry failed,
  - status,
  - verify,
  - deep verify,
  - cutover check.
- [ ] Jobs UI supports retry and error sidecar opening.
- [ ] Rules UI supports create/edit/delete/test.
- [ ] Review UI loads reports and applies corrections via dry-run/final flow.
- [ ] Preview UI shows Markdown/frontmatter and original preview where possible.
- [ ] Logs separate stdout/stderr and redact secrets.

**Exit gate:** app can configure, ingest, watch, review, and verify a small pilot without terminal usage.

## Phase 6 — Production Watcher / Consume Folder

**Goal:** replace Paperless consume behavior for new documents.

- [ ] Harden file stability detection.
- [ ] Use processing/processed/failed folders consistently.
- [ ] Write `.error.json` sidecars for failed files.
- [ ] Add retry/backoff controls.
- [ ] Recover pending/processing jobs after crash.
- [ ] Add watcher status and doctor commands.
- [ ] Add service management commands:
  - `service install`,
  - `service start`,
  - `service stop`,
  - `service status`,
  - `service logs`,
  - `service uninstall`.
- [ ] Generate a macOS LaunchAgent with no secrets embedded.
- [ ] Store logs under `~/Library/Logs/symingest/`.
- [ ] Make GUI control the service.

**Exit gate:** reboot-safe LaunchAgent consumes a temp inbox and survives failure/retry tests.

## Phase 7 — symseek Integration

**Goal:** ingested documents become searchable as part of the normal workflow.

- [ ] Add optional post-ingest indexing hook.
- [ ] Add config section for symseek integration.
- [ ] Add command to index current vault.
- [ ] Add search validation report from query fixtures.
- [ ] Allow `cutover-check` to consume search validation report.
- [ ] Tests with temporary `HOME`/symseek DB.

**Exit gate:** a pilot ingest can be indexed and validated through a machine-readable report.

## Phase 8 — Paperless Parity Features

**Goal:** cover the important daily Paperless capabilities.

- [ ] Mail consumer design and implementation:
  - IMAP account config,
  - attachment import,
  - processed/failed mailbox handling,
  - secrets via SymVault/Keychain.
- [ ] Barcode/separator page detection and PDF splitting.
- [ ] Workflow engine:
  - conditions,
  - actions,
  - dry-run,
  - conflict reporting.
- [ ] Classification improvements:
  - regex,
  - priorities,
  - stop-on-match,
  - suggestions from existing metadata.
- [ ] Decide whether a separate web UI is needed or whether Symaira Hub should own this.

**Exit gate:** new-document daily operations do not require Paperless-only features for Daniel's normal use.

## Phase 9 — Security, Privacy, Recovery

**Goal:** no token leaks, no silent data loss.

- [ ] Secret redaction in all CLI/GUI logs.
- [ ] SymVault/Keychain credential resolution.
- [ ] Path traversal tests for Paperless filenames/storage paths.
- [ ] HTML/EML sanitization.
- [ ] Temp-file cleanup and permissions checks.
- [ ] Backup before bulk corrections.
- [ ] Recovery docs and commands.
- [ ] Privacy mode for reports.

**Exit gate:** security tests and manual log review show no secrets/content leakage beyond intended metadata.

## Phase 10 — Test Matrix and QA

**Goal:** make regressions boring to catch.

- [ ] `go test ./...`.
- [ ] `go vet ./...`.
- [ ] `go test -race ./...` where feasible.
- [ ] Fake Paperless integration suite.
- [ ] MCP JSON-RPC smoke tests.
- [ ] Homebrew install smoke tests.
- [ ] Swift build/typecheck tests.
- [ ] Watcher crash/retry tests.
- [ ] Golden report/frontmatter tests.

**Exit gate:** local and CI test matrix catches the known historical bugs.

## Phase 11 — Docs and Operator UX

**Goal:** Daniel can operate the thing without remembering tribal knowledge from Telegram.

- [ ] README feature list matches reality.
- [ ] Architecture docs match current packages and appkit/daemonkit setup.
- [ ] Frontmatter docs complete.
- [ ] Production setup guide.
- [ ] Paperless replacement runbook updated for no-full-import prep.
- [ ] Troubleshooting guide.
- [ ] Example configs, corrections, workflow rules.

**Exit gate:** a clean install can be configured from docs alone.

## Phase 12 — Software-Ready Release

**Goal:** release a software-complete version before the full Paperless import.

- [ ] All P0/P1 items closed.
- [ ] CLI release green.
- [ ] GUI DMG release green.
- [ ] Homebrew formula/cask verified.
- [ ] App signed/notarized.
- [ ] Watcher service verified.
- [ ] Review/correction workflow verified.
- [ ] Optional formats either supported or honestly hidden.
- [ ] 7-document mixed-MIME Paperless pilot still green.
- [ ] No full Paperless archive import performed.

**Exit gate:** tag a release candidate only after Daniel approves. No automatic release from this roadmap.
