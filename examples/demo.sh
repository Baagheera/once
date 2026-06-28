#!/bin/sh
set -eu

if ! command -v curl >/dev/null 2>&1; then
	echo "demo needs curl" >&2
	exit 1
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/once-demo.XXXXXX")"
server_pid=""
old_path="$PATH"

cleanup() {
	if [ -n "$server_pid" ]; then
		kill "$server_pid" 2>/dev/null || true
	fi
	PATH="$old_path"
	rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

if [ -n "${ONCE_BIN:-}" ]; then
	once_bin="$ONCE_BIN"
else
	once_bin="$tmp/once"
	go build -o "$once_bin" ./cmd/once
fi

cat > "$tmp/send-webhook" <<'EOF'
#!/bin/sh
printf 'POST /webhook %s\n' "$1" >> "$ONCE_DEMO_LOG"
printf 'ok\n'
EOF
chmod +x "$tmp/send-webhook"

cat > "$tmp/charge" <<'EOF'
#!/bin/sh
printf 'attempt\n' >> "$ONCE_DEMO_FAIL_LOG"
printf 'failed\n'
exit 42
EOF
chmod +x "$tmp/charge"

PATH="$tmp:$PATH"

count_lines() {
	wc -l < "$1" | tr -d '[:space:]'
}

prompt() {
	printf '\n$ %s\n' "$1"
}

log="$tmp/webhook.log"
db="$tmp/once.db"
fail_log="$tmp/charge.log"
fail_db="$tmp/once-fail.db"
http_db="$tmp/once-http.db"
token_file="$tmp/once-http.token"
server_log="$tmp/once-http.log"
addr="${ONCE_ADDR:-127.0.0.1:17410}"
token="once-demo-token-123456789012345678901234"

printf 'once: a small idempotency log for side effects\n'

printf '\n# without once\n'
export ONCE_DEMO_LOG="$log"
prompt "send-webhook event-123"
send-webhook event-123
prompt "send-webhook event-123"
send-webhook event-123
unset ONCE_DEMO_LOG
printf 'side effects written: %s\n' "$(count_lines "$log")"

printf '\n# with once\n'
rm -f "$log"
export ONCE_DEMO_LOG="$log"
prompt "once run --key webhook:event-123 -- send-webhook event-123"
"$once_bin" --store "$db" run --key webhook:event-123 -- send-webhook event-123
prompt "once run --key webhook:event-123 -- send-webhook event-123"
"$once_bin" --store "$db" run --key webhook:event-123 -- send-webhook event-123
unset ONCE_DEMO_LOG
printf 'side effects written: %s\n' "$(count_lines "$log")"
prompt "once status webhook:event-123"
"$once_bin" --store "$db" status webhook:event-123

printf '\n# failed results are replayed too\n'
export ONCE_DEMO_FAIL_LOG="$fail_log"
prompt "once run --key charge:order-42 -- charge order-42"
set +e
"$once_bin" --store "$fail_db" run --key charge:order-42 -- charge order-42
code1=$?
set -e
printf 'exit code: %s\n' "$code1"
prompt "once run --key charge:order-42 -- charge order-42"
set +e
"$once_bin" --store "$fail_db" run --key charge:order-42 -- charge order-42
code2=$?
set -e
unset ONCE_DEMO_FAIL_LOG
printf 'exit code: %s\n' "$code2"
printf 'actual attempts: %s\n' "$(count_lines "$fail_log")"
prompt "once status charge:order-42"
"$once_bin" --store "$fail_db" status charge:order-42

printf '\n# HTTP reserve / commit / replay\n'
printf '%s\n' "$token" > "$token_file"
chmod 600 "$token_file" 2>/dev/null || true
prompt "once serve --listen $addr --token-file demo.token"
"$once_bin" --store "$http_db" serve --listen "$addr" --token-file "$token_file" > "$server_log" 2>&1 &
server_pid=$!

i=0
until curl -fs "http://$addr/healthz" >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -gt 50 ]; then
		cat "$server_log" >&2
		exit 1
	fi
	sleep 0.2
done
printf 'once: listening on %s\n' "$addr"
printf 'once: auth token file demo.token\n'

prompt "curl -X POST /v1/reserve -d '{key:webhook:event-456}'"
reserve_response="$(curl -s "http://$addr/v1/reserve" \
	-H "authorization: Bearer $token" \
	-H 'content-type: application/json' \
	-d '{"key":"webhook:event-456","command":["POST /webhook event-456"]}')"
attempt_token="$(printf '%s\n' "$reserve_response" | sed -n 's/.*"attempt_token":"\([^"]*\)".*/\1/p')"
fresh="$(printf '%s\n' "$reserve_response" | sed -n 's/.*"fresh":\([^,}]*\).*/\1/p')"
state="$(printf '%s\n' "$reserve_response" | sed -n 's/.*"state":"\([^"]*\)".*/\1/p')"
if [ -z "$attempt_token" ]; then
	echo "reserve did not return an attempt token" >&2
	exit 1
fi
printf 'fresh: %s\n' "$fresh"
printf 'state: %s\n' "$state"
printf 'attempt_token: returned\n'

prompt "curl -X POST /v1/commit -d '{state:succeeded}'"
commit_response="$(curl -s "http://$addr/v1/commit" \
	-H "authorization: Bearer $token" \
	-H 'content-type: application/json' \
	-d '{"key":"webhook:event-456","attempt_token":"'"$attempt_token"'","state":"succeeded","exit_code":0,"stdout_b64":"b2sK"}')"
state="$(printf '%s\n' "$commit_response" | sed -n 's/.*"state":"\([^"]*\)".*/\1/p')"
stdout_b64="$(printf '%s\n' "$commit_response" | sed -n 's/.*"stdout_b64":"\([^"]*\)".*/\1/p')"
printf 'state: %s\n' "$state"
printf 'stdout_b64: %s\n' "$stdout_b64"

prompt "curl -X POST /v1/reserve -d '{key:webhook:event-456}'"
replay_response="$(curl -s "http://$addr/v1/reserve" \
	-H "authorization: Bearer $token" \
	-H 'content-type: application/json' \
	-d '{"key":"webhook:event-456","command":["POST /webhook event-456"]}')"
fresh="$(printf '%s\n' "$replay_response" | sed -n 's/.*"fresh":\([^,}]*\).*/\1/p')"
state="$(printf '%s\n' "$replay_response" | sed -n 's/.*"state":"\([^"]*\)".*/\1/p')"
stdout_b64="$(printf '%s\n' "$replay_response" | sed -n 's/.*"stdout_b64":"\([^"]*\)".*/\1/p')"
printf 'fresh: %s\n' "$fresh"
printf 'state: %s\n' "$state"
printf 'stdout_b64: %s\n' "$stdout_b64"

printf '\ndone\n'
