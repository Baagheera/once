# Repair cookbook

once keeps uncertainty visible. A `running` record means the key was reserved,
but no terminal result was stored. The command might still be running, or the
process might have died before or after the side effect.

Do not treat repair as automatic retry. Start by finding the record, checking
the system that owns the side effect, and then choosing the smallest deletion or
cleanup that matches what you know.

## Find stuck records

List records that are still in flight:

```sh
once list --state running
```

To focus on records that have been uncertain for a while, filter by update
time:

```sh
once list --state running --older-than 15m
once list --state running --older-than 1h
```

For local scripts or a controlled audit capture, export the same records as
JSONL:

```sh
once export --state running
once export --state running --older-than 1h
```

`export` includes keys, command arguments, errors, and timestamps. Treat the
JSONL as sensitive if any of those fields can contain secrets or private data.
Review or redact the default export before copying it into logs, tickets, audit
systems, or support bundles. It omits stored stdout and stderr by default; use
`--include-output` only when you have checked that those bytes are also safe to
move.

If you are not using the default `once.db`, pass the store explicitly:

```sh
once --store /var/lib/myapp/once.db list --state running --older-than 15m
```

## Inspect one key

Check the state first:

```sh
once status payment:order-123
```

Then inspect the record metadata:

```sh
once get payment:order-123
```

`get` prints JSON for the key, state, exit code, error when present, command,
and timestamps. It does not print stored stdout or stderr.

## Decide what happened outside once

Before deleting a `running` record, answer these outside once:

1. Is the original command still running?
2. Did the external side effect happen?
3. Is it safe to run the command again with the same key?

Examples:

- for an email, check the mail provider or delivery log
- for a charge, check the payment provider by idempotency key or order ID
- for a webhook, check the receiver's event log
- for a file or remote write, check the destination state

If the answer is unknown, leaving the record `running` is often the most honest
state. It blocks another `once run` attempt with the same key while you keep
investigating.

## Delete one record deliberately

Terminal records can be deleted without `--force`:

```sh
once forget payment:order-123
```

Use `forget --force` only when the record is `running` and you have decided that
deleting it is the right repair:

```sh
once forget --force payment:order-123
```

That makes a future `once run --key payment:order-123 -- ...` a fresh attempt.
If the earlier side effect actually happened, the retry can duplicate it. The
force flag is there to make that operator decision explicit.

## Clean up old terminal records

`prune` is for old `succeeded` or `failed` records. It never prunes `running`
records.

Run it without `--force` first:

```sh
once prune --state succeeded --older-than 30d
once prune --state failed --older-than 30d
```

If the dry run matches what you intended, delete the matching records:

```sh
once prune --state succeeded --older-than 30d --force
once prune --state failed --older-than 30d --force
```

Durations accept Go duration syntax such as `24h` and day syntax such as `30d`.

## What not to do

- Do not force-delete `running` records as a blind retry policy.
- Do not paste exported output into public issues or chat transcripts without
  checking it for secrets.
- Do not edit the SQLite database by hand unless you are prepared to own the
  recovery.
- Do not use `prune` when you mean to resolve an uncertain `running` record.
