# Cookbook

These examples show where once is useful: a command may be retried, but the
side effect should not be duplicated blindly.

Use stable keys that name the external operation. Keep the store with the app or
script that owns the side effect, and treat the SQLite files as sensitive.

## Webhook delivery

Use the upstream event ID as part of the key:

```sh
once --store "$HOME/.local/state/myapp/once.db" \
  run --key webhook:stripe:event_123 -- \
  ./deliver-webhook event_123
```

If the script is retried with the same key and command, once replays the stored
result instead of calling the receiver again.

Good keys:

```text
webhook:stripe:event_123
webhook:github:delivery_8f1a
webhook:tenant_42:event_123
```

Avoid keys such as `webhook-retry` or `latest`. They do not identify one
external operation.

## Email or deploy notification

Use a key that names the notification, not the process attempt:

```sh
once --store "$HOME/.local/state/deploy/once.db" \
  run --key deploy:prod:2026-07-05:notify -- \
  ./notify-deploy prod
```

If the deploy script is restarted after the notification succeeds, the second
attempt gets the saved output and exit code. The notification command is not run
again.

For user email, put the user and email purpose in the key:

```sh
once --store "$HOME/.local/state/myapp/once.db" \
  run --key email:user42:welcome -- \
  ./send-welcome-email user42
```

If the template, tenant, or remote event changes the side effect, include that
identity in the key.

## HTTP reserve and commit

Use HTTP mode when another process should reserve and commit records, but once
should not execute local commands.

Start on loopback:

```sh
once serve --listen 127.0.0.1:7410 --token-file once.token
token="$(cat once.token)"
```

The `curl` examples below put the bearer token in the command arguments. That is
acceptable for local testing, but process arguments can be visible to other
local users on shared machines. Use a private script, config file, or your own
HTTP client code when that matters.

Reserve a key:

```sh
curl -s http://127.0.0.1:7410/v1/reserve \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-456","command":["POST /webhook event-456"]}'
```

The fresh reserve response includes an `attempt_token`. Keep it private until
the caller commits the terminal result:

```sh
curl -s http://127.0.0.1:7410/v1/commit \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-456","attempt_token":"...","state":"succeeded","exit_code":0,"stdout_b64":"b2sK"}'
```

Do not log bearer tokens, attempt tokens, command arguments, stdout, stderr, or
keys unless you have checked that they are safe to disclose.

## Stuck running record

A `running` record is not a retry instruction. It means once reserved the key,
but no terminal result was stored.

Start with:

```sh
once list --state running
once get webhook:event-123
```

Then check the external system that owns the side effect. For example:

- mail provider or delivery log for email
- payment provider for a charge
- receiver logs for a webhook
- destination state for a file or remote write

Only delete the record when you have decided what happened outside once:

```sh
once forget --force webhook:event-123
```

If the earlier side effect happened, a retry can duplicate it. Leaving the
record `running` is often the most honest state while you investigate.
