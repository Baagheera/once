# Security

## Supported versions

once is experimental.

Security fixes are applied to `main` and to the latest released minor line when
practical.

Currently supported:

- `main`
- latest `v0.3.x`

Older experimental releases may be superseded instead of patched. The public API
and storage format may still change before `v1.0.0`.

## Reporting a vulnerability

Please use GitHub private vulnerability reporting if available on the
repository. If it is not available, open a minimal public issue that says a
security report is needed without including exploit details.

## Security model

The CLI executes commands only on the local machine.

The HTTP server does not execute commands, but it is still a control plane for
the idempotency ledger. It can reserve keys, commit results, read command
metadata and output, and delete records.

Do not expose `once serve` to an untrusted network. The server requires a bearer
token by default; keep it secret and put the server behind your own transport
security and access controls if it leaves localhost. The HTTP API is meant to
be a small local or trusted-network control plane, not a public edge service.

Keys, command arguments, stdout, stderr and error strings may contain sensitive
data. Treat the SQLite store as sensitive.
