# Changelog

User-facing changes are recorded here as releases are prepared.

## Unreleased

No unreleased changes.

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
