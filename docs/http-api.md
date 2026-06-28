# HTTP API

Start the server:

```sh
once serve --listen 127.0.0.1:7410 --token secret
```

All endpoints except `GET /healthz` require:

```http
Authorization: Bearer secret
```

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
Running records are protected. Use `?force=1` to delete a running record
deliberately.

When force deleting over HTTP, send the reservation token:

```http
X-Once-Attempt-Token: opaque-token
```
