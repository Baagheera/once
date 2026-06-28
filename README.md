# once

once is a small persistent log for side effects.

It solves a boring problem: retries are useful, but duplicated side effects
are not.

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

## Quick start

```sh
go install github.com/Baagheera/once/cmd/once@latest
```

For local development:

```sh
go run ./cmd/once run --key demo -- echo hello
go run ./cmd/once run --key demo -- echo hello
go run ./cmd/once status demo
go run ./cmd/once get demo
```

Use a different database:

```sh
once --store /tmp/once.db run --key payment:order-123 -- ./charge order-123
```

## States

`running`

The key was reserved, but no result has been stored yet. This can mean that
the command is still running, or that the process died before committing the
result.

`succeeded`

The command exited with code `0`. Future runs with the same key replay the
saved result.

`failed`

The command exited with a non-zero code, or could not be started. Future runs
with the same key replay the saved failure.

## Important limitation

once does not provide exactly-once execution against the outside world.

If a process sends an email, charges a card, or calls a webhook, and then dies
before storing the result, once cannot know what happened outside the process.
It keeps the key in `running` so the uncertainty is visible instead of hidden.

This is still useful. The common case becomes safe to retry, and the bad case
becomes a record you can inspect.

## Commands

```sh
once run --key KEY -- COMMAND [ARG...]
once serve [--listen ADDR]
once status KEY
once get KEY
once forget [--force] KEY
```

Global flags:

```sh
--store PATH    SQLite database path. Default: once.db
```

## HTTP mode

`once serve` exposes the same ledger over HTTP:

```sh
once serve --listen 127.0.0.1:7410
```

Reserve a key:

```sh
curl -s http://127.0.0.1:7410/v1/reserve \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","command":["send-webhook"]}'
```

Commit the result:

```sh
curl -s http://127.0.0.1:7410/v1/commit \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","state":"succeeded","exit_code":0,"stdout_b64":"b2sK"}'
```

Fetch the record:

```sh
curl -s http://127.0.0.1:7410/v1/records/webhook:event-123
```

Byte fields such as `stdout_b64` and `stderr_b64` are JSON base64 strings. The HTTP
server does not run commands; it only stores and returns idempotency records.

Deleting a `running` record requires `--force` in the CLI or `?force=1` in the
HTTP API.

## Non-goals

once is not a workflow engine, job queue, scheduler, broker, or distributed
transaction system.

It is one small primitive: keep a durable idempotency record for a side effect.
