# Changelog

User-facing changes are recorded here as releases are prepared.

## Unreleased

No unreleased changes.

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
