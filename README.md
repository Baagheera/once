# once

once is a small SQLite-backed idempotency log for side effects.

Run the side effect once. Store the terminal result. Replay that result on
later attempts with the same key.

```sh
once run --key email:user42:welcome -- ./send-welcome-email user42
once run --key email:user42:welcome -- ./send-welcome-email user42
```

The first command runs the program and stores its result. The second command
does not run it again. It prints the saved stdout and stderr, and exits with
the saved exit code.

If the same key is reused with a different command, once returns an error. A
key should name one operation, not a family of similar operations.

## What it is

once is an idempotency log for commands that perform side effects. Give an
operation a stable key, and once records whether the operation is running,
succeeded, or failed.

The model is:

1. reserve a key
2. run the command
3. store the result
4. replay the result when the same key is used again

The store is a SQLite database. By default it uses `once.db` in the current
directory.

Good fits:

- sending an email from a script
- calling a webhook from a deploy step
- recording the result of a one-off local side effect
- wrapping a command where retrying is useful but duplicating work is not

## Quick start

```sh
go install github.com/Baagheera/once/cmd/once@latest
```

For local development:

```sh
go run ./cmd/once run --key demo -- echo hello
go run ./cmd/once run --key demo -- echo hello
go run ./cmd/once list
go run ./cmd/once status demo
go run ./cmd/once get demo
```

Run the local demo:

```sh
sh examples/demo.sh
```

It shows duplicate side effects, replayed command output, replayed failure
results, and the HTTP reserve/commit/replay flow.

Use a different database:

```sh
once --store "$HOME/.local/state/myapp/once.db" run --key payment:order-123 -- ./charge order-123
```

Put the store in a directory owned by the app or by the user running it. Avoid
shared temporary directories for real side effects; the database, WAL/SHM
sidecars, and token file are all sensitive local files.

## States

`running`

The key was reserved, but no result has been stored yet. This can mean that
the command is still running, or that the process died before committing the
result.

`succeeded`

The command exited with code `0`. Future runs with the same key replay the
saved result.

`failed`

The command exited with a non-zero code, could not be started, or hit a local
execution limit such as timeout or maximum stored output. Future runs with the
same key replay the saved failure.

## Important limitation

once does not provide exactly-once execution against the outside world.

If a process sends an email, charges a card, or calls a webhook, and then dies
before storing the result, once cannot know what happened outside the process.
It keeps the key in `running` so the uncertainty is visible instead of hidden.

This is still useful. The common case becomes safe to retry, and the bad case
becomes a record you can inspect.

For concrete crash cases, see
[`docs/failure-model.md`](docs/failure-model.md). For operational repair steps,
see [`docs/repair-cookbook.md`](docs/repair-cookbook.md).

## When not to use once

Do not use once as a job queue, scheduler, broker, workflow engine, or
distributed transaction system. It stores one idempotency record; it does not
assign work, retry work in the background, or coordinate workers.

Do not use once when you need exactly-once execution against an external
system. It can replay a stored result, but it cannot prove whether a crashed
process already changed the outside world.

Avoid `once run` for long-running or interactive jobs. It is intended for
finite commands; current command-execution limits are listed below.

Treat the SQLite store as sensitive if command arguments, stdout, stderr, error
strings, or keys can contain secrets. once persists those bytes so it can replay
the result later. Treat SQLite sidecar files such as `once.db-wal` and
`once.db-shm` the same way.

## Current limitations

`once run` is intended for finite, non-interactive commands.

Today it buffers stdout and stderr in memory before storing them, so output is
not streamed live. Use `once run --max-output-bytes N` when a command might
write too much output to keep. It also does not pass stdin to the child process.
Use `once run --timeout DURATION` when a command should be stopped after a
local runtime limit.

The command line is stored to catch accidental key reuse, but once cannot infer
changes in environment variables, input files, remote state, or other context
outside the command arguments. Put that identity in the key today.

Keys are exact identifiers. once does not trim or normalize them. Keys may use
ASCII letters, digits, `.`, `_`, `:`, `@`, `=`, and `-`, up to 256 bytes.

## Commands

