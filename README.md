# once

once is a small SQLite-backed idempotency log for side effects.

Use it when a script may be retried, but duplicating the side effect would be a
bug.

```sh
once run --key email:user42:welcome -- ./send-welcome-email user42
once run --key email:user42:welcome -- ./send-welcome-email user42
```

The first command reserves the key, runs the program, and stores the terminal
result. The second command does not run the program again. It replays the saved
stdout, stderr, and exit code.

once does not make the outside world exactly-once. If a process crashes after
the side effect but before the result is stored, once leaves a `running` record
so the uncertainty is visible.

## Table of Contents

- [Quick start](#quick-start)
- [The model](#the-model)
- [Good fits](#good-fits)
- [When not to use once](#when-not-to-use-once)
- [Keys](#keys)
- [Commands](#commands)
- [Exit codes](#exit-codes)
- [Inspect and repair](#inspect-and-repair)
- [Durability, retention, and backups](#durability-retention-and-backups)
- [HTTP mode](#http-mode)
- [Go HTTP client](#go-http-client)
- [Security notes](#security-notes)
- [Current limitations](#current-limitations)
- [Why not](#why-not)
- [Stability](#stability)
- [Development](#development)
- [Release verification](#release-verification)

## Quick start

Install:

```sh
go install github.com/Baagheera/once/cmd/once@latest
```

Try it:

```sh
tmp="$(mktemp -d)"
once --store "$tmp/once.db" run --key demo -- sh -c 'echo hello'
once --store "$tmp/once.db" run --key demo -- sh -c 'echo hello'
once --store "$tmp/once.db" list
```

Both runs print `hello`, but the command only runs once.

Small side-effect demo:

```sh
tmp="$(mktemp -d)"
once --store "$tmp/once.db" run --key webhook:event-123 -- \
  sh -c 'echo POST >> "$1"; echo ok' sh "$tmp/side-effects.log"
once --store "$tmp/once.db" run --key webhook:event-123 -- \
  sh -c 'echo POST >> "$1"; echo ok' sh "$tmp/side-effects.log"
wc -l < "$tmp/side-effects.log"
```

The command prints `ok` both times. The side-effect log has one line.

On Windows PowerShell:

```powershell
$root = Join-Path $env:LOCALAPPDATA ("once-demo-" + [guid]::NewGuid())
$store = Join-Path $root "once.db"
once --store $store run --key demo -- powershell -NoProfile -Command "Write-Output 'hello'"
once --store $store run --key demo -- powershell -NoProfile -Command "Write-Output 'hello'"
once --store $store list
```

When the directory is missing, once creates it with a protected user-only DACL.

Use a store owned by the app or by the user running it:

```sh
once --store "$HOME/.local/state/myapp/once.db" run --key payment:order-123 -- ./charge order-123
```

Avoid shared temporary directories for real side effects. The database,
WAL/SHM sidecars, and HTTP token file are sensitive local files.

## The model

once stores one row per idempotency key:

1. reserve a stable key
2. perform the side effect outside once
3. commit the terminal result
4. replay that stored result on future attempts

The states are:

| State | Meaning |
| --- | --- |
| `running` | The key was reserved, but no terminal result has been stored. The command may still be running, or the process may have died. |
| `succeeded` | The command or HTTP client committed success. Future attempts replay the saved result. |
| `failed` | The command or HTTP client committed failure. Future attempts replay the saved failure. |

The store is a local SQLite database. By default it uses `once.db` in the
current directory.

For crash behavior, see [`docs/failure-model.md`](docs/failure-model.md). For
operator repair steps, see
[`docs/repair-cookbook.md`](docs/repair-cookbook.md).

## Good fits

- sending an email from a script
- calling a webhook from a deploy step
- recording the result of a one-off local side effect
- wrapping a command where retrying is useful but duplicating work is not
- exposing a small local or trusted-network ledger over HTTP

For recipes, see [`docs/cookbook.md`](docs/cookbook.md).

## When not to use once

Do not use once as a job queue, scheduler, broker, workflow engine, or
distributed transaction system. It stores one idempotency record; it does not
assign work, retry work in the background, or coordinate workers.

Do not use once when you need exactly-once execution against an external
system. It can replay a stored result, but it cannot prove whether a crashed
process already changed the outside world.

Avoid `once run` for long-running or interactive jobs. It is intended for
finite, non-interactive commands.

## Keys

Key by the side effect, not by the retry attempt:

```sh
once run --key email:user42:welcome -- ./send-welcome-email user42
once run --key webhook:stripe:event_123 -- ./deliver-webhook event_123
```

Good keys usually name one external operation:

- `email:user42:welcome`
- `webhook:stripe:event_123`
- `deploy:prod:2026-07-03:notify`
- `charge:order_123`

Weak keys usually name an attempt or a vague category:

- `retry-1`
- `send-email`
- `today`

If environment variables, input files, tenant IDs, template versions, or remote
event IDs change the side effect, put that identity in the key.

Keys are exact identifiers. once does not trim or normalize them. Keys may use
ASCII letters, digits, `.`, `_`, `:`, `@`, `=`, and `-`, up to 256 bytes.

## Commands

| Command | Purpose |
| --- | --- |
| `once run --key KEY -- COMMAND [ARG...]` | reserve a key, run a command, store and replay its result |
| `once status KEY` | print the state for one key |
| `once get [--include-output] KEY` | print one record as JSON |
| `once list [--state STATE] [--limit N] [--older-than DURATION]` | list local records for operators |
| `once export [--state STATE] [--limit N] [--older-than DURATION] [--include-output]` | write JSONL for scripts or audits |
| `once prune --state STATE --older-than DURATION [--force]` | dry-run or delete old terminal records |
| `once forget [--force] KEY` | delete one record deliberately |
| `once doctor [--json]` | inspect local store, permissions, schema, and token-file state |
| `once serve [--listen ADDR] [--token-file PATH]` | expose reserve, commit, get, and delete over HTTP |
| `once version` | print the CLI version |

Global flag:

```sh
--store PATH    SQLite database path. Default: once.db
```

`list --older-than` and `export --older-than` filter by `updated_at`. They are
useful for finding stale `running` records without deleting anything:

```sh
once list --state running --older-than 15m
once export --state running --older-than 1h
```

`--older-than` durations use Go syntax such as `30s`, `5m`, and `24h`. They
also accept day syntax such as `30d`, where a day is 24 hours.

`get` and `export` omit stored stdout and stderr by default. Use
`--include-output` only when those bytes are safe to handle.

## Exit codes

`once run` replays the stored exit code for terminal records. That means a
stored failure remains a failure on later attempts.

| Code | Meaning |
| --- | --- |
| `0` | command succeeded, or a stored success was replayed |
| child exit code | the command failed, or a stored command failure was replayed |
| `1` | once hit an operational error, such as opening the store or committing a result |
| `2` | invalid command-line usage |
| `75` | the key is still `running` and was not rerun |
| `124` | `once run --timeout` stopped the command |
| `125` | `once run --max-output-bytes` was exceeded |
| `127` | the command could not be started |

`list`, `get`, `export`, `doctor`, `serve`, `prune`, `forget`, and `version`
return `0` for success and non-zero for errors.

## Inspect and repair

Start with the record. Do not treat repair as automatic retry:

```sh
once list --state running
once list --state running --older-than 15m
once get payment:order-123
```

A `running` record means once does not know whether the outside world changed.
Check the system that owns the side effect, then choose the smallest repair that
matches what you know.

Terminal records can be deleted without `--force`:

```sh
once forget payment:order-123
```

Use `forget --force` only when the record is `running` and you have decided a
retry is safe:

```sh
once forget --force payment:order-123
```

Clean up old terminal records with a dry run first:

```sh
once prune --state succeeded --older-than 30d
once prune --state succeeded --older-than 30d --force
```

`prune` never deletes `running` records.

## Durability, retention, and backups

Keep the store on a local filesystem. once opens SQLite in WAL mode and
requires `synchronous=FULL`; it verifies both settings before using the store.
These settings protect the log within SQLite and filesystem guarantees. They
do not make the external side effect atomic with the commit.

An idempotency key protects a side effect only while its record remains in the
active store. `forget`, forced `prune`, or restoring an older snapshot can make
the same key fresh again. Keep records longer than the longest period in which
a request can be retried or a key can be reused, and inspect a prune dry run
before deleting anything.

A live WAL database is not just its `.db` file. Copying that file by itself can
miss committed data. For a simple file backup, every SQLite user must close the
store cleanly and remain stopped while it is copied. If a `-wal` file remains,
it is part of the database state and must stay paired with that exact `.db`.
If users cannot remain stopped, or the database and WAL cannot be preserved as
a matched set, use SQLite's online backup mechanism.

Restore only while every SQLite user remains stopped. Replace or remove the old
`.db`, `-wal`, `-shm`, and `-journal` files as one controlled set so journals
from different points in time are never mixed. Use a once version that supports
the stored schema, preserve restrictive permissions, and keep the HTTP token
file when one is in use.

After restoring an older snapshot, reconcile external effects newer than that
snapshot before allowing retries. once does not automate retention, backup,
restore, leases, or takeover.

## HTTP mode

`once serve` exposes the same ledger over HTTP for programs that should reserve
and commit records but should not execute local commands:

```sh
once serve --listen 127.0.0.1:7410
```

If no token is provided, once creates a token file next to the store and prints
only the token file path. Prefer token files for normal use. Tokens passed
through flags or environment variables can be visible to other local processes
on shared machines. Explicit `--token` values must be at least 32 characters.

Every endpoint except `/healthz` requires:

```http
Authorization: Bearer <token>
```

The `curl` examples below put the bearer token in the command arguments. That
is convenient for local testing, but process arguments can be visible to other
local users on shared machines. Use a private script, config file, or your own
HTTP client code when that matters.

Reserve a key:

```sh
token="$(cat once.db.token)"
curl -s http://127.0.0.1:7410/v1/reserve \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","command":["send-webhook"]}'
```

Commit the result with the returned `attempt_token`:

```sh
curl -s http://127.0.0.1:7410/v1/commit \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","attempt_token":"...","state":"succeeded","exit_code":0,"stdout_b64":"b2sK"}'
```

Fetch the record:

```sh
curl -s http://127.0.0.1:7410/v1/records/webhook:event-123 \
  -H "authorization: Bearer $token"
```

Byte fields such as `stdout_b64` and `stderr_b64` are JSON base64 strings. JSON
request bodies are capped at 1 MiB. Base64 output in commit requests counts
toward that limit.

Deleting over HTTP requires the reservation token in `X-Once-Attempt-Token`.
Deleting a `running` record also requires `?force=1`.

For the full API, see [`docs/http-api.md`](docs/http-api.md).

## Go HTTP client

Go programs can use the small HTTP client package instead of hand-writing the
JSON calls:

```go
client, err := oncehttp.New("http://127.0.0.1:7410", oncehttp.WithBearerToken(token))
if err != nil {
	return err
}

reserved, err := client.Reserve(ctx, oncehttp.ReserveRequest{
	Key:     "webhook:event-123",
	Command: []string{"deliver-webhook", "event-123"},
})
if err != nil {
	return err
}
if !reserved.Fresh {
	return replay(reserved.Record)
}

// Perform the side effect here, then commit the terminal result.
_, err = client.Commit(ctx, oncehttp.CommitRequest{
	Key:          "webhook:event-123",
	AttemptToken: reserved.AttemptToken,
	State:        oncehttp.Succeeded,
	ExitCode:     0,
	Stdout:       []byte("ok\n"),
})
return err
```

Import path:

```go
import "github.com/Baagheera/once/oncehttp"
```

The client caps successful JSON responses at 16 MiB by default. Use
`oncehttp.WithMaxResponseBytes` if stored output can be larger. It does not
follow redirects, so bearer tokens, attempt tokens, and commit bodies stay
bound to the configured server.

## Security notes

Treat the SQLite store as sensitive if command arguments, stdout, stderr, error
strings, or keys can contain secrets. once persists those bytes so it can
replay the result later. Treat SQLite sidecar files such as `once.db-wal` and
`once.db-shm` the same way.

Do not expose `once serve` to untrusted networks. Non-loopback listeners require
`--allow-remote`, and bearer authentication is still required unless auth is
explicitly disabled on a loopback address with `--unsafe-no-auth`. If you bind
it beyond localhost, keep it on a trusted network or put it behind your own TLS
and access controls.

For vulnerability reporting and supported versions, see
[`SECURITY.md`](SECURITY.md).

## Current limitations

`once run` buffers stdout and stderr in memory before storing them, so output is
not streamed live. Use `once run --max-output-bytes N` when a command might
write too much output to keep.

`once run` does not pass stdin to the child process.

`once run --timeout DURATION` uses Go duration syntax such as `30s`, `5m`, or
`1h`. It stores a timed-out command as `failed` with exit code `124`, then
replays that result like any other stored result. Timeout is not a sandbox or
transaction boundary. Commands that spawn or detach child processes can still
leave work behind after the parent command is stopped.

The command line is stored to catch accidental key reuse, but once cannot infer
changes in environment variables, input files, remote state, feature flags, or
other context outside the command arguments. Put that identity in the key.

## Why not

`flock` or a lock file?

A lock can stop two processes from entering the same critical section at the
same time. It does not store the terminal result, replay stdout/stderr, or keep
a durable record that a previous attempt is uncertain.

A job queue?

Queues assign and deliver work. once does not. It records one operation by key
and lets your program decide when to run, retry, inspect, or repair it.

An outbox table?

Use an outbox when the side effect belongs inside your application database
transaction and a worker can deliver it later. Use once when you need a small
local ledger around an existing command or HTTP client flow.

Provider idempotency keys?

Use them when the provider supports them. once is useful around providers or
commands that do not, and around local side effects where you still want a
record to inspect.

A workflow engine?

If you need scheduling, background retries, timers, fan-out, compensation, or a
long-running state machine, use a workflow engine. once is deliberately smaller.

## Stability

once is experimental before `v1.0.0`.

The CLI and HTTP API are the main public surfaces. `oncehttp` is a small
experimental wrapper around the HTTP API. Other Go packages are internal today.
The SQLite schema may still change before `v1.0.0`; once records a schema
version and rejects stores that declare an unsupported version, but the storage
file is not yet a separately stable integration contract.

## Development

For local development:

```sh
go run ./cmd/once run --key demo -- echo hello
go run ./cmd/once run --key demo -- echo hello
go run ./cmd/once list
go run ./cmd/once status demo
go run ./cmd/once get demo
```

Run the demo:

```sh
sh examples/demo.sh
```

Run the standard checks:

```sh
go test ./...
go vet ./...
go build ./cmd/once
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...
```

## Release verification

Release archives are attached to GitHub releases. After downloading one archive
and `SHA256SUMS`, verify that archive's checksum:

```sh
archive=once_vX.Y.Z_linux_amd64.tar.gz
grep "  $archive$" SHA256SUMS | sha256sum -c -
```

If you download every archive listed in `SHA256SUMS`, this verifies the full
set:

```sh
sha256sum -c SHA256SUMS
```

Release artifacts also include GitHub provenance attestations:

```sh
gh attestation verify once_vX.Y.Z_linux_amd64.tar.gz \
  --repo Baagheera/once \
  --signer-workflow Baagheera/once/.github/workflows/release.yml
```
