# HTTP API

Start the server:

```sh
once serve --listen 127.0.0.1:7410
```

By default, the server creates `once.db.token` and prints only the token file
path. Read that file, or pass `--token-file` for scripted use. Tokens passed
through flags or environment variables can be visible to other local processes
on shared machines. Explicit `--token` values must be at least 32 characters.

Do not put `once serve` directly on an untrusted network. If it needs to listen
beyond localhost, keep it on a trusted network or put it behind your own TLS
and access controls.

All endpoints except `GET /healthz` require:

```http
Authorization: Bearer <token>
```

JSON request bodies are capped at 1 MiB. Base64 output in commit requests
counts toward that limit.

Keys are exact identifiers. The server does not trim or normalize them. Use
ASCII letters, digits, `.`, `_`, `:`, `@`, `=`, and `-`, up to 256 bytes.

## Go client

Go programs can use `github.com/Baagheera/once/oncehttp` for the same reserve,
commit, get, and delete calls:

```go
client, err := oncehttp.New("http://127.0.0.1:7410", oncehttp.WithBearerToken(token))
if err != nil {
	return err
}

reserved, err := client.Reserve(ctx, oncehttp.ReserveRequest{
	Key:     "webhook:event-123",
	Command: []string{"deliver-webhook", "event-123"},
})
if err != nil {
	return err
}
if !reserved.Fresh {
	return replay(reserved.Record)
}

_, err = client.Commit(ctx, oncehttp.CommitRequest{
	Key:          reserved.Record.Key,
	AttemptToken: reserved.AttemptToken,
	State:        oncehttp.Succeeded,
	ExitCode:     0,
	Stdout:       []byte("ok\n"),
})
return err
```

The client is a wrapper around the HTTP API. It does not execute commands or
open the SQLite store directly. It caps successful JSON responses at 16 MiB by
default; use `oncehttp.WithMaxResponseBytes` for larger stored output. Use
`oncehttp.WithHTTPClient` to supply transport or timeout settings. Redirects
remain disabled so credentials and attempt tokens stay bound to the configured
server.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | return server health without authentication |
| `POST` | `/v1/reserve` | reserve a key or return the existing record |
| `POST` | `/v1/commit` | commit a terminal result for a running key |
| `GET` | `/v1/records/{key}` | fetch one record |
| `DELETE` | `/v1/records/{key}` | delete one record for deliberate repair |

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

`attempt_token` is returned only for a fresh reservation. Keep it until the
caller either commits the terminal result or decides to delete the record as a
manual repair.

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

Commit only accepts terminal states: `succeeded` and `failed`.

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

If the delete token is missing or malformed, the server returns `400`. If the
token is well formed but does not match the record, the server returns `404`,
the same response used for a missing key.

Example:

```sh
curl -X DELETE \
  "http://127.0.0.1:7410/v1/records/webhook:event-123?force=1" \
  -H "authorization: Bearer $token" \
  -H "X-Once-Attempt-Token: $attempt_token"
```

## Common status codes

| Code | Meaning |
| --- | --- |
| `200` | request succeeded and returned JSON |
| `204` | delete succeeded and returned no body |
| `400` | invalid JSON, invalid key, invalid commit, or missing delete token |
| `401` | missing or invalid bearer token |
| `404` | record not found |
| `409` | key conflict, stale commit token, or protected running record |
| `415` | JSON endpoint called without `Content-Type: application/json` |