| Command | Purpose |
| --- | --- |
| `once run --key KEY -- COMMAND [ARG...]` | reserve a key, run a command, store and replay its result |
| `once status KEY` | print the state for one key |
| `once get [--include-output] KEY` | print one record as JSON |
| `once list [--state STATE] [--limit N]` | list local records for operators |
| `once export [--state STATE] [--limit N] [--include-output]` | write JSONL for scripts or audits |
| `once prune --state STATE --older-than DURATION [--force]` | dry-run or delete old terminal records |
| `once forget [--force] KEY` | delete one record deliberately |
| `once doctor [--json]` | inspect local store, permissions, schema, and token-file state |
| `once serve [--listen ADDR] [--token-file PATH]` | expose reserve/commit/get/delete over HTTP |
| `once version` | print the CLI version |

Global flags:

```sh
--store PATH    SQLite database path. Default: once.db
```

`doctor` checks the local store path, parent directory, file permissions,
SQLite sidecar files, schema, and default token-file state. It reports problems
and skipped checks; it does not repair the store, print token contents, or
print stored command output. Use `--json` when another program needs the same
checks.

`list` prints a local summary of records for operators. `get` prints one record
as JSON. `export` writes JSONL, one record per line, for scripts and audit
trails. Both `get` and `export` omit stored stdout and stderr by default; use
`--include-output` only when those bytes are safe to handle.

`prune` finds terminal records updated more than `DURATION` ago in the local
store. It requires `--state succeeded` or `--state failed`; durations can use
Go syntax such as `24h` or day syntax such as `30d`. Without `--force` it only
prints the records it would delete.

`run --timeout` times out the started command after the given duration. A
timed-out command is stored as `failed` with exit code `124`, then replayed like
any other stored result. Durations use Go syntax such as `30s`, `5m`, or `1h`.

The timeout applies when the command is first executed. If a result already
exists for the key and command, once replays that stored result; changing
`--timeout` later does not rerun it. Timeout is not a sandbox or transaction
boundary. Commands that spawn or detach child processes can still leave work
behind after the parent command is stopped.

`run --max-output-bytes` limits the total stored stdout and stderr bytes. Output
past the limit is discarded while the command continues. If the limit is
exceeded, once stores a `failed` result with exit code `125` and replays the
truncated output and stored error on later runs. Changing `--max-output-bytes`
later does not rerun an existing record. Use `--max-output-bytes 0` to store no
output; omit the flag for no output cap.

For stuck `running` records and terminal cleanup, see the
[`repair cookbook`](docs/repair-cookbook.md).

## HTTP mode

`once serve` exposes the same ledger over HTTP:

```sh
once serve --listen 127.0.0.1:7410
```

If no token is provided, once creates `once.db.token` next to the default
database and prints only the token file path. Prefer token files for normal
use. Tokens passed through flags or environment variables can be visible to
other local processes on shared machines. Explicit `--token` values must be at
least 32 characters.

Every endpoint except `/healthz` requires:

```http
Authorization: Bearer <token>
```

JSON request bodies are capped at 1 MiB. Base64 output in commit requests
counts toward that limit.

For local testing:

```sh
token="$(cat once.db.token)"
```

Reserve a key:

```sh
curl -s http://127.0.0.1:7410/v1/reserve \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","command":["send-webhook"]}'
```

The response includes an `attempt_token` for the fresh reservation. Commit the
result with that token:

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

Byte fields such as `stdout_b64` and `stderr_b64` are JSON base64 strings. The
HTTP server does not run commands; it only stores and returns idempotency
records.

Deleting over HTTP requires the reservation token in `X-Once-Attempt-Token`.
Deleting a `running` record also requires `?force=1`. The local CLI keeps
`forget --force` as an explicit administrative repair path for people with
direct access to the SQLite database.

Do not expose `once serve` to untrusted networks. Non-loopback listeners require
`--allow-remote`, and bearer authentication is still required unless auth is
explicitly disabled on a loopback address with `--unsafe-no-auth`. If you bind
it beyond localhost, keep it on a trusted network or put it behind your own TLS
and access controls.

## Non-goals

once is not a workflow engine, job queue, scheduler, broker, or distributed
transaction system.

It is one small primitive: keep a durable idempotency record for a side effect.
