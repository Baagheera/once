# HTTP API

Start the server:

```sh
once serve --listen 127.0.0.1:7410
```

By default, the server creates `once.db.token` and prints only the token file
path. Read that file, or pass `--token-file` for scripted use. Tokens passed
through flags or environment variables can be visible to other local processes
on shared machines. Explicit `--token` values must be at least 32 characters.

All endpoints except `GET /healthz` require:

```http
Authorization: Bearer <token>
```

JSON request bodies are capped at 1 MiB. Base64 output in commit requests
counts toward that limit.

## Reserve

```http
POST /v1/reserve
```

Request:

```json
{
  "key": "webhook:event-123",
  "command": ["send-webhook"]
}
```

Response:

```json
{
  "fresh": true,
  "attempt_token": "opaque-token",
  "record": {
    "key": "webhook:event-123",
    "state": "running",
    "exit_code": 0
  }
}
```

If `fresh` is false, the key already existed and the returned record should be
replayed instead of performing the side effect again.

## Commit

```http
POST /v1/commit
```

Request:

```json
{
  "key": "webhook:event-123",
  "attempt_token": "opaque-token",
  "state": "succeeded",
  "exit_code": 0,
  "stdout_b64": "b2sK"
}
```

`stdout_b64` and `stderr_b64` are base64 encoded byte fields.

## Get

```http
GET /v1/records/webhook:event-123
```

## Delete

```http
DELETE /v1/records/webhook:event-123
```

Deletes the record. This is mostly useful for local testing and manual repair.
Send the reservation token for every HTTP delete:

```http
X-Once-Attempt-Token: opaque-token
```

Running records are additionally protected. Use `?force=1` to delete a running
record deliberately.
