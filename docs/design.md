# Design notes

once stores one row per idempotency key.

The row is created before the side effect starts. If the command finishes, once
updates the row with stdout, stderr, exit code and a terminal state. If the
same key is used again, once replays the stored result instead of running the
command again.

The hard case is a process that dies after the side effect happened but before
the row is committed. once does not guess. The row remains `running`.

This makes uncertainty visible. Callers can inspect the key and decide whether
to reconcile externally, forget the key, or leave it alone.
