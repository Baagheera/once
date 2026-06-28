#!/bin/sh
set -eu

addr="${ONCE_ADDR:-127.0.0.1:7410}"
token="${ONCE_TOKEN:-secret}"

reserve_response="$(curl -s "http://$addr/v1/reserve" \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","command":["send-webhook"]}')"
printf '%s\n' "$reserve_response"

attempt_token="$(printf '%s\n' "$reserve_response" | sed -n 's/.*"attempt_token":"\([^"]*\)".*/\1/p')"

curl -s "http://$addr/v1/commit" \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","attempt_token":"'"$attempt_token"'","state":"succeeded","exit_code":0,"stdout_b64":"UE9TVCAvd2ViaG9vayBldmVudC0xMjMK"}'
printf '\n'

curl -s "http://$addr/v1/reserve" \
  -H "authorization: Bearer $token" \
  -H 'content-type: application/json' \
  -d '{"key":"webhook:event-123","command":["send-webhook"]}'
printf '\n'
