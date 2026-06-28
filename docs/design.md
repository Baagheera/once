# Design notes

once stores one row per idempotency key.

The row is created before the side effect starts. If the command finishes, once
updates the row with stdout, stderr, exit code and a terminal state. If the
same key is used again, once replays the stored result instead of running the
command again.

The same key with a different command is rejected. This catches a common class
of accidental key reuse bugs early.

The hard case is a process that dies after the side effect happened but before
the row is committed. once does not guess. The row remains `running`.

This makes uncertainty visible. Callers can inspect the key and decide whether
to reconcile externally, forget the key, or leave it alone.

## HTTP server

`once serve` exposes the ledger over HTTP for programs that cannot or should not
shell out to the CLI.

The server deliberately does not execute commands. Remote command execution
would turn once into a job runner and create a much larger security model. The
HTTP API only supports the primitive operations:

- reserve a key
- commit a result
- read a record
- delete a record

This keeps the boundary narrow. Applications still own the side effect. once
owns the idempotency record.

HTTP commit is idempotent when the repeated commit contains the same terminal
result. A repeated commit with different output, state or exit code is a
conflict.
