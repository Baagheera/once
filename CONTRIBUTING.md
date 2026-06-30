# Contributing

once is a small SQLite-backed idempotency log for side effects. Keep changes
small, explicit, and honest about the boundary of the tool.

## Before opening an issue

Please check the README, `docs/design.md`, `docs/http-api.md`, and
`docs/failure-model.md` first. Many surprising cases are expected behavior:
once keeps uncertainty visible instead of guessing whether an external side
effect happened.

Use private vulnerability reporting for security issues. If that is not
available, open a minimal public issue that asks for a security contact without
including exploit details.

## Good contributions

Good changes usually look like one of these:

- a bug fix with a focused test
- a clearer failure case or documentation correction
- a small security hardening improvement
- a narrow CLI or HTTP improvement that preserves the idempotency-log model
- a CI, packaging, or release hygiene fix

Please avoid broad rewrites, formatting-only churn, unsolicited dependency
adds, or features that turn once into a workflow engine, queue, scheduler,
broker, or distributed transaction system.

## Local development

once uses Go 1.25 or newer.

```sh
go test ./...
go vet ./...
go build ./cmd/once
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.5.0 ./...
```

For CLI behavior, prefer tests around the public command surface. For store
behavior, prefer direct tests against the SQLite store so edge cases remain
small and deterministic.

## Pull requests

Keep pull requests focused enough that a reviewer can explain every changed
line. Update docs when behavior or operator expectations change.

For behavior changes, include tests for the failure path as well as the happy
path. For changes touching storage, auth, filesystem paths, tokens, parsing, or
network behavior, describe the security impact in the PR body.

Do not claim exactly-once execution. once records and replays results; it cannot
prove what happened in an external system after a crash.
