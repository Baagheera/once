# Changelog

User-facing changes are recorded here as releases are prepared.

## Unreleased

## v0.5.0 - 2026-07-06

### Added

- Cookbook examples for webhook delivery, notification commands, HTTP reserve
  and commit, and stuck `running` record repair.
- Checksummed Linux, macOS, and Windows release archives.
- GitHub provenance attestations for release artifacts.
- `list --older-than DURATION` and `export --older-than DURATION` for finding
  stale records, especially old `running` records.
- README exit-code reference and stability notes for the CLI, HTTP API, Go
  packages, and SQLite schema.

### Changed

- README now shows the retry-safe side-effect model earlier, including the
  exactly-once limitation, key guidance, and comparisons with locks, queues,
  outbox tables, provider idempotency keys, and workflow engines.
- README now includes a table of contents and a more compact command-focused
  structure.
- The demo script now invokes its helper scripts through `sh` for better
  portability.
- Quick start now includes installed CLI examples and PowerShell examples.
- Release documentation now shows checksum and provenance verification.

### Fixed

- Store records no longer expose stored attempt hashes through returned record
  values. Fresh reservations still return the raw attempt token that callers
  need to commit or delete that reservation.
- `serve --token` validation now fails before opening or creating the store.
- SQLite stores and HTTP token files now reject Unix-like parent directories
  that are writable by group or other users.

## v0.4.2 - 2026-07-03

### Added

- `once version` command for printing the CLI version.

### Changed

- Documentation now includes common usage patterns and a clearer HTTP API
  reference.

## v0.4.1 - 2026-07-03

### Added

- `doctor --json` for machine-readable local diagnostics.

## v0.4.0 - 2026-07-03

### Added

- `doctor` now warns about Unix-like store parent directories with broad
  permissions.

### Changed

- Idempotency keys are now limited to ASCII letters, digits, `.`, `_`, `:`,
  `@`, `=`, and `-`.
- HTTP key handling now rejects surrounding whitespace instead of trimming it.
- Documentation now recommends private store directories and clearer handling
  for non-local HTTP listeners.

## v0.3.1 - 2026-07-02

### Added

- CLI `get --include-output` option for explicit single-record stdout/stderr
  inspection.

### Changed

- Security policy now names the latest `v0.3.x` line as supported.
- Documentation now calls out the 1 MiB HTTP JSON request body limit.

## v0.3.0 - 2026-07-01

### Added

- CLI `run --timeout DURATION` option for timing out a command after a local
  runtime limit and replaying the stored timeout result.
- CLI `run --max-output-bytes N` option for bounding stored command output and
  replaying a stored output-limit failure.

### Changed

- CLI `list` and `prune` now do more filtering, ordering, and limit work in
  SQLite while preserving the existing record ordering.

## v0.2.2 - 2026-07-01

### Added

- CLI `doctor` command for local store, permission, sidecar, schema, and
  token-file diagnostics without repairing the store or printing sensitive
  contents.

## v0.2.1 - 2026-07-01

### Added

- Repair cookbook for finding stuck records, inspecting keys, deleting
  uncertain `running` records deliberately, and pruning old terminal records.
- CLI `prune` command for dry-run and forced cleanup of old terminal records.

### Changed

- CI now verifies the declared Go 1.25 minimum on Ubuntu.
- Security policy now names the latest `v0.2.x` line as supported.

## v0.2.0 - 2026-06-30

### Added

- Contribution guidelines and GitHub issue templates.
- A code of conduct for public project participation.
- A standalone failure model for crash, duplicate-key, token, lock, output, and
  manual-repair cases.
- CLI `list` and `export` commands for local store inspection.

### Changed

- Tightened public documentation around security and operational limits.
- README guidance now calls out concrete cases where once is the wrong tool.
