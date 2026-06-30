# Failure model

once is not an exactly-once system. It is a durable idempotency log around a
side effect owned by your program.

The normal flow is:

1. reserve a stable key
2. perform the side effect outside once
3. commit the terminal result
4. replay the stored result on future attempts

The hard cases are the gaps between those steps. once does not guess across
those gaps. It keeps the record inspectable so a caller or operator can repair
the situation with external context.

## States

`running`

The key was reserved, but no terminal result has been stored. The command might
still be running, or the process might have died before committing.

`succeeded`

For `once run`, the command exited with code `0`, and the result was committed.
For HTTP clients, the caller committed a successful terminal result. Future
attempts with the same key and command replay the saved stdout, stderr, and
exit code.

`failed`

For `once run`, the command exited with a non-zero code or could not be
started, and that failure was committed. For HTTP clients, the caller committed
a failed terminal result. Future attempts with the same key and command replay
the same failure.

## Crash before the side effect

If once reserves the key and the process dies before the child command performs
the side effect, the record remains `running`.

From once's point of view, this is indistinguishable from a crash after the side
effect. The next `once run` with the same key does not run the command. It
reports that the key is already running.

Repair requires external knowledge. If you can prove the side effect did not
happen, you can delete the running record with `once forget --force KEY` and
retry. Do not automate that as a blind retry policy.

## Crash after the side effect

If the child command performs the side effect and once dies before committing
the result, the record remains `running`.

This is the main uncertainty once is designed to expose. The external system
may already have sent the email, charged the card, called the webhook, or
written the remote state. once cannot infer that from SQLite.

Repair should start in the external system, not in once. Confirm whether the
side effect happened, then decide whether to leave the record for audit, force
delete it for a deliberate retry, or reconcile the external system by hand.

## Crash during commit

The commit is a single SQLite update. After a crash or lock failure, inspect the
record:

- if it is terminal, future attempts replay the committed result
- if it is still `running`, once did not record a terminal result

Treat a remaining `running` record as uncertain. The child process may already
have finished and produced output that was never committed.

## Child command failure

If the child command exits non-zero while once stays alive, once commits a
`failed` record with the exit code, stdout, and stderr. Later attempts replay
that failure instead of running the command again.

If the command cannot be started, once records a `failed` result with exit code
`127` and the start error. That result is also replayed.

## Duplicate key

The first successful reservation owns the key. A later attempt with the same
key and the same command gets the existing record:

- `running` records are not rerun
- `succeeded` records replay success
- `failed` records replay failure

This is true even when the caller believes the previous attempt is dead. once
uses the record, not process liveness, as the source of truth.

## Different command with the same key

If the same key is reused with different command arguments, once rejects the
attempt. The command is not run.

The command comparison covers the argument vector stored by once. It does not
cover environment variables, stdin, input files, remote state, feature flags, or
other context. Put identity that matters in the key.

## Lost attempt token

Fresh reservations get an opaque attempt token. once stores only a hash of that
token, and HTTP commit/delete operations require the original token.

Treat the attempt token as a secret capability for that record. Anyone with HTTP
auth and the token can commit or delete the record. Do not post it in public
issues, logs, support bundles, or chat transcripts.

If the token is lost while the record is `running`, another client that only
knows the key cannot commit the result or delete it over HTTP. A local operator
with direct access to the SQLite store can still use the CLI administrative
repair path:

```sh
once forget --force KEY
```

Only do this after checking the external side effect.

## Wrong token

A commit or HTTP delete with the wrong attempt token is rejected, and the record
is left unchanged. This prevents a client that merely knows the key from
finalizing or deleting someone else's in-flight operation.

## Locked database

once uses SQLite with WAL mode and a busy timeout. If another process holds the
database lock too long, an operation can fail.

Treat the main database and SQLite sidecar files as sensitive: for example
`once.db`, `once.db-wal`, and `once.db-shm`.

A lock error is not proof that the side effect did or did not happen. Check the
record before retrying. If the key is `running`, handle it as an uncertain
attempt.

## Large stdout or stderr

`once run` currently buffers stdout and stderr in memory, then stores them in
SQLite after the command exits. Very large output can put pressure on memory and
make the database large.

If once dies after the child produced output but before commit, that output is
not available from once. The record remains `running`.

## Manual repair

Use the smallest repair that matches what you know:

```sh
once status KEY
once get KEY
once forget KEY
once forget --force KEY
```

`forget` without `--force` deletes terminal records. `forget --force` can also
delete `running` records, and should be treated as an explicit operator action.

Before force-deleting a `running` record, answer two questions outside once:

1. Did the side effect happen?
2. Is it safe to retry the command with the same key?

If either answer is unknown, leaving the record `running` is often the most
honest state.
